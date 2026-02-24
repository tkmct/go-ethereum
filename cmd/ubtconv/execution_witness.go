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
	"fmt"
	"math/big"
	"sort"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/ubtemit"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/ethereum/go-ethereum/trie/bintrie"
)

const executionWitnessFormatV1 uint16 = 1

type executionWitnessProofNode struct {
	Hash common.Hash
	Data []byte
}

type executionWitnessStorageTouch struct {
	Address common.Address
	Slot    common.Hash
}

type executionWitnessAccountEntry struct {
	Address  common.Address
	Key      common.Hash
	Nonce    uint64
	Balance  *big.Int
	CodeHash common.Hash
	Alive    bool
	Proof    []executionWitnessProofNode
}

type executionWitnessStorageEntry struct {
	Address common.Address
	Slot    common.Hash
	Key     common.Hash
	Value   common.Hash
	Proof   []executionWitnessProofNode
}

type executionWitnessCodeEntry struct {
	Address  common.Address
	CodeHash common.Hash
	Code     []byte
}

type executionWitnessPackV1 struct {
	Version         uint16
	BlockNumber     uint64
	BlockHash       common.Hash
	ParentHash      common.Hash
	ParentStateRoot common.Hash
	StateRoot       common.Hash
	AccountsTouched []common.Address
	StorageTouched  []executionWitnessStorageTouch
	CodeTouched     []common.Address
	Accounts        []executionWitnessAccountEntry
	Storage         []executionWitnessStorageEntry
	Codes           []executionWitnessCodeEntry
}

func buildExecutionWitnessPack(applier *Applier, diff *ubtemit.QueuedDiffV1, blockNumber uint64, blockHash, parentHash, parentStateRoot, stateRoot common.Hash) (*executionWitnessPackV1, error) {
	if applier == nil {
		return nil, fmt.Errorf("execution witness build: applier is nil")
	}
	if diff == nil {
		return nil, fmt.Errorf("execution witness build: diff is nil")
	}
	if stateRoot == (common.Hash{}) {
		return nil, fmt.Errorf("execution witness build: state root is zero")
	}
	accounts := coalesceAccountEntries(diff.Accounts)
	storage := coalesceStorageEntries(diff.Storage)
	codes := coalesceCodeEntries(diff.Codes)

	pack := &executionWitnessPackV1{
		Version:         executionWitnessFormatV1,
		BlockNumber:     blockNumber,
		BlockHash:       blockHash,
		ParentHash:      parentHash,
		ParentStateRoot: parentStateRoot,
		StateRoot:       stateRoot,
		AccountsTouched: make([]common.Address, 0, len(accounts)),
		StorageTouched:  make([]executionWitnessStorageTouch, 0, len(storage)),
		CodeTouched:     make([]common.Address, 0, len(codes)),
		Accounts:        make([]executionWitnessAccountEntry, 0, len(accounts)),
		Storage:         make([]executionWitnessStorageEntry, 0, len(storage)),
		Codes:           make([]executionWitnessCodeEntry, 0, len(codes)),
	}

	for _, acct := range accounts {
		key := common.BytesToHash(bintrie.GetBinaryTreeKeyBasicData(acct.Address))
		proofMap, err := applier.GenerateProofAt(stateRoot, key.Bytes())
		if err != nil {
			return nil, fmt.Errorf("execution witness account proof %s: %w", acct.Address, err)
		}
		balance := new(big.Int)
		if acct.Balance != nil {
			balance.Set(acct.Balance)
		}
		pack.AccountsTouched = append(pack.AccountsTouched, acct.Address)
		pack.Accounts = append(pack.Accounts, executionWitnessAccountEntry{
			Address:  acct.Address,
			Key:      key,
			Nonce:    acct.Nonce,
			Balance:  balance,
			CodeHash: acct.CodeHash,
			Alive:    acct.Alive,
			Proof:    proofMapToSortedNodes(proofMap),
		})
	}

	for _, slot := range storage {
		key := common.BytesToHash(bintrie.GetBinaryTreeKeyStorageSlot(slot.Address, slot.SlotKeyRaw.Bytes()))
		proofMap, err := applier.GenerateProofAt(stateRoot, key.Bytes())
		if err != nil {
			return nil, fmt.Errorf("execution witness storage proof %s/%s: %w", slot.Address, slot.SlotKeyRaw, err)
		}
		pack.StorageTouched = append(pack.StorageTouched, executionWitnessStorageTouch{Address: slot.Address, Slot: slot.SlotKeyRaw})
		pack.Storage = append(pack.Storage, executionWitnessStorageEntry{
			Address: slot.Address,
			Slot:    slot.SlotKeyRaw,
			Key:     key,
			Value:   slot.Value,
			Proof:   proofMapToSortedNodes(proofMap),
		})
	}

	for _, code := range codes {
		copied := append([]byte(nil), code.Code...)
		pack.CodeTouched = append(pack.CodeTouched, code.Address)
		pack.Codes = append(pack.Codes, executionWitnessCodeEntry{
			Address:  code.Address,
			CodeHash: code.CodeHash,
			Code:     copied,
		})
	}

	sort.Slice(pack.AccountsTouched, func(i, j int) bool {
		return bytes.Compare(pack.AccountsTouched[i][:], pack.AccountsTouched[j][:]) < 0
	})
	sort.Slice(pack.StorageTouched, func(i, j int) bool {
		cmp := bytes.Compare(pack.StorageTouched[i].Address[:], pack.StorageTouched[j].Address[:])
		if cmp != 0 {
			return cmp < 0
		}
		return bytes.Compare(pack.StorageTouched[i].Slot[:], pack.StorageTouched[j].Slot[:]) < 0
	})
	sort.Slice(pack.CodeTouched, func(i, j int) bool {
		return bytes.Compare(pack.CodeTouched[i][:], pack.CodeTouched[j][:]) < 0
	})

	return pack, nil
}

