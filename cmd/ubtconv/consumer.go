// Copyright 2024 The go-ethereum Authors
// This file is part of go-ethereum.
//
// go-ethereum is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// go-ethereum is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with go-ethereum. If not, see <http://www.gnu.org/licenses/>.

package main

import (
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/ubtemit"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/ethdb/leveldb"
	"github.com/ethereum/go-ethereum/log"
)

// errValidationHalt is a sentinel error returned when strict validation fails
// and ValidationHaltOnMismatch is true. The runner recognizes this as fatal
// and stops the daemon instead of retrying.
type errValidationHalt struct {
	err error
}

func (e *errValidationHalt) Error() string { return "validation halt: " + e.err.Error() }
func (e *errValidationHalt) Unwrap() error { return e.err }

// errReorgManualRequired indicates a reorg that requires manual operator intervention.
type errReorgManualRequired struct {
	msg string
	err error
}

func (e *errReorgManualRequired) Error() string {
	if e.err != nil {
		return fmt.Sprintf("reorg recovery requires manual intervention: %s: %v", e.msg, e.err)
	}
	return fmt.Sprintf("reorg recovery requires manual intervention: %s", e.msg)
}
func (e *errReorgManualRequired) Unwrap() error { return e.err }

// errReorgReplayRequired indicates a reorg that needs archive replay to recover.
type errReorgReplayRequired struct {
	msg string
	err error
}

func (e *errReorgReplayRequired) Error() string {
	if e.err != nil {
		return fmt.Sprintf("reorg recovery requires archive replay: %s: %v", e.msg, e.err)
	}
	return fmt.Sprintf("reorg recovery requires archive replay: %s", e.msg)
}
func (e *errReorgReplayRequired) Unwrap() error { return e.err }

// ConsumerState tracks the consumer's durable checkpoint.
type ConsumerState struct {
	PendingSeq       uint64
	PendingSeqActive bool
	PendingStatus    rawdb.UBTConsumerPendingStatus
	PendingUpdatedAt uint64
	AppliedSeq       uint64
	AppliedRoot      common.Hash
	AppliedBlock     uint64
}

// pendingBlockRoot tracks per-block UBT roots within a commit batch.
type pendingBlockRoot struct {
	block      uint64
	root       common.Hash
	blockHash  common.Hash
	parentHash common.Hash
}

type consumeDecision struct {
	targetSeq uint64
	env       *ubtemit.OutboxEnvelope
}

// Consumer reads outbox events and applies them to the UBT.
type Consumer struct {
	cfg          *Config
	db           ethdb.Database // Local consumer state DB
	applier      *Applier
	reader       *OutboxReader
	validator    *Validator    // Cross-checks UBT against MPT (nil if validation disabled)
	replayClient *ReplayClient // Archive replay for deep recovery (nil if not configured)

	state    ConsumerState
	hasState bool // true if state was loaded from DB (distinguishes "consumed seq 0" from "never consumed")
	mu       sync.Mutex

	// In-memory tracking (not persisted until commit)
	processedSeq      uint64      // Last event applied to trie (not necessarily committed)
	pendingRoot       common.Hash // Root after latest in-memory apply (not committed)
	pendingBlock      uint64      // Block number after latest in-memory apply
	pendingBlockHash  common.Hash // Canonical hash for pendingBlock
	pendingParentHash common.Hash // Canonical parent hash for pendingBlock

	// Per-block root tracking for batch commits (§14 R19)
	pendingBlockRoots []pendingBlockRoot

	// Commit policy
	uncommittedBlocks uint64
	lastCommitTime    time.Time
	commitCount       uint64 // Total number of commits performed

	// Addresses touched since last validation (for sampled cross-checks)
	recentAddresses []common.Address

	// Backpressure: latest known outbox seq for lag computation
	outboxLag uint64

	// Last diff for strict validation (retained between ConsumeNext and commit)
	lastDiff *ubtemit.QueuedDiffV1

	// Phase tracker reference (set by runner for status reporting)
	phaseTracker *PhaseTracker
}

