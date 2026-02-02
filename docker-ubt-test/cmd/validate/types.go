package main

import (
	"context"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/stateless"
	"github.com/ethereum/go-ethereum/core/types"
)

// BlockAnchor identifies a stable block for cross-node comparisons.
type BlockAnchor struct {
	Number uint64
	Hash   common.Hash
}

// SamplingConfig controls Phase 2 sampling behavior.
type SamplingConfig struct {
	AccountCount            int
	StorageSlotsPerContract int
	RandomSeed              int64
	BatchSize               int
}

// UBTProofResult is the response type for debug_getUBTProof.
type UBTProofResult struct {
	Address      common.Address    `json:"address"`
	AccountProof []hexutil.Bytes   `json:"accountProof"`
	Balance      *hexutil.Big      `json:"balance"`
	CodeHash     common.Hash       `json:"codeHash"`
	Nonce        hexutil.Uint64    `json:"nonce"`
	StorageHash  common.Hash       `json:"storageHash"`
	StorageProof []UBTStorageProof `json:"storageProof"`
	StateRoot    common.Hash       `json:"stateRoot"`
	UbtRoot      common.Hash       `json:"ubtRoot"`
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

// UBTStorageProof represents a storage proof entry from debug_getUBTProof.
type UBTStorageProof struct {
	Key   common.Hash     `json:"key"`
	Value hexutil.Bytes   `json:"value"`
	Proof []hexutil.Bytes `json:"proof"`
}

// RPCTest represents a Phase 5 RPC consistency test.
type RPCTest struct {
	Name string
	Run  func(ctx context.Context, anchor *BlockAnchor) error
}

// ExtUBTWitness and PathNode reuse the core/stateless definitions.
type ExtUBTWitness = stateless.ExtUBTWitness

type PathNode = stateless.PathNode

// PhaseError annotates errors with a phase number.
type PhaseError struct {
	Phase int
	Err   error
}

func (e *PhaseError) Error() string {
	return "phase " + string(rune('0'+e.Phase)) + ": " + e.Err.Error()
}

// BlockSummary is a minimal block representation for anchor selection.
type BlockSummary struct {
	Number *hexutil.Uint64 `json:"number"`
	Hash   common.Hash     `json:"hash"`
}

// RPCCall is a helper for generic RPC results where full decoding isn't required.
type RPCCall struct {
	Result any
	Err    error
}

// StorageRangeResult mirrors debug_storageRangeAt output.
type StorageRangeResult struct {
	Storage storageMap   `json:"storage"`
	NextKey *common.Hash `json:"nextKey"`
}

type storageMap map[common.Hash]storageEntry

type storageEntry struct {
	Key   *common.Hash `json:"key"`
	Value common.Hash  `json:"value"`
}

// Ensure types that need to be referenced to avoid unused warnings.
var _ = types.Header{}
