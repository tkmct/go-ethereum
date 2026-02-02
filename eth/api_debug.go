// Copyright 2023 The go-ethereum Authors
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
	"crypto/sha256"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/stateless"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/internal/ethapi"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/ethereum/go-ethereum/trie"
	"github.com/ethereum/go-ethereum/trie/bintrie"
)

// DebugAPI is the collection of Ethereum full node APIs for debugging the
// protocol.
type DebugAPI struct {
	eth *Ethereum
}

// NewDebugAPI creates a new DebugAPI instance.
func NewDebugAPI(eth *Ethereum) *DebugAPI {
	return &DebugAPI{eth: eth}
}

// DumpBlock retrieves the entire state of the database at a given block.
func (api *DebugAPI) DumpBlock(blockNr rpc.BlockNumber) (state.Dump, error) {
	opts := &state.DumpConfig{
		OnlyWithAddresses: true,
		Max:               AccountRangeMaxResults, // Sanity limit over RPC
	}
	if blockNr == rpc.PendingBlockNumber {
		// If we're dumping the pending state, we need to request
		// both the pending block as well as the pending state from
		// the miner and operate on those
		_, _, stateDb := api.eth.miner.Pending()
		if stateDb == nil {
			return state.Dump{}, errors.New("pending state is not available")
		}
		return stateDb.RawDump(opts), nil
	}
	var header *types.Header
	switch blockNr {
	case rpc.LatestBlockNumber:
		header = api.eth.blockchain.CurrentBlock()
	case rpc.FinalizedBlockNumber:
		header = api.eth.blockchain.CurrentFinalBlock()
	case rpc.SafeBlockNumber:
		header = api.eth.blockchain.CurrentSafeBlock()
	default:
		block := api.eth.blockchain.GetBlockByNumber(uint64(blockNr))
		if block == nil {
			return state.Dump{}, fmt.Errorf("block #%d not found", blockNr)
		}
		header = block.Header()
	}
	if header == nil {
		return state.Dump{}, fmt.Errorf("block #%d not found", blockNr)
	}
	stateDb, err := api.eth.BlockChain().StateAt(header.Root)
	if err != nil {
		return state.Dump{}, err
	}
	return stateDb.RawDump(opts), nil
}

// Preimage is a debug API function that returns the preimage for a sha3 hash, if known.
func (api *DebugAPI) Preimage(ctx context.Context, hash common.Hash) (hexutil.Bytes, error) {
	if preimage := rawdb.ReadPreimage(api.eth.ChainDb(), hash); preimage != nil {
		return preimage, nil
	}
	return nil, errors.New("unknown preimage")
}

// BadBlockArgs represents the entries in the list returned when bad blocks are queried.
type BadBlockArgs struct {
	Hash  common.Hash            `json:"hash"`
	Block map[string]interface{} `json:"block"`
	RLP   string                 `json:"rlp"`
}

// GetBadBlocks returns a list of the last 'bad blocks' that the client has seen on the network
// and returns them as a JSON list of block hashes.
func (api *DebugAPI) GetBadBlocks(ctx context.Context) ([]*BadBlockArgs, error) {
	var (
		blocks  = rawdb.ReadAllBadBlocks(api.eth.chainDb)
		results = make([]*BadBlockArgs, 0, len(blocks))
	)
	for _, block := range blocks {
		var (
			blockRlp  string
			blockJSON map[string]interface{}
		)
		if rlpBytes, err := rlp.EncodeToBytes(block); err != nil {
			blockRlp = err.Error() // Hacky, but hey, it works
		} else {
			blockRlp = fmt.Sprintf("%#x", rlpBytes)
		}
		blockJSON = ethapi.RPCMarshalBlock(block, true, true, api.eth.APIBackend.ChainConfig())
		results = append(results, &BadBlockArgs{
			Hash:  block.Hash(),
			RLP:   blockRlp,
			Block: blockJSON,
		})
	}
	return results, nil
}

// AccountRangeMaxResults is the maximum number of results to be returned per call
const AccountRangeMaxResults = 256