// NewConsumer creates a new outbox consumer.
func NewConsumer(cfg *Config) (*Consumer, error) {
	// Open local state DB for consumer checkpoints
	dbPath := filepath.Join(cfg.DataDir, "consumer")
	kvdb, err := leveldb.New(dbPath, 16, 16, "ubtconv/consumer", false)
	if err != nil {
		return nil, fmt.Errorf("failed to open consumer DB: %w", err)
	}
	db := rawdb.NewDatabase(kvdb)

	// Create outbox reader (RPC client)
	reader := NewOutboxReader(cfg.OutboxRPCEndpoint)

	var validator *Validator
	if (cfg.ValidationEnabled && cfg.ValidationSampleRate > 0) || cfg.ValidationStrictMode || cfg.ValidateOnlyMode {
		validator = NewValidator(reader)
	}

	var replayClient *ReplayClient
	if cfg.RequireArchiveReplay {
		replayClient = NewReplayClient(reader)
	}

	c := &Consumer{
		cfg:            cfg,
		db:             db,
		reader:         reader,
		validator:      validator,
		replayClient:   replayClient,
		lastCommitTime: time.Now(),
	}

	// Load consumer state from DB first
	if err := c.loadState(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to load consumer state: %w", err)
	}

	// Initialize in-memory processed counter from durable state.
	// For fresh starts (no persisted state), use ^uint64(0) so that
	// targetSeq = processedSeq + 1 wraps to 0, correctly consuming seq=0 first.
	if c.hasState {
		c.processedSeq = c.state.AppliedSeq
	} else {
		c.processedSeq = ^uint64(0) // sentinel: +1 overflows to 0
	}

	// Create applier with the expected root from loaded state
	applier, err := NewApplier(cfg, c.state.AppliedRoot)
	if err != nil && c.hasState {
		// Startup recovery: trie is corrupted but we have persisted state.
		// Try opening with empty root and restoring from anchor.
		originalErr := err // save before overwrite
		log.Warn("Applier failed with expected root, attempting anchor recovery",
			"expectedRoot", c.state.AppliedRoot, "err", originalErr)
		daemonRecoveryAttempts.Inc(1)

		applier, err = NewApplier(cfg, common.Hash{})
		if err != nil {
			db.Close()
			daemonRecoveryFailures.Inc(1)
			return nil, fmt.Errorf("failed to create applier for recovery: %w (original: %v)", err, originalErr)
		}
		c.applier = applier

		if restoreErr := c.restoreFromAnchor(c.state.AppliedBlock); restoreErr != nil {
			applier.Close()
			db.Close()
			daemonRecoveryFailures.Inc(1)
			return nil, fmt.Errorf("startup recovery failed: anchor restore: %w (original error: %v)", restoreErr, originalErr)
		}
		daemonRecoverySuccesses.Inc(1)
		log.Info("Startup recovery via anchor restore succeeded")
		return c, nil
	} else if err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to create applier: %w", err)
	}
	c.applier = applier

	// Wire slot index if configured
	if cfg.SlotIndexMode != "" && cfg.SlotIndexMode != "off" {
		cancunBlock := cfg.CancunBlock
		if cancunBlock == 0 {
			// No explicit Cancun block provided — estimate from chain config timestamp.
			// This is approximate (assumes ~12s blocks from genesis). For production use,
			// set --cancun-block explicitly for correctness.
			chainCfg := cfg.resolveChainConfig()
			if chainCfg.CancunTime != nil {
				cancunBlock = *chainCfg.CancunTime / 12
				log.Warn("Cancun block estimated from timestamp (use --cancun-block for precision)",
					"estimated", cancunBlock, "cancunTime", *chainCfg.CancunTime)
			}
		}
		si := NewSlotIndex(c.db, cfg.SlotIndexMode, cancunBlock, cfg.SlotIndexDiskBudget, 80)
		applier.SetSlotIndex(si)
	}

	return c, nil
}

