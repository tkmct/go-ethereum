// Copyright 2024 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package ubtemit

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/ethdb/leveldb"
	"github.com/ethereum/go-ethereum/log"
)

// ErrEventNotFound is returned when an outbox event does not exist at the requested sequence.
var ErrEventNotFound = errors.New("outbox event not found")

// OutboxStore manages the dedicated outbox database for UBT conversion events.
type OutboxStore struct {
	db           ethdb.Database // Dedicated outbox database (NOT chainDB)
	mu           sync.Mutex
	nextSeq      uint64 // Next sequence number to assign
	lowestSeq    uint64 // Lowest non-deleted sequence for incremental compaction
	writeTimeout time.Duration

	// Retention settings
	retentionSeqWindow uint64 // Keep last N sequences (0 = unlimited)

	// Disk budget enforcement (§19 R44/R45)
	diskBudgetBytes     uint64 // 0 = unlimited
	cumulativeDiskUsage uint64 // Track total bytes written
}

// NewOutboxStore opens or creates the dedicated outbox database.
func NewOutboxStore(path string, writeTimeout time.Duration, retentionWindow uint64, diskBudgetBytes uint64) (*OutboxStore, error) {
	// Open a LevelDB at the given path with reasonable defaults
	// cache: 256 MB, handles: 256, namespace: "ubtoutbox", readonly: false
	kvdb, err := leveldb.New(path, 256, 256, "ubtoutbox", false)
	if err != nil {
		return nil, fmt.Errorf("failed to open outbox database at %s: %w", path, err)
	}

	// Wrap the key-value store as a database
	db := rawdb.NewDatabase(kvdb)

	// Read the current sequence counter to initialize nextSeq
	nextSeq := rawdb.ReadUBTOutboxSeqCounter(db)
	lowestSeq := rawdb.ReadUBTOutboxLowestSeq(db)
	diskUsage := rawdb.ReadUBTOutboxDiskUsage(db)

	log.Info("Opened UBT outbox store", "path", path, "nextSeq", nextSeq, "lowestSeq", lowestSeq, "diskUsage", diskUsage)

	return &OutboxStore{
		db:                  db,
		nextSeq:             nextSeq,
		lowestSeq:           lowestSeq,
		cumulativeDiskUsage: diskUsage,
		writeTimeout:        writeTimeout,
		retentionSeqWindow:  retentionWindow,
		diskBudgetBytes:     diskBudgetBytes,
	}, nil
}

// Append durably writes an event envelope to the outbox and advances the sequence counter.
// Returns the assigned sequence number.
func (s *OutboxStore) Append(env *OutboxEnvelope) (uint64, error) {
	start := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()

	// Assign sequence number
	seq := s.nextSeq
	env.Seq = seq

	// Check for sequence counter overflow before writing anything
	if s.nextSeq == ^uint64(0) {
		return 0, fmt.Errorf("outbox sequence counter overflow")
	}

	// Encode the envelope
	data, err := EncodeEnvelope(env)
	if err != nil {
		return 0, fmt.Errorf("failed to encode envelope: %w", err)
	}

	// Atomically write event + sequence counter to prevent desynchronization on crash
	if err := rawdb.WriteUBTOutboxEventAtomic(s.db, seq, data, s.nextSeq+1); err != nil {
		return 0, err
	}

	// Only update in-memory counter after successful atomic write
	s.nextSeq++

	// Update metrics
	outboxAppendLatency.UpdateSince(start)
	s.cumulativeDiskUsage += uint64(len(data))
	outboxDiskUsage.Update(int64(s.cumulativeDiskUsage))

	// Persist disk usage periodically to avoid excessive writes
	if s.nextSeq%100 == 0 {
		rawdb.WriteUBTOutboxDiskUsage(s.db, s.cumulativeDiskUsage)
	}

	// §19 R44/R45: High-watermark protection — warn when approaching disk budget
	if s.diskBudgetBytes > 0 && s.cumulativeDiskUsage > s.diskBudgetBytes {
		log.Error("UBT outbox disk budget exceeded",
			"usage", s.cumulativeDiskUsage, "budget", s.diskBudgetBytes,
			"seq", seq)
		// Note: we do NOT reject writes — canonical import must not be blocked (§7 R20).
		// The emitter service handles degradation gracefully.
	} else if s.diskBudgetBytes > 0 {
		alertThreshold := s.diskBudgetBytes * 80 / 100
		if s.cumulativeDiskUsage >= alertThreshold {
			log.Warn("UBT outbox approaching disk budget",
				"usage", s.cumulativeDiskUsage, "budget", s.diskBudgetBytes,
				"pct", s.cumulativeDiskUsage*100/s.diskBudgetBytes)
		}
	}

	// Trigger periodic compaction (every 1000 appends)
	if s.nextSeq%1000 == 0 {
		s.compactLocked()
	}

	return seq, nil
}