// AccountRange enumerates all accounts in the given block and start point in paging request
func (api *DebugAPI) AccountRange(blockNrOrHash rpc.BlockNumberOrHash, start hexutil.Bytes, maxResults int, nocode, nostorage, incompletes bool) (state.Dump, error) {
	var stateDb *state.StateDB
	var err error

	if number, ok := blockNrOrHash.Number(); ok {
		if number == rpc.PendingBlockNumber {
			// If we're dumping the pending state, we need to request
			// both the pending block as well as the pending state from
			// the miner and operate on those
			_, _, stateDb = api.eth.miner.Pending()
			if stateDb == nil {
				return state.Dump{}, errors.New("pending state is not available")
			}
		} else {
			var header *types.Header
			switch number {
			case rpc.LatestBlockNumber:
				header = api.eth.blockchain.CurrentBlock()
			case rpc.FinalizedBlockNumber:
				header = api.eth.blockchain.CurrentFinalBlock()
			case rpc.SafeBlockNumber:
				header = api.eth.blockchain.CurrentSafeBlock()
			default:
				block := api.eth.blockchain.GetBlockByNumber(uint64(number))
				if block == nil {
					return state.Dump{}, fmt.Errorf("block #%d not found", number)
				}
				header = block.Header()
			}
			if header == nil {
				return state.Dump{}, fmt.Errorf("block #%d not found", number)
			}
			stateDb, err = api.eth.BlockChain().StateAt(header.Root)
			if err != nil {
				return state.Dump{}, err
			}
		}
	} else if hash, ok := blockNrOrHash.Hash(); ok {
		block := api.eth.blockchain.GetBlockByHash(hash)
		if block == nil {
			return state.Dump{}, fmt.Errorf("block %s not found", hash.Hex())
		}
		stateDb, err = api.eth.BlockChain().StateAt(block.Root())
		if err != nil {
			return state.Dump{}, err
		}
	} else {
		return state.Dump{}, errors.New("either block number or block hash must be specified")
	}

	opts := &state.DumpConfig{
		SkipCode:          nocode,
		SkipStorage:       nostorage,
		OnlyWithAddresses: !incompletes,
		Start:             start,
		Max:               uint64(maxResults),
	}
	if maxResults > AccountRangeMaxResults || maxResults <= 0 {
		opts.Max = AccountRangeMaxResults
	}
	return stateDb.RawDump(opts), nil
}

// StorageRangeResult is the result of a debug_storageRangeAt API call.
type StorageRangeResult struct {
	Storage storageMap   `json:"storage"`
	NextKey *common.Hash `json:"nextKey"` // nil if Storage includes the last key in the trie.
}

type storageMap map[common.Hash]storageEntry

type storageEntry struct {
	Key   *common.Hash `json:"key"`
	Value common.Hash  `json:"value"`
}

// StorageRangeAt returns the storage at the given block height and transaction index.
func (api *DebugAPI) StorageRangeAt(ctx context.Context, blockNrOrHash rpc.BlockNumberOrHash, txIndex int, contractAddress common.Address, keyStart hexutil.Bytes, maxResult int) (StorageRangeResult, error) {
	var block *types.Block

	block, err := api.eth.APIBackend.BlockByNumberOrHash(ctx, blockNrOrHash)
	if err != nil {
		return StorageRangeResult{}, err
	}
	if block == nil {
		return StorageRangeResult{}, fmt.Errorf("block %v not found", blockNrOrHash)
	}
	_, _, statedb, release, err := api.eth.stateAtTransaction(ctx, block, txIndex, 0)
	if err != nil {
		return StorageRangeResult{}, err
	}
	defer release()

	return storageRangeAt(statedb, block.Root(), contractAddress, keyStart, maxResult)
}

