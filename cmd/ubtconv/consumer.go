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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// errOutboxGap indicates the consumer fell behind retained outbox range.
type errOutboxGap struct {
	targetSeq uint64
	lowestSeq uint64
	latestSeq uint64
}

func (e *errOutboxGap) Error() string {
	if e.latestSeq > 0 {
		return fmt.Sprintf("outbox gap detected: required seq %d is below retained lowest seq %d (latest=%d)", e.targetSeq, e.lowestSeq, e.latestSeq)
	}
	return fmt.Sprintf("outbox gap detected: required seq %d is below retained lowest seq %d", e.targetSeq, e.lowestSeq)
}

var errNoEventAvailable = errors.New("no outbox event available")

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

type strictValidationTask struct {
	block uint64
	root  common.Hash
	diff  *ubtemit.QueuedDiffV1
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

	// Consumer-side read-ahead queue for sequential consumption.
	readAheadQueue   []*ubtemit.OutboxEnvelope
	readAheadNextSeq uint64

	// Last diff for strict validation (retained between ConsumeNext and commit)
	lastDiff *ubtemit.QueuedDiffV1

	// Debounced durability for pending state.
	stateDirty         bool
	lastStatePersistAt time.Time

	// Async strict validation (only used when halt-on-mismatch is disabled).
	pendingStrictValidations map[uint64]*ubtemit.QueuedDiffV1
	validationQueue          chan strictValidationTask
	validationStop           chan struct{}
	validationWG             sync.WaitGroup
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
	if cfg.OutboxReadBatch > 0 {
		reader.SetPrefetchBatch(cfg.OutboxReadBatch)
	}

	var validator *Validator
	if (cfg.ValidationEnabled && cfg.ValidationSampleRate > 0) || cfg.ValidationStrictMode {
		validator = NewValidator(reader)
	}

	var replayClient *ReplayClient
	if cfg.RequireArchiveReplay {
		replayClient = NewReplayClient(reader)
	}

	c := &Consumer{
		cfg:                      cfg,
		db:                       db,
		reader:                   reader,
		validator:                validator,
		replayClient:             replayClient,
		lastCommitTime:           time.Now(),
		lastStatePersistAt:       time.Now(),
		readAheadQueue:           make([]*ubtemit.OutboxEnvelope, 0),
		pendingStrictValidations: make(map[uint64]*ubtemit.QueuedDiffV1),
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

	// Create applier with the expected root from loaded state.
	// On some platforms/filesystems, reopening LevelDB right after a failed open
	// can transiently return "resource temporarily unavailable" (LOCK not yet released).
	// Retry a few times to make restarts stable.
	applier, err := newApplierWithRetry(cfg, c.state.AppliedRoot, 8, 300*time.Millisecond)
	if err != nil && c.hasState {
		// Startup recovery: trie is corrupted but we have persisted state.
		// Try opening with empty root and restoring from anchor.
		originalErr := err // save before overwrite
		log.Warn("Applier failed with expected root, attempting anchor recovery",
			"expectedRoot", c.state.AppliedRoot, "err", originalErr)
		daemonRecoveryAttempts.Inc(1)

		applier, err = newApplierWithRetry(cfg, common.Hash{}, 30, 500*time.Millisecond)
		if err != nil {
			log.Warn("Recovery open on existing trie DB failed, rotating trie DB and retrying clean open", "err", err)
			rotatedPath, rotateErr := rotateCorruptTrieDB(cfg)
			if rotateErr != nil {
				db.Close()
				daemonRecoveryFailures.Inc(1)
				return nil, fmt.Errorf("failed to create applier for recovery: %w; trie DB rotate failed: %v (original: %v)", err, rotateErr, originalErr)
			}
			if rotatedPath != "" {
				log.Warn("Rotated corrupted trie DB", "to", rotatedPath)
			}
			applier, err = newApplierWithRetry(cfg, common.Hash{}, 12, 300*time.Millisecond)
			if err != nil {
				db.Close()
				daemonRecoveryFailures.Inc(1)
				return nil, fmt.Errorf("failed to create fresh applier after trie DB rotation: %w (original: %v)", err, originalErr)
			}
		}
		c.applier = applier

		if restoreErr := c.restoreFromAnchor(c.state.AppliedBlock); restoreErr != nil {
			log.Warn("Anchor restore failed during startup, falling back to genesis",
				"targetBlock", c.state.AppliedBlock, "err", restoreErr)
			if genesisErr := c.restoreToGenesis(c.state.AppliedBlock, fmt.Sprintf("startup anchor restore failed: %v", restoreErr)); genesisErr != nil {
				log.Warn("Genesis fallback on existing trie DB failed, rotating trie DB and retrying",
					"targetBlock", c.state.AppliedBlock, "err", genesisErr)
				applier.Close()
				rotatedPath, rotateErr := rotateCorruptTrieDB(cfg)
				if rotateErr != nil {
					db.Close()
					daemonRecoveryFailures.Inc(1)
					return nil, fmt.Errorf("startup recovery failed: anchor restore err=%v; genesis fallback err=%v; trie DB rotate failed: %w (original error: %v)", restoreErr, genesisErr, rotateErr, originalErr)
				}
				if rotatedPath != "" {
					log.Warn("Rotated corrupted trie DB", "to", rotatedPath)
				}
				applier, err = newApplierWithRetry(cfg, common.Hash{}, 12, 300*time.Millisecond)
				if err != nil {
					db.Close()
					daemonRecoveryFailures.Inc(1)
					return nil, fmt.Errorf("startup recovery failed: anchor restore err=%v; fresh applier open err=%w (original error: %v)", restoreErr, err, originalErr)
				}
				c.applier = applier
				if retryGenesisErr := c.restoreToGenesis(c.state.AppliedBlock, fmt.Sprintf("startup recovery retry after trie DB rotation; anchor restore failed: %v", restoreErr)); retryGenesisErr != nil {
					applier.Close()
					db.Close()
					daemonRecoveryFailures.Inc(1)
					return nil, fmt.Errorf("startup recovery failed after trie DB rotation: %w (original error: %v)", retryGenesisErr, originalErr)
				}
			}
		}
		daemonRecoverySuccesses.Inc(1)
		log.Info("Startup recovery succeeded")
		return c, nil
	} else if err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to create applier: %w", err)
	}
	c.applier = applier

	// Wire slot index in fixed pre-Cancun tracking mode.
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
	if cfg.SlotIndexEnabled {
		si := NewSlotIndex(c.db, cancunBlock, cfg.SlotIndexDiskBudget, 80)
		applier.SetSlotIndex(si)
	}

	// Start async strict validation worker when enabled and halt-on-mismatch is off.
	if c.validator != nil && c.cfg.ValidationStrictMode && c.cfg.ValidationStrictAsync && !c.cfg.ValidationHaltOnMismatch {
		capacity := c.cfg.ValidationQueueCapacity
		if capacity == 0 {
			capacity = 1024
		}
		c.validationQueue = make(chan strictValidationTask, capacity)
		c.validationStop = make(chan struct{})
		c.validationWG.Add(1)
		go c.validationLoop()
	}

	return c, nil
}

