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
	"errors"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/stateless"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/internal/ethapi"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/ethereum/go-ethereum/trie/bintrie"
)

// UBTDebugAPI provides UBT-specific debug RPC methods under the debug_ubt namespace.
type UBTDebugAPI struct {
	eth *Ethereum
}

// NewUBTDebugAPI creates a new UBTDebugAPI instance.
func NewUBTDebugAPI(eth *Ethereum) *UBTDebugAPI {
	return &UBTDebugAPI{eth: eth}
}

// resolveUBTRoot resolves the UBT root for the given block, handling conversion-fallback logic.
func (api *UBTDebugAPI) resolveUBTRoot(ctx context.Context, blockNrOrHash rpc.BlockNumberOrHash) (common.Hash, *types.Header, error) {
	header, err := api.eth.APIBackend.HeaderByNumberOrHash(ctx, blockNrOrHash)
	if err != nil {
		return common.Hash{}, nil, err
	}
	if header == nil {
		return common.Hash{}, nil, fmt.Errorf("block %v not found", blockNrOrHash)
	}
	sc := api.eth.blockchain.UBTSidecar()
	if sc == nil {
		return common.Hash{}, nil, errors.New("ubt sidecar not available")
	}
	ubtRoot, ok := sc.GetUBTRoot(header.Hash())
	if !ok {
		if num, numOk := blockNrOrHash.Number(); numOk && (num == rpc.LatestBlockNumber || num == rpc.PendingBlockNumber) {
			if sc.Converting() {
				root, number, hash := sc.CurrentInfo()
				if hash != (common.Hash{}) {
					header = api.eth.blockchain.GetHeader(hash, number)
					if header == nil {
						return common.Hash{}, nil, fmt.Errorf("block %x not found", hash)
					}
					ubtRoot = root
					ok = true
				}
			}
			if !ok {
				if root, number, hash, ok2 := rawdb.ReadUBTCommittedRoot(api.eth.ChainDb()); ok2 {
					header = api.eth.blockchain.GetHeader(hash, number)
					if header == nil {
						return common.Hash{}, nil, fmt.Errorf("block %x not found", hash)
					}
					ubtRoot = root
					ok = true
				}
			}
		}
		if !ok {
			if sc.Ready() {
				return common.Hash{}, nil, fmt.Errorf("ubt root not found for block %x", header.Hash())
			}
			return common.Hash{}, nil, errors.New("ubt sidecar not ready")
		}
	}
	return ubtRoot, header, nil
}

// GetBalance returns the balance of an account from UBT state.
func (api *UBTDebugAPI) GetBalance(ctx context.Context, address common.Address, blockNrOrHash rpc.BlockNumberOrHash) (*hexutil.Big, error) {
	ubtRoot, _, err := api.resolveUBTRoot(ctx, blockNrOrHash)
	if err != nil {
		return nil, err
	}
	sc := api.eth.blockchain.UBTSidecar()
	acc, err := sc.ReadAccount(ubtRoot, address)
	if err != nil {
		return nil, err
	}
	if acc == nil {
		return (*hexutil.Big)(new(big.Int)), nil
	}
	return (*hexutil.Big)(acc.Balance.ToBig()), nil
}

// GetAccount returns account data from UBT state.
func (api *UBTDebugAPI) GetAccount(ctx context.Context, address common.Address, blockNrOrHash rpc.BlockNumberOrHash) (*UBTAccountResult, error) {
	ubtRoot, _, err := api.resolveUBTRoot(ctx, blockNrOrHash)
	if err != nil {
		return nil, err
	}
	sc := api.eth.blockchain.UBTSidecar()
	acc, err := sc.ReadAccount(ubtRoot, address)
	if err != nil {
		return nil, err
	}
	result := &UBTAccountResult{Address: address}
	if acc != nil {
		result.Balance = (*hexutil.Big)(acc.Balance.ToBig())
		result.Nonce = hexutil.Uint64(acc.Nonce)
		result.CodeHash = acc.CodeHash
		result.CodeSize = hexutil.Uint64(acc.CodeSize)
	} else {
		result.Balance = (*hexutil.Big)(new(big.Int))
	}
	return result, nil
}

