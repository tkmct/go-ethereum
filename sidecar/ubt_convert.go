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
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/ethereum/go-ethereum/trie"
	"github.com/ethereum/go-ethereum/trie/bintrie"
	"github.com/ethereum/go-ethereum/trie/trienode"
	"github.com/ethereum/go-ethereum/triedb"
)

const ubtConversionLogInterval = 200_000

// BeginConversion transitions the sidecar into converting mode.
func (sc *UBTSidecar) BeginConversion(root common.Hash, blockNum uint64, blockHash common.Hash) bool {
	sc.mu.Lock()
	if !sc.enabled || sc.converting {
		sc.mu.Unlock()
		return false
	}
	sc.converting = true
	sc.ready = false
	sc.stale = false
	sc.conversionRoot = root
	sc.conversionBlock = blockNum
	sc.conversionHash = blockHash
	sc.mu.Unlock()

	rawdb.WriteUBTConversionProgress(sc.chainDB, &rawdb.UBTConversionProgress{
		Root:      root,
		Block:     blockNum,
		BlockHash: blockHash,
		Started:   uint64(time.Now().Unix()),
	})
	if err := sc.clearUpdateQueue(); err != nil {
		log.Warn("Failed to clear UBT update queue", "err", err)
	}
	return true
}

// ConvertFromMPT rebuilds the UBT sidecar from the MPT state root.
func (sc *UBTSidecar) ConvertFromMPT(root common.Hash, blockNum uint64, blockHash common.Hash, mptDB state.Database) error {
	if mptDB == nil {
		return sc.fail("convert init", errors.New("missing state database"))
	}
	if !sc.Converting() {
		if !sc.BeginConversion(root, blockNum, blockHash) {
			return errors.New("conversion already running")
		}
	}

	log.Info("Starting UBT sidecar conversion", "block", blockNum, "hash", blockHash)
	tr, err := mptDB.OpenTrie(root)
	if err != nil {
		return sc.fail("open state trie", err)
	}
	bt, err := bintrie.NewBinaryTrie(types.EmptyBinaryHash, sc.triedb)
	if err != nil {
		return sc.fail("open ubt trie", err)
	}
	nodeIt, err := tr.NodeIterator(nil)
	if err != nil {
		return sc.fail("iterate account trie", err)
	}
	it := trie.NewIterator(nodeIt)

	var accounts uint64
	for it.Next() {
		var data types.StateAccount
		if err := rlp.DecodeBytes(it.Value, &data); err != nil {
			return sc.fail("decode account", err)
		}
		addrBytes := rawdb.ReadPreimage(sc.chainDB, common.BytesToHash(it.Key))
		if len(addrBytes) == 0 {
			return sc.fail("address preimage", fmt.Errorf("missing preimage for %x", it.Key))
		}
		if len(addrBytes) != common.AddressLength {
			return sc.fail("address preimage", fmt.Errorf("invalid address preimage length %d", len(addrBytes)))
		}
		addr := common.BytesToAddress(addrBytes)
		codeHash := codeHashFromBytes(data.CodeHash)
		codeLen := 0
		if codeHash != types.EmptyCodeHash {
			code := rawdb.ReadCode(sc.chainDB, codeHash)
			if len(code) == 0 {
				return sc.fail("account code", fmt.Errorf("missing code for %x", codeHash))
			}
			codeLen = len(code)
		}
		if err := bt.UpdateAccount(addr, &data, codeLen); err != nil {
			return sc.fail("update account", err)
		}
		if codeLen > 0 {
			code := rawdb.ReadCode(sc.chainDB, codeHash)
			if err := bt.UpdateContractCode(addr, codeHash, code); err != nil {
				return sc.fail("update code", err)
			}
		}
		if data.Root != types.EmptyRootHash {
			storageTr, err := mptDB.OpenStorageTrie(root, addr, data.Root, tr)
			if err != nil {
				return sc.fail("open storage trie", err)
			}
			storageIt, err := storageTr.NodeIterator(nil)
			if err != nil {
				return sc.fail("iterate storage trie", err)
			}
			stIt := trie.NewIterator(storageIt)
			for stIt.Next() {
				_, content, _, err := rlp.Split(stIt.Value)
				if err != nil {
					return sc.fail("decode storage", err)
				}
				rawKey := rawdb.ReadPreimage(sc.chainDB, common.BytesToHash(stIt.Key))
				if len(rawKey) == 0 {
					return sc.fail("storage preimage", fmt.Errorf("missing storage preimage for %x", stIt.Key))
				}
				if len(rawKey) != common.HashLength {
					return sc.fail("storage preimage", fmt.Errorf("invalid storage preimage length %d", len(rawKey)))
				}
				if len(content) == 0 {
					continue
				}
				if err := bt.UpdateStorage(addr, rawKey, content); err != nil {
					return sc.fail("update storage", err)
				}
			}
			if stIt.Err != nil {
				return sc.fail("iterate storage trie", stIt.Err)
			}
		}
		accounts++
		if accounts%ubtConversionLogInterval == 0 {
			log.Info("UBT conversion progress", "accounts", accounts, "block", blockNum)
		}
	}
	if it.Err != nil {
		return sc.fail("iterate account trie", it.Err)
	}

	newRoot, nodeset := bt.Commit(false)
	merged := trienode.NewWithNodeSet(nodeset)
	if err := sc.triedb.Update(newRoot, types.EmptyBinaryHash, blockNum, merged, triedb.NewStateSet()); err != nil {
		return sc.fail("update ubt trie", err)
	}
	rawdb.WriteUBTCurrentRoot(sc.chainDB, newRoot, blockNum, blockHash)
	rawdb.WriteUBTBlockRoot(sc.chainDB, blockHash, newRoot)
	sc.mu.Lock()
	sc.currentRoot = newRoot
	sc.currentBlock = blockNum
	sc.currentHash = blockHash
	sc.mu.Unlock()

	if err := sc.triedb.Commit(newRoot, false); err != nil {
		return sc.fail("commit ubt trie", err)
	}
	rawdb.WriteUBTCommittedRoot(sc.chainDB, newRoot, blockNum, blockHash)
	sc.mu.Lock()
	sc.lastCommittedBlock = blockNum
	sc.mu.Unlock()

	if err := sc.replayUpdateQueue(); err != nil {
		return sc.fail("replay updates", err)
	}
	rawdb.DeleteUBTConversionProgress(sc.chainDB)
	sc.mu.Lock()
	sc.converting = false
	sc.ready = true
	sc.stale = false
	sc.mu.Unlock()
	log.Info("UBT sidecar conversion completed", "block", blockNum, "hash", blockHash)
	return nil
}