func encodeExecutionWitnessPack(pack *executionWitnessPackV1) ([]byte, error) {
	if pack == nil {
		return nil, fmt.Errorf("execution witness encode: nil pack")
	}
	return rlp.EncodeToBytes(pack)
}

func decodeExecutionWitnessPack(blob []byte) (*executionWitnessPackV1, error) {
	if len(blob) == 0 {
		return nil, fmt.Errorf("execution witness decode: empty blob")
	}
	var pack executionWitnessPackV1
	if err := rlp.DecodeBytes(blob, &pack); err != nil {
		return nil, fmt.Errorf("execution witness decode: %w", err)
	}
	return &pack, nil
}

func witnessMetaFromPack(pack *executionWitnessPackV1, blobSize int) *rawdb.UBTExecutionWitnessMeta {
	if pack == nil {
		return nil
	}
	return &rawdb.UBTExecutionWitnessMeta{
		FormatVersion:   pack.Version,
		BlockNumber:     pack.BlockNumber,
		BlockHash:       pack.BlockHash,
		ParentHash:      pack.ParentHash,
		ParentStateRoot: pack.ParentStateRoot,
		StateRoot:       pack.StateRoot,
		AccountsCount:   uint32(len(pack.Accounts)),
		StorageCount:    uint32(len(pack.Storage)),
		CodeCount:       uint32(len(pack.Codes)),
		BlobSize:        uint32(blobSize),
	}
}

func proofMapToSortedNodes(proofMap map[common.Hash][]byte) []executionWitnessProofNode {
	nodes := make([]executionWitnessProofNode, 0, len(proofMap))
	for hash, data := range proofMap {
		nodes = append(nodes, executionWitnessProofNode{Hash: hash, Data: append([]byte(nil), data...)})
	}
	sort.Slice(nodes, func(i, j int) bool {
		return bytes.Compare(nodes[i].Hash[:], nodes[j].Hash[:]) < 0
	})
	return nodes
}

func proofNodesToHexMap(nodes []executionWitnessProofNode) map[common.Hash]hexutil.Bytes {
	out := make(map[common.Hash]hexutil.Bytes, len(nodes))
	for _, node := range nodes {
		out[node.Hash] = hexutil.Bytes(node.Data)
	}
	return out
}