// loadState reads the persisted consumer checkpoint.
func (c *Consumer) loadState() error {
	state := rawdb.ReadUBTConsumerState(c.db)
	if state != nil {
		hasAppliedCheckpoint := state.AppliedSeq > 0 || state.AppliedBlock > 0 || state.AppliedRoot != (common.Hash{})
		pendingStatus := state.PendingStatus
		if pendingStatus == rawdb.UBTConsumerPendingNone && (state.PendingSeqActive || state.PendingSeq > 0) {
			// Backward compatibility for checkpoints persisted before PendingStatus existed.
			pendingStatus = rawdb.UBTConsumerPendingInFlight
		}
		pendingSeqActive := pendingStatus == rawdb.UBTConsumerPendingInFlight
		c.state = ConsumerState{
			PendingSeq:       state.PendingSeq,
			PendingSeqActive: pendingSeqActive,
			PendingStatus:    pendingStatus,
			PendingUpdatedAt: state.PendingUpdatedAt,
			AppliedSeq:       state.AppliedSeq,
			AppliedRoot:      state.AppliedRoot,
			AppliedBlock:     state.AppliedBlock,
		}
		c.hasState = hasAppliedCheckpoint
		pendingActive := c.pendingInFlight()
		if pendingActive {
			log.Warn("Detected incomplete apply from previous run (crash recovery)",
				"pendingSeq", state.PendingSeq, "appliedSeq", state.AppliedSeq,
				"appliedBlock", state.AppliedBlock, "appliedRoot", state.AppliedRoot,
				"pendingStatus", c.state.PendingStatus, "pendingUpdatedAt", c.state.PendingUpdatedAt)
			// Clear pendingSeq — the trie was opened at appliedRoot (clean state),
			// and processedSeq will be set to appliedSeq, so the pending event
			// will be replayed automatically by the next ConsumeNext call.
			c.clearPendingMetadata()
			if hasAppliedCheckpoint {
				c.persistState()
			}
		}
		if hasAppliedCheckpoint {
			log.Info("Loaded consumer state", "applied", c.state.AppliedSeq, "block", c.state.AppliedBlock, "root", c.state.AppliedRoot)
		} else {
			log.Info("No durable applied checkpoint found, starting from genesis")
		}
	} else {
		c.hasState = false
		log.Info("No existing consumer state found, starting from genesis")
	}
	return nil
}

// ConsumeNext reads and processes the next outbox event.
func (c *Consumer) ConsumeNext() error {
	start := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()

	decision, err := c.decideNextAction()
	if err != nil {
		return err
	}
	handled, err := c.executeTransition(decision, start)
	if err != nil {
		return err
	}
	if handled {
		return nil
	}

	// Advance in-memory processed counter (NOT persisted)
	c.processedSeq = decision.targetSeq
	c.uncommittedBlocks++

	// Check commit policy
	if c.shouldCommit() {
		if err := c.commit(); err != nil {
			return fmt.Errorf("commit at seq %d: %w", decision.targetSeq, err)
		}
	}

	log.Debug("UBT event applied", "seq", decision.targetSeq, "kind", decision.env.Kind, "block", decision.env.BlockNumber)
	return nil
}

func (c *Consumer) decideNextAction() (*consumeDecision, error) {
	// Use processedSeq (in-memory) to determine next target, NOT AppliedSeq (durable).
	targetSeq := c.processedSeq + 1
	env, err := c.reader.ReadEvent(targetSeq)
	if err != nil {
		consumerErrorsTotal.Inc(1)
		return nil, fmt.Errorf("read event %d: %w", targetSeq, err)
	}
	if env == nil {
		consumerErrorsTotal.Inc(1)
		return nil, fmt.Errorf("no event at seq %d", targetSeq)
	}
	return &consumeDecision{targetSeq: targetSeq, env: env}, nil
}

func (c *Consumer) executeTransition(decision *consumeDecision, start time.Time) (bool, error) {
	handled, err := c.handleImplicitReorg(decision)
	if handled || err != nil {
		return handled, err
	}
	switch decision.env.Kind {
	case ubtemit.KindDiff:
		return false, c.executeDiffTransition(decision, start)
	case ubtemit.KindReorg:
		return true, c.executeReorgTransition(decision)
	default:
		consumerErrorsTotal.Inc(1)
		return false, fmt.Errorf("unknown event kind %q at seq %d", decision.env.Kind, decision.targetSeq)
	}
}

func (c *Consumer) handleImplicitReorg(decision *consumeDecision) (bool, error) {
	env := decision.env
	// §11 R9: Detect implicit reorg via parent-hash mismatch
	if env.Kind != ubtemit.KindDiff || c.pendingBlockHash == (common.Hash{}) || env.ParentHash == c.pendingBlockHash {
		return false, nil
	}
	log.Warn("UBT parent-hash mismatch detected (implicit reorg)",
		"expected", c.pendingBlockHash, "got", env.ParentHash,
		"block", env.BlockNumber, "seq", decision.targetSeq)
	// Treat as reorg: revert to last committed state
	if c.uncommittedBlocks > 0 {
		if err := c.applier.Revert(c.state.AppliedRoot); err != nil {
			return false, fmt.Errorf("revert on parent-hash mismatch: %w", err)
		}
		c.pendingRoot = c.state.AppliedRoot
		c.pendingBlock = c.state.AppliedBlock
		c.pendingBlockHash = rawdb.ReadUBTCanonicalBlockHash(c.db, c.state.AppliedBlock)
		c.pendingParentHash = rawdb.ReadUBTCanonicalParentHash(c.db, c.state.AppliedBlock)
		c.uncommittedBlocks = 0
		c.pendingBlockRoots = c.pendingBlockRoots[:0]
	}
	consumerReorgTotal.Inc(1)
	// Re-read the event will happen on the next ConsumeNext cycle.
	return true, nil
}

