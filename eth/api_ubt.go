// Copyright 2025 The go-ethereum Authors
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

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/ethereum/go-ethereum/sidecar"
)

// UBTAccountResult represents account information from the UBT.
type UBTAccountResult struct {
	Address  common.Address `json:"address"`
	Balance  *hexutil.Big   `json:"balance"`
	Nonce    hexutil.Uint64 `json:"nonce"`
	CodeHash common.Hash    `json:"codeHash"`
	CodeSize hexutil.Uint64 `json:"codeSize"`
}

// UBTProofNode represents a single node in a UBT Merkle proof path.
type UBTProofNode struct {
	Depth uint16      `json:"depth"`
	Hash  common.Hash `json:"hash"`
}

// UBTStorageProof represents a storage proof for a single key in the UBT.
type UBTStorageProof struct {
	Key       common.Hash    `json:"key"`
	Value     hexutil.Bytes  `json:"value"`
	ProofPath []UBTProofNode `json:"proofPath"`
}

// UBTProofResult represents the full proof result for an account and its storage keys.
type UBTProofResult struct {
	Address          common.Address    `json:"address"`
	AccountProofPath []UBTProofNode    `json:"accountProofPath"`
	Balance          *hexutil.Big      `json:"balance"`
	CodeHash         common.Hash       `json:"codeHash"`
	Nonce            hexutil.Uint64    `json:"nonce"`
	BlockHash        common.Hash       `json:"blockHash"`
	BlockNumber      hexutil.Uint64    `json:"blockNumber"`
	StorageProof     []UBTStorageProof `json:"storageProof"`
	UbtRoot          common.Hash       `json:"ubtRoot"`
}

// UBTSyncStatus represents the synchronization status of the UBT sidecar.
type UBTSyncStatus struct {
	State        string         `json:"state"`
	CurrentBlock hexutil.Uint64 `json:"currentBlock"`
	CurrentRoot  common.Hash    `json:"currentRoot"`
	ChainHead    hexutil.Uint64 `json:"chainHead"`
}

// UBTDebugAPI provides debug RPC endpoints for the UBT sidecar.
type UBTDebugAPI struct {
	e *Ethereum
}

// resolveUBTRoot resolves a block number or hash to a UBT root, header pair.
// It validates the sidecar state and block reference according to the spec:
//   - Disabled: return error
//   - "pending": return error (not supported)
//   - "latest" + Ready: return CurrentRoot
//   - "latest" + not Ready: return error
//   - explicit block / earliest / finalized / safe: resolve to header, look up UBT root
func (api *UBTDebugAPI) resolveUBTRoot(blockNrOrHash rpc.BlockNumberOrHash) (common.Hash, *types.Header, error) {
	sc := api.e.BlockChain().UBTSidecar()
	if sc == nil {
		return common.Hash{}, nil, sidecar.ErrSidecarNotEnabled
	}
	// Per spec: all non-syncing RPC endpoints require Ready state.
	if sc.State() != sidecar.StateReady {
		return common.Hash{}, nil, sidecar.ErrSidecarNotReady
	}

	// Try to resolve by block number first.
	if blockNr, ok := blockNrOrHash.Number(); ok {
		switch blockNr {
		case rpc.PendingBlockNumber:
			return common.Hash{}, nil, sidecar.ErrPendingNotSupported

		case rpc.LatestBlockNumber:
			root, block, blockHash := sc.CurrentInfo()
			header := api.e.BlockChain().GetHeader(blockHash, block)
			if header == nil {
				return common.Hash{}, nil, fmt.Errorf("header not found for UBT current block %d", block)
			}
			return root, header, nil

		case rpc.EarliestBlockNumber:
			header := api.e.BlockChain().GetHeaderByNumber(0)
			if header == nil {
				return common.Hash{}, nil, fmt.Errorf("genesis header not found")
			}
			ubtRoot, found := sc.GetUBTRoot(header.Hash())
			if !found {
				return common.Hash{}, nil, fmt.Errorf("UBT root not found for block %d", 0)
			}
			return ubtRoot, header, nil

		case rpc.FinalizedBlockNumber:
			header := api.e.BlockChain().CurrentFinalBlock()
			if header == nil {
				return common.Hash{}, nil, fmt.Errorf("finalized block not available")
			}
			ubtRoot, found := sc.GetUBTRoot(header.Hash())
			if !found {
				return common.Hash{}, nil, fmt.Errorf("UBT root not found for finalized block %d", header.Number.Uint64())
			}
			return ubtRoot, header, nil

		case rpc.SafeBlockNumber:
			header := api.e.BlockChain().CurrentSafeBlock()
			if header == nil {
				return common.Hash{}, nil, fmt.Errorf("safe block not available")
			}
			ubtRoot, found := sc.GetUBTRoot(header.Hash())
			if !found {
				return common.Hash{}, nil, fmt.Errorf("UBT root not found for safe block %d", header.Number.Uint64())
			}
			return ubtRoot, header, nil

		default:
			// Explicit block number
			header := api.e.BlockChain().GetHeaderByNumber(uint64(blockNr))
			if header == nil {
				return common.Hash{}, nil, fmt.Errorf("block %d not found", blockNr)
			}
			ubtRoot, found := sc.GetUBTRoot(header.Hash())
			if !found {
				return common.Hash{}, nil, fmt.Errorf("UBT root not found for block %d", blockNr)
			}
			return ubtRoot, header, nil
		}
	}

	// Resolve by block hash.
	if blockHash, ok := blockNrOrHash.Hash(); ok {
		header := api.e.BlockChain().GetHeaderByHash(blockHash)
		if header == nil {
			return common.Hash{}, nil, fmt.Errorf("block not found for hash %s", blockHash.Hex())
		}
		ubtRoot, found := sc.GetUBTRoot(blockHash)
		if !found {
			return common.Hash{}, nil, fmt.Errorf("UBT root not found for block hash %s", blockHash.Hex())
		}
		return ubtRoot, header, nil
	}

	return common.Hash{}, nil, fmt.Errorf("invalid block number or hash")
}

