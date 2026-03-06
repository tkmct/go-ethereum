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

package rawdb

import (
	"encoding/binary"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/rlp"
)

// ubtCurrentRoot is the RLP-serializable structure for the UBT current root metadata.
type ubtCurrentRoot struct {
	Root      common.Hash
	Block     uint64
	BlockHash common.Hash
}

// ReadUBTCurrentRoot retrieves the current UBT root, block number, and block hash.
func ReadUBTCurrentRoot(db ethdb.KeyValueReader) (common.Hash, uint64, common.Hash, bool) {
	data, _ := db.Get(UBTCurrentRootKey)
	if len(data) == 0 {
		return common.Hash{}, 0, common.Hash{}, false
	}
	var entry ubtCurrentRoot
	if err := rlp.DecodeBytes(data, &entry); err != nil {
		log.Error("Failed to decode UBT current root", "err", err)
		return common.Hash{}, 0, common.Hash{}, false
	}
	return entry.Root, entry.Block, entry.BlockHash, true
}

// WriteUBTCurrentRoot stores the current UBT root, block number, and block hash.
func WriteUBTCurrentRoot(db ethdb.KeyValueWriter, root common.Hash, block uint64, blockHash common.Hash) {
	data, err := rlp.EncodeToBytes(&ubtCurrentRoot{Root: root, Block: block, BlockHash: blockHash})
	if err != nil {
		log.Crit("Failed to encode UBT current root", "err", err)
	}
	if err := db.Put(UBTCurrentRootKey, data); err != nil {
		log.Crit("Failed to store UBT current root", "err", err)
	}
}

// DeleteUBTCurrentRoot removes the current UBT root metadata.
func DeleteUBTCurrentRoot(db ethdb.KeyValueWriter) {
	if err := db.Delete(UBTCurrentRootKey); err != nil {
		log.Crit("Failed to delete UBT current root", "err", err)
	}
}

// UBTConversionProgress tracks the progress of MPT to UBT conversion.
type UBTConversionProgress struct {
	Root                    common.Hash // MPT root at conversion start
	Block                   uint64      // Block number at conversion start
	BlockHash               common.Hash // Block hash at conversion start
	LastCommittedRoot       common.Hash // Last committed UBT root for resume
	Started                 uint64      // Start time (unix)
	LastProcessedAccountKey common.Hash // Iterator resume position
	ProcessedAccounts       uint64      // Number of processed accounts
}

// ReadUBTConversionProgress retrieves the UBT conversion progress.
func ReadUBTConversionProgress(db ethdb.KeyValueReader) *UBTConversionProgress {
	data, _ := db.Get(UBTConversionProgressKey)
	if len(data) == 0 {
		return nil
	}
	var entry UBTConversionProgress
	if err := rlp.DecodeBytes(data, &entry); err != nil {
		log.Error("Failed to decode UBT conversion progress", "err", err)
		return nil
	}
	return &entry
}

// WriteUBTConversionProgress stores the UBT conversion progress.
func WriteUBTConversionProgress(db ethdb.KeyValueWriter, entry *UBTConversionProgress) {
	data, err := rlp.EncodeToBytes(entry)
	if err != nil {
		log.Crit("Failed to encode UBT conversion progress", "err", err)
	}
	if err := db.Put(UBTConversionProgressKey, data); err != nil {
		log.Crit("Failed to store UBT conversion progress", "err", err)
	}
}

// DeleteUBTConversionProgress removes the UBT conversion progress.
func DeleteUBTConversionProgress(db ethdb.KeyValueWriter) {
	if err := db.Delete(UBTConversionProgressKey); err != nil {
		log.Crit("Failed to delete UBT conversion progress", "err", err)
	}
}

// ReadUBTBlockRoot retrieves the UBT root hash associated with a block hash.
func ReadUBTBlockRoot(db ethdb.KeyValueReader, blockHash common.Hash) (common.Hash, bool) {
	data, _ := db.Get(append(UBTBlockRootPrefix, blockHash.Bytes()...))
	if len(data) != common.HashLength {
		return common.Hash{}, false
	}
	return common.BytesToHash(data), true
}