func (c *Consumer) executeDiffTransition(decision *consumeDecision, start time.Time) error {
	diff, err := ubtemit.DecodeDiff(decision.env.Payload)
	if err != nil {
		consumerErrorsTotal.Inc(1)
		return fmt.Errorf("decode diff at seq %d: %w", decision.targetSeq, err)
	}
	// Crash-consistency: persist pendingSeq AFTER decode validation but BEFORE apply (plan §12).
	c.markPendingSeq(decision.targetSeq)
	root, err := c.applier.ApplyDiff(diff, decision.env.BlockNumber)
	if err != nil {
		consumerErrorsTotal.Inc(1)
		return fmt.Errorf("apply diff at seq %d: %w", decision.targetSeq, err)
	}
	// Collect addresses for validation sampling.
	for _, acct := range diff.Accounts {
		c.recentAddresses = append(c.recentAddresses, acct.Address)
	}
	// Track in-memory state (NOT persisted until commit).
	c.pendingRoot = root
	c.pendingBlock = decision.env.BlockNumber
	c.pendingBlockHash = decision.env.BlockHash
	c.pendingParentHash = decision.env.ParentHash
	c.lastDiff = diff
	c.pendingBlockRoots = append(c.pendingBlockRoots, pendingBlockRoot{
		block:      decision.env.BlockNumber,
		root:       root,
		blockHash:  decision.env.BlockHash,
		parentHash: decision.env.ParentHash,
	})
	consumerAppliedTotal.Inc(1)
	consumerAppliedLatency.UpdateSince(start)

	// Strict validation: cross-check ALL accounts/storage in diff against MPT.
	if c.cfg.ValidationStrictMode && c.validator != nil {
		if err := c.validator.ValidateStrict(c.applier.Trie(), decision.env.BlockNumber, diff); err != nil {
			if c.cfg.ValidationHaltOnMismatch {
				return &errValidationHalt{err: err}
			}
			log.Error("Strict validation mismatch (continuing)", "block", decision.env.BlockNumber, "err", err)
		}
	}
	return nil
}

func (c *Consumer) executeReorgTransition(decision *consumeDecision) error {
	marker, err := ubtemit.DecodeReorgMarker(decision.env.Payload)
	if err != nil {
		consumerErrorsTotal.Inc(1)
		return fmt.Errorf("decode reorg marker at seq %d: %w", decision.targetSeq, err)
	}
	// Crash-consistency: persist pendingSeq AFTER decode validation but BEFORE apply (plan §12).
	c.markPendingSeq(decision.targetSeq)
	if err := c.handleReorg(marker); err != nil {
		consumerErrorsTotal.Inc(1)
		return fmt.Errorf("handle reorg at seq %d: %w", decision.targetSeq, err)
	}
	// handleReorg already reverted to the ancestor and committed the clean state.
	c.processedSeq = decision.targetSeq
	c.clearPendingSeq()
	consumerReorgTotal.Inc(1)
	log.Debug("UBT event applied", "seq", decision.targetSeq, "kind", decision.env.Kind, "block", decision.env.BlockNumber)
	return nil
}

// shouldCommit checks if the commit policy dictates a commit.
func (c *Consumer) shouldCommit() bool {
	if c.uncommittedBlocks >= c.cfg.ApplyCommitInterval {
		return true
	}
	if time.Since(c.lastCommitTime) >= c.cfg.ApplyCommitMaxLatency {
		return true
	}
	// Backpressure: if outboxLag > threshold, commit more aggressively (every block).
	if c.cfg.BackpressureLagThreshold > 0 && c.outboxLag > c.cfg.BackpressureLagThreshold {
		return true
	}
	return false
}

