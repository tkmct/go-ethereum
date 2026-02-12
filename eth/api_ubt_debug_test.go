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

import (
	"context"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
)

// TestNewUBTDebugAPI tests that NewUBTDebugAPI creates a valid instance.
func TestNewUBTDebugAPI(t *testing.T) {
	endpoint := "http://localhost:8546"
	timeout := 5 * time.Second

	api := NewUBTDebugAPI(endpoint, timeout)
	if api == nil {
		t.Fatal("Expected non-nil API")
	}
	if api.endpoint != endpoint {
		t.Errorf("Expected endpoint=%s, got %s", endpoint, api.endpoint)
	}
	if api.timeout != timeout {
		t.Errorf("Expected timeout=%v, got %v", timeout, api.timeout)
	}
	if api.client != nil {
		t.Error("Expected client to be nil initially")
	}
}

// TestUBTDebugAPI_ConnectError tests that getClient succeeds but RPC calls fail for bad endpoint.
func TestUBTDebugAPI_ConnectError(t *testing.T) {
	// The RPC DialContext succeeds even for invalid endpoints (lazy connection).
	// The actual error occurs when trying to make an RPC call.
	api := NewUBTDebugAPI("http://invalid-host-that-does-not-exist:99999", 1*time.Second)

	// getClient should succeed (lazy dial)
	client, err := api.getClient()
	if err != nil {
		// Some implementations may fail immediately, which is also acceptable
		t.Logf("getClient() failed immediately: %v", err)
		return
	}

	// The client should be non-nil after getClient
	if client == nil {
		t.Error("Expected client to be non-nil after getClient")
	}
}

// TestUBTDebugAPI_GetUBTBalance_NoConnection tests GetUBTBalance fails without connection.
func TestUBTDebugAPI_GetUBTBalance_NoConnection(t *testing.T) {
	api := NewUBTDebugAPI("http://invalid-host:99999", 1*time.Second)
	ctx := context.Background()

	addr := common.HexToAddress("0x1234")
	_, err := api.GetUBTBalance(ctx, addr, nil)
	if err == nil {
		t.Fatal("Expected error when calling GetUBTBalance without valid connection")
	}
}

// TestUBTDebugAPI_GetUBTStorageAt_NoConnection tests GetUBTStorageAt fails without connection.
func TestUBTDebugAPI_GetUBTStorageAt_NoConnection(t *testing.T) {
	api := NewUBTDebugAPI("http://invalid-host:99999", 1*time.Second)
	ctx := context.Background()

	addr := common.HexToAddress("0x1234")
	slot := common.HexToHash("0x5678")
	_, err := api.GetUBTStorageAt(ctx, addr, slot, nil)
	if err == nil {
		t.Fatal("Expected error when calling GetUBTStorageAt without valid connection")
	}
}

// TestUBTDebugAPI_GetUBTCode_NoConnection tests GetUBTCode fails without connection.
func TestUBTDebugAPI_GetUBTCode_NoConnection(t *testing.T) {
	api := NewUBTDebugAPI("http://invalid-host:99999", 1*time.Second)
	ctx := context.Background()

	addr := common.HexToAddress("0x1234")
	_, err := api.GetUBTCode(ctx, addr, nil)
	if err == nil {
		t.Fatal("Expected error when calling GetUBTCode without valid connection")
	}
}

// TestUBTDebugAPI_GetUBTStatus_NoConnection tests GetUBTStatus fails without connection.
func TestUBTDebugAPI_GetUBTStatus_NoConnection(t *testing.T) {
	api := NewUBTDebugAPI("http://invalid-host:99999", 1*time.Second)
	ctx := context.Background()

	_, err := api.GetUBTStatus(ctx)
	if err == nil {
		t.Fatal("Expected error when calling GetUBTStatus without valid connection")
	}
}

// TestUBTDebugAPI_GetUBTProof_NoConnection tests GetUBTProof fails without connection.
func TestUBTDebugAPI_GetUBTProof_NoConnection(t *testing.T) {
	api := NewUBTDebugAPI("http://invalid-host:99999", 1*time.Second)
	ctx := context.Background()

	addr := common.HexToAddress("0x1234567890123456789012345678901234567890")
	_, err := api.GetUBTProof(ctx, addr, nil, nil)
	if err == nil {
		t.Fatal("Expected error when calling GetUBTProof without valid connection")
	}
}

// TestUBTDebugAPI_GetUBTRawProof_NoConnection tests GetUBTRawProof fails without connection.
func TestUBTDebugAPI_GetUBTRawProof_NoConnection(t *testing.T) {
	api := NewUBTDebugAPI("http://invalid-host:99999", 1*time.Second)
	ctx := context.Background()

	key := common.HexToHash("0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef")
	_, err := api.GetUBTRawProof(ctx, key, nil)
	if err == nil {
		t.Fatal("Expected error when calling GetUBTRawProof without valid connection")
	}
}

// TestUBTProofResult_Structure tests that UBTProofResult has the expected fields.
func TestUBTProofResult_Structure(t *testing.T) {
	// Create a sample UBTProofResult to verify structure
	key := common.HexToHash("0x1234")
	root := common.HexToHash("0x5678")
	proofNodes := make(map[common.Hash]hexutil.Bytes)
	proofNodes[common.HexToHash("0xabcd")] = hexutil.Bytes{0x01, 0x02, 0x03}

	result := &UBTProofResult{
		Key:        key,
		Root:       root,
		ProofNodes: proofNodes,
	}

	if result.Key != key {
		t.Errorf("Expected Key=%v, got %v", key, result.Key)
	}
	if result.Root != root {
		t.Errorf("Expected Root=%v, got %v", root, result.Root)
	}
	if len(result.ProofNodes) != 1 {
		t.Errorf("Expected 1 proof node, got %d", len(result.ProofNodes))
	}
}

// TestUBTDebugAPI_CallUBT_NoConnection tests CallUBT fails without connection.
func TestUBTDebugAPI_CallUBT_NoConnection(t *testing.T) {
	api := NewUBTDebugAPI("http://invalid-host:99999", 1*time.Second)
	ctx := context.Background()

	args := map[string]any{
		"to":   "0x1234567890123456789012345678901234567890",
		"data": "0xabcdef",
	}
	_, err := api.CallUBT(ctx, args)
	if err == nil {
		t.Fatal("Expected error when calling CallUBT without valid connection")
	}
}

// TestUBTDebugAPI_ExecutionWitnessUBT_NoConnection tests ExecutionWitnessUBT fails without connection.
func TestUBTDebugAPI_ExecutionWitnessUBT_NoConnection(t *testing.T) {
	api := NewUBTDebugAPI("http://invalid-host:99999", 1*time.Second)
	ctx := context.Background()

	_, err := api.ExecutionWitnessUBT(ctx, 12345)
	if err == nil {
		t.Fatal("Expected error when calling ExecutionWitnessUBT without valid connection")
	}
}
