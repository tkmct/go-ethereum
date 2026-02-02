// Copyright 2026 The go-ethereum Authors
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
	"bytes"
	"io"
	"sort"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rlp"
)

// UBTUpdate is the serialized state update used for sidecar catch-up.
type UBTUpdate struct {
	BlockNum      uint64
	BlockHash     common.Hash
	ParentHash    common.Hash
	RawStorageKey bool

	Accounts       map[common.Hash][]byte
	AccountsOrigin map[common.Address][]byte
	Storages       map[common.Hash]map[common.Hash][]byte
	StoragesOrigin map[common.Address]map[common.Hash][]byte
	Codes          map[common.Address][]byte
}

// NewUBTUpdate constructs a UBTUpdate from a StateUpdate.
func NewUBTUpdate(block *types.Block, update *state.StateUpdate) *UBTUpdate {
	if block == nil || update == nil {
		return nil
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
		Codes:          update.Codes(),
	}
}

// NewEmptyUBTUpdate constructs a noop update for blocks without state changes.
func NewEmptyUBTUpdate(block *types.Block) *UBTUpdate {
	if block == nil {
		return nil
	}
	return &UBTUpdate{
		BlockNum:   block.NumberU64(),
		BlockHash:  block.Hash(),
		ParentHash: block.ParentHash(),
	}
}

// Empty reports whether the update contains no state mutations.
func (u *UBTUpdate) Empty() bool {
	if u == nil {
		return true
	}
	return len(u.Accounts) == 0 &&
		len(u.AccountsOrigin) == 0 &&
		len(u.Storages) == 0 &&
		len(u.StoragesOrigin) == 0 &&
		len(u.Codes) == 0
}

type ubtAccountKV struct {
	Hash common.Hash
	Data []byte
}

type ubtAccountOriginKV struct {
	Address common.Address
	Data    []byte
}

type ubtSlotKV struct {
	Key   common.Hash
	Value []byte
}

type ubtStorageKV struct {
	AccountHash common.Hash
	Slots       []ubtSlotKV
}

type ubtStorageOriginKV struct {
	Address common.Address
	Slots   []ubtSlotKV
}

type ubtCodeKV struct {
	Address common.Address
	Code    []byte
}

type ubtUpdateRLP struct {
	BlockNum      uint64
	BlockHash     common.Hash
	ParentHash    common.Hash
	RawStorageKey bool

	Accounts       []ubtAccountKV
	AccountsOrigin []ubtAccountOriginKV
	Storages       []ubtStorageKV
	StoragesOrigin []ubtStorageOriginKV
	Codes          []ubtCodeKV
}

// EncodeRLP implements rlp.Encoder.
func (u *UBTUpdate) EncodeRLP(w io.Writer) error {
	enc := ubtUpdateRLP{
		BlockNum:       u.BlockNum,
		BlockHash:      u.BlockHash,
		ParentHash:     u.ParentHash,
		RawStorageKey:  u.RawStorageKey,
		Accounts:       encodeAccounts(u.Accounts),
		AccountsOrigin: encodeAccountsOrigin(u.AccountsOrigin),
		Storages:       encodeStorages(u.Storages),
		StoragesOrigin: encodeStoragesOrigin(u.StoragesOrigin),
		Codes:          encodeCodes(u.Codes),
	}
	return rlp.Encode(w, &enc)
}

// DecodeRLP implements rlp.Decoder.
func (u *UBTUpdate) DecodeRLP(s *rlp.Stream) error {
	var dec ubtUpdateRLP
	if err := s.Decode(&dec); err != nil {
		return err
	}
	u.BlockNum = dec.BlockNum
	u.BlockHash = dec.BlockHash
	u.ParentHash = dec.ParentHash
	u.RawStorageKey = dec.RawStorageKey
	u.Accounts = decodeAccounts(dec.Accounts)
	u.AccountsOrigin = decodeAccountsOrigin(dec.AccountsOrigin)
	u.Storages = decodeStorages(dec.Storages)
	u.StoragesOrigin = decodeStoragesOrigin(dec.StoragesOrigin)
	u.Codes = decodeCodes(dec.Codes)
	return nil
}

func encodeAccounts(accounts map[common.Hash][]byte) []ubtAccountKV {
	if len(accounts) == 0 {
		return nil
	}
	out := make([]ubtAccountKV, 0, len(accounts))
	for hash, data := range accounts {
		out = append(out, ubtAccountKV{Hash: hash, Data: data})
	}
	sort.Slice(out, func(i, j int) bool {
		return bytes.Compare(out[i].Hash[:], out[j].Hash[:]) < 0
	})
	return out
}