// GetCode returns the code of a contract from UBT state.
func (api *UBTDebugAPI) GetCode(ctx context.Context, address common.Address, blockNrOrHash rpc.BlockNumberOrHash) (hexutil.Bytes, error) {
	ubtRoot, _, err := api.resolveUBTRoot(ctx, blockNrOrHash)
	if err != nil {
		return nil, err
	}
	sc := api.eth.blockchain.UBTSidecar()
	acc, err := sc.ReadAccount(ubtRoot, address)
	if err != nil {
		return nil, err
	}
	if acc == nil || acc.CodeHash == types.EmptyCodeHash {
		return hexutil.Bytes{}, nil
	}
	code := rawdb.ReadCode(api.eth.ChainDb(), acc.CodeHash)
	if code == nil {
		return nil, fmt.Errorf("code not found for hash %x", acc.CodeHash)
	}
	return hexutil.Bytes(code), nil
}

// GetStorageAt returns a storage value from UBT state.
func (api *UBTDebugAPI) GetStorageAt(ctx context.Context, address common.Address, slot common.Hash, blockNrOrHash rpc.BlockNumberOrHash) (hexutil.Bytes, error) {
	ubtRoot, _, err := api.resolveUBTRoot(ctx, blockNrOrHash)
	if err != nil {
		return nil, err
	}
	sc := api.eth.blockchain.UBTSidecar()
	value, err := sc.ReadStorage(ubtRoot, address, slot)
	if err != nil {
		return nil, err
	}
	return value.Bytes(), nil
}

// GetProof returns the binary trie proof for a given account and storage keys.
func (api *UBTDebugAPI) GetProof(ctx context.Context, address common.Address, storageKeys []string, blockNrOrHash rpc.BlockNumberOrHash) (*UBTProofResult, error) {
	ubtRoot, header, err := api.resolveUBTRoot(ctx, blockNrOrHash)
	if err != nil {
		return nil, err
	}
	sc := api.eth.blockchain.UBTSidecar()
	acc, err := sc.ReadAccount(ubtRoot, address)
	if err != nil {
		return nil, err
	}

	bt, err := api.openBinaryTrie(ubtRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to open binary trie: %w", err)
	}
	accountKey := bintrie.GetBinaryTreeKeyBasicData(address)
	accountResult, err := bintrie.GenerateProofWithPath(bt, accountKey)
	if err != nil {
		return nil, fmt.Errorf("failed to generate account proof: %w", err)
	}
	accountProof, accountProofPath := marshalProofResult(accountResult)

	storageProofs := make([]UBTStorageProof, len(storageKeys))
	for i, keyHex := range storageKeys {
		key, err := parseStorageKey(keyHex)
		if err != nil {
			return nil, err
		}
		value, err := sc.ReadStorage(ubtRoot, address, key)
		if err != nil {
			return nil, err
		}
		ubtKey := bintrie.GetBinaryTreeKeyStorageSlot(address, key.Bytes())
		result, err := bintrie.GenerateProofWithPath(bt, ubtKey)
		if err != nil {
			return nil, fmt.Errorf("failed to generate storage proof for %s: %w", keyHex, err)
		}
		proof, proofPath := marshalProofResult(result)
		storageProofs[i] = UBTStorageProof{
			Key:       key,
			Value:     value.Bytes(),
			Proof:     proof,
			ProofPath: proofPath,
		}
	}

	result := &UBTProofResult{
		Address:          address,
		AccountProof:     accountProof,
		AccountProofPath: accountProofPath,
		BlockHash:        header.Hash(),
		BlockNumber:      hexutil.Uint64(header.Number.Uint64()),
		StorageProof:     storageProofs,
		StateRoot:        header.Root,
		UbtRoot:          ubtRoot,
		ProofRoot:        accountResult.Root,
	}
	if acc != nil {
		result.Balance = (*hexutil.Big)(acc.Balance.ToBig())
		result.CodeHash = acc.CodeHash
		result.Nonce = hexutil.Uint64(acc.Nonce)
	} else {
		result.Balance = (*hexutil.Big)(new(big.Int))
	}
	return result, nil
}