// EnqueueUpdate appends a UBT update to the conversion queue.
func (sc *UBTSidecar) EnqueueUpdate(block *types.Block, update *state.StateUpdate) error {
	if block == nil {
		return errors.New("missing block")
	}
	if !sc.Converting() {
		return nil
	}
	var ubtUpdate *UBTUpdate
	if update == nil {
		ubtUpdate = NewEmptyUBTUpdate(block)
	} else {
		ubtUpdate = NewUBTUpdate(block, update)
	}
	if ubtUpdate == nil {
		return nil
	}
	blob, err := rlp.EncodeToBytes(ubtUpdate)
	if err != nil {
		return err
	}
	key := ubtUpdateQueueKey(ubtUpdate.BlockNum, ubtUpdate.BlockHash)
	if err := sc.chainDB.Put(key, blob); err != nil {
		return err
	}
	return sc.updateQueueMeta(ubtUpdate.BlockNum)
}

func (sc *UBTSidecar) replayUpdateQueue() error {
	for {
		meta, err := rawdb.ReadUBTUpdateQueueMeta(sc.chainDB)
		if err != nil {
			return err
		}
		if meta == nil {
			return nil
		}
		if err := sc.replayQueueUpTo(meta.End); err != nil {
			return err
		}
		if err := sc.recomputeQueueMeta(); err != nil {
			return err
		}
		meta, err = rawdb.ReadUBTUpdateQueueMeta(sc.chainDB)
		if err != nil {
			return err
		}
		if meta == nil {
			return nil
		}
	}
}

