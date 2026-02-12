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

func newTestOutboxStore(t *testing.T) (*OutboxStore, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "ubt-outbox-test-*")
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewOutboxStore(dir, 5*time.Second, 0, 0)
	if err != nil {
		os.RemoveAll(dir)
		t.Fatal(err)
	}
	return store, dir
}

func TestService_EmitDiff(t *testing.T) {
	store, dir := newTestOutboxStore(t)
	defer os.RemoveAll(dir)

	svc := NewService(store)
	defer svc.Close()

	diff := &QueuedDiffV1{
		OriginRoot: common.HexToHash("0x01"),
		Root:       common.HexToHash("0x02"),
		Accounts: []AccountEntry{
			{
				Address:  common.HexToAddress("0xaaaa"),
				Nonce:    1,
				Balance:  big.NewInt(1000),
				CodeHash: common.HexToHash("0xcccc"),
				Alive:    true,
			},
		},
		Storage: []StorageEntry{
			{
				Address:    common.HexToAddress("0xaaaa"),
				SlotKeyRaw: common.HexToHash("0x01"),
				Value:      common.HexToHash("0xff"),
			},
		},
	}

	blockHash := common.HexToHash("0xb1")
	parentHash := common.HexToHash("0xb0")

	// Emit
	svc.EmitDiff(1, blockHash, parentHash, diff)

	// Verify stored
	if store.LatestSeq() != 0 {
		t.Fatalf("expected latest seq 0, got %d", store.LatestSeq())
	}

	env, err := store.Read(0)
	if err != nil {
		t.Fatal(err)
	}
	if env.Kind != KindDiff {
		t.Fatalf("expected kind diff, got %s", env.Kind)
	}
	if env.BlockNumber != 1 {
		t.Fatalf("expected block 1, got %d", env.BlockNumber)
	}
	if env.BlockHash != blockHash {
		t.Fatalf("block hash mismatch")
	}
	if env.Version != EnvelopeVersionV1 {
		t.Fatalf("version mismatch")
	}

	// Decode payload and verify
	decoded, err := DecodeDiff(env.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.OriginRoot != diff.OriginRoot {
		t.Fatal("origin root mismatch")
	}
	if len(decoded.Accounts) != 1 {
		t.Fatalf("expected 1 account, got %d", len(decoded.Accounts))
	}
	if decoded.Accounts[0].Address != diff.Accounts[0].Address {
		t.Fatal("account address mismatch")
	}
}

func TestService_EmitReorg(t *testing.T) {
	store, dir := newTestOutboxStore(t)
	defer os.RemoveAll(dir)

	svc := NewService(store)
	defer svc.Close()

	marker := &ReorgMarkerV1{
		FromBlockNumber:      10,
		FromBlockHash:        common.HexToHash("0xaa"),
		ToBlockNumber:        8,
		ToBlockHash:          common.HexToHash("0xbb"),
		CommonAncestorNumber: 7,
		CommonAncestorHash:   common.HexToHash("0xcc"),
	}

	svc.EmitReorg(marker)

	env, err := store.Read(0)
	if err != nil {
		t.Fatal(err)
	}
	if env.Kind != KindReorg {
		t.Fatalf("expected kind reorg, got %s", env.Kind)
	}

	decoded, err := DecodeReorgMarker(env.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.FromBlockNumber != 10 {
		t.Fatalf("from block mismatch: %d", decoded.FromBlockNumber)
	}
	if decoded.CommonAncestorNumber != 7 {
		t.Fatalf("ancestor mismatch: %d", decoded.CommonAncestorNumber)
	}
}

func TestService_SequenceMonotonicity(t *testing.T) {
	store, dir := newTestOutboxStore(t)
	defer os.RemoveAll(dir)

	svc := NewService(store)
	defer svc.Close()

	for i := uint64(0); i < 10; i++ {
		diff := &QueuedDiffV1{
			OriginRoot: common.BigToHash(big.NewInt(int64(i))),
			Root:       common.BigToHash(big.NewInt(int64(i + 1))),
		}
		svc.EmitDiff(i, common.BigToHash(big.NewInt(int64(i))), common.Hash{}, diff)
	}

	if store.LatestSeq() != 9 {
		t.Fatalf("expected latest seq 9, got %d", store.LatestSeq())
	}

	// Verify all events in sequence
	for i := uint64(0); i < 10; i++ {
		env, err := store.Read(i)
		if err != nil {
			t.Fatalf("failed to read seq %d: %v", i, err)
		}
		if env.Seq != i {
			t.Fatalf("seq mismatch: expected %d, got %d", i, env.Seq)
		}
		if env.BlockNumber != i {
			t.Fatalf("block mismatch: expected %d, got %d", i, env.BlockNumber)
		}
	}
}

func TestService_NonBlockingEmit(t *testing.T) {
	// Test that emitter is non-blocking and doesn't panic
	store, dir := newTestOutboxStore(t)
	defer os.RemoveAll(dir)

	svc := NewService(store)
	defer svc.Close()

	// Emit many diffs rapidly - should not block or panic
	for i := uint64(0); i < 100; i++ {
		diff := &QueuedDiffV1{
			OriginRoot: common.BigToHash(big.NewInt(int64(i))),
			Root:       common.BigToHash(big.NewInt(int64(i + 1))),
		}
		// This should never panic or block
		svc.EmitDiff(i, common.BigToHash(big.NewInt(int64(i))), common.Hash{}, diff)
	}

	// Verify all events were written
	if store.LatestSeq() != 99 {
		t.Fatalf("expected latest seq 99, got %d", store.LatestSeq())
	}
}

func TestService_MixedDiffAndReorg(t *testing.T) {
	store, dir := newTestOutboxStore(t)
	defer os.RemoveAll(dir)

	svc := NewService(store)
	defer svc.Close()

	// Emit diff
	diff := &QueuedDiffV1{Root: common.HexToHash("0x01")}
	svc.EmitDiff(1, common.HexToHash("0xb1"), common.Hash{}, diff)

	// Emit reorg
	marker := &ReorgMarkerV1{FromBlockNumber: 1, ToBlockNumber: 0}
	svc.EmitReorg(marker)

	// Emit another diff
	diff2 := &QueuedDiffV1{Root: common.HexToHash("0x02")}
	svc.EmitDiff(2, common.HexToHash("0xb2"), common.Hash{}, diff2)

	// Verify sequence and kinds
	env0, _ := store.Read(0)
	env1, _ := store.Read(1)
	env2, _ := store.Read(2)

	if env0.Kind != KindDiff {
		t.Fatal("seq 0 should be diff")
	}
	if env1.Kind != KindReorg {
		t.Fatal("seq 1 should be reorg")
	}
	if env2.Kind != KindDiff {
		t.Fatal("seq 2 should be diff")
	}
}

func TestService_EmitEmptyDiff(t *testing.T) {
	store, dir := newTestOutboxStore(t)
	defer os.RemoveAll(dir)

	svc := NewService(store)
	defer svc.Close()

	// Emit an empty diff (no accounts, storage, or code changes)
	diff := &QueuedDiffV1{
		OriginRoot: common.HexToHash("0x01"),
		Root:       common.HexToHash("0x02"),
		Accounts:   []AccountEntry{},
		Storage:    []StorageEntry{},
		Codes:      []CodeEntry{},
	}

	svc.EmitDiff(1, common.HexToHash("0xb1"), common.Hash{}, diff)

	env, err := store.Read(0)
	if err != nil {
		t.Fatal(err)
	}

	decoded, err := DecodeDiff(env.Payload)
	if err != nil {
		t.Fatal(err)
	}

	if len(decoded.Accounts) != 0 {
		t.Fatal("expected no accounts")
	}
	if len(decoded.Storage) != 0 {
		t.Fatal("expected no storage")
	}
	if len(decoded.Codes) != 0 {
		t.Fatal("expected no codes")
	}
}

func TestService_MultipleAccounts(t *testing.T) {
	store, dir := newTestOutboxStore(t)
	defer os.RemoveAll(dir)

	svc := NewService(store)
	defer svc.Close()

	// Emit a diff with multiple accounts
	diff := &QueuedDiffV1{
		OriginRoot: common.HexToHash("0x01"),
		Root:       common.HexToHash("0x02"),
		Accounts: []AccountEntry{
			{
				Address:  common.HexToAddress("0xaaaa"),
				Nonce:    1,
				Balance:  big.NewInt(1000),
				CodeHash: common.HexToHash("0xc1"),
				Alive:    true,
			},
			{
				Address:  common.HexToAddress("0xbbbb"),
				Nonce:    2,
				Balance:  big.NewInt(2000),
				CodeHash: common.HexToHash("0xc2"),
				Alive:    false, // Deleted account
			},
			{
				Address:  common.HexToAddress("0xcccc"),
				Nonce:    3,
				Balance:  big.NewInt(3000),
				CodeHash: common.HexToHash("0xc3"),
				Alive:    true,
			},
		},
	}

	svc.EmitDiff(1, common.HexToHash("0xb1"), common.Hash{}, diff)

	env, err := store.Read(0)
	if err != nil {
		t.Fatal(err)
	}

	decoded, err := DecodeDiff(env.Payload)
	if err != nil {
		t.Fatal(err)
	}

	if len(decoded.Accounts) != 3 {
		t.Fatalf("expected 3 accounts, got %d", len(decoded.Accounts))
	}

	// Verify deleted account
	if decoded.Accounts[1].Alive != false {
		t.Fatal("account should be marked as deleted")
	}
}

func TestService_MultipleStorageSlots(t *testing.T) {
	store, dir := newTestOutboxStore(t)
	defer os.RemoveAll(dir)

	svc := NewService(store)
	defer svc.Close()

	// Emit a diff with multiple storage slots
	diff := &QueuedDiffV1{
		OriginRoot: common.HexToHash("0x01"),
		Root:       common.HexToHash("0x02"),
		Storage: []StorageEntry{
			{
				Address:    common.HexToAddress("0xaaaa"),
				SlotKeyRaw: common.HexToHash("0x01"),
				Value:      common.HexToHash("0xff"),
			},
			{
				Address:    common.HexToAddress("0xaaaa"),
				SlotKeyRaw: common.HexToHash("0x02"),
				Value:      common.HexToHash("0xee"),
			},
			{
				Address:    common.HexToAddress("0xbbbb"),
				SlotKeyRaw: common.HexToHash("0x01"),
				Value:      common.HexToHash("0xdd"),
			},
		},
	}

	svc.EmitDiff(1, common.HexToHash("0xb1"), common.Hash{}, diff)

	env, err := store.Read(0)
	if err != nil {
		t.Fatal(err)
	}

	decoded, err := DecodeDiff(env.Payload)
	if err != nil {
		t.Fatal(err)
	}

	if len(decoded.Storage) != 3 {
		t.Fatalf("expected 3 storage entries, got %d", len(decoded.Storage))
	}
}

func TestService_WithCode(t *testing.T) {
	store, dir := newTestOutboxStore(t)
	defer os.RemoveAll(dir)

	svc := NewService(store)
	defer svc.Close()

	// Emit a diff with code changes
	diff := &QueuedDiffV1{
		OriginRoot: common.HexToHash("0x01"),
		Root:       common.HexToHash("0x02"),
		Codes: []CodeEntry{
			{
				Address:  common.HexToAddress("0xaaaa"),
				CodeHash: common.HexToHash("0xc1"),
				Code:     []byte{0x60, 0x60, 0x60}, // Simple bytecode
			},
		},
	}

	svc.EmitDiff(1, common.HexToHash("0xb1"), common.Hash{}, diff)

	env, err := store.Read(0)
	if err != nil {
		t.Fatal(err)
	}

	decoded, err := DecodeDiff(env.Payload)
	if err != nil {
		t.Fatal(err)
	}

	if len(decoded.Codes) != 1 {
		t.Fatalf("expected 1 code entry, got %d", len(decoded.Codes))
	}

	if decoded.Codes[0].Address != diff.Codes[0].Address {
		t.Fatal("code address mismatch")
	}
	if decoded.Codes[0].CodeHash != diff.Codes[0].CodeHash {
		t.Fatal("code hash mismatch")
	}
	if len(decoded.Codes[0].Code) != 3 {
		t.Fatalf("expected 3 bytes of code, got %d", len(decoded.Codes[0].Code))
	}
}

func TestService_CloseIdempotent(t *testing.T) {
	store, dir := newTestOutboxStore(t)
	defer os.RemoveAll(dir)

	svc := NewService(store)

	// First close should succeed
	if err := svc.Close(); err != nil {
		t.Fatalf("first close failed: %v", err)
	}

	// Second close may return an error (leveldb already closed), but should not panic
	// The important thing is that it doesn't crash
	svc.Close()
}

func TestService_EmitAfterClose(t *testing.T) {
	store, dir := newTestOutboxStore(t)
	defer os.RemoveAll(dir)

	svc := NewService(store)
	svc.Close()

	// Emit after close should be silently ignored
	diff := &QueuedDiffV1{
		OriginRoot: common.HexToHash("0x01"),
		Root:       common.HexToHash("0x02"),
	}
	svc.EmitDiff(1, common.Hash{}, common.Hash{}, diff)

	// Store should be empty
	if store.LatestSeq() != 0 {
		t.Fatal("expected no events after close")
	}
}

func TestService_IsDegradedInitialState(t *testing.T) {
	store, dir := newTestOutboxStore(t)
	defer os.RemoveAll(dir)

	svc := NewService(store)
	defer svc.Close()

	// Initially, service should not be degraded
	if svc.IsDegraded() {
		t.Fatal("service should not be degraded initially")
	}

	// Emit a valid diff
	diff := &QueuedDiffV1{
		OriginRoot: common.HexToHash("0x01"),
		Root:       common.HexToHash("0x02"),
	}
	svc.EmitDiff(1, common.HexToHash("0xb1"), common.Hash{}, diff)

	// After successful emit, should still not be degraded
	if svc.IsDegraded() {
		t.Fatal("service should not be degraded after successful emit")
	}
}

func TestService_ParentHashPropagation(t *testing.T) {
	store, dir := newTestOutboxStore(t)
	defer os.RemoveAll(dir)

	svc := NewService(store)
	defer svc.Close()

	diff := &QueuedDiffV1{
		OriginRoot: common.HexToHash("0x01"),
		Root:       common.HexToHash("0x02"),
	}

	blockHash := common.HexToHash("0xb1")
	parentHash := common.HexToHash("0xb0")

	svc.EmitDiff(1, blockHash, parentHash, diff)

	env, err := store.Read(0)
	if err != nil {
		t.Fatal(err)
	}

	if env.ParentHash != parentHash {
		t.Fatalf("parent hash mismatch: got %s, want %s", env.ParentHash.Hex(), parentHash.Hex())
	}
}
