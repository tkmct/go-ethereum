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

package eth

// Verification Gate B: Debug proxy wiring integration test.
// Tests the full chain: UBTDebugAPI (geth side) → RPC → mock daemon QueryAPI.

import (
	"context"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/rpc"
)

// mockDaemonAPI implements the ubt_* namespace that the debug proxy forwards to.
type mockDaemonAPI struct {
	balances map[common.Address]*big.Int
	status   map[string]any
}

func validateMockSelector(blockNrOrHash *rpc.BlockNumberOrHash) error {
	if blockNrOrHash == nil {
		return nil
	}
	if bn, ok := blockNrOrHash.Number(); ok {
		switch bn {
		case rpc.LatestBlockNumber:
			return nil
		case rpc.PendingBlockNumber, rpc.SafeBlockNumber, rpc.FinalizedBlockNumber:
			return fmt.Errorf("unsupported block selector tag for UBT debug RPC: %s", bn.String())
		}
		if bn > 200 {
			return fmt.Errorf("state not yet available: requested block %d is ahead of daemon applied head 200", bn)
		}
		if bn < 100 {
			return fmt.Errorf("state not available: block %d is outside retained UBT state history window", bn)
		}
		return nil
	}
	if hash, ok := blockNrOrHash.Hash(); ok {
		if hash != common.HexToHash("0x1111") {
			return fmt.Errorf("state not available: unknown canonical block hash %s", hash)
		}
		return nil
	}
	return fmt.Errorf("invalid block selector")
}

func (m *mockDaemonAPI) GetBalance(_ context.Context, addr common.Address, blockNrOrHash *rpc.BlockNumberOrHash) (*hexutil.Big, error) {
	if err := validateMockSelector(blockNrOrHash); err != nil {
		return nil, err
	}
	if bal, ok := m.balances[addr]; ok {
		return (*hexutil.Big)(bal), nil
	}
	return (*hexutil.Big)(common.Big0), nil
}

func (m *mockDaemonAPI) GetStorageAt(_ context.Context, _ common.Address, _ common.Hash, blockNrOrHash *rpc.BlockNumberOrHash) (hexutil.Bytes, error) {
	if err := validateMockSelector(blockNrOrHash); err != nil {
		return nil, err
	}
	return make(hexutil.Bytes, 32), nil
}

func (m *mockDaemonAPI) GetCode(_ context.Context, _ common.Address, blockNrOrHash *rpc.BlockNumberOrHash) (hexutil.Bytes, error) {
	if err := validateMockSelector(blockNrOrHash); err != nil {
		return nil, err
	}
	return hexutil.Bytes{}, nil
}

func (m *mockDaemonAPI) Status(_ context.Context) (map[string]any, error) {
	return m.status, nil
}

func (m *mockDaemonAPI) GetProof(_ context.Context, key common.Hash, blockNrOrHash *rpc.BlockNumberOrHash) (*UBTProofResult, error) {
	if err := validateMockSelector(blockNrOrHash); err != nil {
		return nil, err
	}
	return &UBTProofResult{
		Key:        key,
		Root:       common.HexToHash("0xabcd"),
		ProofNodes: map[common.Hash]hexutil.Bytes{common.HexToHash("0x01"): {0x01}},
	}, nil
}

func (m *mockDaemonAPI) GetAccountProof(_ context.Context, addr common.Address, _ []common.Hash, blockNrOrHash *rpc.BlockNumberOrHash) (*UBTAccountProofResult, error) {
	if err := validateMockSelector(blockNrOrHash); err != nil {
		return nil, err
	}
	return &UBTAccountProofResult{
		Address:      addr,
		AccountProof: map[common.Hash]hexutil.Bytes{common.HexToHash("0x01"): {0x01}},
		Root:         common.HexToHash("0xabcd"),
	}, nil
}

func (m *mockDaemonAPI) CallUBT(_ context.Context, _ map[string]any) (hexutil.Bytes, error) {
	return nil, fmt.Errorf("ubt_callUBT: execution-class RPC not yet available (Phase 7)")
}

func (m *mockDaemonAPI) ExecutionWitnessUBT(_ context.Context, _ hexutil.Uint64) (map[string]any, error) {
	return nil, fmt.Errorf("ubt_executionWitnessUBT: execution-class RPC not yet available (Phase 7)")
}

// startMockDaemon starts a mock UBT daemon RPC server and returns the endpoint URL.
func startMockDaemon(t *testing.T) (string, func()) {
	t.Helper()

	server := rpc.NewServer()
	daemon := &mockDaemonAPI{
		balances: map[common.Address]*big.Int{
			common.HexToAddress("0x1111111111111111111111111111111111111111"): big.NewInt(42000),
			common.HexToAddress("0x2222222222222222222222222222222222222222"): big.NewInt(99000),
		},
		status: map[string]any{
			"appliedSeq":   uint64(100),
			"appliedBlock": uint64(200),
			"appliedRoot":  common.HexToHash("0xbeef"),
		},
	}

	if err := server.RegisterName("ubt", daemon); err != nil {
		t.Fatalf("register mock daemon: %v", err)
	}

	listener, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	httpSrv := &http.Server{Handler: server}
	go httpSrv.Serve(listener)

	endpoint := "http://" + listener.Addr().String()
	cleanup := func() {
		httpSrv.Close()
	}
	return endpoint, cleanup
}