func storageRangeAt(statedb *state.StateDB, root common.Hash, address common.Address, start []byte, maxResult int) (StorageRangeResult, error) {
	storageRoot := statedb.GetStorageRoot(address)
	if storageRoot == types.EmptyRootHash || storageRoot == (common.Hash{}) {
		return StorageRangeResult{}, nil // empty storage
	}
	id := trie.StorageTrieID(root, crypto.Keccak256Hash(address.Bytes()), storageRoot)
	tr, err := trie.NewStateTrie(id, statedb.Database().TrieDB())
	if err != nil {
		return StorageRangeResult{}, err
	}
	trieIt, err := tr.NodeIterator(start)
	if err != nil {
		return StorageRangeResult{}, err
	}
	it := trie.NewIterator(trieIt)
	result := StorageRangeResult{Storage: storageMap{}}
	for i := 0; i < maxResult && it.Next(); i++ {
		_, content, _, err := rlp.Split(it.Value)
		if err != nil {
			return StorageRangeResult{}, err
		}
		e := storageEntry{Value: common.BytesToHash(content)}
		if preimage := tr.GetKey(it.Key); preimage != nil {
			preimage := common.BytesToHash(preimage)
			e.Key = &preimage
		}
		result.Storage[common.BytesToHash(it.Key)] = e
	}
	// Add the 'next key' so clients can continue downloading.
	if it.Next() {
		next := common.BytesToHash(it.Key)
		result.NextKey = &next
	}
	return result, nil
}

// GetModifiedAccountsByNumber returns all accounts that have changed between the
// two blocks specified. A change is defined as a difference in nonce, balance,
// code hash, or storage hash.
//
// With one parameter, returns the list of accounts modified in the specified block.
func (api *DebugAPI) GetModifiedAccountsByNumber(startNum uint64, endNum *uint64) ([]common.Address, error) {
	var startHeader, endHeader *types.Header

	startHeader = api.eth.blockchain.GetHeaderByNumber(startNum)
	if startHeader == nil {
		return nil, fmt.Errorf("start block %x not found", startNum)
	}

	if endNum == nil {
		endHeader = startHeader
		startHeader = api.eth.blockchain.GetHeaderByHash(startHeader.ParentHash)
		if startHeader == nil {
			return nil, fmt.Errorf("block %x has no parent", endHeader.Number)
		}
	} else {
		endHeader = api.eth.blockchain.GetHeaderByNumber(*endNum)
		if endHeader == nil {
			return nil, fmt.Errorf("end block %d not found", *endNum)
		}
	}
	return api.getModifiedAccounts(startHeader, endHeader)
}

// GetModifiedAccountsByHash returns all accounts that have changed between the
// two blocks specified. A change is defined as a difference in nonce, balance,
// code hash, or storage hash.
//
// With one parameter, returns the list of accounts modified in the specified block.
func (api *DebugAPI) GetModifiedAccountsByHash(startHash common.Hash, endHash *common.Hash) ([]common.Address, error) {
	var startHeader, endHeader *types.Header
	startHeader = api.eth.blockchain.GetHeaderByHash(startHash)
	if startHeader == nil {
		return nil, fmt.Errorf("start block %x not found", startHash)
	}

	if endHash == nil {
		endHeader = startHeader
		startHeader = api.eth.blockchain.GetHeaderByHash(startHeader.ParentHash)
		if startHeader == nil {
			return nil, fmt.Errorf("block %x has no parent", endHeader.Number)
		}
	} else {
		endHeader = api.eth.blockchain.GetHeaderByHash(*endHash)
		if endHeader == nil {
			return nil, fmt.Errorf("end block %x not found", *endHash)
		}
	}
	return api.getModifiedAccounts(startHeader, endHeader)
}