// compactLocked prunes events older than the retention window.
// Caller must hold s.mu.
func (s *OutboxStore) compactLocked() {
	if s.retentionSeqWindow == 0 || s.nextSeq <= s.retentionSeqWindow {
		return
	}

	oldestToKeep := s.nextSeq - s.retentionSeqWindow

	if oldestToKeep <= s.lowestSeq {
		return
	}
	count, err := rawdb.DeleteUBTOutboxEventRange(s.db, s.lowestSeq, oldestToKeep-1)
	if err != nil {
		log.Error("UBT outbox auto-compact failed", "err", err, "oldestToKeep", oldestToKeep)
		return
	}

	if count > 0 {
		s.lowestSeq = oldestToKeep
		rawdb.WriteUBTOutboxLowestSeq(s.db, s.lowestSeq)
		outboxCompactedTotal.Inc(int64(count))
		log.Debug("UBT outbox auto-compact", "pruned", count, "oldestKept", oldestToKeep)
	}
}

// Compact prunes events older than the retention window.
// Returns the number of pruned events.
func (s *OutboxStore) Compact() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.retentionSeqWindow == 0 || s.nextSeq == 0 {
		return 0, nil // No retention configured or empty outbox
	}

	// Calculate the oldest seq to keep
	oldestToKeep := uint64(0)
	if s.nextSeq > s.retentionSeqWindow {
		oldestToKeep = s.nextSeq - s.retentionSeqWindow
	}

	if oldestToKeep == 0 {
		return 0, nil // Nothing to prune
	}

	if oldestToKeep <= s.lowestSeq {
		return 0, nil // Already compacted past this point
	}

	// Use the existing rawdb delete range function
	count, err := rawdb.DeleteUBTOutboxEventRange(s.db, s.lowestSeq, oldestToKeep-1)
	if err != nil {
		return count, fmt.Errorf("compaction failed: %w", err)
	}

	if count > 0 {
		s.lowestSeq = oldestToKeep
		rawdb.WriteUBTOutboxLowestSeq(s.db, s.lowestSeq)
		outboxCompactedTotal.Inc(int64(count))
		log.Info("UBT outbox compacted", "pruned", count, "oldestKept", oldestToKeep)
	}
	return count, nil
}

// Read retrieves an outbox event by sequence number.
// Returns ErrEventNotFound if the event does not exist.
func (s *OutboxStore) Read(seq uint64) (*OutboxEnvelope, error) {
	data, err := rawdb.ReadUBTOutboxEvent(s.db, seq)
	if err != nil {
		// Distinguish "not found" from real I/O errors using Has().
		has, hasErr := rawdb.HasUBTOutboxEvent(s.db, seq)
		if hasErr == nil && !has {
			return nil, ErrEventNotFound
		}
		return nil, fmt.Errorf("read outbox event seq=%d: %w", seq, err)
	}
	if len(data) == 0 {
		return nil, ErrEventNotFound
	}

	env, err := DecodeEnvelope(data)
	if err != nil {
		return nil, fmt.Errorf("failed to decode envelope at seq %d: %w", seq, err)
	}

	return env, nil
}

