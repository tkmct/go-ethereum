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
)

// TestQueryAPI_CallUBT_NoApplier tests that CallUBT returns error when applier is nil.
func TestQueryAPI_CallUBT_NoApplier(t *testing.T) {
	consumer := &Consumer{}
	api := NewQueryAPI(consumer)
	ctx := context.Background()

	args := map[string]any{
		"to":   "0x1234567890123456789012345678901234567890",
		"data": "0xabcdef",
	}

	result, err := api.CallUBT(ctx, args)
	if err == nil {
		t.Fatal("Expected error for CallUBT without applier, got nil")
	}
	if result != nil {
		t.Errorf("Expected nil result, got %v", result)
	}
	if !strings.Contains(err.Error(), "not initialized") {
		t.Errorf("Expected 'not initialized' error, got: %v", err)
	}
}

// TestQueryAPI_ExecutionWitnessUBT_NoApplier tests that ExecutionWitnessUBT returns error when applier is nil.
func TestQueryAPI_ExecutionWitnessUBT_NoApplier(t *testing.T) {
	consumer := &Consumer{}
	api := NewQueryAPI(consumer)
	ctx := context.Background()

	blockNumber := hexutil.Uint64(12345)

	result, err := api.ExecutionWitnessUBT(ctx, blockNumber)
	if err == nil {
		t.Fatal("Expected error for ExecutionWitnessUBT without applier, got nil")
	}
	if result != nil {
		t.Errorf("Expected nil result, got %v", result)
	}
	if !strings.Contains(err.Error(), "not initialized") {
		t.Errorf("Expected 'not initialized' error, got: %v", err)
	}
}