func (sc *UBTSidecar) replayQueueUpTo(end uint64) error {
	it := sc.chainDB.NewIterator(rawdb.UBTUpdateQueuePrefix, nil)
	defer it.Release()

	sc.mu.RLock()
	expectedParent := sc.currentHash
	currentBlock := sc.currentBlock
	sc.mu.RUnlock()

	var deletes [][]byte
	for it.Next() {
		blockNum, _, ok := parseUBTUpdateQueueKey(it.Key())
		if !ok {
			continue
		}
		if blockNum > end {
			break
		}
		var update UBTUpdate
		if err := rlp.DecodeBytes(it.Value(), &update); err != nil {
			return err
		}
		// drop stale entries
		if update.BlockNum <= currentBlock {
			deletes = append(deletes, append([]byte{}, it.Key()...))
			continue
		}
		canonical := rawdb.ReadCanonicalHash(sc.chainDB, update.BlockNum)
		if canonical == (common.Hash{}) || canonical != update.BlockHash {
			deletes = append(deletes, append([]byte{}, it.Key()...))
			continue
		}
		if update.ParentHash != expectedParent {
			return fmt.Errorf("ubt update queue gap at block %d", update.BlockNum)
		}
		if update.Empty() {
			sc.applyNoopUpdate(update.BlockNum, update.BlockHash)
		} else {
			if err := sc.applyUBTUpdate(&update); err != nil {
				return err
			}
		}
		if err := sc.maybeCommit(update.BlockNum, update.BlockHash); err != nil {
			return err
		}
		expectedParent = update.BlockHash
		currentBlock = update.BlockNum
		deletes = append(deletes, append([]byte{}, it.Key()...))
	}
	if err := it.Error(); err != nil {
		return err
	}
	if len(deletes) == 0 {
		return nil
	}
	batch := sc.chainDB.NewBatch()
	for _, key := range deletes {
		_ = batch.Delete(key)
	}
	if err := batch.Write(); err != nil {
		return err
	}
	return nil
}

func (sc *UBTSidecar) clearUpdateQueue() error {
	it := sc.chainDB.NewIterator(rawdb.UBTUpdateQueuePrefix, nil)
	defer it.Release()

	batch := sc.chainDB.NewBatch()
	for it.Next() {
		_ = batch.Delete(it.Key())
	}
	if err := it.Error(); err != nil {
		return err
	}
	if err := batch.Write(); err != nil {
		return err
	}
	rawdb.DeleteUBTUpdateQueueMeta(sc.chainDB)
	return nil
}

func (sc *UBTSidecar) updateQueueMeta(blockNum uint64) error {
	meta, err := rawdb.ReadUBTUpdateQueueMeta(sc.chainDB)
	if err != nil {
		return err
	}
	if meta == nil {
		meta = &rawdb.UBTUpdateQueueMeta{Start: blockNum, End: blockNum}
	} else {
		if blockNum < meta.Start {
			meta.Start = blockNum
		}
		if blockNum > meta.End {
			meta.End = blockNum
		}
	}
	rawdb.WriteUBTUpdateQueueMeta(sc.chainDB, meta)
	return nil
}

func (sc *UBTSidecar) recomputeQueueMeta() error {
	it := sc.chainDB.NewIterator(rawdb.UBTUpdateQueuePrefix, nil)
	defer it.Release()

	var (
		start uint64
		end   uint64
		seen  bool
	)
	for it.Next() {
		blockNum, _, ok := parseUBTUpdateQueueKey(it.Key())
		if !ok {
			continue
		}
		if !seen {
			start = blockNum
			end = blockNum
			seen = true
			continue
		}
		if blockNum < start {
			start = blockNum
		}
		if blockNum > end {
			end = blockNum
		}
	}
	if err := it.Error(); err != nil {
		return err
	}
	if !seen {
		rawdb.DeleteUBTUpdateQueueMeta(sc.chainDB)
		return nil
	}
	rawdb.WriteUBTUpdateQueueMeta(sc.chainDB, &rawdb.UBTUpdateQueueMeta{Start: start, End: end})
	return nil
}

func ubtUpdateQueueKey(blockNum uint64, blockHash common.Hash) []byte {
	key := make([]byte, len(rawdb.UBTUpdateQueuePrefix)+8+common.HashLength)
	copy(key, rawdb.UBTUpdateQueuePrefix)
	off := len(rawdb.UBTUpdateQueuePrefix)
	binary.BigEndian.PutUint64(key[off:off+8], blockNum)
	off += 8
	copy(key[off:], blockHash.Bytes())
	return key
}

func parseUBTUpdateQueueKey(key []byte) (uint64, common.Hash, bool) {
	prefix := rawdb.UBTUpdateQueuePrefix
	if !bytes.HasPrefix(key, prefix) {
		return 0, common.Hash{}, false
	}
	need := len(prefix) + 8 + common.HashLength
	if len(key) != need {
		return 0, common.Hash{}, false
	}
	start := len(prefix)
	blockNum := binary.BigEndian.Uint64(key[start : start+8])
	var hash common.Hash
	copy(hash[:], key[start+8:])
	return blockNum, hash, true
}