func (api *DebugAPI) getModifiedAccounts(startHeader, endHeader *types.Header) ([]common.Address, error) {
	if startHeader.Number.Uint64() >= endHeader.Number.Uint64() {
		return nil, fmt.Errorf("start block height (%d) must be less than end block height (%d)", startHeader.Number.Uint64(), endHeader.Number.Uint64())
	}
	triedb := api.eth.BlockChain().TrieDB()

	oldTrie, err := trie.NewStateTrie(trie.StateTrieID(startHeader.Root), triedb)
	if err != nil {
		return nil, err
	}
	newTrie, err := trie.NewStateTrie(trie.StateTrieID(endHeader.Root), triedb)
	if err != nil {
		return nil, err
	}
	oldIt, err := oldTrie.NodeIterator([]byte{})
	if err != nil {
		return nil, err
	}
	newIt, err := newTrie.NodeIterator([]byte{})
	if err != nil {
		return nil, err
	}
	diff, _ := trie.NewDifferenceIterator(oldIt, newIt)
	iter := trie.NewIterator(diff)

	var dirty []common.Address
	for iter.Next() {
		key := newTrie.GetKey(iter.Key)
		if key == nil {
			return nil, fmt.Errorf("no preimage found for hash %x", iter.Key)
		}
		dirty = append(dirty, common.BytesToAddress(key))
	}
	return dirty, nil
}

// GetAccessibleState returns the first number where the node has accessible
// state on disk. Note this being the post-state of that block and the pre-state
// of the next block.
// The (from, to) parameters are the sequence of blocks to search, which can go
// either forwards or backwards
func (api *DebugAPI) GetAccessibleState(from, to rpc.BlockNumber) (uint64, error) {
	if api.eth.blockchain.TrieDB().Scheme() == rawdb.PathScheme {
		return 0, errors.New("state history is not yet available in path-based scheme")
	}
	db := api.eth.ChainDb()
	var pivot uint64
	if p := rawdb.ReadLastPivotNumber(db); p != nil {
		pivot = *p
		log.Info("Found fast-sync pivot marker", "number", pivot)
	}
	var resolveNum = func(num rpc.BlockNumber) (uint64, error) {
		// We don't have state for pending (-2), so treat it as latest
		if num.Int64() < 0 {
			block := api.eth.blockchain.CurrentBlock()
			if block == nil {
				return 0, errors.New("current block missing")
			}
			return block.Number.Uint64(), nil
		}
		return uint64(num.Int64()), nil
	}
	var (
		start   uint64
		end     uint64
		delta   = int64(1)
		lastLog time.Time
		err     error
	)
	if start, err = resolveNum(from); err != nil {
		return 0, err
	}
	if end, err = resolveNum(to); err != nil {
		return 0, err
	}
	if start == end {
		return 0, errors.New("from and to needs to be different")
	}
	if start > end {
		delta = -1
	}
	for i := int64(start); i != int64(end); i += delta {
		if time.Since(lastLog) > 8*time.Second {
			log.Info("Finding roots", "from", start, "to", end, "at", i)
			lastLog = time.Now()
		}
		if i < int64(pivot) {
			continue
		}
		h := api.eth.BlockChain().GetHeaderByNumber(uint64(i))
		if h == nil {
			return 0, fmt.Errorf("missing header %d", i)
		}
		if ok, _ := api.eth.ChainDb().Has(h.Root[:]); ok {
			return uint64(i), nil
		}
	}
	return 0, errors.New("no state found")
}

// SetTrieFlushInterval configures how often in-memory tries are persisted
// to disk. The value is in terms of block processing time, not wall clock.
// If the value is shorter than the block generation time, or even 0 or negative,
// the node will flush trie after processing each block (effectively archive mode).
func (api *DebugAPI) SetTrieFlushInterval(interval string) error {
	if api.eth.blockchain.TrieDB().Scheme() == rawdb.PathScheme {
		return errors.New("trie flush interval is undefined for path-based scheme")
	}
	t, err := time.ParseDuration(interval)
	if err != nil {
		return err
	}
	api.eth.blockchain.SetTrieFlushInterval(t)
	return nil
}

// GetTrieFlushInterval gets the current value of in-memory trie flush interval
func (api *DebugAPI) GetTrieFlushInterval() (string, error) {
	if api.eth.blockchain.TrieDB().Scheme() == rawdb.PathScheme {
		return "", errors.New("trie flush interval is undefined for path-based scheme")
	}
	return api.eth.blockchain.GetTrieFlushInterval().String(), nil
}