// TestCallUBT_SimpleBalance tests CallUBT to read balance of a funded account.
func TestCallUBT_SimpleBalance(t *testing.T) {
	applier := newTestApplier(t)
	defer applier.Close()

	// Set up an account with balance
	addr := common.HexToAddress("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	diff := makeDiff(addr, 0, big.NewInt(1000000))
	if _, err := applier.ApplyDiff(diff); err != nil {
		t.Fatalf("ApplyDiff: %v", err)
	}
	if err := applier.CommitAt(1); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	consumer := &Consumer{
		cfg:     &Config{ChainID: 1},
		applier: applier,
		state: ConsumerState{
			AppliedSeq:   1,
			AppliedBlock: 1,
			AppliedRoot:  applier.Root(),
		},
	}
	api := NewQueryAPI(consumer)
	ctx := context.Background()

	// Call to the funded address (no data = just check it doesn't error)
	result, err := api.CallUBT(ctx, map[string]any{
		"to": addr.Hex(),
	})
	// A simple call to an EOA with no code should succeed with empty result
	if err != nil {
		t.Fatalf("CallUBT should succeed for EOA call: %v", err)
	}
	// Result should be empty bytes for a call to an account with no code
	if len(result) != 0 {
		t.Errorf("Expected empty result for EOA call, got %x", result)
	}
}

// TestCallUBT_NonExistentAccount tests CallUBT to a non-existent address.
func TestCallUBT_NonExistentAccount(t *testing.T) {
	applier := newTestApplier(t)
	defer applier.Close()

	// Set up minimal state (need at least one account so trie is non-empty)
	addr := common.HexToAddress("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	diff := makeDiff(addr, 0, big.NewInt(1000))
	if _, err := applier.ApplyDiff(diff); err != nil {
		t.Fatalf("ApplyDiff: %v", err)
	}
	if err := applier.CommitAt(1); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	consumer := &Consumer{
		cfg:     &Config{ChainID: 1},
		applier: applier,
		state: ConsumerState{
			AppliedSeq:   1,
			AppliedBlock: 1,
			AppliedRoot:  applier.Root(),
		},
	}
	api := NewQueryAPI(consumer)
	ctx := context.Background()

	nonExistent := common.HexToAddress("0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	result, err := api.CallUBT(ctx, map[string]any{
		"to": nonExistent.Hex(),
	})
	if err != nil {
		t.Fatalf("CallUBT to non-existent should succeed: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("Expected empty result for non-existent account, got %x", result)
	}
}

// TestExecutionWitnessUBT_Basic tests that ExecutionWitnessUBT returns pre/post state roots.
func TestExecutionWitnessUBT_Basic(t *testing.T) {
	applier := newTestApplier(t)
	defer applier.Close()

	addr := common.HexToAddress("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")

	// Block 1: create account
	diff1 := makeDiff(addr, 1, big.NewInt(1000))
	if _, err := applier.ApplyDiff(diff1); err != nil {
		t.Fatalf("ApplyDiff #1: %v", err)
	}
	if err := applier.CommitAt(1); err != nil {
		t.Fatalf("Commit #1: %v", err)
	}
	root1 := applier.Root()

	// Block 2: update balance
	diff2 := makeDiff(addr, 2, big.NewInt(2000))
	if _, err := applier.ApplyDiff(diff2); err != nil {
		t.Fatalf("ApplyDiff #2: %v", err)
	}
	if err := applier.CommitAt(2); err != nil {
		t.Fatalf("Commit #2: %v", err)
	}
	root2 := applier.Root()

	// Write UBT block roots
	db := rawdb.NewMemoryDatabase()
	rawdb.WriteUBTBlockRoot(db, 1, root1)
	rawdb.WriteUBTBlockRoot(db, 2, root2)

	consumer := &Consumer{
		applier: applier,
		db:      db,
		state: ConsumerState{
			AppliedSeq:   2,
			AppliedBlock: 2,
			AppliedRoot:  root2,
		},
	}
	api := NewQueryAPI(consumer)
	ctx := context.Background()

	result, err := api.ExecutionWitnessUBT(ctx, hexutil.Uint64(2))
	if err != nil {
		t.Fatalf("ExecutionWitnessUBT: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result["blockNumber"] != uint64(2) {
		t.Errorf("expected blockNumber=2, got %v", result["blockNumber"])
	}
	if result["preStateRoot"] != root1 {
		t.Errorf("expected preStateRoot=%s, got %v", root1, result["preStateRoot"])
	}
	if result["postStateRoot"] != root2 {
		t.Errorf("expected postStateRoot=%s, got %v", root2, result["postStateRoot"])
	}
}

// TestExecutionWitnessUBT_AheadOfHead tests that requesting a future block is rejected.
func TestExecutionWitnessUBT_AheadOfHead(t *testing.T) {
	applier := newTestApplier(t)
	defer applier.Close()

	consumer := &Consumer{
		applier: applier,
		state: ConsumerState{
			AppliedSeq:   5,
			AppliedBlock: 5,
			AppliedRoot:  common.Hash{0xab},
		},
	}
	api := NewQueryAPI(consumer)
	ctx := context.Background()

	_, err := api.ExecutionWitnessUBT(ctx, hexutil.Uint64(10))
	if err == nil {
		t.Fatal("expected error for ahead-of-head block")
	}
	if !strings.Contains(err.Error(), "not yet applied") {
		t.Errorf("expected 'not yet applied' error, got: %v", err)
	}
}

// --- Part B: Tests for review findings ---

// TestCallUBT_GasCap tests that the RPC gas cap is enforced.
func TestCallUBT_GasCap(t *testing.T) {
	applier := newTestApplier(t)
	defer applier.Close()

	addr := common.HexToAddress("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	diff := makeDiff(addr, 0, big.NewInt(1000000))
	if _, err := applier.ApplyDiff(diff); err != nil {
		t.Fatalf("ApplyDiff: %v", err)
	}
	if err := applier.CommitAt(1); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	// Use a small gas cap for testing
	gasCap := uint64(1_000_000)

	t.Run("gas exceeding cap is capped", func(t *testing.T) {
		msg, err := parseCallArgs(map[string]any{
			"to":  addr.Hex(),
			"gas": "0x5F5E100", // 100_000_000, exceeds cap
		}, gasCap)
		if err != nil {
			t.Fatalf("parseCallArgs: %v", err)
		}
		if msg.GasLimit != gasCap {
			t.Errorf("expected gas to be capped to %d, got %d", gasCap, msg.GasLimit)
		}
	})

	t.Run("gas within cap is preserved", func(t *testing.T) {
		msg, err := parseCallArgs(map[string]any{
			"to":  addr.Hex(),
			"gas": "0x7A120", // 500_000, within cap
		}, gasCap)
		if err != nil {
			t.Fatalf("parseCallArgs: %v", err)
		}
		if msg.GasLimit != 500_000 {
			t.Errorf("expected gas=500000, got %d", msg.GasLimit)
		}
	})

	t.Run("absent gas defaults to cap", func(t *testing.T) {
		msg, err := parseCallArgs(map[string]any{
			"to": addr.Hex(),
		}, gasCap)
		if err != nil {
			t.Fatalf("parseCallArgs: %v", err)
		}
		if msg.GasLimit != gasCap {
			t.Errorf("expected default gas=%d, got %d", gasCap, msg.GasLimit)
		}
	})

	t.Run("default RPCGasCap is 50M", func(t *testing.T) {
		cfg := &Config{} // RPCGasCap unset
		if cfg.effectiveRPCGasCap() != 50_000_000 {
			t.Errorf("expected default RPCGasCap=50000000, got %d", cfg.effectiveRPCGasCap())
		}
	})

	t.Run("configured RPCGasCap is respected", func(t *testing.T) {
		cfg := &Config{RPCGasCap: 25_000_000}
		if cfg.effectiveRPCGasCap() != 25_000_000 {
			t.Errorf("expected RPCGasCap=25000000, got %d", cfg.effectiveRPCGasCap())
		}
	})
}

// TestParseCallArgs_InvalidAddress tests that invalid addresses are rejected.
func TestParseCallArgs_InvalidAddress(t *testing.T) {
	gasCap := uint64(50_000_000)

	tests := []struct {
		name    string
		args    map[string]any
		wantErr string
	}{
		{
			name:    "invalid from address",
			args:    map[string]any{"from": "not-an-address", "to": "0x1234567890123456789012345678901234567890"},
			wantErr: "invalid from address",
		},
		{
			name:    "invalid to address with bad hex",
			args:    map[string]any{"to": "0xZZZZ"},
			wantErr: "invalid to address",
		},
		{
			name:    "empty from address",
			args:    map[string]any{"from": "", "to": "0x1234567890123456789012345678901234567890"},
			wantErr: "invalid from address",
		},
		{
			name:    "empty to address",
			args:    map[string]any{"to": ""},
			wantErr: "invalid to address",
		},
		{
			name:    "short from address",
			args:    map[string]any{"from": "0x1234"},
			wantErr: "invalid from address",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseCallArgs(tt.args, gasCap)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("expected error containing %q, got: %v", tt.wantErr, err)
			}
		})
	}

	// Valid addresses should succeed
	t.Run("valid addresses succeed", func(t *testing.T) {
		msg, err := parseCallArgs(map[string]any{
			"from": "0x1234567890123456789012345678901234567890",
			"to":   "0xabcdefabcdefabcdefabcdefabcdefabcdefabcd",
		}, gasCap)
		if err != nil {
			t.Fatalf("expected success for valid addresses: %v", err)
		}
		if msg.From != common.HexToAddress("0x1234567890123456789012345678901234567890") {
			t.Errorf("from address mismatch: %s", msg.From)
		}
		if msg.To == nil || *msg.To != common.HexToAddress("0xabcdefabcdefabcdefabcdefabcdefabcdefabcd") {
			t.Errorf("to address mismatch")
		}
	})

	// No from/to should also succeed (from defaults to zero, to is nil)
	t.Run("no addresses succeeds", func(t *testing.T) {
		msg, err := parseCallArgs(map[string]any{
			"data": "0xabcdef",
		}, gasCap)
		if err != nil {
			t.Fatalf("expected success with no addresses: %v", err)
		}
		if msg.From != (common.Address{}) {
			t.Errorf("expected zero from address, got %s", msg.From)
		}
		if msg.To != nil {
			t.Errorf("expected nil to address")
		}
	})
}

// TestCallUBT_ChainID tests that chain ID wiring works correctly.
func TestCallUBT_ChainID(t *testing.T) {
	t.Run("mainnet chain config for chain ID 1", func(t *testing.T) {
		cfg := &Config{ChainID: 1}
		cc := cfg.resolveChainConfig()
		if cc.ChainID.Uint64() != 1 {
			t.Errorf("expected chain ID 1, got %d", cc.ChainID.Uint64())
		}
	})

	t.Run("mainnet chain config for chain ID 0 (default)", func(t *testing.T) {
		cfg := &Config{ChainID: 0}
		cc := cfg.resolveChainConfig()
		if cc.ChainID.Uint64() != 1 {
			t.Errorf("expected chain ID 1 for default, got %d", cc.ChainID.Uint64())
		}
	})

	t.Run("sepolia chain config", func(t *testing.T) {
		cfg := &Config{ChainID: 11155111}
		cc := cfg.resolveChainConfig()
		if cc.ChainID.Uint64() != 11155111 {
			t.Errorf("expected chain ID 11155111, got %d", cc.ChainID.Uint64())
		}
	})

	t.Run("holesky chain config", func(t *testing.T) {
		cfg := &Config{ChainID: 17000}
		cc := cfg.resolveChainConfig()
		if cc.ChainID.Uint64() != 17000 {
			t.Errorf("expected chain ID 17000, got %d", cc.ChainID.Uint64())
		}
	})

	t.Run("unknown chain ID uses AllEthash with custom ID", func(t *testing.T) {
		cfg := &Config{ChainID: 42161} // Arbitrum One
		cc := cfg.resolveChainConfig()
		if cc.ChainID.Uint64() != 42161 {
			t.Errorf("expected chain ID 42161, got %d", cc.ChainID.Uint64())
		}
	})

	// End-to-end: CallUBT with non-default chain ID doesn't error
	t.Run("CallUBT with sepolia chain ID", func(t *testing.T) {
		applier := newTestApplier(t)
		defer applier.Close()

		addr := common.HexToAddress("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
		diff := makeDiff(addr, 0, big.NewInt(1000000))
		if _, err := applier.ApplyDiff(diff); err != nil {
			t.Fatalf("ApplyDiff: %v", err)
		}
		if err := applier.CommitAt(1); err != nil {
			t.Fatalf("Commit: %v", err)
		}

		consumer := &Consumer{
			cfg:     &Config{ChainID: 11155111},
			applier: applier,
			state: ConsumerState{
				AppliedSeq:   1,
				AppliedBlock: 1,
				AppliedRoot:  applier.Root(),
			},
		}
		api := NewQueryAPI(consumer)

		result, err := api.CallUBT(context.Background(), map[string]any{
			"to": addr.Hex(),
		})
		if err != nil {
			t.Fatalf("CallUBT with sepolia chain ID should succeed: %v", err)
		}
		if len(result) != 0 {
			t.Errorf("Expected empty result, got %x", result)
		}
	})
}
