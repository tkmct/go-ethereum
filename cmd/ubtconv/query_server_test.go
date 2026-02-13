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
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/ubtemit"
	"github.com/ethereum/go-ethereum/rpc"
)

// TestQueryAPI_Status tests that Status returns expected fields.
func TestQueryAPI_Status(t *testing.T) {
	// Create a minimal consumer with no applier to test status reporting
	consumer := &Consumer{
		cfg: &Config{
			ExecutionClassRPCEnabled: true,
		},
		state: ConsumerState{
			AppliedSeq:       123,
			AppliedBlock:     456,
			AppliedRoot:      common.HexToHash("0xabc"),
			PendingSeq:       124,
			PendingStatus:    rawdb.UBTConsumerPendingInFlight,
			PendingUpdatedAt: 1700000000,
		},
	}

	api := NewQueryAPI(consumer)
	ctx := context.Background()

	status, err := api.Status(ctx)
	if err != nil {
		t.Fatalf("Status() failed: %v", err)
	}

	if status["appliedSeq"] != uint64(123) {
		t.Errorf("Expected appliedSeq=123, got %v", status["appliedSeq"])
	}
	if status["appliedBlock"] != uint64(456) {
		t.Errorf("Expected appliedBlock=456, got %v", status["appliedBlock"])
	}
	if status["appliedRoot"] != common.HexToHash("0xabc") {
		t.Errorf("Expected appliedRoot=0xabc, got %v", status["appliedRoot"])
	}
	if status["outboxLag"] != uint64(0) {
		t.Errorf("Expected outboxLag=0, got %v", status["outboxLag"])
	}
	if status["pendingSeq"] != uint64(124) {
		t.Errorf("Expected pendingSeq=124, got %v", status["pendingSeq"])
	}
	if status["pendingState"] != "inflight" {
		t.Errorf("Expected pendingState=inflight, got %v", status["pendingState"])
	}
	if status["pendingUpdatedAt"] != uint64(1700000000) {
		t.Errorf("Expected pendingUpdatedAt=1700000000, got %v", status["pendingUpdatedAt"])
	}
	if status["backpressureLagThreshold"] != uint64(0) {
		t.Errorf("Expected backpressureLagThreshold=0, got %v", status["backpressureLagThreshold"])
	}
	if status["backpressureTriggered"] != false {
		t.Errorf("Expected backpressureTriggered=false, got %v", status["backpressureTriggered"])
	}
	if status["executionClassRPCEnabled"] != true {
		t.Errorf("Expected executionClassRPCEnabled=true, got %v", status["executionClassRPCEnabled"])
	}
}

// TestQueryAPI_GetBalance_NoApplier tests that GetBalance returns error when applier is not initialized.
func TestQueryAPI_GetBalance_NoApplier(t *testing.T) {
	consumer := &Consumer{}
	api := NewQueryAPI(consumer)
	ctx := context.Background()

	addr := common.HexToAddress("0x1234")
	_, err := api.GetBalance(ctx, addr, nil)
	if err == nil {
		t.Fatal("Expected error when applier is nil, got nil")
	}
	if err.Error() != "UBT trie not initialized" {
		t.Errorf("Expected 'UBT trie not initialized' error, got: %v", err)
	}
}

// TestQueryAPI_GetStorageAt_NoApplier tests that GetStorageAt returns error when applier is not initialized.
func TestQueryAPI_GetStorageAt_NoApplier(t *testing.T) {
	consumer := &Consumer{}
	api := NewQueryAPI(consumer)
	ctx := context.Background()

	addr := common.HexToAddress("0x1234")
	slot := common.HexToHash("0x5678")
	_, err := api.GetStorageAt(ctx, addr, slot, nil)
	if err == nil {
		t.Fatal("Expected error when applier is nil, got nil")
	}
	if err.Error() != "UBT trie not initialized" {
		t.Errorf("Expected 'UBT trie not initialized' error, got: %v", err)
	}
}