// StateSize returns the current state size statistics from the state size tracker.
// Returns an error if the state size tracker is not initialized or if stats are not ready.
func (api *DebugAPI) StateSize(blockHashOrNumber *rpc.BlockNumberOrHash) (interface{}, error) {
	sizer := api.eth.blockchain.StateSizer()
	if sizer == nil {
		return nil, errors.New("state size tracker is not enabled")
	}
	var (
		err   error
		stats *state.SizeStats
	)
	if blockHashOrNumber == nil {
		stats, err = sizer.Query(nil)
	} else {
		header, herr := api.eth.APIBackend.HeaderByNumberOrHash(context.Background(), *blockHashOrNumber)
		if herr != nil || header == nil {
			return nil, fmt.Errorf("block %s is unknown", blockHashOrNumber)
		}
		stats, err = sizer.Query(&header.Root)
	}
	if err != nil {
		return nil, err
	}
	if stats == nil {
		var s string
		if blockHashOrNumber == nil {
			s = "chain head"
		} else {
			s = blockHashOrNumber.String()
		}
		return nil, fmt.Errorf("state size of %s is not available", s)
	}
	return map[string]interface{}{
		"stateRoot":            stats.StateRoot,
		"blockNumber":          hexutil.Uint64(stats.BlockNumber),
		"accounts":             hexutil.Uint64(stats.Accounts),
		"accountBytes":         hexutil.Uint64(stats.AccountBytes),
		"storages":             hexutil.Uint64(stats.Storages),
		"storageBytes":         hexutil.Uint64(stats.StorageBytes),
		"accountTrienodes":     hexutil.Uint64(stats.AccountTrienodes),
		"accountTrienodeBytes": hexutil.Uint64(stats.AccountTrienodeBytes),
		"storageTrienodes":     hexutil.Uint64(stats.StorageTrienodes),
		"storageTrienodeBytes": hexutil.Uint64(stats.StorageTrienodeBytes),
		"contractCodes":        hexutil.Uint64(stats.ContractCodes),
		"contractCodeBytes":    hexutil.Uint64(stats.ContractCodeBytes),
	}, nil
}

func (api *DebugAPI) ExecutionWitness(bn rpc.BlockNumber) (*stateless.ExtWitness, error) {
	bc := api.eth.blockchain
	block, err := api.eth.APIBackend.BlockByNumber(context.Background(), bn)
	if err != nil {
		return &stateless.ExtWitness{}, fmt.Errorf("block number %v not found", bn)
	}
	parent := bc.GetHeader(block.ParentHash(), block.NumberU64()-1)
	if parent == nil {
		return &stateless.ExtWitness{}, fmt.Errorf("block number %v found, but parent missing", bn)
	}
	result, err := bc.ProcessBlock(parent.Root, block, false, true)
	if err != nil {
		return nil, err
	}
	return result.Witness().ToExtWitness(), nil
}

func (api *DebugAPI) ExecutionWitnessByHash(hash common.Hash) (*stateless.ExtWitness, error) {
	bc := api.eth.blockchain
	block := bc.GetBlockByHash(hash)
	if block == nil {
		return &stateless.ExtWitness{}, fmt.Errorf("block hash %x not found", hash)
	}
	parent := bc.GetHeader(block.ParentHash(), block.NumberU64()-1)
	if parent == nil {
		return &stateless.ExtWitness{}, fmt.Errorf("block number %x found, but parent missing", hash)
	}
	result, err := bc.ProcessBlock(parent.Root, block, false, true)
	if err != nil {
		return nil, err
	}
	return result.Witness().ToExtWitness(), nil
}

// UBTStorageProof represents a storage proof for UBT.
type UBTStorageProof struct {
	Key       common.Hash     `json:"key"`
	Value     hexutil.Bytes   `json:"value"`
	Proof     []hexutil.Bytes `json:"proof"`
	ProofPath []UBTProofNode  `json:"proofPath,omitempty"`
}

