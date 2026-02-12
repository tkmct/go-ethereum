// Copyright 2024 The go-ethereum Authors
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
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/rlp"
)

// UBTSlotIndexEntry tracks when a storage slot was created and last modified.
type UBTSlotIndexEntry struct {
	BlockCreated      uint64
	BlockLastModified uint64
}

// UBTSlotIndexMeta holds aggregate metadata about the slot index.
type UBTSlotIndexMeta struct {
	EntryCount    uint64
	ByteSize      uint64
	Frozen        bool
	FrozenAtBlock uint64
	Pruned        bool
}

var (
	ubtSlotIndexPrefix  = []byte("ubtslotidx")
	ubtSlotIndexMetaKey = []byte("ubtslotidxmeta")
)

// ubtSlotIndexKey constructs the key for a slot index entry: prefix + address + slot.
func ubtSlotIndexKey(addr common.Address, slot common.Hash) []byte {
	key := make([]byte, len(ubtSlotIndexPrefix)+common.AddressLength+common.HashLength)
	copy(key, ubtSlotIndexPrefix)
	copy(key[len(ubtSlotIndexPrefix):], addr.Bytes())
	copy(key[len(ubtSlotIndexPrefix)+common.AddressLength:], slot.Bytes())
	return key
}

// ubtSlotIndexPrefixForAddr returns the prefix for iterating all slots of an address.
func ubtSlotIndexPrefixForAddr(addr common.Address) []byte {
	key := make([]byte, len(ubtSlotIndexPrefix)+common.AddressLength)
	copy(key, ubtSlotIndexPrefix)
	copy(key[len(ubtSlotIndexPrefix):], addr.Bytes())
	return key
}

// WriteUBTSlotIndex writes a slot index entry for the given address and slot.
func WriteUBTSlotIndex(db ethdb.KeyValueWriter, addr common.Address, slot common.Hash, entry *UBTSlotIndexEntry) {
	data, err := rlp.EncodeToBytes(entry)
	if err != nil {
		log.Crit("Failed to RLP encode UBT slot index entry", "err", err)
	}
	key := ubtSlotIndexKey(addr, slot)
	if err := db.Put(key, data); err != nil {
		log.Crit("Failed to write UBT slot index entry", "addr", addr, "slot", slot, "err", err)
	}
}

// ReadUBTSlotIndex reads a slot index entry for the given address and slot.
func ReadUBTSlotIndex(db ethdb.KeyValueReader, addr common.Address, slot common.Hash) *UBTSlotIndexEntry {
	key := ubtSlotIndexKey(addr, slot)
	data, err := db.Get(key)
	if err != nil {
		return nil
	}
	var entry UBTSlotIndexEntry
	if err := rlp.DecodeBytes(data, &entry); err != nil {
		log.Error("Failed to decode UBT slot index entry", "addr", addr, "slot", slot, "err", err)
		return nil
	}
	return &entry
}

// DeleteUBTSlotIndex deletes a slot index entry.
func DeleteUBTSlotIndex(db ethdb.KeyValueWriter, addr common.Address, slot common.Hash) {
	key := ubtSlotIndexKey(addr, slot)
	if err := db.Delete(key); err != nil {
		log.Crit("Failed to delete UBT slot index entry", "addr", addr, "slot", slot, "err", err)
	}
}

// DeleteUBTSlotIndexForAccount deletes all slot index entries for an address.
// Returns the number of deleted entries.
func DeleteUBTSlotIndexForAccount(db ethdb.KeyValueStore, addr common.Address) int {
	prefix := ubtSlotIndexPrefixForAddr(addr)
	it := db.NewIterator(prefix, nil)
	defer it.Release()

	count := 0
	batch := db.NewBatch()
	for it.Next() {
		if err := batch.Delete(it.Key()); err != nil {
			log.Error("Failed to delete UBT slot index entry in batch", "err", err)
			break
		}
		count++
		if count%1000 == 0 {
			if err := batch.Write(); err != nil {
				log.Error("Failed to write slot index delete batch", "err", err)
				return count
			}
			batch.Reset()
		}
	}
	if batch.ValueSize() > 0 {
		if err := batch.Write(); err != nil {
			log.Error("Failed to write final slot index delete batch", "err", err)
		}
	}
	return count
}

// WriteUBTSlotIndexMeta writes the slot index metadata.
func WriteUBTSlotIndexMeta(db ethdb.KeyValueWriter, meta *UBTSlotIndexMeta) {
	data, err := rlp.EncodeToBytes(meta)
	if err != nil {
		log.Crit("Failed to RLP encode UBT slot index meta", "err", err)
	}
	if err := db.Put(ubtSlotIndexMetaKey, data); err != nil {
		log.Crit("Failed to write UBT slot index meta", "err", err)
	}
}

// ReadUBTSlotIndexMeta reads the slot index metadata.
func ReadUBTSlotIndexMeta(db ethdb.KeyValueReader) *UBTSlotIndexMeta {
	data, err := db.Get(ubtSlotIndexMetaKey)
	if err != nil {
		return nil
	}
	var meta UBTSlotIndexMeta
	if err := rlp.DecodeBytes(data, &meta); err != nil {
		log.Error("Failed to decode UBT slot index meta", "err", err)
		return nil
	}
	return &meta
}