// TestQueryAPI_GetCode_NoApplier tests that GetCode returns error when applier is not initialized.
func TestQueryAPI_GetCode_NoApplier(t *testing.T) {
	consumer := &Consumer{}
	api := NewQueryAPI(consumer)
	ctx := context.Background()

	addr := common.HexToAddress("0x1234")
	_, err := api.GetCode(ctx, addr, nil)
	if err == nil {
		t.Fatal("Expected error when applier is nil, got nil")
	}
	if err.Error() != "UBT trie not initialized" {
		t.Errorf("Expected 'UBT trie not initialized' error, got: %v", err)
	}
}

// TestQueryAPI_GetBalance_EmptyTrie tests that GetBalance returns zero for non-existent account.
func TestQueryAPI_GetBalance_EmptyTrie(t *testing.T) {
	// Create a consumer with an empty trie applier
	cfg := &Config{
		DataDir:            t.TempDir(),
		TrieDBScheme:       "path",
		TrieDBStateHistory: 128,
	}

	applier, err := NewApplier(cfg, common.Hash{})
	if err != nil {
		t.Fatalf("Failed to create applier: %v", err)
	}
	defer applier.Close()

	consumer := &Consumer{
		applier: applier,
	}

	api := NewQueryAPI(consumer)
	ctx := context.Background()

	addr := common.HexToAddress("0x1234")
	balance, err := api.GetBalance(ctx, addr, nil)
	if err != nil {
		t.Fatalf("GetBalance() failed: %v", err)
	}

	if balance.ToInt().Cmp(common.Big0) != 0 {
		t.Errorf("Expected balance=0, got %v", balance)
	}
}

// TestQueryAPI_GetStorageAt_EmptyTrie tests that GetStorageAt returns zero for non-existent slot.
func TestQueryAPI_GetStorageAt_EmptyTrie(t *testing.T) {
	// Create a consumer with an empty trie applier
	cfg := &Config{
		DataDir:            t.TempDir(),
		TrieDBScheme:       "path",
		TrieDBStateHistory: 128,
	}

	applier, err := NewApplier(cfg, common.Hash{})
	if err != nil {
		t.Fatalf("Failed to create applier: %v", err)
	}
	defer applier.Close()

	consumer := &Consumer{
		applier: applier,
	}

	api := NewQueryAPI(consumer)
	ctx := context.Background()

	addr := common.HexToAddress("0x1234")
	slot := common.HexToHash("0x5678")
	value, err := api.GetStorageAt(ctx, addr, slot, nil)
	if err != nil {
		t.Fatalf("GetStorageAt() failed: %v", err)
	}

	expectedZero := common.Hash{}
	if common.BytesToHash(value) != expectedZero {
		t.Errorf("Expected value=0x0, got %v", hexutil.Encode(value))
	}
}