// commit commits the current UBT state to disk.
func (c *Consumer) commit() error {
	start := time.Now()
	if err := c.applier.CommitAt(c.pendingBlock); err != nil {
		return err
	}

	// NOW update durable state - only after successful trie commit
	c.state.AppliedSeq = c.processedSeq
	c.state.AppliedRoot = c.pendingRoot
	c.state.AppliedBlock = c.pendingBlock
	c.clearPendingMetadata()
	c.persistState()
	c.hasState = true

	// Write per-block root index for ALL blocks in this commit batch (§14 R19)
	for _, pbr := range c.pendingBlockRoots {
		rawdb.WriteUBTBlockRoot(c.db, pbr.block, pbr.root)
		rawdb.WriteUBTCanonicalBlock(c.db, pbr.block, pbr.blockHash, pbr.parentHash)
	}
	c.pendingBlockRoots = c.pendingBlockRoots[:0]
	// Always write the final committed block root
	rawdb.WriteUBTBlockRoot(c.db, c.state.AppliedBlock, c.state.AppliedRoot)
	rawdb.WriteUBTCanonicalBlock(c.db, c.state.AppliedBlock, c.pendingBlockHash, c.pendingParentHash)

	c.uncommittedBlocks = 0
	c.lastCommitTime = time.Now()
	c.commitCount++

	consumerCommitLatency.UpdateSince(start)
	consumerCommitTotal.Inc(1)

	// Deterministic sampling: validate every Nth block.
	// This provides consistent, reproducible validation coverage.
	// To validate every block, set validation-sample-rate=1.
	if c.validator != nil && c.cfg.ValidationSampleRate > 0 && c.state.AppliedBlock%c.cfg.ValidationSampleRate == 0 {
		if err := c.validator.ValidateBlock(c.applier.Trie(), c.state.AppliedBlock, c.state.AppliedRoot, c.recentAddresses); err != nil {
			log.Error("UBT validation failed", "block", c.state.AppliedBlock, "err", err)
		}
	}
	c.recentAddresses = c.recentAddresses[:0]

	// Create anchor snapshot if interval is reached
	if c.cfg.AnchorSnapshotInterval > 0 && c.commitCount%c.cfg.AnchorSnapshotInterval == 0 {
		c.createAnchorSnapshot()
	}
	return nil
}

// createAnchorSnapshot creates a new anchor snapshot at the current state.
func (c *Consumer) createAnchorSnapshot() {
	count := rawdb.ReadUBTAnchorSnapshotCount(c.db)
	snap := &rawdb.UBTAnchorSnapshot{
		BlockNumber: c.state.AppliedBlock,
		BlockRoot:   c.state.AppliedRoot,
		Seq:         c.state.AppliedSeq,
		Timestamp:   uint64(time.Now().Unix()),
	}
	rawdb.WriteUBTAnchorSnapshot(c.db, count, snap)
	rawdb.WriteUBTAnchorSnapshotCount(c.db, count+1)

	log.Info("UBT anchor snapshot created",
		"index", count,
		"block", snap.BlockNumber,
		"root", snap.BlockRoot,
		"seq", snap.Seq)

	// Prune old anchors if retention is configured
	if c.cfg.AnchorSnapshotRetention > 0 && count+1 > c.cfg.AnchorSnapshotRetention {
		pruneFrom := count + 1 - c.cfg.AnchorSnapshotRetention
		for i := uint64(0); i < pruneFrom; i++ {
			rawdb.DeleteUBTAnchorSnapshot(c.db, i)
		}
		log.Debug("UBT anchor snapshots pruned", "pruned", pruneFrom, "retained", c.cfg.AnchorSnapshotRetention)
	}
}

