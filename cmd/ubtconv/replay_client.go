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
	"bytes"
	"context"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/ubtemit"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/log"
)

// classifyRPCError determines whether an RPC error is retriable.
// Connection errors and timeouts are retriable; method-not-found and
// block-not-found errors indicate fundamental incompatibility and should not be retried.
func classifyRPCError(err error) (retriable bool, reason string) {
	if err == nil {
		return false, ""
	}
	errMsg := err.Error()
	// Method not supported by node
	if strings.Contains(errMsg, "method not found") || strings.Contains(errMsg, "the method") {
		return false, "RPC method not supported (node may not be an archive node)"
	}
	// Block not available
	if strings.Contains(errMsg, "block not found") || strings.Contains(errMsg, "unknown block") {
		return false, "block not available (node may not retain historical state)"
	}
	// Everything else is likely transient (connection errors, timeouts)
	return true, "transient error"
}

// BlockReplayer defines the interface for block replay capability.
// This is used for deep recovery when the UBT trie state needs to be
// reconstructed from archive blocks.
type BlockReplayer interface {
	// ReplayBlock reconstructs a UBT diff by replaying the given block
	// via debug_traceBlockByNumber on an archive node.
	ReplayBlock(blockNumber uint64) (*ubtemit.QueuedDiffV1, error)
}

// ReplayClient reconstructs state diffs from an archive node for deep recovery.
// When the daemon loses outbox events beyond its reorg window, it can use this
// client to reconstruct diffs by calling debug_traceBlockByNumber.
type ReplayClient struct {
	reader  *OutboxReader // reuse existing RPC connection
	timeout time.Duration
}

// NewReplayClient creates a new ReplayClient using the outbox reader's connection.
func NewReplayClient(reader *OutboxReader) *ReplayClient {
	return &ReplayClient{
		reader:  reader,
		timeout: 60 * time.Second,
	}
}

// AccountInfo holds canonical account data from geth.
type AccountInfo struct {
	Nonce    uint64
	Balance  *big.Int
	CodeHash common.Hash
}

