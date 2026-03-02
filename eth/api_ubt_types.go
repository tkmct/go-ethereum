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
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
)

// UBTAccountResult is the response type for debug_ubt_getAccount.
type UBTAccountResult struct {
	Address  common.Address `json:"address"`
	Balance  *hexutil.Big   `json:"balance"`
	Nonce    hexutil.Uint64 `json:"nonce"`
	CodeHash common.Hash    `json:"codeHash"`
	CodeSize hexutil.Uint64 `json:"codeSize"`
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

// UBTProofResult is the response type for debug_ubt_getProof.
type UBTProofResult struct {
	Address          common.Address    `json:"address"`
	AccountProof     []hexutil.Bytes   `json:"accountProof"`
	AccountProofPath []UBTProofNode    `json:"accountProofPath,omitempty"`
	Balance          *hexutil.Big      `json:"balance"`
	CodeHash         common.Hash       `json:"codeHash"`
	Nonce            hexutil.Uint64    `json:"nonce"`
	BlockHash        common.Hash       `json:"blockHash"`
	BlockNumber      hexutil.Uint64    `json:"blockNumber"`
	StorageProof     []UBTStorageProof `json:"storageProof"`
	StateRoot        common.Hash       `json:"stateRoot"`
	UbtRoot          common.Hash       `json:"ubtRoot"`
	ProofRoot        common.Hash       `json:"proofRoot"`
}
