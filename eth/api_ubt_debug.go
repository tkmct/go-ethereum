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
	"fmt"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/metrics"
	"github.com/ethereum/go-ethereum/rpc"
)

var (
	ubtDebugProxyLatency  = metrics.NewRegisteredTimer("ubt/debug/proxy/latency", nil)
	ubtDebugProxyErrors   = metrics.NewRegisteredCounter("ubt/debug/proxy/errors", nil)
	ubtDebugProxyInflight = metrics.NewRegisteredGauge("ubt/debug/proxy/inflight", nil)
)

// UBTDebugAPI provides debug RPC methods that proxy to the UBT daemon.
type UBTDebugAPI struct {
	endpoint string
	timeout  time.Duration
	client   *rpc.Client
	mu       sync.Mutex
}

// NewUBTDebugAPI creates a new UBT debug API proxy.
func NewUBTDebugAPI(endpoint string, timeout time.Duration) *UBTDebugAPI {
	return &UBTDebugAPI{
		endpoint: endpoint,
		timeout:  timeout,
	}
}

// getClient returns the current RPC client, connecting if necessary.
func (api *UBTDebugAPI) getClient() (*rpc.Client, error) {
	api.mu.Lock()
	defer api.mu.Unlock()
	if api.client != nil {
		return api.client, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), api.timeout)
	defer cancel()

	client, err := rpc.DialContext(ctx, api.endpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to UBT daemon at %s: %w", api.endpoint, err)
	}
	api.client = client
	return client, nil
}

// callWithMetrics wraps an RPC proxy call with latency, error, and inflight metrics.
func (api *UBTDebugAPI) callWithMetrics(ctx context.Context, result any, method string, args ...any) error {
	start := time.Now()
	ubtDebugProxyInflight.Inc(1)
	defer ubtDebugProxyInflight.Dec(1)

	client, err := api.getClient()
	if err != nil {
		ubtDebugProxyErrors.Inc(1)
		return err
	}
	if err := client.CallContext(ctx, result, method, args...); err != nil {
		ubtDebugProxyErrors.Inc(1)
		api.resetClient(err)
		return err
	}
	ubtDebugProxyLatency.UpdateSince(start)
	return nil
}

