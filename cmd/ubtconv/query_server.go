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
	"fmt"
	"math/big"
	"net"
	"net/http"
	"sort"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/ethdb/memorydb"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/ethereum/go-ethereum/trie/bintrie"
)

// QueryAPI provides read-only UBT state queries.
type QueryAPI struct {
	consumer *Consumer
}

// NewQueryAPI creates a new QueryAPI instance.
func NewQueryAPI(consumer *Consumer) *QueryAPI {
	return &QueryAPI{consumer: consumer}
}

type queryStateRef struct {
	blockNumber uint64
	root        common.Hash
}

func (api *QueryAPI) historyWindow() uint64 {
	if api.consumer.cfg == nil {
		return 0
	}
	return api.consumer.cfg.TrieDBStateHistory
}

func (api *QueryAPI) maxBatchSize() uint64 {
	if api.consumer.cfg != nil && api.consumer.cfg.QueryRPCMaxBatch > 0 {
		return api.consumer.cfg.QueryRPCMaxBatch
	}
	return 100
}

func (api *QueryAPI) latestStateRef() queryStateRef {
	root := api.consumer.state.AppliedRoot
	if root == (common.Hash{}) && api.consumer.applier != nil {
		root = api.consumer.applier.Root()
	}
	return queryStateRef{
		blockNumber: api.consumer.state.AppliedBlock,
		root:        root,
	}
}

func (api *QueryAPI) resolveBlockByNumber(blockNumber uint64, latest queryStateRef) (queryStateRef, error) {
	if blockNumber > latest.blockNumber {
		return queryStateRef{}, fmt.Errorf("state not yet available: requested block %d is ahead of daemon applied head %d", blockNumber, latest.blockNumber)
	}
	if blockNumber == latest.blockNumber {
		return latest, nil
	}
	window := api.historyWindow()
	if window == 0 || latest.blockNumber-blockNumber > window {
		return queryStateRef{}, fmt.Errorf("state not available: block %d is outside retained UBT state history window", blockNumber)
	}
	if api.consumer.db == nil {
		return queryStateRef{}, fmt.Errorf("state not available: historical UBT index is unavailable")
	}
	root := rawdb.ReadUBTBlockRoot(api.consumer.db, blockNumber)
	if root == (common.Hash{}) {
		return queryStateRef{}, fmt.Errorf("state not available: missing UBT root for block %d", blockNumber)
	}
	return queryStateRef{blockNumber: blockNumber, root: root}, nil
}

func (api *QueryAPI) resolveBlockByHash(blockHash common.Hash, requireCanonical bool, latest queryStateRef) (queryStateRef, error) {
	if api.consumer.db == nil {
		return queryStateRef{}, fmt.Errorf("state not available: hash selector requires canonical UBT index")
	}
	blockNumber, ok := rawdb.ReadUBTCanonicalBlockNumber(api.consumer.db, blockHash)
	if !ok {
		return queryStateRef{}, fmt.Errorf("state not available: unknown canonical block hash %s", blockHash)
	}
	canonicalHash := rawdb.ReadUBTCanonicalBlockHash(api.consumer.db, blockNumber)
	if canonicalHash != blockHash {
		if requireCanonical {
			return queryStateRef{}, fmt.Errorf("state not available: non-canonical block hash %s", blockHash)
		}
		return queryStateRef{}, fmt.Errorf("state not available: block hash %s is not indexed", blockHash)
	}
	return api.resolveBlockByNumber(blockNumber, latest)
}