func TestQueryAPI_BlockSelectorResolution(t *testing.T) {
	cfg := &Config{
		DataDir:            t.TempDir(),
		TrieDBScheme:       "path",
		TrieDBStateHistory: 128,
	}
	applier, err := NewApplier(cfg, common.Hash{})
	if err != nil {
		t.Fatalf("Failed to create applier: %v", err)
	}
	defer applier.Close()

	addr := common.HexToAddress("0x1111111111111111111111111111111111111111")
	diff1 := &ubtemit.QueuedDiffV1{
		Accounts: []ubtemit.AccountEntry{{
			Address:  addr,
			Nonce:    1,
			Balance:  big.NewInt(1),
			CodeHash: types.EmptyCodeHash,
			Alive:    true,
		}},
	}
	if _, err := applier.ApplyDiff(diff1); err != nil {
		t.Fatalf("ApplyDiff #1 failed: %v", err)
	}
	if err := applier.CommitAt(1); err != nil {
		t.Fatalf("Commit #1 failed: %v", err)
	}
	root1 := applier.Root()

	diff2 := &ubtemit.QueuedDiffV1{
		Accounts: []ubtemit.AccountEntry{{
			Address:  addr,
			Nonce:    2,
			Balance:  big.NewInt(2),
			CodeHash: types.EmptyCodeHash,
			Alive:    true,
		}},
	}
	if _, err := applier.ApplyDiff(diff2); err != nil {
		t.Fatalf("ApplyDiff #2 failed: %v", err)
	}
	if err := applier.CommitAt(2); err != nil {
		t.Fatalf("Commit #2 failed: %v", err)
	}
	root2 := applier.Root()

	db := rawdb.NewMemoryDatabase()
	hash1 := common.HexToHash("0xaaa1")
	hash2 := common.HexToHash("0xaaa2")
	rawdb.WriteUBTBlockRoot(db, 1, root1)
	rawdb.WriteUBTBlockRoot(db, 2, root2)
	rawdb.WriteUBTCanonicalBlock(db, 1, hash1, common.Hash{})
	rawdb.WriteUBTCanonicalBlock(db, 2, hash2, hash1)

	consumer := &Consumer{
		cfg:     cfg,
		db:      db,
		applier: applier,
		state: ConsumerState{
			AppliedSeq:   2,
			AppliedBlock: 2,
			AppliedRoot:  root2,
		},
	}
	api := NewQueryAPI(consumer)
	ctx := context.Background()

	t.Run("latest succeeds", func(t *testing.T) {
		b, err := api.GetBalance(ctx, addr, nil)
		if err != nil {
			t.Fatalf("latest balance failed: %v", err)
		}
		if b.ToInt().Cmp(big.NewInt(2)) != 0 {
			t.Fatalf("unexpected latest balance: %s", b.ToInt())
		}
	})
	t.Run("historical within window succeeds", func(t *testing.T) {
		block1 := rpc.BlockNumberOrHashWithNumber(1)
		b, err := api.GetBalance(ctx, addr, &block1)
		if err != nil {
			t.Fatalf("historical balance within window should succeed: %v", err)
		}
		if b.ToInt().Cmp(big.NewInt(1)) != 0 {
			t.Fatalf("expected historical balance=1, got %s", b.ToInt())
		}
	})
	t.Run("hash selector succeeds", func(t *testing.T) {
		selector := rpc.BlockNumberOrHashWithHash(hash2, true)
		b, err := api.GetBalance(ctx, addr, &selector)
		if err != nil {
			t.Fatalf("hash selector failed: %v", err)
		}
		if b.ToInt().Cmp(big.NewInt(2)) != 0 {
			t.Fatalf("unexpected hash balance: %s", b.ToInt())
		}
	})
	t.Run("unsupported tags rejected", func(t *testing.T) {
		for _, tag := range []rpc.BlockNumber{rpc.PendingBlockNumber, rpc.SafeBlockNumber, rpc.FinalizedBlockNumber} {
			selector := rpc.BlockNumberOrHashWithNumber(tag)
			_, err := api.GetBalance(ctx, addr, &selector)
			if err == nil {
				t.Fatalf("selector %s should be rejected", tag)
			}
			if !strings.Contains(err.Error(), "unsupported block selector tag") {
				t.Fatalf("unexpected error for selector %s: %v", tag, err)
			}
		}
	})
	t.Run("ahead head rejected", func(t *testing.T) {
		block3 := rpc.BlockNumberOrHashWithNumber(3)
		_, err := api.GetBalance(ctx, addr, &block3)
		if err == nil || !strings.Contains(err.Error(), "state not yet available") {
			t.Fatalf("expected state not yet available, got: %v", err)
		}
	})
	t.Run("window out rejected", func(t *testing.T) {
		consumer.cfg.TrieDBStateHistory = 0
		block1 := rpc.BlockNumberOrHashWithNumber(1)
		_, err := api.GetBalance(ctx, addr, &block1)
		if err == nil || !strings.Contains(err.Error(), "state not available") {
			t.Fatalf("expected state not available, got: %v", err)
		}
		consumer.cfg.TrieDBStateHistory = 128
	})
}