func decodeAccounts(accounts []ubtAccountKV) map[common.Hash][]byte {
	if len(accounts) == 0 {
		return nil
	}
	out := make(map[common.Hash][]byte, len(accounts))
	for _, entry := range accounts {
		out[entry.Hash] = entry.Data
	}
	return out
}

func encodeAccountsOrigin(accounts map[common.Address][]byte) []ubtAccountOriginKV {
	if len(accounts) == 0 {
		return nil
	}
	out := make([]ubtAccountOriginKV, 0, len(accounts))
	for addr, data := range accounts {
		out = append(out, ubtAccountOriginKV{Address: addr, Data: data})
	}
	sort.Slice(out, func(i, j int) bool {
		return bytes.Compare(out[i].Address[:], out[j].Address[:]) < 0
	})
	return out
}

func decodeAccountsOrigin(accounts []ubtAccountOriginKV) map[common.Address][]byte {
	if len(accounts) == 0 {
		return nil
	}
	out := make(map[common.Address][]byte, len(accounts))
	for _, entry := range accounts {
		out[entry.Address] = entry.Data
	}
	return out
}

func encodeSlots(slots map[common.Hash][]byte) []ubtSlotKV {
	if len(slots) == 0 {
		return nil
	}
	out := make([]ubtSlotKV, 0, len(slots))
	for key, val := range slots {
		out = append(out, ubtSlotKV{Key: key, Value: val})
	}
	sort.Slice(out, func(i, j int) bool {
		return bytes.Compare(out[i].Key[:], out[j].Key[:]) < 0
	})
	return out
}

func decodeSlots(slots []ubtSlotKV) map[common.Hash][]byte {
	if len(slots) == 0 {
		return nil
	}
	out := make(map[common.Hash][]byte, len(slots))
	for _, entry := range slots {
		out[entry.Key] = entry.Value
	}
	return out
}

func encodeStorages(storages map[common.Hash]map[common.Hash][]byte) []ubtStorageKV {
	if len(storages) == 0 {
		return nil
	}
	out := make([]ubtStorageKV, 0, len(storages))
	for addrHash, slots := range storages {
		out = append(out, ubtStorageKV{AccountHash: addrHash, Slots: encodeSlots(slots)})
	}
	sort.Slice(out, func(i, j int) bool {
		return bytes.Compare(out[i].AccountHash[:], out[j].AccountHash[:]) < 0
	})
	return out
}

func decodeStorages(storages []ubtStorageKV) map[common.Hash]map[common.Hash][]byte {
	if len(storages) == 0 {
		return nil
	}
	out := make(map[common.Hash]map[common.Hash][]byte, len(storages))
	for _, entry := range storages {
		out[entry.AccountHash] = decodeSlots(entry.Slots)
	}
	return out
}

func encodeStoragesOrigin(storages map[common.Address]map[common.Hash][]byte) []ubtStorageOriginKV {
	if len(storages) == 0 {
		return nil
	}
	out := make([]ubtStorageOriginKV, 0, len(storages))
	for addr, slots := range storages {
		out = append(out, ubtStorageOriginKV{Address: addr, Slots: encodeSlots(slots)})
	}
	sort.Slice(out, func(i, j int) bool {
		return bytes.Compare(out[i].Address[:], out[j].Address[:]) < 0
	})
	return out
}

func decodeStoragesOrigin(storages []ubtStorageOriginKV) map[common.Address]map[common.Hash][]byte {
	if len(storages) == 0 {
		return nil
	}
	out := make(map[common.Address]map[common.Hash][]byte, len(storages))
	for _, entry := range storages {
		out[entry.Address] = decodeSlots(entry.Slots)
	}
	return out
}

func encodeCodes(codes map[common.Address][]byte) []ubtCodeKV {
	if len(codes) == 0 {
		return nil
	}
	out := make([]ubtCodeKV, 0, len(codes))
	for addr, code := range codes {
		out = append(out, ubtCodeKV{Address: addr, Code: code})
	}
	sort.Slice(out, func(i, j int) bool {
		return bytes.Compare(out[i].Address[:], out[j].Address[:]) < 0
	})
	return out
}

func decodeCodes(codes []ubtCodeKV) map[common.Address][]byte {
	if len(codes) == 0 {
		return nil
	}
	out := make(map[common.Address][]byte, len(codes))
	for _, entry := range codes {
		out[entry.Address] = entry.Code
	}
	return out
}
