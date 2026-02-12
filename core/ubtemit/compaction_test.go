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
	"math/big"
	"os"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

// TestCompact_NoRetention tests that compaction is a no-op when retention is 0.
func TestCompact_NoRetention(t *testing.T) {
	dir, err := os.MkdirTemp("", "ubt-compact-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	// Create store with no retention (0)
	store, err := NewOutboxStore(dir, 5*time.Second, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	// Append 100 events
	for i := uint64(0); i < 100; i++ {
		env := &OutboxEnvelope{
			Version:     EnvelopeVersionV1,
			Kind:        KindDiff,
			BlockNumber: i,
			BlockHash:   common.BigToHash(big.NewInt(int64(i))),
			Payload:     []byte{byte(i)},
		}
		if _, err := store.Append(env); err != nil {
			t.Fatalf("Failed to append event %d: %v", i, err)
		}
	}

	// Compact should be no-op
	count, err := store.Compact()
	if err != nil {
		t.Fatalf("Compact failed: %v", err)
	}
	if count != 0 {
		t.Errorf("Compact should prune 0 events with no retention, got %d", count)
	}

	// Verify all 100 events still exist
	latest := store.LatestSeq()
	if latest != 99 {
		t.Errorf("LatestSeq mismatch: got %d, want 99", latest)
	}

	// Spot check a few events
	for _, seq := range []uint64{0, 50, 99} {
		env, err := store.Read(seq)
		if err != nil {
			t.Errorf("Event %d should still exist: %v", seq, err)
		}
		if env == nil {
			t.Errorf("Event %d should not be nil", seq)
		}
	}
}

// TestCompact_WithRetention tests compaction with retention window.
func TestCompact_WithRetention(t *testing.T) {
	dir, err := os.MkdirTemp("", "ubt-compact-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	// Create store with retention window of 50
	store, err := NewOutboxStore(dir, 5*time.Second, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	// Append 100 events
	for i := uint64(0); i < 100; i++ {
		env := &OutboxEnvelope{
			Version:     EnvelopeVersionV1,
			Kind:        KindDiff,
			BlockNumber: i,
			BlockHash:   common.BigToHash(big.NewInt(int64(i))),
			Payload:     []byte{byte(i)},
		}
		if _, err := store.Append(env); err != nil {
			t.Fatalf("Failed to append event %d: %v", i, err)
		}
	}

	// Compact should prune events 0-49 (50 events)
	count, err := store.Compact()
	if err != nil {
		t.Fatalf("Compact failed: %v", err)
	}
	if count != 50 {
		t.Errorf("Compact should prune 50 events, got %d", count)
	}

	// Verify events 0-49 are gone
	for seq := uint64(0); seq < 50; seq++ {
		env, err := store.Read(seq)
		if err == nil || env != nil {
			t.Errorf("Event %d should be pruned", seq)
		}
	}

	// Verify events 50-99 still exist
	for seq := uint64(50); seq < 100; seq++ {
		env, err := store.Read(seq)
		if err != nil {
			t.Errorf("Event %d should still exist: %v", seq, err)
		}
		if env == nil {
			t.Errorf("Event %d should not be nil", seq)
		}
		if env.BlockNumber != seq {
			t.Errorf("Event %d block number mismatch: got %d, want %d", seq, env.BlockNumber, seq)
		}
	}

	// Latest seq should still be 99
	latest := store.LatestSeq()
	if latest != 99 {
		t.Errorf("LatestSeq mismatch: got %d, want 99", latest)
	}
}

// TestCompact_EmptyStore tests compaction on an empty store.
func TestCompact_EmptyStore(t *testing.T) {
	dir, err := os.MkdirTemp("", "ubt-compact-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	// Create store with retention
	store, err := NewOutboxStore(dir, 5*time.Second, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	// Compact on empty store should be no-op
	count, err := store.Compact()
	if err != nil {
		t.Fatalf("Compact failed: %v", err)
	}
	if count != 0 {
		t.Errorf("Compact should prune 0 events on empty store, got %d", count)
	}

	// Latest seq should be 0
	latest := store.LatestSeq()
	if latest != 0 {
		t.Errorf("LatestSeq mismatch: got %d, want 0", latest)
	}
}

// TestCompact_RetentionLargerThanStore tests that nothing is pruned when retention > total events.
func TestCompact_RetentionLargerThanStore(t *testing.T) {
	dir, err := os.MkdirTemp("", "ubt-compact-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	// Create store with retention window of 100
	store, err := NewOutboxStore(dir, 5*time.Second, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	// Append only 50 events
	for i := uint64(0); i < 50; i++ {
		env := &OutboxEnvelope{
			Version:     EnvelopeVersionV1,
			Kind:        KindDiff,
			BlockNumber: i,
			BlockHash:   common.BigToHash(big.NewInt(int64(i))),
			Payload:     []byte{byte(i)},
		}
		if _, err := store.Append(env); err != nil {
			t.Fatalf("Failed to append event %d: %v", i, err)
		}
	}

	// Compact should prune 0 events (retention 100 > total 50)
	count, err := store.Compact()
	if err != nil {
		t.Fatalf("Compact failed: %v", err)
	}
	if count != 0 {
		t.Errorf("Compact should prune 0 events when retention > total, got %d", count)
	}

	// Verify all 50 events still exist
	for seq := uint64(0); seq < 50; seq++ {
		env, err := store.Read(seq)
		if err != nil {
			t.Errorf("Event %d should still exist: %v", seq, err)
		}
		if env == nil {
			t.Errorf("Event %d should not be nil", seq)
		}
	}

	// Latest seq should be 49
	latest := store.LatestSeq()
	if latest != 49 {
		t.Errorf("LatestSeq mismatch: got %d, want 49", latest)
	}
}

// TestCompact_MultipleRounds tests that compaction can be called multiple times.
func TestCompact_MultipleRounds(t *testing.T) {
	dir, err := os.MkdirTemp("", "ubt-compact-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	// Create store with retention window of 30
	store, err := NewOutboxStore(dir, 5*time.Second, 30, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	// Append 50 events
	for i := uint64(0); i < 50; i++ {
		env := &OutboxEnvelope{
			Version:     EnvelopeVersionV1,
			Kind:        KindDiff,
			BlockNumber: i,
			BlockHash:   common.BigToHash(big.NewInt(int64(i))),
			Payload:     []byte{byte(i)},
		}
		if _, err := store.Append(env); err != nil {
			t.Fatalf("Failed to append event %d: %v", i, err)
		}
	}

	// First compact: should prune 0-19 (20 events, keep 30-49)
	count1, err := store.Compact()
	if err != nil {
		t.Fatalf("First compact failed: %v", err)
	}
	if count1 != 20 {
		t.Errorf("First compact should prune 20 events, got %d", count1)
	}

	// Append 20 more events (50-69)
	for i := uint64(50); i < 70; i++ {
		env := &OutboxEnvelope{
			Version:     EnvelopeVersionV1,
			Kind:        KindDiff,
			BlockNumber: i,
			BlockHash:   common.BigToHash(big.NewInt(int64(i))),
			Payload:     []byte{byte(i)},
		}
		if _, err := store.Append(env); err != nil {
			t.Fatalf("Failed to append event %d: %v", i, err)
		}
	}

	// Second compact: lowestSeq is now 20, nextSeq is 70, retention is 30
	// So oldestToKeep = 70-30 = 40, prune [20, 39] = 20 events
	count2, err := store.Compact()
	if err != nil {
		t.Fatalf("Second compact failed: %v", err)
	}
	if count2 != 20 {
		t.Errorf("Second compact should prune 20 events (20-39), got %d", count2)
	}

	// Verify events 0-39 are gone
	for seq := uint64(0); seq < 40; seq++ {
		env, err := store.Read(seq)
		if err == nil || env != nil {
			t.Errorf("Event %d should be pruned", seq)
		}
	}

	// Verify events 40-69 still exist
	for seq := uint64(40); seq < 70; seq++ {
		env, err := store.Read(seq)
		if err != nil {
			t.Errorf("Event %d should still exist: %v", seq, err)
		}
		if env == nil {
			t.Errorf("Event %d should not be nil", seq)
		}
	}
}

// TestAutoCompact tests that automatic compaction triggers every 1000 appends.
func TestAutoCompact(t *testing.T) {
	dir, err := os.MkdirTemp("", "ubt-compact-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	// Create store with retention window of 500
	store, err := NewOutboxStore(dir, 5*time.Second, 500, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	// Append 1500 events (should trigger auto-compact at seq 1000)
	for i := uint64(0); i < 1500; i++ {
		env := &OutboxEnvelope{
			Version:     EnvelopeVersionV1,
			Kind:        KindDiff,
			BlockNumber: i,
			BlockHash:   common.BigToHash(big.NewInt(int64(i))),
			Payload:     []byte{byte(i % 256)},
		}
		if _, err := store.Append(env); err != nil {
			t.Fatalf("Failed to append event %d: %v", i, err)
		}
	}

	// After 1500 appends, events 0-499 should be gone (auto-compact at seq 1000)
	// and events 500-999 should also be gone (auto-compact triggered again at seq 1000 with retention=500)
	// Actually, auto-compact at seq 1000 prunes 0-499, keeping 500-999
	// No more auto-compact until seq 2000

	// Events 0-999 should be gone (after reaching seq 1500, keep last 500)
	// Wait, let me recalculate:
	// - At seq 1000: nextSeq=1000, keep last 500, so prune 0-499
	// - At seq 1500: no auto-compact (only triggers at multiples of 1000)
	// So after 1500 appends, manual compact would prune 0-999, but auto-compact only ran at seq 1000

	// Let me verify by checking events
	// Events 0-499 should be gone (pruned at seq 1000)
	for seq := uint64(0); seq < 500; seq++ {
		env, err := store.Read(seq)
		if err == nil || env != nil {
			t.Errorf("Event %d should be pruned by auto-compact", seq)
		}
	}

	// Events 500-1499 should still exist
	for seq := uint64(500); seq < 1500; seq++ {
		env, err := store.Read(seq)
		if err != nil {
			t.Errorf("Event %d should still exist: %v", seq, err)
		}
		if env == nil {
			t.Errorf("Event %d should not be nil", seq)
		}
	}
}

// TestCompact_ExactBoundary tests compaction at exact retention boundary.
func TestCompact_ExactBoundary(t *testing.T) {
	dir, err := os.MkdirTemp("", "ubt-compact-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	// Create store with retention window of 10
	store, err := NewOutboxStore(dir, 5*time.Second, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	// Append exactly 10 events
	for i := uint64(0); i < 10; i++ {
		env := &OutboxEnvelope{
			Version:     EnvelopeVersionV1,
			Kind:        KindDiff,
			BlockNumber: i,
			BlockHash:   common.BigToHash(big.NewInt(int64(i))),
			Payload:     []byte{byte(i)},
		}
		if _, err := store.Append(env); err != nil {
			t.Fatalf("Failed to append event %d: %v", i, err)
		}
	}

	// Compact should prune 0 events (nextSeq=10, retention=10, so keep all)
	count, err := store.Compact()
	if err != nil {
		t.Fatalf("Compact failed: %v", err)
	}
	if count != 0 {
		t.Errorf("Compact should prune 0 events at exact boundary, got %d", count)
	}

	// Append one more event (total 11)
	env := &OutboxEnvelope{
		Version:     EnvelopeVersionV1,
		Kind:        KindDiff,
		BlockNumber: 10,
		BlockHash:   common.BigToHash(big.NewInt(10)),
		Payload:     []byte{10},
	}
	if _, err := store.Append(env); err != nil {
		t.Fatalf("Failed to append event 10: %v", err)
	}

	// Now compact should prune event 0 (nextSeq=11, keep 1-10)
	count, err = store.Compact()
	if err != nil {
		t.Fatalf("Compact failed: %v", err)
	}
	if count != 1 {
		t.Errorf("Compact should prune 1 event, got %d", count)
	}

	// Verify event 0 is gone
	env0, err := store.Read(0)
	if err == nil || env0 != nil {
		t.Errorf("Event 0 should be pruned")
	}

	// Verify events 1-10 still exist
	for seq := uint64(1); seq <= 10; seq++ {
		envSeq, err := store.Read(seq)
		if err != nil {
			t.Errorf("Event %d should still exist: %v", seq, err)
		}
		if envSeq == nil {
			t.Errorf("Event %d should not be nil", seq)
		}
	}
}