func (api *QueryAPI) resolveBlockSelector(blockNrOrHash *rpc.BlockNumberOrHash) (queryStateRef, error) {
	latest := api.latestStateRef()
	if blockNrOrHash == nil {
		return latest, nil
	}
	if bn, ok := blockNrOrHash.Number(); ok {
		switch bn {
		case rpc.LatestBlockNumber:
			return latest, nil
		case rpc.PendingBlockNumber, rpc.SafeBlockNumber, rpc.FinalizedBlockNumber:
			return queryStateRef{}, fmt.Errorf("unsupported block selector tag for UBT debug RPC: %s", bn.String())
		}
		if bn < 0 {
			return queryStateRef{}, fmt.Errorf("unsupported block selector tag for UBT debug RPC: %s", bn.String())
		}
		return api.resolveBlockByNumber(uint64(bn), latest)
	}
	if hash, ok := blockNrOrHash.Hash(); ok {
		return api.resolveBlockByHash(hash, blockNrOrHash.RequireCanonical, latest)
	}
	return queryStateRef{}, fmt.Errorf("invalid block selector")
}

func (api *QueryAPI) trieForQuery(root common.Hash) (*bintrie.BinaryTrie, error) {
	if api.consumer.applier == nil {
		return nil, fmt.Errorf("UBT trie not initialized")
	}
	if root == api.consumer.applier.Root() && api.consumer.applier.Trie() != nil {
		return api.consumer.applier.Trie(), nil
	}
	tr, err := api.consumer.applier.TrieAt(root)
	if err != nil {
		return nil, fmt.Errorf("state not available: %w", err)
	}
	return tr, nil
}

// GetBalance returns the UBT balance for an address.
// The blockNrOrHash parameter is accepted for API compatibility with eth_getBalance.
func (api *QueryAPI) GetBalance(ctx context.Context, addr common.Address, blockNrOrHash *rpc.BlockNumberOrHash) (*hexutil.Big, error) {
	// Snapshot state under lock
	api.consumer.mu.Lock()
	stateRef, err := api.resolveBlockSelector(blockNrOrHash)
	api.consumer.mu.Unlock()
	if err != nil {
		return nil, err
	}

	// Trie reads outside the lock
	tr, err := api.trieForQuery(stateRef.root)
	if err != nil {
		return nil, err
	}
	acct, err := tr.GetAccount(addr)
	if err != nil {
		return nil, fmt.Errorf("get account: %w", err)
	}
	if acct == nil {
		return (*hexutil.Big)(common.Big0), nil
	}
	return (*hexutil.Big)(acct.Balance.ToBig()), nil
}

// GetStorageAt returns UBT storage value at the given address and slot.
// The blockNrOrHash parameter is accepted for API compatibility with eth_getStorageAt.
func (api *QueryAPI) GetStorageAt(ctx context.Context, addr common.Address, slot common.Hash, blockNrOrHash *rpc.BlockNumberOrHash) (result hexutil.Bytes, err error) {
	defer func() {
		if r := recover(); r != nil {
			log.Error("GetStorageAt panic", "panic", r, "addr", addr, "slot", slot)
			result = nil
			err = fmt.Errorf("internal error: panic in GetStorageAt")
		}
	}()

	// Snapshot state under lock
	api.consumer.mu.Lock()
	stateRef, err := api.resolveBlockSelector(blockNrOrHash)
	api.consumer.mu.Unlock()
	if err != nil {
		return nil, err
	}

	// Trie reads outside the lock
	tr, err := api.trieForQuery(stateRef.root)
	if err != nil {
		return nil, err
	}
	val, err := tr.GetStorage(addr, slot.Bytes())
	if err != nil {
		return nil, fmt.Errorf("get storage: %w", err)
	}
	// If nil value, return 32 zero bytes
	if val == nil {
		return make(hexutil.Bytes, 32), nil
	}
	// Return zero-padded 32-byte value
	padded := common.BytesToHash(val)
	return hexutil.Bytes(padded[:]), nil
}

