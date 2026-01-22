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
	"bytes"
	"encoding/binary"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/rlp"
)

// UBTConversionProgress represents persistent conversion metadata.
type UBTConversionProgress struct {
	Root      common.Hash
	Block     uint64
	BlockHash common.Hash
}

type ubtCurrentRoot struct {
	Root      common.Hash
	Block     uint64
	BlockHash common.Hash
}

// UBTUpdateQueueMeta tracks the update queue bounds.
type UBTUpdateQueueMeta struct {
	Count  uint64
	Oldest uint64
	Newest uint64
}

// ubtBlockRootKey is the prefix for block hash -> UBT root lookup.
func ubtBlockRootKey(blockHash common.Hash) []byte {
	return append(UBTBlockRootPrefix, blockHash.Bytes()...)
}

// ubtUpdateQueueKey constructs the key for a queued update.
func ubtUpdateQueueKey(blockNum uint64, blockHash common.Hash) []byte {
	return append(append(UBTUpdateQueuePrefix, encodeBlockNumber(blockNum)...), blockHash.Bytes()...)
}

// ParseUBTUpdateQueueKey parses a queued update key into block number and hash.
func ParseUBTUpdateQueueKey(key []byte) (uint64, common.Hash, bool) {
	keyLen := len(UBTUpdateQueuePrefix) + 8 + common.HashLength
	if len(key) != keyLen || !bytes.HasPrefix(key, UBTUpdateQueuePrefix) {
		return 0, common.Hash{}, false
	}
	num := binary.BigEndian.Uint64(key[len(UBTUpdateQueuePrefix) : len(UBTUpdateQueuePrefix)+8])
	var hash common.Hash
	copy(hash[:], key[len(UBTUpdateQueuePrefix)+8:])
	return num, hash, true
}

// ReadUBTCurrentRoot retrieves the current UBT root and block info.
func ReadUBTCurrentRoot(db ethdb.KeyValueReader) (root common.Hash, block uint64, hash common.Hash, ok bool) {
	data, _ := db.Get(UBTCurrentRootKey)
	if len(data) == 0 {
		return common.Hash{}, 0, common.Hash{}, false
	}
	var entry ubtCurrentRoot
	if err := rlp.DecodeBytes(data, &entry); err != nil {
		return common.Hash{}, 0, common.Hash{}, false
	}
	return entry.Root, entry.Block, entry.BlockHash, true
}

// WriteUBTCurrentRoot stores the current UBT root and block info.
func WriteUBTCurrentRoot(db ethdb.KeyValueWriter, root common.Hash, block uint64, blockHash common.Hash) {
	blob, err := rlp.EncodeToBytes(&ubtCurrentRoot{Root: root, Block: block, BlockHash: blockHash})
	if err != nil {
		log.Crit("Failed to encode UBT current root", "err", err)
	}
	if err := db.Put(UBTCurrentRootKey, blob); err != nil {
		log.Crit("Failed to store UBT current root", "err", err)
	}
}

// ReadUBTBlockRoot retrieves the UBT root for the given block hash.
func ReadUBTBlockRoot(db ethdb.KeyValueReader, blockHash common.Hash) (common.Hash, bool) {
	data, _ := db.Get(ubtBlockRootKey(blockHash))
	if len(data) != common.HashLength {
		return common.Hash{}, false
	}
	return common.BytesToHash(data), true
}

// WriteUBTBlockRoot stores the UBT root for the given block hash.
func WriteUBTBlockRoot(db ethdb.KeyValueWriter, blockHash common.Hash, root common.Hash) {
	if err := db.Put(ubtBlockRootKey(blockHash), root.Bytes()); err != nil {
		log.Crit("Failed to store UBT block root", "err", err)
	}
}

// WriteUBTConversionProgress stores conversion progress metadata.
func WriteUBTConversionProgress(db ethdb.KeyValueWriter, p *UBTConversionProgress) {
	blob, err := rlp.EncodeToBytes(p)
	if err != nil {
		log.Crit("Failed to encode UBT conversion progress", "err", err)
	}
	if err := db.Put(UBTConversionProgressKey, blob); err != nil {
		log.Crit("Failed to store UBT conversion progress", "err", err)
	}
}

// ReadUBTConversionProgress retrieves conversion progress metadata.
func ReadUBTConversionProgress(db ethdb.KeyValueReader) *UBTConversionProgress {
	data, _ := db.Get(UBTConversionProgressKey)
	if len(data) == 0 {
		return nil
	}
	var p UBTConversionProgress
	if err := rlp.DecodeBytes(data, &p); err != nil {
		return nil
	}
	return &p
}

// DeleteUBTConversionProgress removes conversion progress metadata.
func DeleteUBTConversionProgress(db ethdb.KeyValueWriter) {
	if err := db.Delete(UBTConversionProgressKey); err != nil {
		log.Crit("Failed to remove UBT conversion progress", "err", err)
	}
}

// ReadUBTUpdateQueueMeta retrieves the update queue metadata.
func ReadUBTUpdateQueueMeta(db ethdb.KeyValueReader) *UBTUpdateQueueMeta {
	data, _ := db.Get(UBTUpdateQueueMetaKey)
	if len(data) == 0 {
		return nil
	}
	var meta UBTUpdateQueueMeta
	if err := rlp.DecodeBytes(data, &meta); err != nil {
		return nil
	}
	return &meta
}

// WriteUBTUpdateQueueMeta stores the update queue metadata.
func WriteUBTUpdateQueueMeta(db ethdb.KeyValueWriter, meta *UBTUpdateQueueMeta) {
	blob, err := rlp.EncodeToBytes(meta)
	if err != nil {
		log.Crit("Failed to encode UBT update queue meta", "err", err)
	}
	if err := db.Put(UBTUpdateQueueMetaKey, blob); err != nil {
		log.Crit("Failed to store UBT update queue meta", "err", err)
	}
}

// DeleteUBTUpdateQueueMeta removes the update queue metadata.
func DeleteUBTUpdateQueueMeta(db ethdb.KeyValueWriter) {
	if err := db.Delete(UBTUpdateQueueMetaKey); err != nil {
		log.Crit("Failed to remove UBT update queue meta", "err", err)
	}
}

// ReadUBTUpdateQueueEntry retrieves a queued update entry.
func ReadUBTUpdateQueueEntry(db ethdb.KeyValueReader, blockNum uint64, blockHash common.Hash) []byte {
	data, _ := db.Get(ubtUpdateQueueKey(blockNum, blockHash))
	return data
}

// WriteUBTUpdateQueueEntry stores a queued update entry.
func WriteUBTUpdateQueueEntry(db ethdb.KeyValueWriter, blockNum uint64, blockHash common.Hash, blob []byte) {
	if err := db.Put(ubtUpdateQueueKey(blockNum, blockHash), blob); err != nil {
		log.Crit("Failed to store UBT update queue entry", "err", err)
	}
}

// DeleteUBTUpdateQueueEntry removes a queued update entry.
func DeleteUBTUpdateQueueEntry(db ethdb.KeyValueWriter, blockNum uint64, blockHash common.Hash) {
	if err := db.Delete(ubtUpdateQueueKey(blockNum, blockHash)); err != nil {
		log.Crit("Failed to delete UBT update queue entry", "err", err)
	}
}

// IterateUBTUpdateQueue returns an iterator over queued updates.
func IterateUBTUpdateQueue(db ethdb.Iteratee) ethdb.Iterator {
	return NewKeyLengthIterator(db.NewIterator(UBTUpdateQueuePrefix, nil), len(UBTUpdateQueuePrefix)+8+common.HashLength)
}
