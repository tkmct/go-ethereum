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