// GetAccountAt fetches the canonical account state at the given block.
func (rc *ReplayClient) GetAccountAt(blockNumber uint64, addr common.Address) (*AccountInfo, error) {
	client, err := rc.reader.getClient()
	if err != nil {
		return nil, fmt.Errorf("get RPC client: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), rc.timeout)
	defer cancel()

	// Try eth_getProof first (returns codeHash directly)
	type proofAccountResult struct {
		Nonce    hexutil.Uint64 `json:"nonce"`
		Balance  *hexutil.Big   `json:"balance"`
		CodeHash common.Hash    `json:"codeHash"`
	}
	var proofResult proofAccountResult
	err = client.CallContext(ctx, &proofResult, "eth_getProof", addr, []string{}, hexutil.EncodeUint64(blockNumber))
	if err == nil {
		bal := proofResult.Balance.ToInt()
		if bal == nil {
			bal = new(big.Int)
		}
		return &AccountInfo{
			Nonce:    uint64(proofResult.Nonce),
			Balance:  bal,
			CodeHash: proofResult.CodeHash,
		}, nil
	}

	// Fallback: compose individual calls for balance, nonce, and code hash.
	log.Debug("eth_getProof failed, falling back to individual calls", "err", err)

	ctx2, cancel2 := context.WithTimeout(context.Background(), rc.timeout)
	defer cancel2()

	var (
		balanceHex hexutil.Big
		nonceHex   hexutil.Uint64
		codeHex    hexutil.Bytes
	)
	blockHex := hexutil.EncodeUint64(blockNumber)

	if err := client.CallContext(ctx2, &balanceHex, "eth_getBalance", addr, blockHex); err != nil {
		retriable, reason := classifyRPCError(err)
		if !retriable {
			return nil, fmt.Errorf("eth_getBalance at block %d: non-retriable (%s): %w", blockNumber, reason, err)
		}
		return nil, fmt.Errorf("eth_getBalance at block %d: %w", blockNumber, err)
	}
	if err := client.CallContext(ctx2, &nonceHex, "eth_getTransactionCount", addr, blockHex); err != nil {
		retriable, reason := classifyRPCError(err)
		if !retriable {
			return nil, fmt.Errorf("eth_getTransactionCount at block %d: non-retriable (%s): %w", blockNumber, reason, err)
		}
		return nil, fmt.Errorf("eth_getTransactionCount at block %d: %w", blockNumber, err)
	}
	if err := client.CallContext(ctx2, &codeHex, "eth_getCode", addr, blockHex); err != nil {
		retriable, reason := classifyRPCError(err)
		if !retriable {
			return nil, fmt.Errorf("eth_getCode at block %d: non-retriable (%s): %w", blockNumber, reason, err)
		}
		return nil, fmt.Errorf("eth_getCode at block %d: %w", blockNumber, err)
	}

	codeHash := types.EmptyCodeHash
	if len(codeHex) > 0 {
		codeHash = crypto.Keccak256Hash(codeHex)
	}

	bal := balanceHex.ToInt()
	if bal == nil {
		bal = new(big.Int)
	}
	return &AccountInfo{
		Nonce:    uint64(nonceHex),
		Balance:  bal,
		CodeHash: codeHash,
	}, nil
}

// rpcBlockHeader is a partial header for RPC responses.
type rpcBlockHeader struct {
	Number     hexutil.Uint64 `json:"number"`
	Hash       common.Hash    `json:"hash"`
	ParentHash common.Hash    `json:"parentHash"`
	StateRoot  common.Hash    `json:"stateRoot"`
}

// GetBlockHeader fetches a block header by number.
func (rc *ReplayClient) GetBlockHeader(blockNumber uint64) (*types.Header, error) {
	client, err := rc.reader.getClient()
	if err != nil {
		return nil, fmt.Errorf("get RPC client: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), rc.timeout)
	defer cancel()

	var header rpcBlockHeader
	if err := client.CallContext(ctx, &header, "eth_getBlockByNumber", hexutil.EncodeUint64(blockNumber), false); err != nil {
		retriable, reason := classifyRPCError(err)
		if !retriable {
			return nil, fmt.Errorf("eth_getBlockByNumber(%d): non-retriable (%s): %w", blockNumber, reason, err)
		}
		return nil, fmt.Errorf("eth_getBlockByNumber(%d): %w", blockNumber, err)
	}

	return &types.Header{
		Number:     new(big.Int).SetUint64(blockNumber),
		ParentHash: header.ParentHash,
		Root:       header.StateRoot,
	}, nil
}

// ReplayBlock reconstructs a state diff for the given block number by reading
// the block's state changes from the archive node. It uses debug_accountRange
// to compare the pre and post state.
//
// This is a slow-path recovery method and should only be used when normal outbox
// events are unavailable (e.g., after deep reorg beyond retention window).
func (rc *ReplayClient) ReplayBlock(blockNumber uint64) (*ubtemit.QueuedDiffV1, error) {
	client, err := rc.reader.getClient()
	if err != nil {
		return nil, fmt.Errorf("get RPC client: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), rc.timeout)
	defer cancel()

	// Use debug_traceBlockByNumber with prestateTracer to get state changes
	var result []prestateTracerResult
	tracerOpts := map[string]any{
		"tracer": "prestateTracer",
		"tracerConfig": map[string]any{
			"diffMode": true,
		},
	}
	if err := client.CallContext(ctx, &result, "debug_traceBlockByNumber", hexutil.EncodeUint64(blockNumber), tracerOpts); err != nil {
		retriable, reason := classifyRPCError(err)
		if !retriable {
			return nil, fmt.Errorf("replay block %d: non-retriable error (%s): %w", blockNumber, reason, err)
		}
		return nil, fmt.Errorf("replay block %d: %w", blockNumber, err)
	}

	// Merge per-tx results into unified pre/post maps.
	// For multi-tx blocks, the first tx's Pre for an address is the true pre-state,
	// and the last tx's Post is the true post-state.
	mergedPre := make(map[common.Address]prestateAccount)
	mergedPost := make(map[common.Address]prestateAccount)
	for _, txResult := range result {
		for addrStr, pre := range txResult.Pre {
			addr := common.HexToAddress(addrStr)
			if _, exists := mergedPre[addr]; !exists {
				mergedPre[addr] = pre
			}
		}
		for addrStr, post := range txResult.Post {
			addr := common.HexToAddress(addrStr)
			mergedPost[addr] = post // last write wins
		}
	}

	diff := &ubtemit.QueuedDiffV1{}

	// Process accounts that exist in post-state (created or modified)
	for addr, post := range mergedPost {
		balance, _ := new(big.Int).SetString(post.Balance, 0)
		if balance == nil {
			balance = new(big.Int)
		}
		codeHash := types.EmptyCodeHash
		if post.Code != "" {
			// Code changed in this block — emit the new code
			code := common.Hex2Bytes(post.Code[2:]) // strip 0x prefix
			codeHash = crypto.Keccak256Hash(code)
			diff.Codes = append(diff.Codes, ubtemit.CodeEntry{
				Address:  addr,
				CodeHash: codeHash,
				Code:     code,
			})
		} else if pre, hasPre := mergedPre[addr]; hasPre && pre.Code != "" {
			// Code was NOT changed, but the pre-state had code — carry forward the hash.
			// The prestateTracer in diffMode only includes Code when it changes,
			// so we must preserve the existing code hash to avoid overwriting it with empty.
			preCode := common.Hex2Bytes(pre.Code[2:])
			codeHash = crypto.Keccak256Hash(preCode)
		} else {
			// No code in tracer output. The account may already have code deployed
			// from a prior block that the tracer did not touch. Fetch the canonical
			// codeHash from the archive node to avoid incorrectly emitting EmptyCodeHash.
			acctInfo, err := rc.GetAccountAt(blockNumber, addr)
			if err != nil {
				log.Warn("Failed to fetch canonical account for codeHash", "addr", addr, "block", blockNumber, "err", err)
			} else if acctInfo.CodeHash != types.EmptyCodeHash && acctInfo.CodeHash != (common.Hash{}) {
				codeHash = acctInfo.CodeHash
			}
		}

		diff.Accounts = append(diff.Accounts, ubtemit.AccountEntry{
			Address:  addr,
			Nonce:    post.Nonce,
			Balance:  balance,
			CodeHash: codeHash,
			Alive:    true,
		})

		// Collect storage changes for this address.
		// The prestateTracer in diffMode includes all changed slots (including
		// slots cleared to zero), so this correctly captures both updates and deletes.
		for slotStr, valStr := range post.Storage {
			diff.Storage = append(diff.Storage, ubtemit.StorageEntry{
				Address:    addr,
				SlotKeyRaw: common.HexToHash(slotStr),
				Value:      common.HexToHash(valStr),
			})
		}

		// Detect cleared slots: slots in pre-state but not in post-state for surviving accounts.
		// The prestateTracer may omit slots from Post when they are cleared to zero and the
		// account survives. Emit explicit zero-value entries so the applier deletes them.
		if pre, hasPre := mergedPre[addr]; hasPre {
			for slotStr := range pre.Storage {
				if _, inPost := post.Storage[slotStr]; !inPost {
					// Slot was cleared during this block
					diff.Storage = append(diff.Storage, ubtemit.StorageEntry{
						Address:    addr,
						SlotKeyRaw: common.HexToHash(slotStr),
						Value:      common.Hash{}, // zero value = deletion
					})
				}
			}
		}
	}

	// Detect deletions: accounts in pre-state but not in post-state (SELFDESTRUCT).
	// For deleted accounts, also emit zero-value storage entries for all pre-state slots
	// so the applier zeros them in the trie (plan §8: "Iterate indexed slots and zero via UpdateStorage").
	for addr, pre := range mergedPre {
		if _, inPost := mergedPost[addr]; !inPost {
			diff.Accounts = append(diff.Accounts, ubtemit.AccountEntry{
				Address:  addr,
				CodeHash: types.EmptyCodeHash,
				Alive:    false,
			})
			// Zero all pre-state storage slots for the deleted account
			for slotStr := range pre.Storage {
				diff.Storage = append(diff.Storage, ubtemit.StorageEntry{
					Address:    addr,
					SlotKeyRaw: common.HexToHash(slotStr),
					Value:      common.Hash{}, // zero value
				})
			}
		}
	}

	// Sort all entries for deterministic output.
	sort.Slice(diff.Accounts, func(i, j int) bool {
		return bytes.Compare(diff.Accounts[i].Address[:], diff.Accounts[j].Address[:]) < 0
	})
	sort.Slice(diff.Storage, func(i, j int) bool {
		if diff.Storage[i].Address != diff.Storage[j].Address {
			return bytes.Compare(diff.Storage[i].Address[:], diff.Storage[j].Address[:]) < 0
		}
		return bytes.Compare(diff.Storage[i].SlotKeyRaw[:], diff.Storage[j].SlotKeyRaw[:]) < 0
	})
	sort.Slice(diff.Codes, func(i, j int) bool {
		return bytes.Compare(diff.Codes[i].Address[:], diff.Codes[j].Address[:]) < 0
	})

	log.Info("Replayed block from archive", "block", blockNumber,
		"accounts", len(diff.Accounts), "storage", len(diff.Storage), "codes", len(diff.Codes))
	return diff, nil
}

// prestateTracerResult is the per-tx output of the prestateTracer in diff mode.
type prestateTracerResult struct {
	Pre  map[string]prestateAccount `json:"pre"`
	Post map[string]prestateAccount `json:"post"`
}

// prestateAccount mirrors the prestate tracer account fields.
type prestateAccount struct {
	Balance string            `json:"balance"`
	Nonce   uint64            `json:"nonce"`
	Code    string            `json:"code,omitempty"`
	Storage map[string]string `json:"storage,omitempty"`
}

var _ BlockReplayer = (*ReplayClient)(nil)
