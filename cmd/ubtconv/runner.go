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
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/log"
)

// Runner manages the daemon lifecycle.
type Runner struct {
	cfg         *Config
	consumer    *Consumer
	queryServer *QueryServer

	stopCh          chan struct{}
	wg              sync.WaitGroup
	mu              sync.Mutex
	running         bool
	lastPrunedBlock uint64 // Track block root pruning progress
}

// NewRunner creates a new daemon runner.
func NewRunner(cfg *Config) (*Runner, error) {
	consumer, err := NewConsumer(cfg)
	if err != nil {
		return nil, err
	}

	return &Runner{
		cfg:      cfg,
		consumer: consumer,
		stopCh:   make(chan struct{}),
	}, nil
}

// Start starts the daemon runner.
func (r *Runner) Start() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.running {
		return fmt.Errorf("already running")
	}
	r.running = true

	// Start query server if enabled
	if r.cfg.QueryRPCEnabled {
		qs, err := NewQueryServer(r.cfg.QueryRPCListenAddr, r.consumer)
		if err != nil {
			return fmt.Errorf("failed to start query server: %w", err)
		}
		r.queryServer = qs
	}

	r.wg.Add(1)
	go r.loop()

	// Start compaction coordinator
	r.wg.Add(1)
	go r.compactionLoop()

	return nil
}

// Stop stops the daemon runner.
func (r *Runner) Stop() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.running {
		return nil
	}
	close(r.stopCh)
	r.wg.Wait()
	r.running = false

	// Stop query server if running
	if r.queryServer != nil {
		if err := r.queryServer.Close(); err != nil {
			log.Error("Failed to close query server", "err", err)
		}
	}

	return r.consumer.Close()
}

// loop is the main event consumption loop.
func (r *Runner) loop() {
	defer r.wg.Done()

	log.Info("UBT consumer loop started", "appliedSeq", r.consumer.state.AppliedSeq)

	backoff := time.Second
	maxBackoff := 30 * time.Second
	lagCheckInterval := 30 * time.Second
	lastLagCheck := time.Now()

	for {
		select {
		case <-r.stopCh:
			log.Info("UBT consumer loop stopped")
			return
		default:
			consumeErr := r.consumer.ConsumeNext()

			if consumeErr != nil {
				// Check for fatal validation halt — stop the daemon, don't retry
				var haltErr *errValidationHalt
				if errors.As(consumeErr, &haltErr) {
					log.Crit("UBT daemon halted due to validation mismatch", "err", consumeErr)
					return
				}
				var manualErr *errReorgManualRequired
				if errors.As(consumeErr, &manualErr) {
					log.Crit("UBT reorg requires manual intervention", "err", consumeErr,
						"action", "Check logs, reset state if needed, or increase --max-recoverable-reorg-depth")
					return
				}
				var replayErr *errReorgReplayRequired
				if errors.As(consumeErr, &replayErr) {
					log.Crit("UBT reorg requires archive replay", "err", consumeErr,
						"action", "Restart with --require-archive-replay=true pointing to an archive node")
					return
				}
				// Log and backoff on transient error
				log.Debug("UBT consume backoff", "err", consumeErr, "backoff", backoff)
				consumerBackoffGauge.Update(backoff.Milliseconds())
				select {
				case <-r.stopCh:
					return
				case <-time.After(backoff):
				}
				// Exponential backoff
				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
			} else {
				// Reset backoff on success
				backoff = time.Second
				consumerBackoffGauge.Update(0)

				// Periodically refresh outbox lag for backpressure
				if time.Since(lastLagCheck) >= lagCheckInterval {
					r.refreshOutboxLag()
					lastLagCheck = time.Now()
				}
			}
		}
	}
}

// refreshOutboxLag queries latest outbox seq and updates lag:
// outboxLag = max(latestSeq-processedSeq, 0).
func (r *Runner) refreshOutboxLag() {
	latestSeq, err := r.consumer.reader.LatestSeq()
	if err != nil {
		log.Debug("Failed to fetch latest seq for lag check", "err", err)
		return
	}
	r.consumer.mu.Lock()
	processed := r.consumer.processedSeq
	if latestSeq > processed {
		r.consumer.outboxLag = latestSeq - processed
	} else {
		r.consumer.outboxLag = 0
	}
	consumerLagSeq.Update(int64(r.consumer.outboxLag))
	consumerQueueDepth.Update(int64(r.consumer.outboxLag))
	r.consumer.mu.Unlock()
}