// ReadRange retrieves outbox events in [fromSeq, toSeq].
func (s *OutboxStore) ReadRange(fromSeq, toSeq uint64) ([]*OutboxEnvelope, error) {
	if fromSeq > toSeq {
		return nil, fmt.Errorf("invalid range: fromSeq (%d) > toSeq (%d)", fromSeq, toSeq)
	}

	dataSlice, err := rawdb.ReadUBTOutboxEvents(s.db, fromSeq, toSeq)
	if err != nil {
		return nil, fmt.Errorf("read outbox events [%d, %d]: %w", fromSeq, toSeq, err)
	}
	result := make([]*OutboxEnvelope, 0, len(dataSlice))

	for i, data := range dataSlice {
		env, err := DecodeEnvelope(data)
		if err != nil {
			return nil, fmt.Errorf("failed to decode envelope at index %d in range [%d, %d]: %w", i, fromSeq, toSeq, err)
		}
		result = append(result, env)
	}

	return result, nil
}

// LatestSeq returns the last written sequence number (nextSeq - 1, or 0 if empty).
func (s *OutboxStore) LatestSeq() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.latestSeqLocked()
}

// LowestSeq returns the lowest retained sequence number.
func (s *OutboxStore) LowestSeq() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lowestSeq
}

func (s *OutboxStore) latestSeqLocked() uint64 {
	if s.nextSeq == 0 {
		return 0
	}
	return s.nextSeq - 1
}

// CompactBelow deletes outbox events with sequence numbers below safeSeq.
// This is used for coordinated compaction with the consumer daemon.
// Returns the number of deleted events.
func (s *OutboxStore) CompactBelow(safeSeq uint64) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if safeSeq == 0 {
		return 0, nil
	}
	latestSeq := s.latestSeqLocked()
	// Allow safeSeq == latest+1, which means "compact everything currently persisted".
	// This keeps compaction semantics aligned with "below safeSeq" boundaries.
	if err := ValidateCompactBelowBounds(safeSeq, latestSeq); err != nil {
		return 0, fmt.Errorf("compact below seq %d: %w", safeSeq, err)
	}

	if safeSeq <= s.lowestSeq {
		return 0, nil // Already compacted past this point
	}
	count, err := rawdb.DeleteUBTOutboxEventRange(s.db, s.lowestSeq, safeSeq-1)
	if err != nil {
		return count, fmt.Errorf("compact below seq %d: %w", safeSeq, err)
	}

	if count > 0 {
		s.lowestSeq = safeSeq
		rawdb.WriteUBTOutboxLowestSeq(s.db, s.lowestSeq)
		outboxCompactedTotal.Inc(int64(count))
		log.Info("UBT outbox compacted below safe seq", "safeSeq", safeSeq, "pruned", count)
	}
	return count, nil
}

// PersistFailureCheckpoint writes the last failure info to the outbox DB for restart diagnostics.
func (s *OutboxStore) PersistFailureCheckpoint(blockNumber uint64, failErr error) {
	rawdb.WriteUBTFailureCheckpoint(s.db, blockNumber, failErr.Error())
}

// ReadFailureCheckpoint returns the latest emitter failure checkpoint.
func (s *OutboxStore) ReadFailureCheckpoint() *rawdb.UBTFailureCheckpoint {
	s.mu.Lock()
	defer s.mu.Unlock()
	return rawdb.ReadUBTFailureCheckpoint(s.db)
}

// ClearFailureCheckpoint deletes the latest emitter failure checkpoint.
func (s *OutboxStore) ClearFailureCheckpoint() {
	s.mu.Lock()
	defer s.mu.Unlock()
	rawdb.DeleteUBTFailureCheckpoint(s.db)
}

// Close closes the outbox database.
func (s *OutboxStore) Close() error {
	return s.db.Close()
}