// handleReorg handles a reorg marker event.
// It reverts the UBT trie to the common ancestor state and continues processing.
func (c *Consumer) handleReorg(marker *ubtemit.ReorgMarkerV1) error {
	// The common ancestor is the block we need to revert to
	ancestorBlock := marker.CommonAncestorNumber

	// §11 R10: Independently verify ancestor hash against local canonical metadata
	if c.db != nil {
		localAncestorHash := rawdb.ReadUBTCanonicalBlockHash(c.db, ancestorBlock)
		if localAncestorHash != (common.Hash{}) && marker.CommonAncestorHash != (common.Hash{}) && localAncestorHash != marker.CommonAncestorHash {
			log.Warn("UBT reorg ancestor hash mismatch",
				"block", ancestorBlock,
				"markerHash", marker.CommonAncestorHash,
				"localHash", localAncestorHash)
		}
	}

	// Check for underflow before computing reorg depth
	if marker.FromBlockNumber < ancestorBlock {
		return fmt.Errorf("invalid reorg marker: from block %d < ancestor block %d", marker.FromBlockNumber, ancestorBlock)
	}

	// Check if reorg depth exceeds configured maximum
	depth := marker.FromBlockNumber - ancestorBlock
	if depth > c.cfg.MaxRecoverableReorgDepth {
		return &errReorgManualRequired{msg: fmt.Sprintf("reorg depth %d exceeds max %d; increase --max-recoverable-reorg-depth or manually reset state", depth, c.cfg.MaxRecoverableReorgDepth)}
	}

	log.Warn("UBT reorg detected",
		"from", marker.FromBlockNumber, "to", marker.ToBlockNumber,
		"ancestor", marker.CommonAncestorNumber, "depth", depth)

	// Look up the UBT root at the common ancestor block
	ancestorRoot := rawdb.ReadUBTBlockRoot(c.db, ancestorBlock)
	if ancestorRoot == (common.Hash{}) {
		// If we don't have a root for the ancestor, check if uncommitted changes
		// include the reorged blocks. If so, we can discard them by reverting
		// to the last committed root.
		if c.uncommittedBlocks > 0 && depth <= c.uncommittedBlocks {
			// Reorg is within uncommitted window - safe to revert
			if err := c.applier.Revert(c.state.AppliedRoot); err != nil {
				return fmt.Errorf("revert to committed root: %w", err)
			}
			// Update pending state to match committed state
			c.pendingRoot = c.state.AppliedRoot
			c.pendingBlock = c.state.AppliedBlock
			c.pendingBlockHash = rawdb.ReadUBTCanonicalBlockHash(c.db, c.state.AppliedBlock)
			c.pendingParentHash = rawdb.ReadUBTCanonicalParentHash(c.db, c.state.AppliedBlock)
			c.uncommittedBlocks = 0
			log.Info("UBT reverted to last committed root", "root", c.state.AppliedRoot)
			return nil
		}
		// Slow-path: restore from anchor then replay blocks
		if c.replayClient != nil {
			log.Warn("UBT reorg slow-path: anchor restore + replay", "ancestorBlock", ancestorBlock)
			daemonRecoveryAttempts.Inc(1)

			if err := c.restoreFromAnchor(ancestorBlock); err != nil {
				daemonRecoveryFailures.Inc(1)
				return &errReorgManualRequired{msg: "slow-path anchor restore failed", err: err}
			}

			// Replay blocks from anchor to ancestor
			for block := c.state.AppliedBlock + 1; block <= ancestorBlock; block++ {
				diff, err := c.replayClient.ReplayBlock(block)
				if err != nil {
					daemonRecoveryFailures.Inc(1)
					return fmt.Errorf("slow-path replay block %d: %w", block, err)
				}
				root, err := c.applier.ApplyDiff(diff, block)
				if err != nil {
					daemonRecoveryFailures.Inc(1)
					return fmt.Errorf("slow-path apply block %d: %w", block, err)
				}
				c.pendingRoot = root
				c.pendingBlock = block
				c.uncommittedBlocks++
				daemonReplayBlocksPerSec.Mark(1)

				// Periodic commits during replay
				if c.uncommittedBlocks >= c.cfg.ApplyCommitInterval {
					if err := c.commit(); err != nil {
						return fmt.Errorf("slow-path commit at block %d: %w", block, err)
					}
				}
			}
			// Final commit at ancestor
			if c.uncommittedBlocks > 0 {
				if err := c.commit(); err != nil {
					return fmt.Errorf("slow-path final commit: %w", err)
				}
			}
			daemonRecoverySuccesses.Inc(1)
			log.Info("UBT slow-path recovery complete", "ancestorBlock", ancestorBlock)
			return nil
		}
		return &errReorgReplayRequired{msg: fmt.Sprintf("no UBT root at ancestor block %d; restart with --require-archive-replay=true", ancestorBlock)}
	}

	// Revert the trie to the ancestor root
	if err := c.applier.Revert(ancestorRoot); err != nil {
		return fmt.Errorf("revert to ancestor block %d root %s: %w", ancestorBlock, ancestorRoot, err)
	}

	// Update pending state to reflect the revert
	c.pendingRoot = ancestorRoot
	c.pendingBlock = ancestorBlock
	c.pendingBlockHash = marker.CommonAncestorHash
	c.pendingParentHash = rawdb.ReadUBTCanonicalParentHash(c.db, ancestorBlock)
	c.uncommittedBlocks = 0
	c.lastCommitTime = time.Now()

	// Drop stale canonical metadata and roots above ancestor.
	for block := ancestorBlock + 1; block <= c.state.AppliedBlock; block++ {
		rawdb.DeleteUBTBlockRoot(c.db, block)
		rawdb.DeleteUBTCanonicalBlock(c.db, block)
	}

	// Commit immediately after reorg to persist the clean state
	if err := c.commit(); err != nil {
		return fmt.Errorf("commit after reorg revert: %w", err)
	}

	log.Info("UBT reorg recovery complete",
		"ancestorBlock", ancestorBlock,
		"ancestorRoot", ancestorRoot,
		"reorgDepth", depth)
	return nil
}