func newApplierWithRetry(cfg *Config, expectedRoot common.Hash, attempts int, delay time.Duration) (*Applier, error) {
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	for i := 0; i < attempts; i++ {
		applier, err := NewApplier(cfg, expectedRoot)
		if err == nil {
			return applier, nil
		}
		lastErr = err
		if !isRetryableApplierOpenError(err) || i == attempts-1 {
			break
		}
		time.Sleep(delay)
	}
	return nil, lastErr
}

func isRetryableApplierOpenError(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "resource temporarily unavailable") ||
		strings.Contains(msg, "lock") ||
		strings.Contains(msg, "temporarily unavailable")
}

func rotateCorruptTrieDB(cfg *Config) (string, error) {
	dbPath := filepath.Join(cfg.DataDir, "triedb")
	if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	backupPath := fmt.Sprintf("%s.corrupt.%s", dbPath, time.Now().UTC().Format("20060102-150405"))
	if err := os.Rename(dbPath, backupPath); err != nil {
		return "", err
	}
	if err := os.MkdirAll(dbPath, 0o755); err != nil {
		return "", err
	}
	return backupPath, nil
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
	targetSeq := c.nextTargetSeq()
	env, err := c.readNextEnvelope(targetSeq)
	if err != nil {
		return err
	}
	if env == nil {
		if c.maybeBootstrapFromOutboxFloor(targetSeq) {
			return nil
		}
		if c.cfg.TreatNoEventAsIdle {
			c.mu.Lock()
			lag := c.outboxLag
			c.mu.Unlock()
			if lag > 0 {
				if err := c.checkOutboxGap(targetSeq); err != nil {
					return err
				}
			}
			return errNoEventAvailable
		}
		if err := c.checkOutboxGap(targetSeq); err != nil {
			return err
		}
		consumerErrorsTotal.Inc(1)
		return fmt.Errorf("no event at seq %d", targetSeq)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	decision := &consumeDecision{targetSeq: targetSeq, env: env}
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

// maybeBootstrapFromOutboxFloor advances fresh-start processedSeq to the
// outbox floor when historical events were compacted.
func (c *Consumer) maybeBootstrapFromOutboxFloor(targetSeq uint64) bool {
	c.mu.Lock()
	freshStart := !c.hasState && c.processedSeq == ^uint64(0) && targetSeq == 0
	c.mu.Unlock()
	if !freshStart {
		return false
	}

	lowestSeq, err := c.reader.LowestSeq()
	if err != nil || lowestSeq == 0 {
		return false
	}
	latestSeq, err := c.reader.LatestSeq()
	if err != nil || latestSeq < lowestSeq {
		return false
	}

	c.mu.Lock()
	// Re-check freshness in case state advanced while probing RPC.
	if !c.hasState && c.processedSeq == ^uint64(0) {
		c.processedSeq = lowestSeq - 1
		c.mu.Unlock()
		log.Warn("UBT consumer bootstrapped to compacted outbox floor",
			"lowestSeq", lowestSeq, "latestSeq", latestSeq, "nextTarget", lowestSeq)
		return true
	}
	c.mu.Unlock()
	return false
}

func (c *Consumer) checkOutboxGap(targetSeq uint64) error {
	lowestSeq, err := c.reader.LowestSeq()
	if err != nil || lowestSeq == 0 {
		return nil
	}
	if targetSeq >= lowestSeq {
		return nil
	}
	latestSeq, err := c.reader.LatestSeq()
	if err != nil {
		latestSeq = 0
	}
	return &errOutboxGap{
		targetSeq: targetSeq,
		lowestSeq: lowestSeq,
		latestSeq: latestSeq,
	}
}

func (c *Consumer) nextTargetSeq() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Use processedSeq (in-memory) to determine next target, NOT AppliedSeq (durable).
	return c.processedSeq + 1
}

func (c *Consumer) readNextEnvelope(targetSeq uint64) (*ubtemit.OutboxEnvelope, error) {
	c.mu.Lock()
	if len(c.readAheadQueue) > 0 {
		// Queue is sequence-ordered; drop it if it doesn't match next target.
		if c.readAheadNextSeq == targetSeq && c.readAheadQueue[0] != nil && c.readAheadQueue[0].Seq == targetSeq {
			env := c.readAheadQueue[0]
			c.readAheadQueue[0] = nil
			c.readAheadQueue = c.readAheadQueue[1:]
			c.readAheadNextSeq++
			if len(c.readAheadQueue) == 0 {
				c.readAheadNextSeq = 0
			}
			c.mu.Unlock()
			consumerReadQueueHitTotal.Inc(1)
			return env, nil
		}
		c.readAheadQueue = c.readAheadQueue[:0]
		c.readAheadNextSeq = 0
	}
	lag := c.outboxLag
	c.mu.Unlock()

	window := c.readAheadWindow(lag)
	if window <= 1 {
		readStart := time.Now()
		env, err := c.reader.ReadEvent(targetSeq)
		consumerReadEventLatency.UpdateSince(readStart)
		consumerReadRPCEventTotal.Inc(1)
		if err != nil {
			consumerErrorsTotal.Inc(1)
			return nil, fmt.Errorf("read event %d: %w", targetSeq, err)
		}
		return env, nil
	}

	toSeq := targetSeq + window - 1
	if toSeq < targetSeq {
		toSeq = ^uint64(0)
	}
	readStart := time.Now()
	envs, err := c.reader.ReadRange(targetSeq, toSeq)
	consumerReadRangeLatency.UpdateSince(readStart)
	consumerReadRPCRangeTotal.Inc(1)
	if err != nil {
		consumerErrorsTotal.Inc(1)
		return nil, fmt.Errorf("read events [%d,%d]: %w", targetSeq, toSeq, err)
	}
	if len(envs) == 0 {
		return nil, nil
	}
	if envs[0].Seq != targetSeq {
		return nil, nil
	}
	if len(envs) == 1 {
		return envs[0], nil
	}

	c.mu.Lock()
	c.readAheadQueue = c.readAheadQueue[:0]
	c.readAheadQueue = append(c.readAheadQueue, envs[1:]...)
	c.readAheadNextSeq = targetSeq + 1
	if c.readAheadNextSeq == 0 { // overflow guard
		c.readAheadQueue = c.readAheadQueue[:0]
	}
	c.mu.Unlock()
	return envs[0], nil
}

func (c *Consumer) readAheadWindow(lag uint64) uint64 {
	base := c.cfg.OutboxReadAhead
	if base == 0 {
		base = 1
	}
	// Adaptive window sizing based on lag keeps small windows near head
	// and expands during catch-up, while avoiding over-prefetch spikes.
	if lag > 100000 {
		if base < 1024 {
			return 1024
		}
	}
	if lag > 30000 {
		if base < 512 {
			return 512
		}
	}
	if lag > 8000 {
		if base < 256 {
			return 256
		}
	}
	if lag > 2000 {
		if base < 128 {
			return 128
		}
	}
	if lag > 500 {
		if base < 64 {
			return 64
		}
	}
	return base
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
	decodeStart := time.Now()
	diff, err := ubtemit.DecodeDiff(decision.env.Payload)
	consumerDecodeDiffLatency.UpdateSince(decodeStart)
	if err != nil {
		consumerErrorsTotal.Inc(1)
		return fmt.Errorf("decode diff at seq %d: %w", decision.targetSeq, err)
	}
	// Crash-consistency: persist pendingSeq AFTER decode validation but BEFORE apply (plan §12).
	c.markPendingSeq(decision.targetSeq)
	shouldValidateStrict, strictAsync := c.strictValidationPolicy(decision)
	needIntermediateRoot := c.shouldWriteBlockRootIndex(decision.env.BlockNumber) || (strictAsync && shouldValidateStrict)
	applyStart := time.Now()
	var root common.Hash
	if needIntermediateRoot {
		root, err = c.applier.ApplyDiff(diff, decision.env.BlockNumber)
		consumerRootHashComputeTotal.Inc(1)
	} else {
		err = c.applier.ApplyDiffFast(diff, decision.env.BlockNumber)
		consumerRootHashSkipTotal.Inc(1)
	}
	consumerApplyDiffLatency.UpdateSince(applyStart)
	if err != nil {
		consumerErrorsTotal.Inc(1)
		return fmt.Errorf("apply diff at seq %d: %w", decision.targetSeq, err)
	}
	// Collect addresses only when sampled validation is enabled.
	if c.validator != nil && c.cfg.ValidationSampleRate > 0 {
		for _, acct := range diff.Accounts {
			c.recentAddresses = append(c.recentAddresses, acct.Address)
		}
	}
	// Track in-memory state (NOT persisted until commit).
	c.pendingBlock = decision.env.BlockNumber
	c.pendingBlockHash = decision.env.BlockHash
	c.pendingParentHash = decision.env.ParentHash
	c.lastDiff = diff
	if needIntermediateRoot {
		c.pendingRoot = root
		c.pendingBlockRoots = append(c.pendingBlockRoots, pendingBlockRoot{
			block:      decision.env.BlockNumber,
			root:       root,
			blockHash:  decision.env.BlockHash,
			parentHash: decision.env.ParentHash,
		})
	}
	consumerAppliedTotal.Inc(1)
	consumerAppliedLatency.UpdateSince(start)

	// Strict validation: cross-check ALL accounts/storage in diff against MPT.
	// During heavy catch-up, apply sampling to reduce RPC amplification.
	if c.cfg.ValidationStrictMode && c.validator != nil {
		if !shouldValidateStrict {
			return nil
		}
		if strictAsync {
			if needIntermediateRoot {
				c.pendingStrictValidations[decision.env.BlockNumber] = diff
			}
			return nil
		}
		if err := c.validator.ValidateStrict(c.applier.Trie(), decision.env.BlockNumber, diff); err != nil {
			if errors.Is(err, errHistoricalStateUnavailable) {
				log.Debug("Strict validation skipped: historical state unavailable", "block", decision.env.BlockNumber)
				return nil
			}
			if c.cfg.ValidationHaltOnMismatch {
				return &errValidationHalt{err: err}
			}
			log.Error("Strict validation mismatch (continuing)", "block", decision.env.BlockNumber, "err", err)
		}
	}
	return nil
}

func (c *Consumer) strictValidationPolicy(decision *consumeDecision) (shouldValidate bool, strictAsync bool) {
	if !c.cfg.ValidationStrictMode || c.validator == nil {
		return false, false
	}
	shouldValidate = true
	if c.cfg.BackpressureLagThreshold > 0 && c.outboxLag > c.cfg.BackpressureLagThreshold {
		sampleRate := c.cfg.ValidationStrictCatchupSampleRate
		if sampleRate == 0 {
			shouldValidate = false
		} else {
			shouldValidate = decision.env.BlockNumber%sampleRate == 0
		}
	}
	strictAsync = c.cfg.ValidationStrictAsync && !c.cfg.ValidationHaltOnMismatch
	return shouldValidate, strictAsync
}

func (c *Consumer) executeReorgTransition(decision *consumeDecision) error {
	decodeStart := time.Now()
	marker, err := ubtemit.DecodeReorgMarker(decision.env.Payload)
	consumerDecodeReorgLatency.UpdateSince(decodeStart)
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
	// Backpressure mode: cap commit interval/latency while lag is high to bound
	// in-memory trie growth during catch-up.
	if c.cfg.BackpressureLagThreshold > 0 && c.outboxLag > c.cfg.BackpressureLagThreshold {
		commitInterval := c.cfg.ApplyCommitInterval
		const commitIntervalCap = uint64(128)
		if commitInterval > commitIntervalCap {
			commitInterval = commitIntervalCap
		}
		if c.uncommittedBlocks >= commitInterval {
			return true
		}
		maxLatency := c.cfg.ApplyCommitMaxLatency
		const maxLatencyCap = 15 * time.Second
		if maxLatency > maxLatencyCap {
			maxLatency = maxLatencyCap
		}
		if time.Since(c.lastCommitTime) >= maxLatency {
			return true
		}
		return false
	}
	if c.uncommittedBlocks >= c.cfg.ApplyCommitInterval {
		return true
	}
	if time.Since(c.lastCommitTime) >= c.cfg.ApplyCommitMaxLatency {
		return true
	}
	return false
}

// commit commits the current UBT state to disk.
func (c *Consumer) commit() error {
	start := time.Now()
	trieCommitStart := time.Now()
	if err := c.applier.CommitAt(c.pendingBlock); err != nil {
		return err
	}
	consumerCommitTrieLatency.UpdateSince(trieCommitStart)
	committedRoot := c.applier.Root()
	if c.pendingRoot != (common.Hash{}) && committedRoot != c.pendingRoot {
		log.Warn("Committed root differs from pending root; using committed root",
			"pendingRoot", c.pendingRoot, "committedRoot", committedRoot, "block", c.pendingBlock)
	}
	c.pendingRoot = committedRoot

	// NOW update durable state - only after successful trie commit
	c.state.AppliedSeq = c.processedSeq
	c.state.AppliedRoot = committedRoot
	c.state.AppliedBlock = c.pendingBlock
	c.clearPendingMetadata()

	// Batch state + root index writes to reduce write amplification.
	batch := c.db.NewBatch()
	rawdb.WriteUBTConsumerState(batch, c.consumerStateSnapshot())

	wroteFinalRoot := false
	for _, pbr := range c.pendingBlockRoots {
		if !c.shouldWriteBlockRootIndex(pbr.block) {
			continue
		}
		rawdb.WriteUBTBlockRoot(batch, pbr.block, pbr.root)
		rawdb.WriteUBTCanonicalBlock(batch, pbr.block, pbr.blockHash, pbr.parentHash)
		if pbr.block == c.state.AppliedBlock {
			wroteFinalRoot = pbr.root == c.state.AppliedRoot
		}
	}
	// Always write final committed block root/index.
	if !wroteFinalRoot {
		rawdb.WriteUBTBlockRoot(batch, c.state.AppliedBlock, c.state.AppliedRoot)
		rawdb.WriteUBTCanonicalBlock(batch, c.state.AppliedBlock, c.pendingBlockHash, c.pendingParentHash)
	}
	batchWriteStart := time.Now()
	if err := batch.Write(); err != nil {
		return fmt.Errorf("commit batch write: %w", err)
	}
	consumerCommitBatchWriteLatency.UpdateSince(batchWriteStart)
	c.stateDirty = false
	c.lastStatePersistAt = time.Now()
	c.hasState = true

	// Dispatch any queued strict validation tasks for committed block roots.
	if c.validator != nil && c.cfg.ValidationStrictMode && c.cfg.ValidationStrictAsync && !c.cfg.ValidationHaltOnMismatch {
		for _, pbr := range c.pendingBlockRoots {
			diff := c.pendingStrictValidations[pbr.block]
			if diff == nil {
				continue
			}
			c.enqueueStrictValidation(strictValidationTask{
				block: pbr.block,
				root:  pbr.root,
				diff:  diff,
			})
			delete(c.pendingStrictValidations, pbr.block)
		}
	}
	c.pendingBlockRoots = c.pendingBlockRoots[:0]

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

func (c *Consumer) consumerStateSnapshot() *rawdb.UBTConsumerState {
	return &rawdb.UBTConsumerState{
		PendingSeq:       c.state.PendingSeq,
		PendingSeqActive: c.state.PendingSeqActive,
		PendingStatus:    c.state.PendingStatus,
		PendingUpdatedAt: c.state.PendingUpdatedAt,
		AppliedSeq:       c.state.AppliedSeq,
		AppliedRoot:      c.state.AppliedRoot,
		AppliedBlock:     c.state.AppliedBlock,
	}
}

func (c *Consumer) shouldWriteBlockRootIndex(block uint64) bool {
	stride := c.effectiveBlockRootIndexStride()
	if stride <= 1 {
		return true
	}
	return block%stride == 0 || block == c.pendingBlock
}

func (c *Consumer) effectiveBlockRootIndexStride() uint64 {
	baseStride := c.cfg.BlockRootIndexStrideHighLag
	if baseStride <= 1 {
		return 1
	}
	threshold := c.cfg.BackpressureLagThreshold
	if threshold == 0 || c.outboxLag <= threshold {
		return 1
	}
	stride := baseStride
	ratio := c.outboxLag / threshold
	switch {
	case ratio >= 128 && stride < 4096:
		stride = 4096
	case ratio >= 64 && stride < 2048:
		stride = 2048
	case ratio >= 32 && stride < 1024:
		stride = 1024
	case ratio >= 16 && stride < 256:
		stride = 256
	case ratio >= 8 && stride < 128:
		stride = 128
	case ratio >= 4 && stride < 64:
		stride = 64
	}
	return stride
}

func (c *Consumer) enqueueStrictValidation(task strictValidationTask) {
	if c.validationQueue == nil {
		return
	}
	select {
	case c.validationQueue <- task:
	default:
		log.Warn("Strict validation queue full, dropping task", "block", task.block)
	}
}

func (c *Consumer) validationLoop() {
	defer c.validationWG.Done()
	for {
		select {
		case <-c.validationStop:
			return
		case task := <-c.validationQueue:
			tr, err := c.applier.TrieAt(task.root)
			if err != nil {
				log.Warn("Strict validation skipped: failed to open trie at root", "block", task.block, "root", task.root, "err", err)
				continue
			}
			if err := c.validator.ValidateStrict(tr, task.block, task.diff); err != nil {
				if errors.Is(err, errHistoricalStateUnavailable) {
					log.Debug("Strict validation skipped: historical state unavailable", "block", task.block)
					continue
				}
				log.Error("Strict validation mismatch (async)", "block", task.block, "err", err)
			}
		}
	}
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
	// Pending strict validation tasks for reverted blocks are no longer relevant.
	if len(c.pendingStrictValidations) > 0 {
		c.pendingStrictValidations = make(map[uint64]*ubtemit.QueuedDiffV1)
	}

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

func (c *Consumer) restoreToGenesis(targetBlock uint64, reason string) error {
	log.Warn("Falling back to genesis state for recovery", "targetBlock", targetBlock, "reason", reason)
	if err := c.applier.Revert(common.Hash{}); err != nil {
		return fmt.Errorf("revert to genesis root: %w", err)
	}
	c.state.AppliedSeq = 0
	c.state.AppliedRoot = common.Hash{}
	c.state.AppliedBlock = 0
	c.processedSeq = ^uint64(0) // will wrap to 0 on next consume
	c.pendingRoot = common.Hash{}
	c.pendingBlock = 0
	c.pendingBlockHash = common.Hash{}
	c.pendingParentHash = common.Hash{}
	c.uncommittedBlocks = 0
	c.pendingBlockRoots = c.pendingBlockRoots[:0]
	if c.pendingStrictValidations != nil {
		for block := range c.pendingStrictValidations {
			delete(c.pendingStrictValidations, block)
		}
	}
	c.clearPendingMetadata()
	// Mark as fresh start to enable outbox-floor bootstrap when early events are compacted.
	c.hasState = false
	c.persistState()
	daemonSnapshotRestoreTotal.Inc(1)
	log.Info("Restored to genesis state for recovery")
	return nil
}

// restoreFromAnchor finds the best anchor at or below targetBlock and restores from it.
func (c *Consumer) restoreFromAnchor(targetBlock uint64) error {
	start := time.Now()
	count := rawdb.ReadUBTAnchorSnapshotCount(c.db)
	if count == 0 {
		return fmt.Errorf("no anchor snapshots available for recovery")
	}

	// Iterate anchors from latest to oldest and restore the first readable anchor.
	candidates := 0
	for i := int64(count) - 1; i >= 0; i-- {
		snap := rawdb.ReadUBTAnchorSnapshot(c.db, uint64(i))
		if snap == nil {
			continue
		}
		if snap.BlockNumber > targetBlock {
			continue
		}
		candidates++
		// Revert to the candidate anchor root.
		if err := c.applier.Revert(snap.BlockRoot); err != nil {
			log.Warn("Anchor revert failed, trying older anchor",
				"anchorBlock", snap.BlockNumber,
				"anchorRoot", snap.BlockRoot,
				"targetBlock", targetBlock,
				"err", err)
			continue
		}

		// Update consumer state.
		c.state.AppliedSeq = snap.Seq
		c.state.AppliedRoot = snap.BlockRoot
		c.state.AppliedBlock = snap.BlockNumber
		c.processedSeq = snap.Seq
		c.pendingRoot = snap.BlockRoot
		c.pendingBlock = snap.BlockNumber
		c.pendingBlockHash = rawdb.ReadUBTCanonicalBlockHash(c.db, snap.BlockNumber)
		c.pendingParentHash = rawdb.ReadUBTCanonicalParentHash(c.db, snap.BlockNumber)
		c.uncommittedBlocks = 0
		c.hasState = true
		c.persistState()

		daemonSnapshotRestoreTotal.Inc(1)
		daemonSnapshotRestoreLatency.UpdateSince(start)

		log.Info("Restored from anchor snapshot",
			"anchorBlock", snap.BlockNumber,
			"anchorRoot", snap.BlockRoot,
			"anchorSeq", snap.Seq,
			"targetBlock", targetBlock)
		return nil
	}

	// No usable anchor was found.
	if candidates == 0 {
		return fmt.Errorf("no anchor snapshot found at or below target block %d", targetBlock)
	}
	return fmt.Errorf("all candidate anchors failed to open for target block %d", targetBlock)
}

// persistState writes the consumer state to the database.
func (c *Consumer) persistState() {
	rawdb.WriteUBTConsumerState(c.db, c.consumerStateSnapshot())
	c.stateDirty = false
	c.lastStatePersistAt = time.Now()
}

func (c *Consumer) persistStateMaybe(force bool) {
	if force {
		c.persistState()
		return
	}
	if c.cfg == nil {
		c.persistState()
		return
	}
	interval := c.cfg.PendingStatePersistInterval
	if c.cfg.BackpressureLagThreshold > 0 && c.outboxLag > c.cfg.BackpressureLagThreshold {
		ratio := c.outboxLag / c.cfg.BackpressureLagThreshold
		scale := time.Duration(10)
		if ratio >= 20 {
			scale = 25
		} else if ratio >= 10 {
			scale = 20
		}
		interval *= scale
		if interval < 2*time.Second {
			interval = 2 * time.Second
		}
		if interval > 20*time.Second {
			interval = 20 * time.Second
		}
	}
	if interval <= 0 {
		c.persistState()
		return
	}
	if time.Since(c.lastStatePersistAt) >= interval {
		c.persistState()
		return
	}
	c.stateDirty = true
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
	c.persistStateMaybe(false)
}

// clearPendingSeq clears in-flight event markers once the event is fully handled.
func (c *Consumer) clearPendingSeq() {
	if !c.pendingInFlight() && c.state.PendingSeq == 0 {
		return
	}
	c.clearPendingMetadata()
	c.persistStateMaybe(true)
}

// Close closes the consumer and flushes any pending state.
func (c *Consumer) Close() error {
	c.mu.Lock()
	if c.stateDirty {
		c.persistState()
	}
	if c.uncommittedBlocks > 0 {
		if c.safeToCommit() {
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

	if c.validationStop != nil {
		close(c.validationStop)
		c.validationWG.Wait()
	}
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