// Call executes a message call against UBT state without creating a transaction.
func (api *UBTDebugAPI) Call(ctx context.Context, args ethapi.TransactionArgs, blockNrOrHash rpc.BlockNumberOrHash) (hexutil.Bytes, error) {
	ubtRoot, header, err := api.resolveUBTRoot(ctx, blockNrOrHash)
	if err != nil {
		return nil, err
	}
	sc := api.eth.blockchain.UBTSidecar()
	stateDB, err := state.New(ubtRoot, state.NewDatabase(sc.TrieDB(), nil))
	if err != nil {
		return nil, fmt.Errorf("failed to open UBT state: %w", err)
	}

	msg := args.ToMessage(header.BaseFee, true)
	blockCtx := core.NewEVMBlockContext(header, api.eth.blockchain, nil)
	evm := vm.NewEVM(blockCtx, stateDB, api.eth.blockchain.Config(), vm.Config{NoBaseFee: true})
	gp := new(core.GasPool).AddGas(header.GasLimit)

	result, err := core.ApplyMessage(evm, msg, gp)
	if err != nil {
		return nil, err
	}
	if result.Err != nil {
		return result.ReturnData, result.Err
	}
	return result.ReturnData, nil
}

// ExecutionWitness returns a path-aware witness for UBT nodes.
func (api *UBTDebugAPI) ExecutionWitness(ctx context.Context, blockNrOrHash rpc.BlockNumberOrHash) (*stateless.ExtUBTWitness, error) {
	bc := api.eth.blockchain
	block, err := api.eth.APIBackend.BlockByNumberOrHash(ctx, blockNrOrHash)
	if err != nil {
		return nil, err
	}
	if block == nil {
		return nil, fmt.Errorf("block %v not found", blockNrOrHash)
	}
	parent := bc.GetHeader(block.ParentHash(), block.NumberU64()-1)
	if parent == nil {
		return nil, fmt.Errorf("block %v found, but parent missing", blockNrOrHash)
	}
	if !api.eth.APIBackend.ChainConfig().IsByzantium(block.Number()) {
		return nil, fmt.Errorf("execution witness unavailable before Byzantium (block %v)", block.NumberU64())
	}
	result, err := bc.ProcessBlock(parent.Root, block, false, true)
	if err != nil {
		return nil, err
	}
	witness := result.Witness()
	if witness == nil {
		return nil, errors.New("no witness generated")
	}
	if len(witness.StatePaths) == 0 {
		return nil, errors.New("witness has no state paths")
	}
	ext := &stateless.ExtUBTWitness{
		Headers: witness.Headers,
	}
	ext.Codes = make([]hexutil.Bytes, 0, len(witness.Codes))
	for code := range witness.Codes {
		ext.Codes = append(ext.Codes, []byte(code))
	}
	ext.StatePaths = make([]stateless.PathNode, 0, len(witness.StatePaths))
	for path, node := range witness.StatePaths {
		ext.StatePaths = append(ext.StatePaths, stateless.PathNode{Path: []byte(path), Node: node})
	}
	return ext, nil
}