// restoreFromAnchor finds the best anchor at or below targetBlock and restores from it.
func (c *Consumer) restoreFromAnchor(targetBlock uint64) error {
	start := time.Now()
	count := rawdb.ReadUBTAnchorSnapshotCount(c.db)
	if count == 0 {
		return fmt.Errorf("no anchor snapshots available for recovery")
	}

	// Iterate anchors from latest to oldest to find best match
	var bestAnchor *rawdb.UBTAnchorSnapshot
	for i := int64(count) - 1; i >= 0; i-- {
		snap := rawdb.ReadUBTAnchorSnapshot(c.db, uint64(i))
		if snap == nil {
			continue
		}
		if snap.BlockNumber <= targetBlock {
			bestAnchor = snap
			break
		}
	}

	if bestAnchor == nil {
		// §11 R14: Genesis fallback — no anchor found, start from block 0 (empty state)
		log.Warn("No anchor snapshot found, falling back to genesis (block 0)", "targetBlock", targetBlock)
		if err := c.applier.Revert(common.Hash{}); err != nil {
			return fmt.Errorf("revert to genesis root: %w", err)
		}
		c.state.AppliedSeq = 0
		c.state.AppliedRoot = common.Hash{}
		c.state.AppliedBlock = 0
		c.processedSeq = ^uint64(0) // will wrap to 0 on next consume
		c.pendingRoot = common.Hash{}
		c.pendingBlock = 0
		c.uncommittedBlocks = 0
		c.pendingBlockRoots = c.pendingBlockRoots[:0]
		c.persistState()
		daemonSnapshotRestoreTotal.Inc(1)
		log.Info("Restored to genesis state for recovery")
		return nil
	}

	// Revert to the anchor root
	if err := c.applier.Revert(bestAnchor.BlockRoot); err != nil {
		return fmt.Errorf("revert to anchor root %s at block %d: %w", bestAnchor.BlockRoot, bestAnchor.BlockNumber, err)
	}

	// Update consumer state
	c.state.AppliedSeq = bestAnchor.Seq
	c.state.AppliedRoot = bestAnchor.BlockRoot
	c.state.AppliedBlock = bestAnchor.BlockNumber
	c.processedSeq = bestAnchor.Seq
	c.pendingRoot = bestAnchor.BlockRoot
	c.pendingBlock = bestAnchor.BlockNumber
	c.uncommittedBlocks = 0
	c.persistState()

	daemonSnapshotRestoreTotal.Inc(1)
	daemonSnapshotRestoreLatency.UpdateSince(start)

	log.Info("Restored from anchor snapshot",
		"anchorBlock", bestAnchor.BlockNumber,
		"anchorRoot", bestAnchor.BlockRoot,
		"anchorSeq", bestAnchor.Seq,
		"targetBlock", targetBlock)
	return nil
}

// ConsumeNextValidateOnly reads the next event and validates diff values against MPT
// without applying to the trie. Used in validate-only mode for shadow verification.
func (c *Consumer) ConsumeNextValidateOnly() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	targetSeq := c.processedSeq + 1
	env, err := c.reader.ReadEvent(targetSeq)
	if err != nil {
		consumerErrorsTotal.Inc(1)
		return fmt.Errorf("read event %d: %w", targetSeq, err)
	}
	if env == nil {
		consumerErrorsTotal.Inc(1)
		return fmt.Errorf("no event at seq %d", targetSeq)
	}

	if env.Kind == ubtemit.KindDiff {
		diff, err := ubtemit.DecodeDiff(env.Payload)
		if err != nil {
			return fmt.Errorf("decode diff at seq %d: %w", targetSeq, err)
		}
		// Crash-consistency: persist pendingSeq AFTER decode validation but BEFORE validation (plan §12).
		c.markPendingSeq(targetSeq)

		// Validate diff values directly against MPT (not the local trie,
		// which doesn't advance in validate-only mode).
		if c.validator != nil {
			if err := c.validator.ValidateDiffAgainstMPT(env.BlockNumber, diff); err != nil {
				log.Error("Validate-only mode: validation failed", "block", env.BlockNumber, "err", err)
				validationMismatches.Inc(1)
				return &errValidationHalt{err: err}
			}
			validationChecksTotal.Inc(1)
		}
	}

	c.processedSeq = targetSeq
	c.pendingBlock = env.BlockNumber

	// Persist progress periodically so restarts don't re-validate from the beginning
	c.uncommittedBlocks++
	if c.uncommittedBlocks >= c.cfg.ApplyCommitInterval {
		c.persistValidateOnlyProgress()
	}

	log.Debug("Validate-only event processed", "seq", targetSeq, "kind", env.Kind, "block", env.BlockNumber)
	return nil
}