// TestVerifyProof_ZeroRootRejected tests that VerifyProof rejects a zero root hash.
func TestVerifyProof_ZeroRootRejected(t *testing.T) {
	consumer := &Consumer{
		cfg: &Config{},
		state: ConsumerState{
			AppliedSeq:   1,
			AppliedBlock: 1,
		},
	}
	api := NewQueryAPI(consumer)
	ctx := context.Background()

	proofNodes := map[common.Hash]hexutil.Bytes{
		common.HexToHash("0x01"): hexutil.Bytes{0x01},
	}

	result, err := api.VerifyProof(ctx, common.Hash{}, common.HexToHash("0x01"), proofNodes)
	if err == nil {
		t.Fatal("expected error for zero root hash")
	}
	if result != nil {
		t.Errorf("expected nil result, got %+v", result)
	}
	if !strings.Contains(err.Error(), "root hash cannot be zero") {
		t.Errorf("expected 'root hash cannot be zero' error, got: %v", err)
	}
}

// TestVerifyProof_TooManyNodes tests that VerifyProof rejects proof requests exceeding the node limit.
func TestVerifyProof_TooManyNodes(t *testing.T) {
	consumer := &Consumer{
		cfg: &Config{
			QueryRPCMaxBatch: 5, // Set a low limit for testing
		},
		state: ConsumerState{
			AppliedSeq:   1,
			AppliedBlock: 1,
		},
	}
	api := NewQueryAPI(consumer)
	ctx := context.Background()

	// Create more proof nodes than the limit
	proofNodes := make(map[common.Hash]hexutil.Bytes)
	for i := 0; i < 10; i++ {
		h := common.Hash{}
		h[0] = byte(i)
		proofNodes[h] = hexutil.Bytes{0x01}
	}

	root := common.HexToHash("0xdeadbeef")
	result, err := api.VerifyProof(ctx, root, common.HexToHash("0x01"), proofNodes)
	if err == nil {
		t.Fatal("expected error for too many proof nodes")
	}
	if result != nil {
		t.Errorf("expected nil result, got %+v", result)
	}
	if !strings.Contains(err.Error(), "too many proof nodes") {
		t.Errorf("expected 'too many proof nodes' error, got: %v", err)
	}
}

// TestQueryAPI_MaxBatch_GetAccountProof verifies that GetAccountProof rejects
// requests with too many storage keys.
func TestQueryAPI_MaxBatch_GetAccountProof(t *testing.T) {
	consumer := &Consumer{
		cfg: &Config{
			QueryRPCMaxBatch: 5,
		},
	}
	api := NewQueryAPI(consumer)
	ctx := context.Background()

	addr := common.HexToAddress("0x1234")
	// Create 6 storage keys, exceeding the batch limit of 5
	storageKeys := make([]common.Hash, 6)
	for i := range storageKeys {
		storageKeys[i] = common.Hash{byte(i)}
	}

	_, err := api.GetAccountProof(ctx, addr, storageKeys, nil)
	if err == nil {
		t.Fatal("expected error for too many storage keys")
	}
	if !strings.Contains(err.Error(), "too many storage keys") {
		t.Errorf("expected 'too many storage keys' error, got: %v", err)
	}

	// Verify that a request within limits would not be rejected at the batch check
	// (it may fail later due to nil applier, but not at the batch check)
	smallKeys := make([]common.Hash, 3)
	for i := range smallKeys {
		smallKeys[i] = common.Hash{byte(i)}
	}
	_, err = api.GetAccountProof(ctx, addr, smallKeys, nil)
	if err != nil && strings.Contains(err.Error(), "too many storage keys") {
		t.Errorf("request within batch limit should not be rejected for batch size: %v", err)
	}
}