// GetUBTBalance returns the UBT balance for an address.
func (api *UBTDebugAPI) GetUBTBalance(ctx context.Context, addr common.Address, blockNrOrHash *rpc.BlockNumberOrHash) (*hexutil.Big, error) {
	var result hexutil.Big
	if err := api.callWithMetrics(ctx, &result, "ubt_getBalance", addr, blockNrOrHash); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetUBTStorageAt returns the UBT storage value at the given address and slot.
func (api *UBTDebugAPI) GetUBTStorageAt(ctx context.Context, addr common.Address, slot common.Hash, blockNrOrHash *rpc.BlockNumberOrHash) (hexutil.Bytes, error) {
	var result hexutil.Bytes
	if err := api.callWithMetrics(ctx, &result, "ubt_getStorageAt", addr, slot, blockNrOrHash); err != nil {
		return nil, err
	}
	return result, nil
}

// GetUBTCode returns the UBT contract code for an address.
func (api *UBTDebugAPI) GetUBTCode(ctx context.Context, addr common.Address, blockNrOrHash *rpc.BlockNumberOrHash) (hexutil.Bytes, error) {
	var result hexutil.Bytes
	if err := api.callWithMetrics(ctx, &result, "ubt_getCode", addr, blockNrOrHash); err != nil {
		return nil, err
	}
	return result, nil
}

// GetUBTStatus returns the daemon's current state.
func (api *UBTDebugAPI) GetUBTStatus(ctx context.Context) (map[string]any, error) {
	var result map[string]any
	if err := api.callWithMetrics(ctx, &result, "ubt_status"); err != nil {
		return nil, err
	}
	return result, nil
}

// UBTProofResult contains the proof data returned by the daemon.
type UBTProofResult struct {
	Key        common.Hash                   `json:"key"`
	Root       common.Hash                   `json:"root"`
	ProofNodes map[common.Hash]hexutil.Bytes `json:"proofNodes"`
}

// --- UBT Proof API ---
// Primary:    debug_getUBTProof(address, storageKeys[], block) -> UBTAccountProofResult
// Raw/Internal: debug_getUBTRawProof(key, block) -> UBTProofResult
// Deprecated: debug_getUBTProofByKey -> alias for debug_getUBTRawProof (will be removed)

// GetUBTProof generates UBT proofs for an account and storage slots.
// This is the primary proof API: (address, storageKeys[], blockNrOrHash).
// It proxies to the daemon's ubt_getAccountProof endpoint.
func (api *UBTDebugAPI) GetUBTProof(ctx context.Context, addr common.Address, storageKeys []common.Hash, blockNrOrHash *rpc.BlockNumberOrHash) (*UBTAccountProofResult, error) {
	return api.GetUBTAccountProof(ctx, addr, storageKeys, blockNrOrHash)
}

// GetUBTProofByKey is the backward-compatible raw-key proof API.
// Deprecated: Use GetUBTProof (account + storageKeys form) instead.
// This method will be removed in a future release.
// Migration: Replace debug_getUBTProofByKey(key, block) with debug_getUBTProof(address, storageKeys, block).
func (api *UBTDebugAPI) GetUBTProofByKey(ctx context.Context, key common.Hash, blockNrOrHash *rpc.BlockNumberOrHash) (*UBTProofResult, error) {
	return api.GetUBTRawProof(ctx, key, blockNrOrHash)
}

// GetUBTRawProof generates a UBT Merkle proof for a raw trie key.
// This is an advanced/internal endpoint for low-level trie debugging.
// Most callers should use GetUBTProof (address + storageKeys form) instead.
func (api *UBTDebugAPI) GetUBTRawProof(ctx context.Context, key common.Hash, blockNrOrHash *rpc.BlockNumberOrHash) (*UBTProofResult, error) {
	var result UBTProofResult
	if err := api.callWithMetrics(ctx, &result, "ubt_getProof", key, blockNrOrHash); err != nil {
		return nil, err
	}
	return &result, nil
}

// UBTAccountProofResult mirrors eth_getProof output for API compatibility.
type UBTAccountProofResult struct {
	Address      common.Address                `json:"address"`
	AccountProof map[common.Hash]hexutil.Bytes `json:"accountProof"`
	StorageProof []UBTStorageProofEntry        `json:"storageProof"`
	Root         common.Hash                   `json:"root"`
}

// UBTStorageProofEntry contains the proof for a single storage slot.
type UBTStorageProofEntry struct {
	Key   common.Hash                   `json:"key"`
	Proof map[common.Hash]hexutil.Bytes `json:"proof"`
}

// GetUBTAccountProof generates UBT proofs for an account and storage slots.
// This follows the eth_getProof pattern: (address, storageKeys[], blockNrOrHash).
func (api *UBTDebugAPI) GetUBTAccountProof(ctx context.Context, addr common.Address, storageKeys []common.Hash, blockNrOrHash *rpc.BlockNumberOrHash) (*UBTAccountProofResult, error) {
	var result UBTAccountProofResult
	if err := api.callWithMetrics(ctx, &result, "ubt_getAccountProof", addr, storageKeys, blockNrOrHash); err != nil {
		return nil, err
	}
	return &result, nil
}

// CallUBT executes a call against the UBT daemon state (Phase 7).
func (api *UBTDebugAPI) CallUBT(ctx context.Context, args map[string]any) (hexutil.Bytes, error) {
	var result hexutil.Bytes
	if err := api.callWithMetrics(ctx, &result, "ubt_callUBT", args); err != nil {
		return nil, err
	}
	return result, nil
}

// ExecutionWitnessUBT generates an execution witness from the UBT daemon (Phase 7).
func (api *UBTDebugAPI) ExecutionWitnessUBT(ctx context.Context, blockNumber hexutil.Uint64) (map[string]any, error) {
	var result map[string]any
	if err := api.callWithMetrics(ctx, &result, "ubt_executionWitnessUBT", blockNumber); err != nil {
		return nil, err
	}
	return result, nil
}

// resetClient closes the current client to force reconnection on next call.
func (api *UBTDebugAPI) resetClient(err error) {
	api.mu.Lock()
	defer api.mu.Unlock()
	if api.client != nil {
		api.client.Close()
		api.client = nil
		log.Warn("UBT debug RPC connection reset", "err", err)
	}
}