// UbtGetBalance returns the balance of the account at the given address from the UBT.
func (api *UBTDebugAPI) UbtGetBalance(ctx context.Context, address common.Address, blockNrOrHash rpc.BlockNumberOrHash) (*hexutil.Big, error) {
	ubtRoot, _, err := api.resolveUBTRoot(blockNrOrHash)
	if err != nil {
		return nil, err
	}
	sc := api.e.BlockChain().UBTSidecar()
	t, err := sc.OpenBinaryTrie(ubtRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to open UBT at root %s: %w", ubtRoot.Hex(), err)
	}
	acct, err := t.GetAccount(address)
	if err != nil {
		return nil, fmt.Errorf("failed to get account %s: %w", address.Hex(), err)
	}
	if acct == nil {
		return (*hexutil.Big)(common.Big0), nil
	}
	return (*hexutil.Big)(acct.Balance.ToBig()), nil
}

// UbtGetAccount returns the full account information for the given address from the UBT.
func (api *UBTDebugAPI) UbtGetAccount(ctx context.Context, address common.Address, blockNrOrHash rpc.BlockNumberOrHash) (*UBTAccountResult, error) {
	ubtRoot, _, err := api.resolveUBTRoot(blockNrOrHash)
	if err != nil {
		return nil, err
	}
	sc := api.e.BlockChain().UBTSidecar()
	t, err := sc.OpenBinaryTrie(ubtRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to open UBT at root %s: %w", ubtRoot.Hex(), err)
	}
	acct, err := t.GetAccount(address)
	if err != nil {
		return nil, fmt.Errorf("failed to get account %s: %w", address.Hex(), err)
	}
	if acct == nil {
		return &UBTAccountResult{
			Address:  address,
			Balance:  (*hexutil.Big)(common.Big0),
			Nonce:    0,
			CodeHash: types.EmptyCodeHash,
			CodeSize: 0,
		}, nil
	}
	codeHash := common.BytesToHash(acct.CodeHash)
	var codeSize uint64
	if codeHash != types.EmptyCodeHash && codeHash != (common.Hash{}) {
		code := rawdb.ReadCode(api.e.ChainDb(), codeHash)
		codeSize = uint64(len(code))
	}
	return &UBTAccountResult{
		Address:  address,
		Balance:  (*hexutil.Big)(acct.Balance.ToBig()),
		Nonce:    hexutil.Uint64(acct.Nonce),
		CodeHash: codeHash,
		CodeSize: hexutil.Uint64(codeSize),
	}, nil
}

