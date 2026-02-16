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

package rawdb

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/log"
)

var ubtSlotPreimagePrefix = []byte("ubt-slot-preimg-")

func ubtSlotPreimageKey(addr common.Address, slotHash common.Hash) []byte {
	key := make([]byte, len(ubtSlotPreimagePrefix)+common.AddressLength+common.HashLength)
	copy(key, ubtSlotPreimagePrefix)
	copy(key[len(ubtSlotPreimagePrefix):], addr.Bytes())
	copy(key[len(ubtSlotPreimagePrefix)+common.AddressLength:], slotHash.Bytes())
	return key
}

// WriteUBTStorageSlotPreimage writes a mapping from (address, slotHash) to raw slot key.
func WriteUBTStorageSlotPreimage(db ethdb.KeyValueWriter, addr common.Address, slotHash common.Hash, rawSlot common.Hash) {
	if err := db.Put(ubtSlotPreimageKey(addr, slotHash), rawSlot.Bytes()); err != nil {
		log.Crit("Failed to write UBT storage slot preimage", "addr", addr, "slotHash", slotHash, "err", err)
	}
}

// ReadUBTStorageSlotPreimage reads a mapping from (address, slotHash) to raw slot key.
func ReadUBTStorageSlotPreimage(db ethdb.KeyValueReader, addr common.Address, slotHash common.Hash) (common.Hash, bool) {
	blob, err := db.Get(ubtSlotPreimageKey(addr, slotHash))
	if err != nil || len(blob) != common.HashLength {
		return common.Hash{}, false
	}
	raw := common.BytesToHash(blob)
	// Defensive integrity check to detect accidental/corrupt mismatches.
	if crypto.Keccak256Hash(raw.Bytes()) != slotHash {
		log.Error("Invalid UBT storage slot preimage mapping", "addr", addr, "slotHash", slotHash, "rawSlot", raw)
		return common.Hash{}, false
	}
	return raw, true
}

// WriteUBTStorageSlotPreimages writes a set of (address, slotHash)->rawSlot mappings.
// The return value is the number of written mappings.
func WriteUBTStorageSlotPreimages(db ethdb.KeyValueWriter, preimages map[common.Address]map[common.Hash]common.Hash) int {
	var written int
	for addr, slots := range preimages {
		for slotHash, rawSlot := range slots {
			WriteUBTStorageSlotPreimage(db, addr, slotHash, rawSlot)
			written++
		}
	}
	return written
}
