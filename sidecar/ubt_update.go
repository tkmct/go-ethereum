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

package sidecar

import (
	"sort"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rlp"
)

// UBTUpdate contains the state changes needed to update a UBT trie for a single block.
type UBTUpdate struct {
	BlockNum      uint64
	BlockHash     common.Hash
	ParentHash    common.Hash
	RawStorageKey bool

	// Accounts maps account hash to slim-RLP encoded account data (nil = deleted).
	Accounts map[common.Hash][]byte
	// AccountsOrigin maps address to original slim-RLP encoded account data.
	AccountsOrigin map[common.Address][]byte
	// Storages maps account hash -> slot hash -> slot value.
	Storages map[common.Hash]map[common.Hash][]byte
	// StoragesOrigin maps address -> slot key -> original slot value.
	StoragesOrigin map[common.Address]map[common.Hash][]byte
	// Codes maps address to contract bytecode.
	Codes map[common.Address][]byte
}

// NewUBTUpdate creates a UBTUpdate from a block and its corresponding StateUpdate.
func NewUBTUpdate(block *types.Block, update *state.StateUpdate) *UBTUpdate {
	codes := make(map[common.Address][]byte)
	for addr, c := range update.Codes() {
		codes[addr] = c.Blob
	}
	return &UBTUpdate{
		BlockNum:       block.NumberU64(),
		BlockHash:      block.Hash(),
		ParentHash:     block.ParentHash(),
		RawStorageKey:  update.RawStorageKey(),
		Accounts:       update.Accounts(),
		AccountsOrigin: update.AccountsOrigin(),
		Storages:       update.Storages(),
		StoragesOrigin: update.StoragesOrigin(),
		Codes:          codes,
	}
}

// rlpUBTUpdate is the RLP-serializable form of UBTUpdate with sorted entries.
type rlpUBTUpdate struct {
	BlockNum      uint64
	BlockHash     common.Hash
	ParentHash    common.Hash
	RawStorageKey bool
	Accounts      []rlpHashEntry
	AccountsOrig  []rlpAddrEntry
	Storages      []rlpHashMapEntry
	StoragesOrig  []rlpAddrMapEntry
	Codes         []rlpAddrEntry
}

type rlpHashEntry struct {
	Key   common.Hash
	Value []byte
}

type rlpAddrEntry struct {
	Key   common.Address
	Value []byte
}

type rlpHashMapEntry struct {
	Key    common.Hash
	Values []rlpHashEntry
}

type rlpAddrMapEntry struct {
	Key    common.Address
	Values []rlpHashEntry
}

// EncodeRLP serializes UBTUpdate to RLP with deterministic key ordering.
func (u *UBTUpdate) EncodeRLP() ([]byte, error) {
	enc := rlpUBTUpdate{
		BlockNum:      u.BlockNum,
		BlockHash:     u.BlockHash,
		ParentHash:    u.ParentHash,
		RawStorageKey: u.RawStorageKey,
	}
	// Accounts
	enc.Accounts = make([]rlpHashEntry, 0, len(u.Accounts))
	for k, v := range u.Accounts {
		enc.Accounts = append(enc.Accounts, rlpHashEntry{k, v})
	}
	sort.Slice(enc.Accounts, func(i, j int) bool {
		return enc.Accounts[i].Key.Cmp(enc.Accounts[j].Key) < 0
	})
	// AccountsOrigin
	enc.AccountsOrig = make([]rlpAddrEntry, 0, len(u.AccountsOrigin))
	for k, v := range u.AccountsOrigin {
		enc.AccountsOrig = append(enc.AccountsOrig, rlpAddrEntry{k, v})
	}
	sort.Slice(enc.AccountsOrig, func(i, j int) bool {
		return enc.AccountsOrig[i].Key.Cmp(enc.AccountsOrig[j].Key) < 0
	})
	// Storages
	enc.Storages = make([]rlpHashMapEntry, 0, len(u.Storages))
	for acctHash, slots := range u.Storages {
		entries := make([]rlpHashEntry, 0, len(slots))
		for k, v := range slots {
			entries = append(entries, rlpHashEntry{k, v})
		}
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].Key.Cmp(entries[j].Key) < 0
		})
		enc.Storages = append(enc.Storages, rlpHashMapEntry{acctHash, entries})
	}
	sort.Slice(enc.Storages, func(i, j int) bool {
		return enc.Storages[i].Key.Cmp(enc.Storages[j].Key) < 0
	})
	// StoragesOrigin
	enc.StoragesOrig = make([]rlpAddrMapEntry, 0, len(u.StoragesOrigin))
	for addr, slots := range u.StoragesOrigin {
		entries := make([]rlpHashEntry, 0, len(slots))
		for k, v := range slots {
			entries = append(entries, rlpHashEntry{k, v})
		}
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].Key.Cmp(entries[j].Key) < 0
		})
		enc.StoragesOrig = append(enc.StoragesOrig, rlpAddrMapEntry{addr, entries})
	}
	sort.Slice(enc.StoragesOrig, func(i, j int) bool {
		return enc.StoragesOrig[i].Key.Cmp(enc.StoragesOrig[j].Key) < 0
	})
	// Codes
	enc.Codes = make([]rlpAddrEntry, 0, len(u.Codes))
	for k, v := range u.Codes {
		enc.Codes = append(enc.Codes, rlpAddrEntry{k, v})
	}
	sort.Slice(enc.Codes, func(i, j int) bool {
		return enc.Codes[i].Key.Cmp(enc.Codes[j].Key) < 0
	})
	return rlp.EncodeToBytes(&enc)
}

// DecodeUBTUpdate deserializes an RLP-encoded UBTUpdate.
func DecodeUBTUpdate(data []byte) (*UBTUpdate, error) {
	var enc rlpUBTUpdate
	if err := rlp.DecodeBytes(data, &enc); err != nil {
		return nil, err
	}
	u := &UBTUpdate{
		BlockNum:       enc.BlockNum,
		BlockHash:      enc.BlockHash,
		ParentHash:     enc.ParentHash,
		RawStorageKey:  enc.RawStorageKey,
		Accounts:       make(map[common.Hash][]byte, len(enc.Accounts)),
		AccountsOrigin: make(map[common.Address][]byte, len(enc.AccountsOrig)),
		Storages:       make(map[common.Hash]map[common.Hash][]byte, len(enc.Storages)),
		StoragesOrigin: make(map[common.Address]map[common.Hash][]byte, len(enc.StoragesOrig)),
		Codes:          make(map[common.Address][]byte, len(enc.Codes)),
	}
	for _, e := range enc.Accounts {
		u.Accounts[e.Key] = e.Value
	}
	for _, e := range enc.AccountsOrig {
		u.AccountsOrigin[e.Key] = e.Value
	}
	for _, e := range enc.Storages {
		slots := make(map[common.Hash][]byte, len(e.Values))
		for _, s := range e.Values {
			slots[s.Key] = s.Value
		}
		u.Storages[e.Key] = slots
	}
	for _, e := range enc.StoragesOrig {
		slots := make(map[common.Hash][]byte, len(e.Values))
		for _, s := range e.Values {
			slots[s.Key] = s.Value
		}
		u.StoragesOrigin[e.Key] = slots
	}
	for _, e := range enc.Codes {
		u.Codes[e.Key] = e.Value
	}
	return u, nil
}
