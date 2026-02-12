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
	"math/big"
	"os"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
)

func TestOutboxStore_AppendAndRead(t *testing.T) {
	store, dir := newTestOutboxStore(t)
	defer os.RemoveAll(dir)
	defer store.Close()

	env := &OutboxEnvelope{
		Version:     EnvelopeVersionV1,
		Kind:        KindDiff,
		BlockNumber: 42,
		BlockHash:   common.HexToHash("0xdead"),
		ParentHash:  common.HexToHash("0xbeef"),
		Timestamp:   1000,
		Payload:     []byte{1, 2, 3},
	}

	seq, err := store.Append(env)
	if err != nil {
		t.Fatal(err)
	}
	if seq != 0 {
		t.Fatalf("expected seq 0, got %d", seq)
	}
	if env.Seq != 0 {
		t.Fatalf("expected env.Seq set to 0, got %d", env.Seq)
	}

	// Read back
	got, err := store.Read(0)
	if err != nil {
		t.Fatal(err)
	}
	if got.BlockNumber != 42 {
		t.Fatal("block number mismatch")
	}
	if got.Kind != KindDiff {
		t.Fatal("kind mismatch")
	}
}

func TestOutboxStore_ReadRange(t *testing.T) {
	store, dir := newTestOutboxStore(t)
	defer os.RemoveAll(dir)
	defer store.Close()

	// Append 5 events
	for i := uint64(0); i < 5; i++ {
		store.Append(&OutboxEnvelope{
			Version:     EnvelopeVersionV1,
			Kind:        KindDiff,
			BlockNumber: i,
			Payload:     []byte{byte(i)},
		})
	}

	envs, err := store.ReadRange(1, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(envs) != 3 {
		t.Fatalf("expected 3 events, got %d", len(envs))
	}
	if envs[0].BlockNumber != 1 {
		t.Fatalf("first event block mismatch: %d", envs[0].BlockNumber)
	}
	if envs[2].BlockNumber != 3 {
		t.Fatalf("last event block mismatch: %d", envs[2].BlockNumber)
	}
}

func TestOutboxStore_LatestSeq(t *testing.T) {
	store, dir := newTestOutboxStore(t)
	defer os.RemoveAll(dir)
	defer store.Close()

	if store.LatestSeq() != 0 {
		t.Fatal("empty store should have latest seq 0")
	}

	store.Append(&OutboxEnvelope{Version: EnvelopeVersionV1, Kind: KindDiff, Payload: []byte{1}})
	if store.LatestSeq() != 0 {
		t.Fatalf("expected 0, got %d", store.LatestSeq())
	}

	store.Append(&OutboxEnvelope{Version: EnvelopeVersionV1, Kind: KindDiff, Payload: []byte{2}})
	if store.LatestSeq() != 1 {
		t.Fatalf("expected 1, got %d", store.LatestSeq())
	}
}

func TestOutboxStore_Persistence(t *testing.T) {
	dir, err := os.MkdirTemp("", "ubt-outbox-persist-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	// Open, write, close
	store1, err := NewOutboxStore(dir, 5*time.Second, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	store1.Append(&OutboxEnvelope{
		Version: EnvelopeVersionV1, Kind: KindDiff, BlockNumber: 100, Payload: []byte{1},
	})
	store1.Close()

	// Reopen and verify
	store2, err := NewOutboxStore(dir, 5*time.Second, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer store2.Close()

	if store2.LatestSeq() != 0 {
		t.Fatalf("expected seq 0 after reopen, got %d", store2.LatestSeq())
	}

	env, err := store2.Read(0)
	if err != nil {
		t.Fatal(err)
	}
	if env.BlockNumber != 100 {
		t.Fatal("block number mismatch after reopen")
	}

	// Append should continue from next seq
	seq, _ := store2.Append(&OutboxEnvelope{
		Version: EnvelopeVersionV1, Kind: KindDiff, BlockNumber: 101, Payload: []byte{2},
	})
	if seq != 1 {
		t.Fatalf("expected seq 1 after reopen append, got %d", seq)
	}
}

func TestOutboxStore_ReadNotFound(t *testing.T) {
	store, dir := newTestOutboxStore(t)
	defer os.RemoveAll(dir)
	defer store.Close()

	_, err := store.Read(999)
	if err == nil {
		t.Fatal("expected error for non-existent event")
	}
}

func TestOutboxStore_ReadRangeInvalid(t *testing.T) {
	store, dir := newTestOutboxStore(t)
	defer os.RemoveAll(dir)
	defer store.Close()

	// fromSeq > toSeq should error
	_, err := store.ReadRange(5, 3)
	if err == nil {
		t.Fatal("expected error for invalid range")
	}
}

func TestOutboxStore_ReadRangeSingleElement(t *testing.T) {
	store, dir := newTestOutboxStore(t)
	defer os.RemoveAll(dir)
	defer store.Close()

	// Append a few events
	for i := uint64(0); i < 3; i++ {
		store.Append(&OutboxEnvelope{
			Version:     EnvelopeVersionV1,
			Kind:        KindDiff,
			BlockNumber: i,
			Payload:     []byte{byte(i)},
		})
	}

	// Read range with single element
	envs, err := store.ReadRange(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(envs) != 1 {
		t.Fatalf("expected 1 event, got %d", len(envs))
	}
	if envs[0].BlockNumber != 1 {
		t.Fatalf("expected block 1, got %d", envs[0].BlockNumber)
	}
}

func TestOutboxStore_ReadRangeAll(t *testing.T) {
	store, dir := newTestOutboxStore(t)
	defer os.RemoveAll(dir)
	defer store.Close()

	// Append 10 events
	for i := uint64(0); i < 10; i++ {
		store.Append(&OutboxEnvelope{
			Version:     EnvelopeVersionV1,
			Kind:        KindDiff,
			BlockNumber: i,
			Payload:     []byte{byte(i)},
		})
	}

	// Read all events
	envs, err := store.ReadRange(0, 9)
	if err != nil {
		t.Fatal(err)
	}
	if len(envs) != 10 {
		t.Fatalf("expected 10 events, got %d", len(envs))
	}

	// Verify sequence
	for i, env := range envs {
		if env.BlockNumber != uint64(i) {
			t.Fatalf("expected block %d, got %d", i, env.BlockNumber)
		}
	}
}

func TestOutboxStore_SequenceCounter(t *testing.T) {
	store, dir := newTestOutboxStore(t)
	defer os.RemoveAll(dir)
	defer store.Close()

	// Verify sequence counter increments correctly
	for i := uint64(0); i < 5; i++ {
		seq, err := store.Append(&OutboxEnvelope{
			Version:     EnvelopeVersionV1,
			Kind:        KindDiff,
			BlockNumber: i,
			Payload:     []byte{byte(i)},
		})
		if err != nil {
			t.Fatalf("append %d failed: %v", i, err)
		}
		if seq != i {
			t.Fatalf("expected seq %d, got %d", i, seq)
		}
	}
}

func TestOutboxStore_DifferentEventTypes(t *testing.T) {
	store, dir := newTestOutboxStore(t)
	defer os.RemoveAll(dir)
	defer store.Close()

	// Append diff event
	diff := &QueuedDiffV1{
		OriginRoot: common.HexToHash("0x01"),
		Root:       common.HexToHash("0x02"),
	}
	diffPayload, _ := EncodeDiff(diff)

	store.Append(&OutboxEnvelope{
		Version: EnvelopeVersionV1,
		Kind:    KindDiff,
		Payload: diffPayload,
	})

	// Append reorg event
	reorg := &ReorgMarkerV1{
		FromBlockNumber: 10,
		ToBlockNumber:   8,
	}
	reorgPayload, _ := EncodeReorgMarker(reorg)

	store.Append(&OutboxEnvelope{
		Version: EnvelopeVersionV1,
		Kind:    KindReorg,
		Payload: reorgPayload,
	})

	// Read both and verify kinds
	env0, _ := store.Read(0)
	env1, _ := store.Read(1)

	if env0.Kind != KindDiff {
		t.Fatal("first event should be diff")
	}
	if env1.Kind != KindReorg {
		t.Fatal("second event should be reorg")
	}
}

func TestOutboxStore_LargePayload(t *testing.T) {
	store, dir := newTestOutboxStore(t)
	defer os.RemoveAll(dir)
	defer store.Close()

	// Create a large diff with many accounts
	accounts := make([]AccountEntry, 1000)
	for i := range accounts {
		accounts[i] = AccountEntry{
			Address:  common.BigToAddress(big.NewInt(int64(i))),
			Nonce:    uint64(i),
			Balance:  big.NewInt(int64(i * 1000)),
			CodeHash: common.BigToHash(big.NewInt(int64(i))),
			Alive:    true,
		}
	}

	diff := &QueuedDiffV1{
		OriginRoot: common.HexToHash("0x01"),
		Root:       common.HexToHash("0x02"),
		Accounts:   accounts,
	}

	payload, err := EncodeDiff(diff)
	if err != nil {
		t.Fatal(err)
	}

	env := &OutboxEnvelope{
		Version:     EnvelopeVersionV1,
		Kind:        KindDiff,
		BlockNumber: 1,
		Payload:     payload,
	}

	seq, err := store.Append(env)
	if err != nil {
		t.Fatal(err)
	}

	// Read back and verify
	got, err := store.Read(seq)
	if err != nil {
		t.Fatal(err)
	}

	decodedDiff, err := DecodeDiff(got.Payload)
	if err != nil {
		t.Fatal(err)
	}

	if len(decodedDiff.Accounts) != 1000 {
		t.Fatalf("expected 1000 accounts, got %d", len(decodedDiff.Accounts))
	}
}

func TestOutboxStore_EmptyStoreLatestSeq(t *testing.T) {
	store, dir := newTestOutboxStore(t)
	defer os.RemoveAll(dir)
	defer store.Close()

	// Empty store should return 0
	if store.LatestSeq() != 0 {
		t.Fatalf("empty store should have latest seq 0, got %d", store.LatestSeq())
	}
}

func TestOutboxStore_MultipleReopen(t *testing.T) {
	dir, err := os.MkdirTemp("", "ubt-outbox-multi-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	// First session: append 3 events
	store1, err := NewOutboxStore(dir, 5*time.Second, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	for i := uint64(0); i < 3; i++ {
		store1.Append(&OutboxEnvelope{
			Version:     EnvelopeVersionV1,
			Kind:        KindDiff,
			BlockNumber: i,
			Payload:     []byte{byte(i)},
		})
	}
	store1.Close()

	// Second session: append 2 more events
	store2, err := NewOutboxStore(dir, 5*time.Second, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	for i := uint64(3); i < 5; i++ {
		store2.Append(&OutboxEnvelope{
			Version:     EnvelopeVersionV1,
			Kind:        KindDiff,
			BlockNumber: i,
			Payload:     []byte{byte(i)},
		})
	}
	store2.Close()

	// Third session: verify all 5 events
	store3, err := NewOutboxStore(dir, 5*time.Second, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer store3.Close()

	if store3.LatestSeq() != 4 {
		t.Fatalf("expected latest seq 4, got %d", store3.LatestSeq())
	}

	for i := uint64(0); i < 5; i++ {
		env, err := store3.Read(i)
		if err != nil {
			t.Fatalf("failed to read seq %d: %v", i, err)
		}
		if env.BlockNumber != i {
			t.Fatalf("expected block %d, got %d", i, env.BlockNumber)
		}
	}
}

func TestOutboxStore_TimestampPreservation(t *testing.T) {
	store, dir := newTestOutboxStore(t)
	defer os.RemoveAll(dir)
	defer store.Close()

	timestamp := uint64(1234567890)
	env := &OutboxEnvelope{
		Version:     EnvelopeVersionV1,
		Kind:        KindDiff,
		BlockNumber: 1,
		Timestamp:   timestamp,
		Payload:     []byte{1, 2, 3},
	}

	store.Append(env)

	got, err := store.Read(0)
	if err != nil {
		t.Fatal(err)
	}

	if got.Timestamp != timestamp {
		t.Fatalf("timestamp mismatch: got %d, want %d", got.Timestamp, timestamp)
	}
}

func TestOutboxStore_HashPreservation(t *testing.T) {
	store, dir := newTestOutboxStore(t)
	defer os.RemoveAll(dir)
	defer store.Close()

	blockHash := common.HexToHash("0xdeadbeef")
	parentHash := common.HexToHash("0xcafebabe")

	env := &OutboxEnvelope{
		Version:     EnvelopeVersionV1,
		Kind:        KindDiff,
		BlockNumber: 1,
		BlockHash:   blockHash,
		ParentHash:  parentHash,
		Payload:     []byte{1, 2, 3},
	}

	store.Append(env)

	got, err := store.Read(0)
	if err != nil {
		t.Fatal(err)
	}

	if got.BlockHash != blockHash {
		t.Fatalf("block hash mismatch: got %s, want %s", got.BlockHash.Hex(), blockHash.Hex())
	}
	if got.ParentHash != parentHash {
		t.Fatalf("parent hash mismatch: got %s, want %s", got.ParentHash.Hex(), parentHash.Hex())
	}
}

// TDD: C2 – Sequence overflow check must happen BEFORE atomic write.
// If nextSeq == ^uint64(0), appending should fail without writing nextSeq+1=0 to disk.
func TestOutboxStore_SequenceOverflow(t *testing.T) {
	store, dir := newTestOutboxStore(t)
	defer os.RemoveAll(dir)
	defer store.Close()

	// Set nextSeq to max value both in-memory AND on disk to simulate
	// a store that has reached the maximum sequence number.
	store.mu.Lock()
	store.nextSeq = ^uint64(0)
	rawdb.WriteUBTOutboxSeqCounter(store.db, ^uint64(0))
	store.mu.Unlock()

	env := &OutboxEnvelope{
		Version: EnvelopeVersionV1,
		Kind:    KindDiff,
		Payload: []byte{1},
	}

	_, err := store.Append(env)
	if err == nil {
		t.Fatal("expected overflow error, got nil")
	}

	// Verify the on-disk seq counter was NOT corrupted to 0.
	// Reopen the store and check the persisted counter.
	store.Close()

	store2, err := NewOutboxStore(dir, 5*time.Second, 0, 0)
	if err != nil {
		t.Fatalf("failed to reopen store: %v", err)
	}
	defer store2.Close()

	// The seq counter should still be ^uint64(0), NOT 0.
	// If it wrapped to 0, the overflow check happened after the write.
	if store2.nextSeq == 0 {
		t.Fatal("CRITICAL: seq counter was corrupted to 0 on disk — overflow check happened after write")
	}
	if store2.nextSeq != ^uint64(0) {
		t.Fatalf("expected nextSeq to remain %d, got %d", ^uint64(0), store2.nextSeq)
	}
}

// TDD: C5 – Read() must return ErrEventNotFound for missing events,
// and a distinct error for corruption/I/O issues.
func TestOutboxStore_ReadReturnsErrEventNotFound(t *testing.T) {
	store, dir := newTestOutboxStore(t)
	defer os.RemoveAll(dir)
	defer store.Close()

	// Read non-existent event should return ErrEventNotFound
	_, err := store.Read(999)
	if err == nil {
		t.Fatal("expected error for non-existent event")
	}
	if !errors.Is(err, ErrEventNotFound) {
		t.Fatalf("expected ErrEventNotFound, got: %v", err)
	}
}

func TestOutboxStore_ReadCorruptData(t *testing.T) {
	store, dir := newTestOutboxStore(t)
	defer os.RemoveAll(dir)
	defer store.Close()

	// Write a valid event so seq 0 exists in the counter
	store.Append(&OutboxEnvelope{
		Version: EnvelopeVersionV1,
		Kind:    KindDiff,
		Payload: []byte{1},
	})

	// Overwrite seq 0 with corrupt data directly in the DB
	rawdb.WriteUBTOutboxEvent(store.db, 0, []byte("corrupted-not-valid-rlp"))

	// Read should fail with a decode error, NOT ErrEventNotFound
	_, err := store.Read(0)
	if err == nil {
		t.Fatal("expected error for corrupt data")
	}
	if errors.Is(err, ErrEventNotFound) {
		t.Fatal("corrupt data should NOT return ErrEventNotFound")
	}
}

func TestOutboxStore_ConcurrentAppends(t *testing.T) {
	store, dir := newTestOutboxStore(t)
	defer os.RemoveAll(dir)
	defer store.Close()

	// Test that concurrent appends are properly serialized by the mutex
	done := make(chan bool, 10)

	for i := 0; i < 10; i++ {
		go func(idx int) {
			env := &OutboxEnvelope{
				Version:     EnvelopeVersionV1,
				Kind:        KindDiff,
				BlockNumber: uint64(idx),
				Payload:     []byte{byte(idx)},
			}
			_, err := store.Append(env)
			if err != nil {
				t.Errorf("concurrent append %d failed: %v", idx, err)
			}
			done <- true
		}(i)
	}

	// Wait for all appends to complete
	for i := 0; i < 10; i++ {
		<-done
	}

	// Verify that exactly 10 events were written
	if store.LatestSeq() != 9 {
		t.Fatalf("expected latest seq 9, got %d", store.LatestSeq())
	}
}