// GetCode returns UBT contract code for an address.
// The blockNrOrHash parameter is accepted for API compatibility with eth_getCode.
func (api *QueryAPI) GetCode(ctx context.Context, addr common.Address, blockNrOrHash *rpc.BlockNumberOrHash) (hexutil.Bytes, error) {
	// Snapshot state under lock
	api.consumer.mu.Lock()
	stateRef, err := api.resolveBlockSelector(blockNrOrHash)
	api.consumer.mu.Unlock()
	if err != nil {
		return nil, err
	}

	// Trie reads outside the lock
	tr, err := api.trieForQuery(stateRef.root)
	if err != nil {
		return nil, err
	}

	// Get account to check if it has code
	acct, err := tr.GetAccount(addr)
	if err != nil {
		return nil, fmt.Errorf("get account: %w", err)
	}
	if acct == nil {
		return hexutil.Bytes{}, nil
	}
	// If no code hash or empty code hash, return empty
	if common.BytesToHash(acct.CodeHash) == types.EmptyCodeHash {
		return hexutil.Bytes{}, nil
	}

	code, err := tr.GetCode(addr)
	if err != nil {
		return nil, fmt.Errorf("get code: %w", err)
	}
	if code == nil {
		return hexutil.Bytes{}, nil
	}
	return hexutil.Bytes(code), nil
}

// Status returns the daemon's current state.
func (api *QueryAPI) Status(ctx context.Context) (map[string]any, error) {
	// Snapshot all state fields under lock, then release
	api.consumer.mu.Lock()
	appliedSeq := api.consumer.state.AppliedSeq
	appliedBlock := api.consumer.state.AppliedBlock
	appliedRoot := api.consumer.state.AppliedRoot
	pendingSeq := api.consumer.state.PendingSeq
	pendingStatus := api.consumer.state.PendingStatus
	pendingUpdatedAt := api.consumer.state.PendingUpdatedAt
	outboxLag := api.consumer.outboxLag
	backpressureLagThreshold := uint64(0)
	executionClassRPCEnabled := false
	if api.consumer.cfg != nil {
		backpressureLagThreshold = api.consumer.cfg.BackpressureLagThreshold
		executionClassRPCEnabled = api.consumer.cfg.ExecutionClassRPCEnabled
	}
	tracker := api.consumer.phaseTracker
	api.consumer.mu.Unlock()

	result := map[string]any{
		"appliedSeq":               appliedSeq,
		"appliedBlock":             appliedBlock,
		"appliedRoot":              appliedRoot,
		"pendingSeq":               pendingSeq,
		"pendingState":             pendingStatus.String(),
		"pendingUpdatedAt":         pendingUpdatedAt,
		"outboxLag":                outboxLag,
		"backpressureLagThreshold": backpressureLagThreshold,
		"backpressureTriggered":    backpressureLagThreshold > 0 && outboxLag > backpressureLagThreshold,
		"executionClassRPCEnabled": executionClassRPCEnabled,
	}

	// Phase tracker methods are safe to call without the consumer lock
	if tracker != nil {
		result["phase"] = string(tracker.Current())
		result["productionReady"] = tracker.IsProductionReady()
		if !tracker.SyncedSince().IsZero() {
			result["syncedSince"] = tracker.SyncedSince().Unix()
		}
	}
	return result, nil
}

// SafeCompactSeq returns the safe-to-compact sequence number for coordinated outbox compaction.
// The geth node can safely delete outbox events below this sequence.
func (api *QueryAPI) SafeCompactSeq(ctx context.Context) (uint64, error) {
	api.consumer.mu.Lock()
	seq := api.consumer.state.AppliedSeq
	api.consumer.mu.Unlock()

	return seq, nil
}

// ProofVerifyResult contains the result of proof verification with explicit presence semantics.
type ProofVerifyResult struct {
	Valid   bool          `json:"valid"`
	Present bool          `json:"present"`
	Value   hexutil.Bytes `json:"value,omitempty"`
}

