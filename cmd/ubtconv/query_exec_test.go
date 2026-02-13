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
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/rpc"
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

	result, err := api.CallUBT(ctx, args, nil, nil, nil)
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

	latest := rpc.BlockNumberOrHashWithNumber(rpc.LatestBlockNumber)
	result, err := api.ExecutionWitnessUBT(ctx, &latest)
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

func TestQueryAPI_CallUBT_Disabled(t *testing.T) {
	applier := newTestApplier(t)
	defer applier.Close()

	consumer := &Consumer{
		cfg:     &Config{ExecutionClassRPCEnabled: false},
		applier: applier,
	}
	api := NewQueryAPI(consumer)

	_, err := api.CallUBT(context.Background(), map[string]any{
		"to": "0x1234567890123456789012345678901234567890",
	}, nil, nil, nil)
	if err == nil {
		t.Fatal("expected disabled error")
	}
	if !strings.Contains(err.Error(), "execution-class RPC disabled") {
		t.Fatalf("expected disabled error, got: %v", err)
	}
}

func TestQueryAPI_ExecutionWitnessUBT_Disabled(t *testing.T) {
	applier := newTestApplier(t)
	defer applier.Close()

	consumer := &Consumer{
		cfg:     &Config{ExecutionClassRPCEnabled: false},
		applier: applier,
	}
	api := NewQueryAPI(consumer)

	latest := rpc.BlockNumberOrHashWithNumber(rpc.LatestBlockNumber)
	_, err := api.ExecutionWitnessUBT(context.Background(), &latest)
	if err == nil {
		t.Fatal("expected disabled error")
	}
	if !strings.Contains(err.Error(), "execution-class RPC disabled") {
		t.Fatalf("expected disabled error, got: %v", err)
	}
}

// TestCallUBT_SimpleBalance tests CallUBT success when execution RPC is enabled.
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
		cfg:     &Config{ChainID: 1, ExecutionClassRPCEnabled: true},
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
	}, nil, nil, nil)
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
		cfg:     &Config{ChainID: 1, ExecutionClassRPCEnabled: true},
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
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("CallUBT to non-existent should succeed: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("Expected empty result for non-existent account, got %x", result)
	}
}

func TestCallUBT_OverridesUnsupported(t *testing.T) {
	applier := newTestApplier(t)
	defer applier.Close()

	consumer := &Consumer{
		cfg: &Config{
			ExecutionClassRPCEnabled: true,
		},
		applier: applier,
		state: ConsumerState{
			AppliedBlock: 1,
			AppliedRoot:  applier.Root(),
		},
	}
	api := NewQueryAPI(consumer)
	ctx := context.Background()

	_, err := api.CallUBT(ctx, map[string]any{
		"to": "0x1234567890123456789012345678901234567890",
	}, nil, map[string]any{"0xabc": map[string]any{}}, nil)
	if err == nil {
		t.Fatal("expected stateOverrides unsupported error")
	}
	if !strings.Contains(err.Error(), "stateOverrides are not yet supported") {
		t.Fatalf("unexpected stateOverrides error: %v", err)
	}

	_, err = api.CallUBT(ctx, map[string]any{
		"to": "0x1234567890123456789012345678901234567890",
	}, nil, nil, map[string]any{"number": "0x1"})
	if err == nil {
		t.Fatal("expected blockOverrides unsupported error")
	}
	if !strings.Contains(err.Error(), "blockOverrides are not yet supported") {
		t.Fatalf("unexpected blockOverrides error: %v", err)
	}
}

func TestCallUBT_BlockSelectorVariants(t *testing.T) {
	applier := newTestApplier(t)
	defer applier.Close()

	addr := common.HexToAddress("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if _, err := applier.ApplyDiff(makeDiff(addr, 1, big.NewInt(1000))); err != nil {
		t.Fatalf("ApplyDiff block1: %v", err)
	}
	if err := applier.CommitAt(1); err != nil {
		t.Fatalf("Commit block1: %v", err)
	}
	root1 := applier.Root()

	if _, err := applier.ApplyDiff(makeDiff(addr, 2, big.NewInt(2000))); err != nil {
		t.Fatalf("ApplyDiff block2: %v", err)
	}
	if err := applier.CommitAt(2); err != nil {
		t.Fatalf("Commit block2: %v", err)
	}
	root2 := applier.Root()

	db := rawdb.NewMemoryDatabase()
	rawdb.WriteUBTBlockRoot(db, 1, root1)
	rawdb.WriteUBTBlockRoot(db, 2, root2)
	hash1 := common.HexToHash("0x1111")
	hash2 := common.HexToHash("0x2222")
	rawdb.WriteUBTCanonicalBlock(db, 1, hash1, common.Hash{})
	rawdb.WriteUBTCanonicalBlock(db, 2, hash2, hash1)

	consumer := &Consumer{
		cfg: &Config{
			ExecutionClassRPCEnabled: true,
			TrieDBStateHistory:       128,
		},
		applier: applier,
		db:      db,
		state: ConsumerState{
			AppliedSeq:   2,
			AppliedBlock: 2,
			AppliedRoot:  root2,
		},
	}
	api := NewQueryAPI(consumer)
	callArgs := map[string]any{"to": addr.Hex()}

	t.Run("nil selector uses latest", func(t *testing.T) {
		res, err := api.CallUBT(context.Background(), callArgs, nil, nil, nil)
		if err != nil {
			t.Fatalf("CallUBT nil selector: %v", err)
		}
		if len(res) != 0 {
			t.Fatalf("expected empty return for EOA call, got %x", res)
		}
	})

	t.Run("latest selector", func(t *testing.T) {
		latest := rpc.BlockNumberOrHashWithNumber(rpc.LatestBlockNumber)
		res, err := api.CallUBT(context.Background(), callArgs, &latest, nil, nil)
		if err != nil {
			t.Fatalf("CallUBT latest selector: %v", err)
		}
		if len(res) != 0 {
			t.Fatalf("expected empty return for EOA call, got %x", res)
		}
	})

	t.Run("number selector", func(t *testing.T) {
		block1 := rpc.BlockNumberOrHashWithNumber(1)
		res, err := api.CallUBT(context.Background(), callArgs, &block1, nil, nil)
		if err != nil {
			t.Fatalf("CallUBT number selector: %v", err)
		}
		if len(res) != 0 {
			t.Fatalf("expected empty return for EOA call, got %x", res)
		}
	})

	t.Run("hash selector", func(t *testing.T) {
		selector := rpc.BlockNumberOrHashWithHash(hash1, true)
		res, err := api.CallUBT(context.Background(), callArgs, &selector, nil, nil)
		if err != nil {
			t.Fatalf("CallUBT hash selector: %v", err)
		}
		if len(res) != 0 {
			t.Fatalf("expected empty return for EOA call, got %x", res)
		}
	})
}