// UBTProofNode represents a proof sibling with its depth.
type UBTProofNode struct {
	Depth uint16        `json:"depth"`
	Hash  hexutil.Bytes `json:"hash"`
}

// UBTProofResult is the response type for debug_getUBTProof.
type UBTProofResult struct {
	Address          common.Address    `json:"address"`
	AccountProof     []hexutil.Bytes   `json:"accountProof"`
	AccountProofPath []UBTProofNode    `json:"accountProofPath,omitempty"`
	Balance          *hexutil.Big      `json:"balance"`
	CodeHash         common.Hash       `json:"codeHash"`
	Nonce            hexutil.Uint64    `json:"nonce"`
	BlockHash        common.Hash       `json:"blockHash"`
	BlockNumber      hexutil.Uint64    `json:"blockNumber"`
	TrieRoot         common.Hash       `json:"trieRoot"`
	ParentBlockHash  common.Hash       `json:"parentBlockHash"`
	ParentUbtRoot    common.Hash       `json:"parentUbtRoot"`
	StorageHash      common.Hash       `json:"storageHash"`
	StorageProof     []UBTStorageProof `json:"storageProof"`
	StateRoot        common.Hash       `json:"stateRoot"`
	UbtRoot          common.Hash       `json:"ubtRoot"`
	ProofRoot        common.Hash       `json:"proofRoot"`
}

// UBTRootResult is the response type for debug_getUBTRoot.
type UBTRootResult struct {
	BlockHash   common.Hash    `json:"blockHash"`
	BlockNumber hexutil.Uint64 `json:"blockNumber"`
	UbtRoot     common.Hash    `json:"ubtRoot"`
	Ok          bool           `json:"ok"`
}

// UBTStateResult is the response type for debug_getUBTState.
type UBTStateResult struct {
	Address   common.Address                `json:"address"`
	Balance   *hexutil.Big                  `json:"balance"`
	Nonce     hexutil.Uint64                `json:"nonce"`
	CodeHash  common.Hash                   `json:"codeHash"`
	CodeSize  hexutil.Uint64                `json:"codeSize"`
	Storage   map[common.Hash]hexutil.Bytes `json:"storage"`
	StateRoot common.Hash                   `json:"stateRoot"`
	UbtRoot   common.Hash                   `json:"ubtRoot"`
}

// GetUBTRoot returns the UBT root for the given block number or hash.
func (api *DebugAPI) GetUBTRoot(ctx context.Context, blockNrOrHash rpc.BlockNumberOrHash) (*UBTRootResult, error) {
	header, err := api.eth.APIBackend.HeaderByNumberOrHash(ctx, blockNrOrHash)
	if err != nil {
		return nil, err
	}
	if header == nil {
		return nil, fmt.Errorf("block %v not found", blockNrOrHash)
	}
	root, ok := rawdb.ReadUBTBlockRoot(api.eth.ChainDb(), header.Hash())
	return &UBTRootResult{
		BlockHash:   header.Hash(),
		BlockNumber: hexutil.Uint64(header.Number.Uint64()),
		UbtRoot:     root,
		Ok:          ok,
	}, nil
}