// VerifyExecutionWitness runs stateless execution against a provided UBT witness.
func (api *UBTDebugAPI) VerifyExecutionWitness(ctx context.Context, blockNrOrHash rpc.BlockNumberOrHash, ext *stateless.ExtUBTWitness) (*ExecutionWitnessVerification, error) {
	if ext == nil {
		return nil, errors.New("witness is required")
	}
	witness, err := stateless.NewWitnessFromUBTWitness(ext)
	if err != nil {
		return nil, err
	}
	if witness == nil {
		return nil, errors.New("empty witness")
	}
	block, err := api.eth.APIBackend.BlockByNumberOrHash(ctx, blockNrOrHash)
	if err != nil {
		return nil, err
	}
	if block == nil {
		return nil, fmt.Errorf("block %v not found", blockNrOrHash)
	}
	sc := api.eth.blockchain.UBTSidecar()
	if sc == nil || !sc.Ready() {
		return nil, errors.New("ubt sidecar not ready")
	}
	ubtRoot, ok := sc.GetUBTRoot(block.Hash())
	if !ok {
		return nil, fmt.Errorf("ubt root not found for block %x", block.Hash())
	}
	expectedStateRoot := ubtRoot
	expectedReceiptRoot := block.ReceiptHash()

	blockContext := types.CopyHeader(block.Header())
	blockContext.Root = common.Hash{}
	blockContext.ReceiptHash = common.Hash{}
	task := types.NewBlockWithHeader(blockContext).WithBody(*block.Body())

	stateRoot, receiptRoot, err := core.ExecuteStatelessWithPathDB(api.eth.blockchain.Config(), vm.Config{}, task, witness, true)
	if err != nil {
		return nil, err
	}
	errorsList := []string{}
	if stateRoot != expectedStateRoot {
		errorsList = append(errorsList, fmt.Sprintf("ubt root mismatch: computed=%s expected=%s", stateRoot, expectedStateRoot))
	}
	if receiptRoot != expectedReceiptRoot {
		errorsList = append(errorsList, fmt.Sprintf("receipt root mismatch: computed=%s header=%s", receiptRoot, expectedReceiptRoot))
	}
	return &ExecutionWitnessVerification{
		Ok:                  len(errorsList) == 0,
		StateRoot:           stateRoot,
		ReceiptRoot:         receiptRoot,
		ExpectedStateRoot:   expectedStateRoot,
		ExpectedReceiptRoot: expectedReceiptRoot,
		Errors:              errorsList,
	}, nil
}

// openBinaryTrie opens a binary trie at the given root, using the sidecar if available.
func (api *UBTDebugAPI) openBinaryTrie(root common.Hash) (*bintrie.BinaryTrie, error) {
	if sc := api.eth.blockchain.UBTSidecar(); sc != nil {
		if !sc.Ready() {
			return nil, errors.New("ubt sidecar not ready")
		}
		return sc.OpenBinaryTrie(root)
	}
	trieDB := api.eth.BlockChain().TrieDB()
	if !trieDB.IsVerkle() {
		return nil, errors.New("ubt is not enabled")
	}
	return bintrie.NewBinaryTrie(root, trieDB)
}

// marshalProofResult converts a bintrie.ProofResult into RPC-friendly types.
func marshalProofResult(pr *bintrie.ProofResult) ([]hexutil.Bytes, []UBTProofNode) {
	legacyProof := make([]hexutil.Bytes, 0, len(pr.Siblings)+1+len(pr.Values))
	for _, s := range pr.Siblings {
		legacyProof = append(legacyProof, hexutil.Bytes(s.Hash.Bytes()))
	}
	if pr.Stem != nil {
		legacyProof = append(legacyProof, hexutil.Bytes(pr.Stem))
		for _, v := range pr.Values {
			legacyProof = append(legacyProof, hexutil.Bytes(v))
		}
	}
	proofPath := make([]UBTProofNode, len(pr.Siblings))
	for i, s := range pr.Siblings {
		proofPath[i] = UBTProofNode{Depth: s.Depth, Hash: hexutil.Bytes(s.Hash.Bytes())}
	}
	return legacyProof, proofPath
}

func parseStorageKey(keyHex string) (common.Hash, error) {
	raw, err := hexutil.Decode(keyHex)
	if err != nil {
		return common.Hash{}, err
	}
	if len(raw) > 32 {
		return common.Hash{}, fmt.Errorf("storage key too long: %d", len(raw))
	}
	var key common.Hash
	copy(key[32-len(raw):], raw)
	return key, nil
}