// persistValidateOnlyProgress persists seq/block checkpoint for validate-only mode
// WITHOUT touching AppliedRoot or calling trie commit.
func (c *Consumer) persistValidateOnlyProgress() {
	c.state.AppliedSeq = c.processedSeq
	c.state.AppliedBlock = c.pendingBlock
	c.clearPendingMetadata()
	// NOTE: Do NOT update c.state.AppliedRoot — validate-only never modifies the trie
	c.persistState()
	c.hasState = true
	c.uncommittedBlocks = 0
}

// persistState writes the consumer state to the database.
func (c *Consumer) persistState() {
	rawdb.WriteUBTConsumerState(c.db, &rawdb.UBTConsumerState{
		PendingSeq:       c.state.PendingSeq,
		PendingSeqActive: c.state.PendingSeqActive,
		PendingStatus:    c.state.PendingStatus,
		PendingUpdatedAt: c.state.PendingUpdatedAt,
		AppliedSeq:       c.state.AppliedSeq,
		AppliedRoot:      c.state.AppliedRoot,
		AppliedBlock:     c.state.AppliedBlock,
	})
}

func (c *Consumer) pendingInFlight() bool {
	return c.state.PendingStatus == rawdb.UBTConsumerPendingInFlight
}

func (c *Consumer) clearPendingMetadata() {
	c.state.PendingSeq = 0
	c.state.PendingSeqActive = false
	c.state.PendingStatus = rawdb.UBTConsumerPendingNone
	c.state.PendingUpdatedAt = uint64(time.Now().Unix())
}

// markPendingSeq records an in-flight event sequence for crash recovery.
func (c *Consumer) markPendingSeq(seq uint64) {
	if c.pendingInFlight() && c.state.PendingSeq == seq {
		return
	}
	c.state.PendingSeq = seq
	c.state.PendingSeqActive = true
	c.state.PendingStatus = rawdb.UBTConsumerPendingInFlight
	c.state.PendingUpdatedAt = uint64(time.Now().Unix())
	c.persistState()
}

// clearPendingSeq clears in-flight event markers once the event is fully handled.
func (c *Consumer) clearPendingSeq() {
	if !c.pendingInFlight() && c.state.PendingSeq == 0 {
		return
	}
	c.clearPendingMetadata()
	c.persistState()
}

// Close closes the consumer and flushes any pending state.
func (c *Consumer) Close() error {
	c.mu.Lock()
	if c.uncommittedBlocks > 0 {
		if c.cfg.ValidateOnlyMode {
			c.persistValidateOnlyProgress()
		} else if c.safeToCommit() {
			if err := c.commit(); err != nil {
				log.Error("Failed to commit on close", "err", err)
			}
		} else {
			log.Warn("Skipping commit on close: state may be inconsistent",
				"uncommittedBlocks", c.uncommittedBlocks,
				"pendingBlock", c.pendingBlock,
				"appliedBlock", c.state.AppliedBlock)
		}
	}
	c.mu.Unlock()
	c.reader.Close()
	c.applier.Close()
	return c.db.Close()
}

// safeToCommit checks if the current state is consistent enough to commit.
func (c *Consumer) safeToCommit() bool {
	// Don't commit if pending state went backward (reorg in progress)
	if c.pendingBlock < c.state.AppliedBlock {
		return false
	}
	// Don't commit if pending root is zero (uninitialized)
	if c.pendingRoot == (common.Hash{}) && c.state.AppliedRoot != (common.Hash{}) {
		return false
	}
	return true
}