// GetUBTProof returns the binary trie proof for a given account and storage keys.
func (api *DebugAPI) GetUBTProof(ctx context.Context, address common.Address, storageKeys []string, blockNrOrHash rpc.BlockNumberOrHash) (*UBTProofResult, error) {
	statedb, header, err := api.eth.APIBackend.StateAndHeaderByNumberOrHash(ctx, blockNrOrHash)
	if err != nil {
		return nil, err
	}
	if statedb == nil || header == nil {
		return nil, fmt.Errorf("state not available for %v", blockNrOrHash)
	}

	balance := statedb.GetBalance(address)
	nonce := statedb.GetNonce(address)
	codeHash := statedb.GetCodeHash(address)

	var ubtRoot common.Hash
	if sc := api.eth.blockchain.UBTSidecar(); sc != nil {
		if !sc.Ready() {
			return nil, errors.New("ubt sidecar not ready")
		}
		root, ok := sc.GetUBTRoot(header.Hash())
		if !ok {
			return nil, fmt.Errorf("ubt root not found for block %x", header.Hash())
		}
		ubtRoot = root
	} else {
		ubtRoot = header.Root
	}
	parentHash := header.ParentHash
	var parentUbtRoot common.Hash
	if sc := api.eth.blockchain.UBTSidecar(); sc != nil {
		if root, ok := sc.GetUBTRoot(parentHash); ok {
			parentUbtRoot = root
		}
	}
	bt, err := api.openBinaryTrie(ubtRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to open binary trie: %w", err)
	}
	trieRoot := bt.Hash()

	accountKey := bintrie.GetBinaryTreeKeyBasicData(address)
	accountProof, accountProofPath, accountProofRoot, err := generateProofWithPathFromBinaryTrie(bt, accountKey)
	if err != nil {
		return nil, fmt.Errorf("failed to generate account proof: %w", err)
	}

	storageProofs := make([]UBTStorageProof, len(storageKeys))
	for i, keyHex := range storageKeys {
		key, err := parseStorageKey(keyHex)
		if err != nil {
			return nil, err
		}
		value := statedb.GetState(address, key)
		ubtKey := bintrie.GetBinaryTreeKeyStorageSlot(address, key.Bytes())
		proof, proofPath, _, err := generateProofWithPathFromBinaryTrie(bt, ubtKey)
		if err != nil {
			return nil, fmt.Errorf("failed to generate storage proof for %s: %w", keyHex, err)
		}
		storageProofs[i] = UBTStorageProof{
			Key:       key,
			Value:     value.Bytes(),
			Proof:     proof,
			ProofPath: proofPath,
		}
	}

	return &UBTProofResult{
		Address:          address,
		AccountProof:     accountProof,
		AccountProofPath: accountProofPath,
		Balance:          (*hexutil.Big)(balance.ToBig()),
		CodeHash:         codeHash,
		Nonce:            hexutil.Uint64(nonce),
		BlockHash:        header.Hash(),
		BlockNumber:      hexutil.Uint64(header.Number.Uint64()),
		TrieRoot:         trieRoot,
		ParentBlockHash:  parentHash,
		ParentUbtRoot:    parentUbtRoot,
		StorageHash:      common.Hash{},
		StorageProof:     storageProofs,
		StateRoot:        header.Root,
		UbtRoot:          ubtRoot,
		ProofRoot:        accountProofRoot,
	}, nil
}

// GetUBTState returns account and storage data read from the UBT sidecar.
func (api *DebugAPI) GetUBTState(ctx context.Context, address common.Address, storageKeys []string, blockNrOrHash rpc.BlockNumberOrHash) (*UBTStateResult, error) {
	sc := api.eth.blockchain.UBTSidecar()
	if sc == nil || !sc.Ready() {
		return nil, errors.New("ubt sidecar not ready")
	}
	header, err := api.eth.APIBackend.HeaderByNumberOrHash(ctx, blockNrOrHash)
	if err != nil {
		return nil, err
	}
	if header == nil {
		return nil, fmt.Errorf("block %v not found", blockNrOrHash)
	}
	ubtRoot, ok := sc.GetUBTRoot(header.Hash())
	if !ok {
		return nil, fmt.Errorf("ubt root not found for block %x", header.Hash())
	}
	acc, err := sc.ReadAccount(ubtRoot, address)
	if err != nil {
		return nil, err
	}
	result := &UBTStateResult{
		Address:   address,
		StateRoot: header.Root,
		UbtRoot:   ubtRoot,
		Storage:   make(map[common.Hash]hexutil.Bytes, len(storageKeys)),
	}
	if acc != nil {
		result.Balance = (*hexutil.Big)(acc.Balance.ToBig())
		result.Nonce = hexutil.Uint64(acc.Nonce)
		result.CodeHash = acc.CodeHash
		result.CodeSize = hexutil.Uint64(acc.CodeSize)
	} else {
		result.Balance = (*hexutil.Big)(new(big.Int))
	}
	for _, keyHex := range storageKeys {
		key, err := parseStorageKey(keyHex)
		if err != nil {
			return nil, err
		}
		value, err := sc.ReadStorage(ubtRoot, address, key)
		if err != nil {
			return nil, err
		}
		result.Storage[key] = value.Bytes()
	}
	return result, nil
}