// pruneStaleBlockRoots removes block→ubtRoot mappings outside the recovery window.
func (r *Runner) pruneStaleBlockRoots() {
	r.consumer.mu.Lock()
	appliedBlock := r.consumer.state.AppliedBlock
	r.consumer.mu.Unlock()

	if appliedBlock <= r.cfg.TrieDBStateHistory {
		return
	}
	pruneBelow := appliedBlock - r.cfg.TrieDBStateHistory
	if pruneBelow <= r.lastPrunedBlock {
		return
	}

	// Prune in batches to avoid holding locks too long
	pruned := 0
	for block := r.lastPrunedBlock; block < pruneBelow; block++ {
		rawdb.DeleteUBTBlockRoot(r.consumer.db, block)
		rawdb.DeleteUBTCanonicalBlock(r.consumer.db, block)
		pruned++
	}
	r.lastPrunedBlock = pruneBelow
	if pruned > 0 {
		log.Debug("Pruned stale block roots", "pruned", pruned, "belowBlock", pruneBelow)
	}
}

const (
	compactionInterval     = 30 * time.Second
	compactionSafetyMargin = uint64(64) // keep 64 seqs of safety margin
)

// compactionLoop periodically triggers outbox compaction via geth RPC.
func (r *Runner) compactionLoop() {
	defer r.wg.Done()

	ticker := time.NewTicker(compactionInterval)
	defer ticker.Stop()

	var lastCompactedBelow uint64

	for {
		select {
		case <-r.stopCh:
			return
		case <-ticker.C:
			r.tryCompaction(&lastCompactedBelow)
			r.pruneStaleBlockRoots()

			// Trigger slot index pruning if safe
			r.consumer.mu.Lock()
			if r.consumer.applier != nil && r.consumer.applier.SlotIndex() != nil {
				r.consumer.applier.SlotIndex().PruneIfSafe(
					r.consumer.state.AppliedBlock,
					r.cfg.TrieDBStateHistory,
				)
			}
			r.consumer.mu.Unlock()
		}
	}
}

// tryCompaction attempts to compact outbox events below a safe threshold.
func (r *Runner) tryCompaction(lastCompactedBelow *uint64) {
	compactionAttemptsTotal.Inc(1)

	r.consumer.mu.Lock()
	safeSeq := r.consumer.state.AppliedSeq
	r.consumer.mu.Unlock()

	if safeSeq <= compactionSafetyMargin {
		return // Not enough events to compact
	}

	compactBelow := safeSeq - compactionSafetyMargin
	if compactBelow <= *lastCompactedBelow {
		return // Already compacted up to this point
	}

	// Compact when the gap exceeds the commit interval
	if compactBelow > *lastCompactedBelow+r.cfg.ApplyCommitInterval {
		if err := r.callCompactOutbox(compactBelow); err != nil {
			compactionErrorsTotal.Inc(1)
			log.Debug("Compaction RPC failed (non-fatal)", "err", err, "target", compactBelow)
			return
		}
		*lastCompactedBelow = compactBelow
		compactionSuccessTotal.Inc(1)
		compactionLastRunGauge.Update(time.Now().Unix())
		log.Info("Outbox compaction triggered", "compactBelow", compactBelow, "safeSeq", safeSeq)
	}
}

// callCompactOutbox invokes the ubt_compactOutboxBelow RPC on geth.
func (r *Runner) callCompactOutbox(belowSeq uint64) error {
	client, err := r.consumer.reader.getClient()
	if err != nil {
		return fmt.Errorf("get RPC client: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var result map[string]any
	if err := client.CallContext(ctx, &result, "ubt_compactOutboxBelow", hexutil.Uint64(belowSeq)); err != nil {
		return err
	}
	return nil
}