// UbtGetStorageAt returns the value of a storage slot for the given address from the UBT.
func (api *UBTDebugAPI) UbtGetStorageAt(ctx context.Context, address common.Address, slot common.Hash, blockNrOrHash rpc.BlockNumberOrHash) (hexutil.Bytes, error) {
	ubtRoot, _, err := api.resolveUBTRoot(blockNrOrHash)
	if err != nil {
		return nil, err
	}
	sc := api.e.BlockChain().UBTSidecar()
	t, err := sc.OpenBinaryTrie(ubtRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to open UBT at root %s: %w", ubtRoot.Hex(), err)
	}
	value, err := t.GetStorage(address, slot.Bytes())
	if err != nil {
		return nil, fmt.Errorf("failed to get storage %s/%s: %w", address.Hex(), slot.Hex(), err)
	}
	if value == nil {
		return make(hexutil.Bytes, 32), nil
	}
	return value, nil
}

// UbtGetProof returns a Merkle proof for the given account and optional storage keys from the UBT.
// Note: proof generation is currently a stub as the BinaryTrie does not yet support full proof paths.
func (api *UBTDebugAPI) UbtGetProof(ctx context.Context, address common.Address, storageKeys []string, blockNrOrHash rpc.BlockNumberOrHash) (*UBTProofResult, error) {
	ubtRoot, header, err := api.resolveUBTRoot(blockNrOrHash)
	if err != nil {
		return nil, err
	}
	sc := api.e.BlockChain().UBTSidecar()
	t, err := sc.OpenBinaryTrie(ubtRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to open UBT at root %s: %w", ubtRoot.Hex(), err)
	}

	// Get account data
	acct, err := t.GetAccount(address)
	if err != nil {
		return nil, fmt.Errorf("failed to get account %s: %w", address.Hex(), err)
	}

	result := &UBTProofResult{
		Address:          address,
		AccountProofPath: []UBTProofNode{}, // TODO: populate when BinaryTrie supports proof paths
		BlockHash:        header.Hash(),
		BlockNumber:      hexutil.Uint64(header.Number.Uint64()),
		UbtRoot:          ubtRoot,
	}

	if acct == nil {
		result.Balance = (*hexutil.Big)(common.Big0)
		result.CodeHash = types.EmptyCodeHash
		result.Nonce = 0
	} else {
		result.Balance = (*hexutil.Big)(acct.Balance.ToBig())
		result.CodeHash = common.BytesToHash(acct.CodeHash)
		result.Nonce = hexutil.Uint64(acct.Nonce)
	}

	// Build storage proofs
	result.StorageProof = make([]UBTStorageProof, len(storageKeys))
	for i, hexKey := range storageKeys {
		key := common.HexToHash(hexKey)
		value, err := t.GetStorage(address, key.Bytes())
		if err != nil {
			return nil, fmt.Errorf("failed to get storage %s/%s: %w", address.Hex(), key.Hex(), err)
		}
		sp := UBTStorageProof{
			Key:       key,
			ProofPath: []UBTProofNode{}, // TODO: populate when BinaryTrie supports proof paths
		}
		if value == nil {
			sp.Value = make(hexutil.Bytes, 32)
		} else {
			sp.Value = value
		}
		result.StorageProof[i] = sp
	}

	return result, nil
}

// UbtSyncing returns the synchronization status of the UBT sidecar.
// This endpoint works in all sidecar states, including Disabled.
func (api *UBTDebugAPI) UbtSyncing(ctx context.Context) (*UBTSyncStatus, error) {
	sc := api.e.BlockChain().UBTSidecar()

	status := &UBTSyncStatus{}

	// Get chain head regardless of sidecar state
	head := api.e.BlockChain().CurrentBlock()
	if head != nil {
		status.ChainHead = hexutil.Uint64(head.Number.Uint64())
	}

	if sc == nil {
		status.State = sidecar.StateDisabled.String()
		return status, nil
	}

	status.State = sc.State().String()
	root, block, _ := sc.CurrentInfo()
	status.CurrentBlock = hexutil.Uint64(block)
	status.CurrentRoot = root

	return status, nil
}