// ExecutionWitnessUBT returns a path-aware witness for UBT nodes.
func (api *DebugAPI) ExecutionWitnessUBT(ctx context.Context, blockNrOrHash rpc.BlockNumberOrHash) (*stateless.ExtUBTWitness, error) {
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

func (api *DebugAPI) openBinaryTrie(root common.Hash) (*bintrie.BinaryTrie, error) {
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

func generateProofWithPathFromBinaryTrie(bt *bintrie.BinaryTrie, targetKey []byte) ([]hexutil.Bytes, []UBTProofNode, common.Hash, error) {
	siblings, stem, values, err := bt.ProofWithDepth(targetKey)
	if err != nil {
		return nil, nil, common.Hash{}, err
	}
	legacyProof := make([]hexutil.Bytes, 0, len(siblings)+1+len(values))
	for _, s := range siblings {
		legacyProof = append(legacyProof, hexutil.Bytes(s.Hash.Bytes()))
	}
	if stem != nil {
		legacyProof = append(legacyProof, hexutil.Bytes(stem))
		for _, v := range values {
			legacyProof = append(legacyProof, hexutil.Bytes(v))
		}
	}
	proofPath := make([]UBTProofNode, len(siblings))
	for i, s := range siblings {
		proofPath[i] = UBTProofNode{Depth: s.Depth, Hash: hexutil.Bytes(s.Hash.Bytes())}
	}
	leaf := computeProofLeafHash(stem, values)
	proofRoot := computeProofRootWithPath(targetKey, siblings, leaf)
	return legacyProof, proofPath, proofRoot, nil
}

func computeProofLeafHash(stem []byte, values [][]byte) common.Hash {
	if stem == nil {
		return common.Hash{}
	}
	var data [bintrie.StemNodeWidth]common.Hash
	for i, v := range values {
		if len(v) == 0 {
			continue
		}
		h := sha256.Sum256(v)
		data[i] = common.BytesToHash(h[:])
	}
	h := sha256.New()
	for level := 1; level <= 8; level++ {
		for i := 0; i < bintrie.StemNodeWidth/(1<<level); i++ {
			if data[i*2] == (common.Hash{}) && data[i*2+1] == (common.Hash{}) {
				data[i] = common.Hash{}
				continue
			}
			h.Reset()
			h.Write(data[i*2][:])
			h.Write(data[i*2+1][:])
			data[i] = common.Hash(h.Sum(nil))
		}
	}
	h.Reset()
	h.Write(stem)
	h.Write([]byte{0})
	h.Write(data[0][:])
	return common.BytesToHash(h.Sum(nil))
}

func computeProofRootWithPath(key []byte, siblings []bintrie.ProofSibling, leaf common.Hash) common.Hash {
	if len(siblings) == 0 {
		return leaf
	}
	ordered := make([]bintrie.ProofSibling, len(siblings))
	copy(ordered, siblings)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Depth < ordered[j].Depth })
	current := leaf
	for i := len(ordered) - 1; i >= 0; i-- {
		depth := int(ordered[i].Depth)
		bit := (key[depth/8] >> (7 - (depth % 8))) & 1
		if bit == 0 {
			current = hashPair(current, ordered[i].Hash)
		} else {
			current = hashPair(ordered[i].Hash, current)
		}
	}
	return current
}

func hashPair(left, right common.Hash) common.Hash {
	var data [64]byte
	copy(data[:32], left[:])
	copy(data[32:], right[:])
	sum := sha256.Sum256(data[:])
	return common.BytesToHash(sum[:])
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