// WriteUBTBlockRoot stores the UBT root hash associated with a block hash.
func WriteUBTBlockRoot(db ethdb.KeyValueWriter, blockHash common.Hash, root common.Hash) {
	if err := db.Put(append(UBTBlockRootPrefix, blockHash.Bytes()...), root.Bytes()); err != nil {
		log.Crit("Failed to store UBT block root", "err", err)
	}
}

// ubtUpdateQueueMeta is the RLP-serializable structure for queue metadata.
type ubtUpdateQueueMeta struct {
	Start uint64
	End   uint64
}

// ReadUBTUpdateQueueMeta retrieves the UBT update queue metadata.
func ReadUBTUpdateQueueMeta(db ethdb.KeyValueReader) (start, end uint64, ok bool) {
	data, _ := db.Get(UBTUpdateQueueMetaKey)
	if len(data) == 0 {
		return 0, 0, false
	}
	var entry ubtUpdateQueueMeta
	if err := rlp.DecodeBytes(data, &entry); err != nil {
		log.Error("Failed to decode UBT update queue meta", "err", err)
		return 0, 0, false
	}
	return entry.Start, entry.End, true
}

// WriteUBTUpdateQueueMeta stores the UBT update queue metadata.
func WriteUBTUpdateQueueMeta(db ethdb.KeyValueWriter, start, end uint64) {
	data, err := rlp.EncodeToBytes(&ubtUpdateQueueMeta{Start: start, End: end})
	if err != nil {
		log.Crit("Failed to encode UBT update queue meta", "err", err)
	}
	if err := db.Put(UBTUpdateQueueMetaKey, data); err != nil {
		log.Crit("Failed to store UBT update queue meta", "err", err)
	}
}

// DeleteUBTUpdateQueueMeta removes the UBT update queue metadata.
func DeleteUBTUpdateQueueMeta(db ethdb.KeyValueWriter) {
	if err := db.Delete(UBTUpdateQueueMetaKey); err != nil {
		log.Crit("Failed to delete UBT update queue meta", "err", err)
	}
}

// ubtUpdateQueueEntryKey constructs the key for a queue entry.
func ubtUpdateQueueEntryKey(blockNum uint64, blockHash common.Hash) []byte {
	key := make([]byte, len(UBTUpdateQueuePrefix)+8+common.HashLength)
	copy(key, UBTUpdateQueuePrefix)
	binary.BigEndian.PutUint64(key[len(UBTUpdateQueuePrefix):], blockNum)
	copy(key[len(UBTUpdateQueuePrefix)+8:], blockHash.Bytes())
	return key
}

// ReadUBTUpdateQueueEntry retrieves a serialized UBT update from the queue.
func ReadUBTUpdateQueueEntry(db ethdb.KeyValueReader, blockNum uint64, blockHash common.Hash) []byte {
	data, _ := db.Get(ubtUpdateQueueEntryKey(blockNum, blockHash))
	return data
}

// WriteUBTUpdateQueueEntry stores a serialized UBT update in the queue.
func WriteUBTUpdateQueueEntry(db ethdb.KeyValueWriter, blockNum uint64, blockHash common.Hash, data []byte) {
	if err := db.Put(ubtUpdateQueueEntryKey(blockNum, blockHash), data); err != nil {
		log.Crit("Failed to store UBT update queue entry", "err", err)
	}
}

// DeleteUBTUpdateQueueEntry removes a serialized UBT update from the queue.
func DeleteUBTUpdateQueueEntry(db ethdb.KeyValueWriter, blockNum uint64, blockHash common.Hash) {
	if err := db.Delete(ubtUpdateQueueEntryKey(blockNum, blockHash)); err != nil {
		log.Crit("Failed to delete UBT update queue entry", "err", err)
	}
}
