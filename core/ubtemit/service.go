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
	"sync"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/log"
)

// Service is the geth-side UBT emitter that persists canonical diffs to the outbox.
// It is designed to be non-blocking: failures are recorded but never block canonical block import.
type Service struct {
	store    *OutboxStore
	degraded atomic.Bool // Set when emitter encounters persistent failures

	mu     sync.Mutex
	closed bool
}

// NewService creates a new emitter service with the given outbox store.
func NewService(store *OutboxStore) *Service {
	return &Service{
		store: store,
	}
}

// EmitDiff appends a canonical state diff to the outbox.
// This must be called from blockchain.go after CommitWithUpdate returns.
// On failure, the emitter enters degraded mode but does NOT return an error
// to the caller (canonical import must not be blocked).
func (s *Service) EmitDiff(blockNumber uint64, blockHash, parentHash common.Hash, diff *QueuedDiffV1) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return
	}

	start := time.Now()

	// Encode the diff
	payload, err := EncodeDiff(diff)
	if err != nil {
		s.handleFailure("encode diff", blockNumber, err)
		return
	}

	// Track deleted accounts
	emitterDeletedAccounts.Inc(int64(diff.DeletedAccountCount()))

	// Create envelope
	env := &OutboxEnvelope{
		Version:     EnvelopeVersionV1,
		Kind:        KindDiff,
		BlockNumber: blockNumber,
		BlockHash:   blockHash,
		ParentHash:  parentHash,
		Timestamp:   uint64(start.Unix()),
		Payload:     payload,
	}

	// Append to outbox
	seq, err := s.store.Append(env)
	if err != nil {
		s.handleFailure("append outbox", blockNumber, err)
		return
	}

	emitterAppendLatency.UpdateSince(start)

	// Clear degraded if we successfully wrote
	if s.degraded.Load() {
		s.degraded.Store(false)
		emitterDegradedGauge.Update(0)
		log.Info("UBT emitter recovered from degraded state", "seq", seq, "block", blockNumber)
	}

	log.Debug("UBT diff emitted", "seq", seq, "block", blockNumber, "hash", blockHash)
}

// EmitReorg appends a reorg marker to the outbox.
// Must be called from blockchain.reorg() BEFORE any new-branch diffs are emitted.
func (s *Service) EmitReorg(marker *ReorgMarkerV1) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return
	}

	start := time.Now()

	// Encode the reorg marker
	payload, err := EncodeReorgMarker(marker)
	if err != nil {
		s.handleFailure("encode reorg marker", marker.FromBlockNumber, err)
		return
	}

	// Create envelope
	env := &OutboxEnvelope{
		Version:     EnvelopeVersionV1,
		Kind:        KindReorg,
		BlockNumber: marker.FromBlockNumber,
		BlockHash:   marker.FromBlockHash,
		ParentHash:  common.Hash{}, // No parent hash for reorg markers
		Timestamp:   uint64(start.Unix()),
		Payload:     payload,
	}

	// Append to outbox
	seq, err := s.store.Append(env)
	if err != nil {
		s.handleFailure("append outbox", marker.FromBlockNumber, err)
		return
	}

	emitterAppendLatency.UpdateSince(start)

	// Clear degraded if we successfully wrote
	if s.degraded.Load() {
		s.degraded.Store(false)
		emitterDegradedGauge.Update(0)
		log.Info("UBT emitter recovered from degraded state", "seq", seq, "from", marker.FromBlockNumber, "to", marker.ToBlockNumber)
	}

	log.Info("UBT reorg marker emitted", "seq", seq, "from", marker.FromBlockNumber, "to", marker.ToBlockNumber)
}

// MarkRawKeyFailure records a raw storage key unavailability as an invariant violation.
// The emitter enters degraded mode but canonical import is NOT blocked (§7 R20).
func (s *Service) MarkRawKeyFailure(blockNumber uint64, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return
	}

	s.degraded.Store(true)
	emitterDegradedTotal.Inc(1)
	emitterDegradedGauge.Update(1)
	emitterRawKeyFailures.Inc(1)
	emitterAppendErrors.Inc(1)
	log.Error("UBT invariant violation: raw storage key unavailable", "block", blockNumber, "err", err)

	// Persist failure checkpoint for diagnostics and restart awareness
	s.store.PersistFailureCheckpoint(blockNumber, err)
}

// IsDegraded returns whether the emitter is in degraded mode.
func (s *Service) IsDegraded() bool {
	return s.degraded.Load()
}

// Close stops the emitter service.
func (s *Service) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return s.store.Close()
}

func (s *Service) handleFailure(op string, blockNumber uint64, err error) {
	s.degraded.Store(true)
	emitterDegradedTotal.Inc(1)
	emitterDegradedGauge.Update(1)
	emitterAppendErrors.Inc(1)
	log.Error("UBT emitter failure", "op", op, "block", blockNumber, "err", err)
}