// TestDebugProxyWiring verifies the full proxy chain:
// UBTDebugAPI.GetUBTBalance → RPC call → mock daemon → response.
func TestDebugProxyWiring(t *testing.T) {
	endpoint, cleanup := startMockDaemon(t)
	defer cleanup()

	api := NewUBTDebugAPI(endpoint, 5*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Test GetUBTBalance proxies correctly
	t.Run("GetUBTBalance", func(t *testing.T) {
		addr := common.HexToAddress("0x1111111111111111111111111111111111111111")
		balance, err := api.GetUBTBalance(ctx, addr, nil)
		if err != nil {
			t.Fatalf("GetUBTBalance: %v", err)
		}
		if balance.ToInt().Cmp(big.NewInt(42000)) != 0 {
			t.Errorf("expected balance=42000, got %s", balance.ToInt())
		}
	})

	// Test GetUBTBalance for different address
	t.Run("GetUBTBalance second addr", func(t *testing.T) {
		addr := common.HexToAddress("0x2222222222222222222222222222222222222222")
		balance, err := api.GetUBTBalance(ctx, addr, nil)
		if err != nil {
			t.Fatalf("GetUBTBalance: %v", err)
		}
		if balance.ToInt().Cmp(big.NewInt(99000)) != 0 {
			t.Errorf("expected balance=99000, got %s", balance.ToInt())
		}
	})

	// Test GetUBTBalance for non-existent address
	t.Run("GetUBTBalance non-existent", func(t *testing.T) {
		addr := common.HexToAddress("0xdead")
		balance, err := api.GetUBTBalance(ctx, addr, nil)
		if err != nil {
			t.Fatalf("GetUBTBalance: %v", err)
		}
		if balance.ToInt().Cmp(big.NewInt(0)) != 0 {
			t.Errorf("expected balance=0, got %s", balance.ToInt())
		}
	})

	// Test GetUBTStorageAt proxies correctly
	t.Run("GetUBTStorageAt", func(t *testing.T) {
		addr := common.HexToAddress("0x1111111111111111111111111111111111111111")
		slot := common.HexToHash("0x01")
		value, err := api.GetUBTStorageAt(ctx, addr, slot, nil)
		if err != nil {
			t.Fatalf("GetUBTStorageAt: %v", err)
		}
		if len(value) != 32 {
			t.Errorf("expected 32-byte value, got %d", len(value))
		}
	})

	// Test GetUBTCode proxies correctly
	t.Run("GetUBTCode", func(t *testing.T) {
		addr := common.HexToAddress("0x1111111111111111111111111111111111111111")
		code, err := api.GetUBTCode(ctx, addr, nil)
		if err != nil {
			t.Fatalf("GetUBTCode: %v", err)
		}
		if len(code) != 0 {
			t.Errorf("expected empty code, got %d bytes", len(code))
		}
	})

	// Test GetUBTStatus proxies correctly
	t.Run("GetUBTStatus", func(t *testing.T) {
		status, err := api.GetUBTStatus(ctx)
		if err != nil {
			t.Fatalf("GetUBTStatus: %v", err)
		}
		// JSON numbers decode as float64
		if seq, ok := status["appliedSeq"].(float64); !ok || uint64(seq) != 100 {
			t.Errorf("expected appliedSeq=100, got %v (type %T)", status["appliedSeq"], status["appliedSeq"])
		}
		if block, ok := status["appliedBlock"].(float64); !ok || uint64(block) != 200 {
			t.Errorf("expected appliedBlock=200, got %v", status["appliedBlock"])
		}
	})

	// Test GetUBTRawProof proxies correctly (raw-key form)
	t.Run("GetUBTRawProof", func(t *testing.T) {
		key := common.HexToHash("0xabcdef")
		proof, err := api.GetUBTRawProof(ctx, key, nil)
		if err != nil {
			t.Fatalf("GetUBTRawProof: %v", err)
		}
		if proof.Key != key {
			t.Errorf("expected proof key=%s, got %s", key, proof.Key)
		}
		if len(proof.ProofNodes) == 0 {
			t.Error("expected non-empty proof nodes")
		}
	})

	// Test GetUBTProof (now address+storageKeys form) proxies correctly
	t.Run("GetUBTProof", func(t *testing.T) {
		addr := common.HexToAddress("0x1111111111111111111111111111111111111111")
		proof, err := api.GetUBTProof(ctx, addr, nil, nil)
		if err != nil {
			t.Fatalf("GetUBTProof: %v", err)
		}
		if proof.Address != addr {
			t.Errorf("expected proof address=%s, got %s", addr, proof.Address)
		}
	})

	// Test GetUBTAccountProof proxies correctly
	t.Run("GetUBTAccountProof", func(t *testing.T) {
		addr := common.HexToAddress("0x1111111111111111111111111111111111111111")
		proof, err := api.GetUBTAccountProof(ctx, addr, nil, nil)
		if err != nil {
			t.Fatalf("GetUBTAccountProof: %v", err)
		}
		if proof.Address != addr {
			t.Errorf("expected address=%s, got %s", addr, proof.Address)
		}
		if len(proof.AccountProof) == 0 {
			t.Error("expected non-empty account proof")
		}
	})

	// Test CallUBT returns Phase 7 error through proxy
	t.Run("CallUBT Phase 7", func(t *testing.T) {
		args := map[string]any{"to": "0x1234"}
		_, err := api.CallUBT(ctx, args)
		if err == nil {
			t.Fatal("CallUBT should return error")
		}
		if !strings.Contains(err.Error(), "Phase 7") {
			t.Errorf("expected Phase 7 error, got: %v", err)
		}
	})

	// Test ExecutionWitnessUBT returns Phase 7 error through proxy
	t.Run("ExecutionWitnessUBT Phase 7", func(t *testing.T) {
		_, err := api.ExecutionWitnessUBT(ctx, 12345)
		if err == nil {
			t.Fatal("ExecutionWitnessUBT should return error")
		}
		if !strings.Contains(err.Error(), "Phase 7") {
			t.Errorf("expected Phase 7 error, got: %v", err)
		}
	})
}

// TestDebugProxyReconnect verifies that the proxy recovers from connection errors.
func TestDebugProxyReconnect(t *testing.T) {
	endpoint, cleanup := startMockDaemon(t)
	api := NewUBTDebugAPI(endpoint, 2*time.Second)
	ctx := context.Background()

	// First call should succeed
	addr := common.HexToAddress("0x1111111111111111111111111111111111111111")
	balance, err := api.GetUBTBalance(ctx, addr, nil)
	if err != nil {
		t.Fatalf("first GetUBTBalance: %v", err)
	}
	if balance.ToInt().Cmp(big.NewInt(42000)) != 0 {
		t.Errorf("expected balance=42000, got %s", balance.ToInt())
	}

	// Shut down the daemon
	cleanup()

	// Second call should fail (server gone)
	_, err = api.GetUBTBalance(ctx, addr, nil)
	if err == nil {
		t.Fatal("expected error after daemon shutdown")
	}

	// Start a new daemon on a different port
	endpoint2, cleanup2 := startMockDaemon(t)
	defer cleanup2()

	// Create a new API pointing to the new endpoint
	api2 := NewUBTDebugAPI(endpoint2, 2*time.Second)
	balance2, err := api2.GetUBTBalance(ctx, addr, nil)
	if err != nil {
		t.Fatalf("GetUBTBalance after reconnect: %v", err)
	}
	if balance2.ToInt().Cmp(big.NewInt(42000)) != 0 {
		t.Errorf("expected balance=42000 after reconnect, got %s", balance2.ToInt())
	}
}

// TestDebugProxyBlockSelector verifies block selector is forwarded to daemon.
func TestDebugProxyBlockSelector(t *testing.T) {
	endpoint, cleanup := startMockDaemon(t)
	defer cleanup()

	api := NewUBTDebugAPI(endpoint, 5*time.Second)
	ctx := context.Background()

	addr := common.HexToAddress("0x1111111111111111111111111111111111111111")

	// nil block selector
	t.Run("nil", func(t *testing.T) {
		_, err := api.GetUBTBalance(ctx, addr, nil)
		if err != nil {
			t.Fatalf("nil selector: %v", err)
		}
	})

	// latest block selector
	t.Run("latest", func(t *testing.T) {
		latest := rpc.BlockNumberOrHashWithNumber(rpc.LatestBlockNumber)
		_, err := api.GetUBTBalance(ctx, addr, &latest)
		if err != nil {
			t.Fatalf("latest selector: %v", err)
		}
	})

	t.Run("pending rejected", func(t *testing.T) {
		pending := rpc.BlockNumberOrHashWithNumber(rpc.PendingBlockNumber)
		_, err := api.GetUBTBalance(ctx, addr, &pending)
		if err == nil {
			t.Fatal("pending selector should be rejected")
		}
		if !strings.Contains(err.Error(), "unsupported block selector tag") {
			t.Fatalf("unexpected pending error: %v", err)
		}
	})

	t.Run("ahead head rejected", func(t *testing.T) {
		block201 := rpc.BlockNumberOrHashWithNumber(201)
		_, err := api.GetUBTBalance(ctx, addr, &block201)
		if err == nil {
			t.Fatal("ahead-head selector should be rejected")
		}
		if !strings.Contains(err.Error(), "state not yet available") {
			t.Fatalf("unexpected ahead-head error: %v", err)
		}
	})

	t.Run("hash selector", func(t *testing.T) {
		blockHash := rpc.BlockNumberOrHashWithHash(common.HexToHash("0x1111"), true)
		_, err := api.GetUBTBalance(ctx, addr, &blockHash)
		if err != nil {
			t.Fatalf("hash selector should succeed: %v", err)
		}
	})
}