// VerifyProof verifies a UBT Merkle proof against the given root hash.
// Returns a structured result indicating validity and key presence.
func (api *QueryAPI) VerifyProof(ctx context.Context, root common.Hash, key common.Hash, proofNodes map[common.Hash]hexutil.Bytes) (*ProofVerifyResult, error) {
	// Validate request bounds
	if root == (common.Hash{}) {
		return nil, fmt.Errorf("root hash cannot be zero")
	}
	if maxBatch := api.maxBatchSize(); uint64(len(proofNodes)) > maxBatch {
		return nil, fmt.Errorf("too many proof nodes: %d exceeds max batch size %d", len(proofNodes), maxBatch)
	}

	// Build an in-memory proof database
	proofDb := memorydb.New()
	for hash, data := range proofNodes {
		if err := proofDb.Put(hash.Bytes(), data); err != nil {
			return nil, fmt.Errorf("failed to store proof node: %w", err)
		}
	}

	value, err := bintrie.VerifyProof(root, key.Bytes(), proofDb)
	if err != nil {
		return &ProofVerifyResult{Valid: false}, err
	}

	return &ProofVerifyResult{
		Valid:   true,
		Present: value != nil,
		Value:   hexutil.Bytes(value),
	}, nil
}

// UBTProofResult contains the proof data for a UBT key.
type UBTProofResult struct {
	Key        common.Hash                   `json:"key"`
	Root       common.Hash                   `json:"root"`
	ProofNodes map[common.Hash]hexutil.Bytes `json:"proofNodes"`
}

// GetProof generates a UBT Merkle proof for the given raw trie key.
// The blockNrOrHash parameter is accepted for API compatibility.
//
// Note: This is a raw-key proof against the unified binary trie. For an
// eth_getProof-compatible interface, use GetAccountProof instead.
func (api *QueryAPI) GetProof(ctx context.Context, key common.Hash, blockNrOrHash *rpc.BlockNumberOrHash) (*UBTProofResult, error) {
	// Snapshot state under lock
	api.consumer.mu.Lock()
	if api.consumer.applier == nil || api.consumer.applier.Trie() == nil {
		api.consumer.mu.Unlock()
		return nil, fmt.Errorf("UBT trie not initialized")
	}
	stateRef, err := api.resolveBlockSelector(blockNrOrHash)
	api.consumer.mu.Unlock()
	if err != nil {
		return nil, err
	}

	// Proof generation outside the lock
	proofMap, err := api.consumer.applier.GenerateProofAt(stateRef.root, key.Bytes())
	if err != nil {
		return nil, fmt.Errorf("generate proof: %w", err)
	}

	// Convert to hex-encoded bytes for JSON serialization
	proofNodes := make(map[common.Hash]hexutil.Bytes)
	for k, v := range proofMap {
		proofNodes[k] = hexutil.Bytes(v)
	}

	return &UBTProofResult{
		Key:        key,
		Root:       stateRef.root,
		ProofNodes: proofNodes,
	}, nil
}

// AccountProofResult mirrors eth_getProof output for API compatibility.
type AccountProofResult struct {
	Address      common.Address                `json:"address"`
	AccountProof map[common.Hash]hexutil.Bytes `json:"accountProof"`
	StorageProof []StorageProofEntry           `json:"storageProof"`
	Root         common.Hash                   `json:"root"`
}

// StorageProofEntry contains the proof for a single storage slot.
type StorageProofEntry struct {
	Key   common.Hash                   `json:"key"`
	Proof map[common.Hash]hexutil.Bytes `json:"proof"`
}

