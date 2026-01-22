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
	"errors"
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/rlp"
)

type ubtQueueKey struct {
	blockNum  uint64
	blockHash common.Hash
}

// EnqueueUpdate stores the update for replay while converting.
func (sc *UBTSidecar) EnqueueUpdate(block *types.Block, update *state.StateUpdate) error {
	if block == nil || update == nil {
		return nil
	}
	ubtUpdate := NewUBTUpdate(block, update)
	blob, err := rlp.EncodeToBytes(ubtUpdate)
	if err != nil {
		return sc.fail("encode update", err)
	}
	sc.queueMu.Lock()
	defer sc.queueMu.Unlock()
	if !sc.Converting() {
		return nil
	}
	rawdb.WriteUBTUpdateQueueEntry(sc.chainDB, ubtUpdate.BlockNum, ubtUpdate.BlockHash, blob)
	meta := rawdb.ReadUBTUpdateQueueMeta(sc.chainDB)
	if meta == nil {
		meta = &rawdb.UBTUpdateQueueMeta{}
	}
	meta.Count++
	if meta.Oldest == 0 || ubtUpdate.BlockNum < meta.Oldest {
		meta.Oldest = ubtUpdate.BlockNum
	}
	if ubtUpdate.BlockNum > meta.Newest {
		meta.Newest = ubtUpdate.BlockNum
	}
	rawdb.WriteUBTUpdateQueueMeta(sc.chainDB, meta)

	if sc.queueLimit > 0 && meta.Count > sc.queueLimit {
		return sc.fail("update queue overflow", fmt.Errorf("queued=%d limit=%d", meta.Count, sc.queueLimit))
	}
	return nil
}

func (sc *UBTSidecar) replayQueuedUpdates() error {
	for {
		keys, err := sc.snapshotQueueKeys()
		if err != nil {
			return err
		}
		if len(keys) == 0 {
			if sc.queueCount() == 0 {
				return nil
			}
			continue
		}
		var (
			appliedAny bool
			removedAny bool
		)
		for _, key := range keys {
			data := rawdb.ReadUBTUpdateQueueEntry(sc.chainDB, key.blockNum, key.blockHash)
			if len(data) == 0 {
				removedAny = true
				continue
			}
			var update UBTUpdate
			if err = rlp.DecodeBytes(data, &update); err != nil {
				return err
			}
			canonical := rawdb.ReadCanonicalHash(sc.chainDB, key.blockNum)
			if canonical != key.blockHash {
				sc.deleteQueueEntry(key.blockNum, key.blockHash)
				removedAny = true
				continue
			}
			sc.mu.RLock()
			parentHash := sc.currentHash
			sc.mu.RUnlock()
			if update.ParentHash != parentHash {
				return sc.fail("queue parent mismatch", fmt.Errorf("block %d", key.blockNum))
			}
			if err = sc.applyUBTUpdate(&update); err != nil {
				return err
			}
			sc.deleteQueueEntry(key.blockNum, key.blockHash)
			appliedAny = true
		}
		if !appliedAny {
			if sc.queueCount() == 0 {
				return nil
			}
			if !removedAny {
				return sc.fail("queue stalled", errors.New("no applicable updates"))
			}
		}
	}
}

func (sc *UBTSidecar) snapshotQueueKeys() ([]ubtQueueKey, error) {
	sc.queueMu.Lock()
	defer sc.queueMu.Unlock()
	iter := rawdb.IterateUBTUpdateQueue(sc.chainDB)
	keys := make([]ubtQueueKey, 0)
	for iter.Next() {
		blockNum, blockHash, ok := rawdb.ParseUBTUpdateQueueKey(iter.Key())
		if ok {
			keys = append(keys, ubtQueueKey{blockNum: blockNum, blockHash: blockHash})
		}
	}
	iter.Release()
	if err := iter.Error(); err != nil {
		return nil, err
	}
	return keys, nil
}

func (sc *UBTSidecar) queueCount() uint64 {
	sc.queueMu.Lock()
	defer sc.queueMu.Unlock()
	meta := rawdb.ReadUBTUpdateQueueMeta(sc.chainDB)
	if meta == nil {
		return 0
	}
	return meta.Count
}

func (sc *UBTSidecar) deleteQueueEntry(blockNum uint64, blockHash common.Hash) {
	sc.queueMu.Lock()
	defer sc.queueMu.Unlock()
	rawdb.DeleteUBTUpdateQueueEntry(sc.chainDB, blockNum, blockHash)
	sc.updateQueueMetaLocked(blockNum)
}

func (sc *UBTSidecar) updateQueueMetaLocked(removedNum uint64) {
	meta := rawdb.ReadUBTUpdateQueueMeta(sc.chainDB)
	if meta == nil {
		return
	}
	if meta.Count > 0 {
		meta.Count--
	}
	if meta.Count == 0 {
		meta.Oldest = 0
		meta.Newest = 0
		rawdb.WriteUBTUpdateQueueMeta(sc.chainDB, meta)
		return
	}
	if removedNum == meta.Oldest || removedNum == meta.Newest {
		meta.Oldest, meta.Newest = sc.scanQueueBoundsLocked()
	}
	rawdb.WriteUBTUpdateQueueMeta(sc.chainDB, meta)
}

func (sc *UBTSidecar) scanQueueBoundsLocked() (uint64, uint64) {
	iter := rawdb.IterateUBTUpdateQueue(sc.chainDB)
	var (
		oldest uint64
		newest uint64
	)
	for iter.Next() {
		blockNum, _, ok := rawdb.ParseUBTUpdateQueueKey(iter.Key())
		if !ok {
			continue
		}
		if oldest == 0 || blockNum < oldest {
			oldest = blockNum
		}
		if blockNum > newest {
			newest = blockNum
		}
	}
	iter.Release()
	if err := iter.Error(); err != nil {
		log.Warn("Failed to scan UBT update queue bounds", "err", err)
		return 0, 0
	}
	return oldest, newest
}

func (sc *UBTSidecar) resetQueueLocked() error {
	iter := rawdb.IterateUBTUpdateQueue(sc.chainDB)
	for iter.Next() {
		blockNum, blockHash, ok := rawdb.ParseUBTUpdateQueueKey(iter.Key())
		if ok {
			rawdb.DeleteUBTUpdateQueueEntry(sc.chainDB, blockNum, blockHash)
		}
	}
	iter.Release()
	if err := iter.Error(); err != nil {
		return err
	}
	rawdb.DeleteUBTUpdateQueueMeta(sc.chainDB)
	return nil
}
