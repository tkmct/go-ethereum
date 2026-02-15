// Copyright 2024 The go-ethereum Authors
// This file is part of go-ethereum.
//
// go-ethereum is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// go-ethereum is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with go-ethereum. If not, see <http://www.gnu.org/licenses/>.

package main

import (
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/log"
)

// estimatedSlotEntrySize is the approximate byte size of a single slot index entry.
// Breakdown: key prefix (10) + address (20) + slot hash (32) + RLP entry data ~16 = ~78 bytes.
// Rounded to 72 for a conservative alignment estimate.
const estimatedSlotEntrySize = 72

// SlotIndex tracks storage slot creation/modification metadata for pre-Cancun replay correctness.
type SlotIndex struct {
	db                ethdb.KeyValueStore
	cancunBlock       uint64
	frozen            bool
	meta              *rawdb.UBTSlotIndexMeta
	diskBudget        uint64 // 0 = unlimited
	alertThresholdPct uint64 // percentage threshold for warnings (default: 80)
}

// NewSlotIndex creates a new SlotIndex manager.
func NewSlotIndex(db ethdb.KeyValueStore, cancunBlock uint64, diskBudget uint64, alertThresholdPct uint64) *SlotIndex {
	if alertThresholdPct == 0 {
		alertThresholdPct = 80
	}

	si := &SlotIndex{
		db:                db,
		cancunBlock:       cancunBlock,
		diskBudget:        diskBudget,
		alertThresholdPct: alertThresholdPct,
	}

	// Load existing meta
	meta := rawdb.ReadUBTSlotIndexMeta(db)
	if meta != nil {
		si.meta = meta
		si.frozen = meta.Frozen
	} else {
		si.meta = &rawdb.UBTSlotIndexMeta{}
	}
	return si
}

// ShouldIndex returns whether slot indexing should be active for the given block.
func (si *SlotIndex) ShouldIndex(blockNumber uint64) bool {
	if si.frozen {
		return false
	}
	// Index pre-Cancun blocks only and freeze at the first Cancun block.
	if si.cancunBlock > 0 && blockNumber >= si.cancunBlock {
		si.Freeze(blockNumber)
		return false
	}
	return true
}

// TrackSlot inserts or updates a slot index entry.
func (si *SlotIndex) TrackSlot(addr common.Address, slot common.Hash, blockNumber uint64) error {
	if si.frozen {
		return nil
	}

	// Alert threshold warning (§8 R7)
	if si.diskBudget > 0 && si.alertThresholdPct > 0 {
		alertBytes := si.diskBudget * si.alertThresholdPct / 100
		if si.meta.ByteSize >= alertBytes && si.meta.ByteSize < si.diskBudget {
			log.Warn("Slot index approaching disk budget",
				"used", si.meta.ByteSize, "budget", si.diskBudget,
				"pct", si.meta.ByteSize*100/si.diskBudget)
		}
	}

	// Check disk budget
	if si.diskBudget > 0 && si.meta.ByteSize >= si.diskBudget {
		log.Warn("Slot index disk budget exhausted", "budget", si.diskBudget, "used", si.meta.ByteSize)
		return fmt.Errorf("slot index disk budget exhausted (%d/%d bytes)", si.meta.ByteSize, si.diskBudget)
	}

	existing := rawdb.ReadUBTSlotIndex(si.db, addr, slot)
	if existing != nil {
		existing.BlockLastModified = blockNumber
		rawdb.WriteUBTSlotIndex(si.db, addr, slot, existing)
	} else {
		entry := &rawdb.UBTSlotIndexEntry{
			BlockCreated:      blockNumber,
			BlockLastModified: blockNumber,
		}
		rawdb.WriteUBTSlotIndex(si.db, addr, slot, entry)
		si.meta.EntryCount++
		si.meta.ByteSize += estimatedSlotEntrySize
		si.persistMeta()
	}
	return nil
}

// DeleteSlotsForAccount clears all slot index entries for a deleted account.
func (si *SlotIndex) DeleteSlotsForAccount(addr common.Address) error {
	count := rawdb.DeleteUBTSlotIndexForAccount(si.db, addr)
	if count > 0 && si.meta.EntryCount >= uint64(count) {
		si.meta.EntryCount -= uint64(count)
		si.meta.ByteSize -= uint64(count) * estimatedSlotEntrySize
		si.persistMeta()
	}
	return nil
}

// Freeze freezes the slot index at the given block number (Cancun boundary).
func (si *SlotIndex) Freeze(blockNumber uint64) {
	si.frozen = true
	si.meta.Frozen = true
	si.meta.FrozenAtBlock = blockNumber
	si.persistMeta()
	log.Info("Slot index frozen", "block", blockNumber, "entries", si.meta.EntryCount)
}

// PruneIfSafe removes all slot index entries when the replay window no longer
// overlaps pre-Cancun range. This is safe because once the state history window
// has moved entirely past the Cancun boundary, pre-Cancun replay is no longer
// possible and the slot index entries serve no purpose.
func (si *SlotIndex) PruneIfSafe(currentBlock, stateHistory uint64) {
	if si.meta.Pruned {
		return // Already pruned, skip redundant work
	}
	if !si.frozen || si.meta.EntryCount == 0 {
		return
	}
	if si.cancunBlock == 0 {
		return
	}
	// Safe to prune when the replay window is entirely past Cancun
	if currentBlock > si.cancunBlock+stateHistory {
		prefix := []byte("ubtslotidx")
		it := si.db.NewIterator(prefix, nil)
		defer it.Release()

		batch := si.db.NewBatch()
		count := 0
		for it.Next() {
			if err := batch.Delete(it.Key()); err != nil {
				log.Error("Failed to delete slot index entry during prune", "err", err)
				break
			}
			count++
			if count%1000 == 0 {
				if err := batch.Write(); err != nil {
					log.Error("Failed to write slot index prune batch", "err", err)
					return
				}
				batch.Reset()
			}
		}
		if batch.ValueSize() > 0 {
			if err := batch.Write(); err != nil {
				log.Error("Failed to write final slot index prune batch", "err", err)
				return
			}
		}
		si.meta.EntryCount = 0
		si.meta.ByteSize = 0
		si.meta.Pruned = true
		si.persistMeta()
		log.Info("Slot index pruned (replay window past Cancun)",
			"cancunBlock", si.cancunBlock,
			"currentBlock", currentBlock,
			"stateHistory", stateHistory,
			"entriesDeleted", count)
	}
}

// persistMeta writes the current meta to the database.
func (si *SlotIndex) persistMeta() {
	rawdb.WriteUBTSlotIndexMeta(si.db, si.meta)
}

// Meta returns the current slot index metadata.
func (si *SlotIndex) Meta() *rawdb.UBTSlotIndexMeta {
	return si.meta
}