// GetAccountProof generates UBT proofs for an account and its storage slots.
// This follows the eth_getProof pattern: (address, storageKeys[], blockNrOrHash).
func (api *QueryAPI) GetAccountProof(ctx context.Context, addr common.Address, storageKeys []common.Hash, blockNrOrHash *rpc.BlockNumberOrHash) (*AccountProofResult, error) {
	if maxBatch := api.maxBatchSize(); uint64(len(storageKeys)) > maxBatch {
		return nil, fmt.Errorf("too many storage keys: %d exceeds max batch size %d", len(storageKeys), maxBatch)
	}

	// Snapshot state under lock
	api.consumer.mu.Lock()
	if api.consumer.applier == nil || api.consumer.applier.Trie() == nil {
		api.consumer.mu.Unlock()
		return nil, fmt.Errorf("UBT trie not initialized")
	}
	stateRef, err := api.resolveBlockSelector(blockNrOrHash)
	api.consumer.mu.Unlock()
	if err != nil {
		return nil, err
	}

	// Proof generation outside the lock
	accountKey := bintrie.GetBinaryTreeKeyBasicData(addr)
	accountProofMap, err := api.consumer.applier.GenerateProofAt(stateRef.root, accountKey)
	if err != nil {
		return nil, fmt.Errorf("generate account proof: %w", err)
	}
	accountProof := make(map[common.Hash]hexutil.Bytes)
	for k, v := range accountProofMap {
		accountProof[k] = hexutil.Bytes(v)
	}

	// Generate storage proofs
	storageProofs := make([]StorageProofEntry, len(storageKeys))
	for i, slot := range storageKeys {
		storageKey := bintrie.GetBinaryTreeKeyStorageSlot(addr, slot.Bytes())
		proofMap, err := api.consumer.applier.GenerateProofAt(stateRef.root, storageKey)
		if err != nil {
			return nil, fmt.Errorf("generate storage proof for slot %s: %w", slot, err)
		}
		proofNodes := make(map[common.Hash]hexutil.Bytes)
		for k, v := range proofMap {
			proofNodes[k] = hexutil.Bytes(v)
		}
		storageProofs[i] = StorageProofEntry{
			Key:   slot,
			Proof: proofNodes,
		}
	}

	return &AccountProofResult{
		Address:      addr,
		AccountProof: accountProof,
		StorageProof: storageProofs,
		Root:         stateRef.root,
	}, nil
}

// CallUBT executes a call against UBT state.
// Signature is kept close to eth_call:
//   ubt_callUBT(callObject, blockNumberOrHash?, stateOverrides?, blockOverrides?)
func (api *QueryAPI) CallUBT(ctx context.Context, args map[string]any, blockNrOrHash *rpc.BlockNumberOrHash, stateOverrides map[string]any, blockOverrides map[string]any) (hexutil.Bytes, error) {
	// Snapshot state under lock
	api.consumer.mu.Lock()
	if api.consumer.applier == nil {
		api.consumer.mu.Unlock()
		return nil, fmt.Errorf("UBT trie not initialized")
	}
	if api.consumer.cfg == nil || !api.consumer.cfg.ExecutionClassRPCEnabled {
		api.consumer.mu.Unlock()
		return nil, fmt.Errorf("ubt_callUBT: execution-class RPC disabled (set --execution-class-rpc-enabled)")
	}
	if len(stateOverrides) > 0 {
		api.consumer.mu.Unlock()
		return nil, fmt.Errorf("ubt_callUBT: stateOverrides are not yet supported")
	}
	if len(blockOverrides) > 0 {
		api.consumer.mu.Unlock()
		return nil, fmt.Errorf("ubt_callUBT: blockOverrides are not yet supported")
	}
	cfg := api.consumer.cfg
	stateRef, err := api.resolveBlockSelector(blockNrOrHash)
	if err != nil {
		api.consumer.mu.Unlock()
		return nil, err
	}
	db := api.consumer.db // for GetHash lookups
	gasCap := cfg.effectiveRPCGasCap()
	chainConfig := cfg.resolveChainConfig()
	api.consumer.mu.Unlock()

	// Parse call arguments with gas cap enforcement
	msg, err := parseCallArgs(args, gasCap)
	if err != nil {
		return nil, fmt.Errorf("ubt_callUBT: %w", err)
	}

	// Create StateDB from UBT (outside the lock)
	stateDB, err := api.createStateDB(stateRef.root)
	if err != nil {
		return nil, fmt.Errorf("ubt_callUBT: %w", err)
	}

	// Create synthetic block context using applied block number and current time.
	// Limitations (by design — we don't have full block headers):
	//   - Coinbase is zero (COINBASE opcode returns 0x0)
	//   - BaseFee is zero with NoBaseFee=true (no EIP-1559 gas pricing)
	//   - Difficulty/PrevRandao is zero (DIFFICULTY/PREVRANDAO returns 0)
	//   - GetHash returns stored canonical hashes within committed range,
	//     zero for blocks outside this range
	blockCtx := vm.BlockContext{
		CanTransfer: core.CanTransfer,
		Transfer:    core.Transfer,
		GetHash: func(n uint64) common.Hash {
			if db != nil {
				return rawdb.ReadUBTCanonicalBlockHash(db, n)
			}
			return common.Hash{}
		},
		Coinbase:    common.Address{},
		BlockNumber: new(big.Int).SetUint64(stateRef.blockNumber),
		Time:        uint64(time.Now().Unix()),
		Difficulty:  new(big.Int),
		BaseFee:     new(big.Int),
		GasLimit:    gasCap,
	}

	// Create EVM with chain config resolved from configured chain ID
	vmConfig := vm.Config{NoBaseFee: true}
	evm := vm.NewEVM(blockCtx, stateDB, chainConfig, vmConfig)

	gp := new(core.GasPool).AddGas(gasCap)
	evm.SetTxContext(core.NewEVMTxContext(msg))
	result, err := core.ApplyMessage(evm, msg, gp)
	if err != nil {
		return nil, fmt.Errorf("ubt_callUBT: execution failed: %w", err)
	}
	if result.Err != nil {
		return result.ReturnData, fmt.Errorf("ubt_callUBT: EVM error: %w", result.Err)
	}
	return result.ReturnData, nil
}