// TestExecutionWitnessUBT_Basic verifies ExecutionWitnessUBT returns deterministic partial witness.
func TestExecutionWitnessUBT_Basic(t *testing.T) {
	applier := newTestApplier(t)
	defer applier.Close()

	addr := common.HexToAddress("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")

	diff := makeDiff(addr, 1, big.NewInt(1000))
	if _, err := applier.ApplyDiff(diff); err != nil {
		t.Fatalf("ApplyDiff: %v", err)
	}
	if err := applier.CommitAt(1); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	db := rawdb.NewMemoryDatabase()
	rawdb.WriteUBTBlockRoot(db, 1, applier.Root())

	consumer := &Consumer{
		cfg: &Config{
			ExecutionClassRPCEnabled: true,
			TrieDBStateHistory:       128,
		},
		applier: applier,
		db:      db,
		state: ConsumerState{
			AppliedSeq:   2,
			AppliedBlock: 2,
			AppliedRoot:  applier.Root(),
		},
	}
	api := NewQueryAPI(consumer)
	ctx := context.Background()

	block1 := rpc.BlockNumberOrHashWithNumber(1)
	result, err := api.ExecutionWitnessUBT(ctx, &block1)
	if err != nil {
		t.Fatalf("ExecutionWitnessUBT should succeed: %v", err)
	}
	if status, ok := result["status"].(string); !ok || status == "" {
		t.Fatalf("expected witness status field, got: %#v", result)
	}
	if root, ok := result["stateRoot"].(common.Hash); !ok || root == (common.Hash{}) {
		t.Fatalf("expected non-zero stateRoot, got: %#v", result["stateRoot"])
	}
}

// TestExecutionWitnessUBT_AheadOfHead tests that requesting a future block is rejected.
func TestExecutionWitnessUBT_AheadOfHead(t *testing.T) {
	applier := newTestApplier(t)
	defer applier.Close()

	consumer := &Consumer{
		cfg:     &Config{ExecutionClassRPCEnabled: true},
		applier: applier,
		state: ConsumerState{
			AppliedSeq:   5,
			AppliedBlock: 5,
			AppliedRoot:  common.Hash{0xab},
		},
	}
	api := NewQueryAPI(consumer)
	ctx := context.Background()

	block10 := rpc.BlockNumberOrHashWithNumber(10)
	_, err := api.ExecutionWitnessUBT(ctx, &block10)
	if err == nil {
		t.Fatal("expected error for ahead-of-head block")
	}
	if !strings.Contains(err.Error(), "state not yet available") {
		t.Errorf("expected state not yet available error, got: %v", err)
	}
}

func TestExecutionWitnessUBT_HistoryPruned(t *testing.T) {
	applier := newTestApplier(t)
	defer applier.Close()

	consumer := &Consumer{
		cfg: &Config{
			ExecutionClassRPCEnabled: true,
			TrieDBStateHistory:       1,
		},
		applier: applier,
		state: ConsumerState{
			AppliedSeq:   5,
			AppliedBlock: 5,
			AppliedRoot:  common.Hash{0xab},
		},
	}
	api := NewQueryAPI(consumer)

	block3 := rpc.BlockNumberOrHashWithNumber(3)
	_, err := api.ExecutionWitnessUBT(context.Background(), &block3)
	if err == nil {
		t.Fatal("expected history-pruned error")
	}
	if !strings.Contains(err.Error(), "outside retained UBT state history window") {
		t.Fatalf("expected history window error, got: %v", err)
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

	// End-to-end: CallUBT works with non-default chain ID when execution RPC is enabled.
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
			cfg:     &Config{ChainID: 11155111, ExecutionClassRPCEnabled: true},
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
		}, nil, nil, nil)
		if err != nil {
			t.Fatalf("CallUBT with sepolia chain ID should succeed: %v", err)
		}
		if len(result) != 0 {
			t.Errorf("Expected empty result, got %x", result)
		}
	})
}
