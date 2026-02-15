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
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/ubtemit"
	"github.com/ethereum/go-ethereum/rpc"
)

// TestQueryServerIntegration tests the full flow of query server:
// 1. Create a consumer with applier
// 2. Apply some state changes
// 3. Start query server
// 4. Query the state via RPC
func TestQueryServerIntegration(t *testing.T) {
	// Create config
	cfg := &Config{
		DataDir:            t.TempDir(),
		TrieDBScheme:       "path",
		TrieDBStateHistory: 128,
		QueryRPCEnabled:    true,
		QueryRPCListenAddr: "localhost:0", // Use random port
	}

	// Create applier with empty trie
	applier, err := NewApplier(cfg, common.Hash{})
	if err != nil {
		t.Fatalf("Failed to create applier: %v", err)
	}
	defer applier.Close()

	// Create a test account update
	testAddr := common.HexToAddress("0x1234567890123456789012345678901234567890")
	testBalance := big.NewInt(1000000000)
	diff := &ubtemit.QueuedDiffV1{
		Accounts: []ubtemit.AccountEntry{
			{
				Address:  testAddr,
				Nonce:    1,
				Balance:  testBalance,
				CodeHash: types.EmptyCodeHash,
				Alive:    true,
			},
		},
	}

	// Apply the diff
	if _, err := applier.ApplyDiff(diff); err != nil {
		t.Fatalf("Failed to apply diff: %v", err)
	}
	if err := applier.CommitAt(100); err != nil {
		t.Fatalf("Failed to commit diff: %v", err)
	}
	root := applier.Root()

	// Create consumer with the applier
	consumer := &Consumer{
		cfg:     cfg,
		applier: applier,
		state: ConsumerState{
			AppliedSeq:   1,
			AppliedBlock: 100,
			AppliedRoot:  root,
		},
	}

	// Start query server
	qs, err := NewQueryServer(cfg.QueryRPCListenAddr, consumer)
	if err != nil {
		t.Fatalf("Failed to start query server: %v", err)
	}
	defer qs.Close()

	// Get the actual listen address (since we used port 0)
	listenAddr := "http://" + qs.listener.Addr().String()

	// Connect RPC client
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client, err := rpc.DialContext(ctx, listenAddr)
	if err != nil {
		t.Fatalf("Failed to connect to query server: %v", err)
	}
	defer client.Close()

	// Test 1: Query status
	t.Run("Status", func(t *testing.T) {
		var status map[string]interface{}
		if err := client.CallContext(ctx, &status, "ubt_status"); err != nil {
			t.Fatalf("ubt_status failed: %v", err)
		}

		// Status should return the expected fields
		appliedSeq, ok := status["appliedSeq"].(float64) // JSON numbers are float64
		if !ok || uint64(appliedSeq) != 1 {
			t.Errorf("Expected appliedSeq=1, got %v (type %T)", status["appliedSeq"], status["appliedSeq"])
		}
		appliedBlock, ok := status["appliedBlock"].(float64)
		if !ok || uint64(appliedBlock) != 100 {
			t.Errorf("Expected appliedBlock=100, got %v (type %T)", status["appliedBlock"], status["appliedBlock"])
		}
	})

	// Test 2: Query balance
	t.Run("GetBalance", func(t *testing.T) {
		var balance hexutil.Big
		if err := client.CallContext(ctx, &balance, "ubt_getBalance", testAddr); err != nil {
			t.Fatalf("ubt_getBalance failed: %v", err)
		}

		if (*big.Int)(&balance).Cmp(testBalance) != 0 {
			t.Errorf("Expected balance=%s, got %s", testBalance, balance.ToInt())
		}
	})

	// Test 3: Query non-existent account (create new applier to ensure clean state)
	t.Run("GetBalance_NonExistent", func(t *testing.T) {
		tmpCfg := &Config{
			DataDir:            t.TempDir() + "/nonexistent",
			TrieDBScheme:       "path",
			TrieDBStateHistory: 128,
		}
		freshApplier, err := NewApplier(tmpCfg, common.Hash{})
		if err != nil {
			t.Fatalf("Failed to create fresh applier: %v", err)
		}
		defer freshApplier.Close()

		oldApplier := consumer.applier
		oldState := consumer.state
		consumer.applier = freshApplier
		consumer.state.AppliedRoot = freshApplier.Root()
		consumer.state.AppliedBlock = 0
		consumer.state.AppliedSeq = 0
		defer func() {
			consumer.applier = oldApplier
			consumer.state = oldState
		}()

		nonExistentAddr := common.HexToAddress("0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
		var balance hexutil.Big
		if err := client.CallContext(ctx, &balance, "ubt_getBalance", nonExistentAddr); err != nil {
			t.Fatalf("ubt_getBalance failed: %v", err)
		}

		if (*big.Int)(&balance).Cmp(big.NewInt(0)) != 0 {
			t.Errorf("Expected balance=0 for non-existent account, got %s", balance.ToInt())
		}
	})

	// Test 4: Query GetCode
	t.Run("GetCode", func(t *testing.T) {
		// Query code for an account without code
		var code hexutil.Bytes
		if err := client.CallContext(ctx, &code, "ubt_getCode", testAddr); err != nil {
			t.Fatalf("ubt_getCode failed: %v", err)
		}

		// Should return empty since account has no code
		if len(code) != 0 {
			t.Errorf("Expected empty code, got %d bytes", len(code))
		}
	})

	// Storage querying behavior is covered in verification tests that assert
	// deterministic value parity for account+slot reads.
}
