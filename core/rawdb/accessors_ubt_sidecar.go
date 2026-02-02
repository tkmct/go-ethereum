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
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/rlp"
)

type ubtCurrentRoot struct {
	Root      common.Hash
	Block     uint64
	BlockHash common.Hash
}

// UBTConversionProgress tracks the progress of MPT->UBT conversion.
type UBTConversionProgress struct {
	Root      common.Hash
	Block     uint64
	BlockHash common.Hash
	Started   uint64
}

// UBTUpdateQueueMeta tracks the queued update bounds.
type UBTUpdateQueueMeta struct {
	Start uint64
	End   uint64
}

// ubtBlockRootKey is the prefix for block hash -> UBT root lookup.
func ubtBlockRootKey(blockHash common.Hash) []byte {
	return append(UBTBlockRootPrefix, blockHash.Bytes()...)
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

// ReadUBTCommittedRoot retrieves the last committed UBT root and block info.
func ReadUBTCommittedRoot(db ethdb.KeyValueReader) (root common.Hash, block uint64, hash common.Hash, ok bool) {
	data, _ := db.Get(UBTCommittedRootKey)
	if len(data) == 0 {
		return common.Hash{}, 0, common.Hash{}, false
	}
	var entry ubtCurrentRoot
	if err := rlp.DecodeBytes(data, &entry); err != nil {
		return common.Hash{}, 0, common.Hash{}, false
	}
	return entry.Root, entry.Block, entry.BlockHash, true
}

// WriteUBTCommittedRoot stores the last committed UBT root and block info.
func WriteUBTCommittedRoot(db ethdb.KeyValueWriter, root common.Hash, block uint64, blockHash common.Hash) {
	blob, err := rlp.EncodeToBytes(&ubtCurrentRoot{Root: root, Block: block, BlockHash: blockHash})
	if err != nil {
		log.Crit("Failed to encode UBT committed root", "err", err)
	}
	if err := db.Put(UBTCommittedRootKey, blob); err != nil {
		log.Crit("Failed to store UBT committed root", "err", err)
	}
}

// ReadUBTConversionProgress retrieves the conversion progress metadata.
func ReadUBTConversionProgress(db ethdb.KeyValueReader) (*UBTConversionProgress, error) {
	data, _ := db.Get(UBTConversionProgressKey)
	if len(data) == 0 {
		return nil, nil
	}
	var entry UBTConversionProgress
	if err := rlp.DecodeBytes(data, &entry); err != nil {
		return nil, err
	}
	return &entry, nil
}

// WriteUBTConversionProgress stores conversion progress metadata.
func WriteUBTConversionProgress(db ethdb.KeyValueWriter, entry *UBTConversionProgress) {
	blob, err := rlp.EncodeToBytes(entry)
	if err != nil {
		log.Crit("Failed to encode UBT conversion progress", "err", err)
	}
	if err := db.Put(UBTConversionProgressKey, blob); err != nil {
		log.Crit("Failed to store UBT conversion progress", "err", err)
	}
}

// DeleteUBTConversionProgress deletes conversion progress metadata.
func DeleteUBTConversionProgress(db ethdb.KeyValueWriter) {
	if err := db.Delete(UBTConversionProgressKey); err != nil {
		log.Crit("Failed to delete UBT conversion progress", "err", err)
	}
}

// ReadUBTUpdateQueueMeta retrieves queued update metadata.
func ReadUBTUpdateQueueMeta(db ethdb.KeyValueReader) (*UBTUpdateQueueMeta, error) {
	data, _ := db.Get(UBTUpdateQueueMetaKey)
	if len(data) == 0 {
		return nil, nil
	}
	var entry UBTUpdateQueueMeta
	if err := rlp.DecodeBytes(data, &entry); err != nil {
		return nil, err
	}
	return &entry, nil
}

// WriteUBTUpdateQueueMeta stores queued update metadata.
func WriteUBTUpdateQueueMeta(db ethdb.KeyValueWriter, entry *UBTUpdateQueueMeta) {
	blob, err := rlp.EncodeToBytes(entry)
	if err != nil {
		log.Crit("Failed to encode UBT update queue meta", "err", err)
	}
	if err := db.Put(UBTUpdateQueueMetaKey, blob); err != nil {
		log.Crit("Failed to store UBT update queue meta", "err", err)
	}
}

// DeleteUBTUpdateQueueMeta deletes queued update metadata.
func DeleteUBTUpdateQueueMeta(db ethdb.KeyValueWriter) {
	if err := db.Delete(UBTUpdateQueueMetaKey); err != nil {
		log.Crit("Failed to delete UBT update queue meta", "err", err)
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