// ExecutionWitnessUBT returns a deterministic, execution-class witness snapshot for a selected block.
// Current implementation is root-bound and intentionally partial until full tx re-execution wiring is completed.
func (api *QueryAPI) ExecutionWitnessUBT(ctx context.Context, blockNrOrHash *rpc.BlockNumberOrHash) (map[string]any, error) {
	api.consumer.mu.Lock()
	if api.consumer.applier == nil {
		api.consumer.mu.Unlock()
		return nil, fmt.Errorf("UBT trie not initialized")
	}
	if api.consumer.cfg == nil || !api.consumer.cfg.ExecutionClassRPCEnabled {
		api.consumer.mu.Unlock()
		return nil, fmt.Errorf("ubt_executionWitnessUBT: execution-class RPC disabled (set --execution-class-rpc-enabled)")
	}
	stateRef, err := api.resolveBlockSelector(blockNrOrHash)
	if err != nil {
		api.consumer.mu.Unlock()
		return nil, err
	}
	blockHash := common.Hash{}
	if api.consumer.db != nil {
		blockHash = rawdb.ReadUBTCanonicalBlockHash(api.consumer.db, stateRef.blockNumber)
	}

	accountsTouched := make([]string, 0)
	storageTouched := make([]string, 0)
	codeTouched := make([]string, 0)
	if api.consumer.lastDiff != nil && api.consumer.lastDiff.Root == stateRef.root && stateRef.blockNumber == api.consumer.state.AppliedBlock {
		for _, acct := range api.consumer.lastDiff.Accounts {
			accountsTouched = append(accountsTouched, acct.Address.Hex())
		}
		for _, slot := range api.consumer.lastDiff.Storage {
			storageTouched = append(storageTouched, fmt.Sprintf("%s:%s", slot.Address.Hex(), slot.SlotKeyRaw.Hex()))
		}
		for _, code := range api.consumer.lastDiff.Codes {
			codeTouched = append(codeTouched, code.Address.Hex())
		}
	}
	api.consumer.mu.Unlock()
	sort.Strings(accountsTouched)
	sort.Strings(storageTouched)
	sort.Strings(codeTouched)

	return map[string]any{
		"blockNumber":     hexutil.Uint64(stateRef.blockNumber),
		"blockHash":       blockHash,
		"stateRoot":       stateRef.root,
		"accountsTouched": accountsTouched,
		"storageTouched":  storageTouched,
		"codeTouched":     codeTouched,
		"status":          "partial",
	}, nil
}

// createStateDB creates a state.StateDB backed by the UBT at the given root.
func (api *QueryAPI) createStateDB(root common.Hash) (*state.StateDB, error) {
	applier := api.consumer.applier
	db := newUBTStateDatabase(applier.TrieDB(), applier.DiskDB())
	return state.New(root, db)
}

// parseCallArgs parses the map-based call arguments into a core.Message.
// gasCap is enforced: user-supplied gas is capped, and absent gas defaults to the cap.
func parseCallArgs(args map[string]any, gasCap uint64) (*core.Message, error) {
	msg := &core.Message{
		GasLimit:              gasCap,
		GasPrice:              new(big.Int),
		GasFeeCap:             new(big.Int),
		GasTipCap:             new(big.Int),
		Value:                 new(big.Int),
		SkipNonceChecks:       true,
		SkipTransactionChecks: true, // Skip EIP-7825 gas cap and EOA checks (read-only call)
	}

	if from, ok := args["from"]; ok {
		if s, ok := from.(string); ok {
			if !common.IsHexAddress(s) {
				return nil, fmt.Errorf("invalid from address: %q", s)
			}
			msg.From = common.HexToAddress(s)
		}
	}
	if to, ok := args["to"]; ok {
		if s, ok := to.(string); ok {
			if !common.IsHexAddress(s) {
				return nil, fmt.Errorf("invalid to address: %q", s)
			}
			addr := common.HexToAddress(s)
			msg.To = &addr
		}
	}
	if data, ok := args["data"]; ok {
		if s, ok := data.(string); ok {
			b, err := hexutil.Decode(s)
			if err != nil {
				return nil, fmt.Errorf("invalid data: %w", err)
			}
			msg.Data = b
		}
	}
	if value, ok := args["value"]; ok {
		if s, ok := value.(string); ok {
			v, ok := new(big.Int).SetString(s, 0)
			if !ok {
				return nil, fmt.Errorf("invalid value: %s", s)
			}
			msg.Value = v
		}
	}
	if gas, ok := args["gas"]; ok {
		var userGas uint64
		switch v := gas.(type) {
		case string:
			g, err := hexutil.DecodeUint64(v)
			if err != nil {
				return nil, fmt.Errorf("invalid gas: %w", err)
			}
			userGas = g
		case float64:
			userGas = uint64(v)
		}
		if userGas > gasCap {
			log.Warn("Capping CallUBT gas to RPCGasCap", "requested", userGas, "cap", gasCap)
			userGas = gasCap
		}
		msg.GasLimit = userGas
	}

	return msg, nil
}

// QueryServer wraps an RPC server for UBT queries.
type QueryServer struct {
	server   *rpc.Server
	listener net.Listener
	httpSrv  *http.Server
}

// NewQueryServer creates and starts a query RPC server.
func NewQueryServer(listenAddr string, consumer *Consumer) (*QueryServer, error) {
	server := rpc.NewServer()
	api := NewQueryAPI(consumer)
	if err := server.RegisterName("ubt", api); err != nil {
		return nil, fmt.Errorf("failed to register ubt query API: %w", err)
	}

	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to listen on %s: %w", listenAddr, err)
	}

	httpSrv := &http.Server{Handler: server}

	go func() {
		if err := httpSrv.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Error("UBT query server error", "err", err)
		}
	}()

	log.Info("UBT query server started", "addr", listener.Addr())

	return &QueryServer{
		server:   server,
		listener: listener,
		httpSrv:  httpSrv,
	}, nil
}

// Close stops the query server.
func (s *QueryServer) Close() error {
	return s.httpSrv.Close()
}
