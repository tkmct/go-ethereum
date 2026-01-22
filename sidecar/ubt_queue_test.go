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
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
)

func TestUBTQueueMeta(t *testing.T) {
	db := rawdb.NewMemoryDatabase()
	sc := &UBTSidecar{
		enabled:    true,
		converting: true,
		chainDB:    db,
		queueLimit: 0,
	}
	var blocks []*types.Block
	for i := uint64(1); i <= 3; i++ {
		block := types.NewBlockWithHeader(&types.Header{Number: new(big.Int).SetUint64(i)})
		if err := sc.EnqueueUpdate(block, &state.StateUpdate{}); err != nil {
			t.Fatalf("enqueue failed: %v", err)
		}
		blocks = append(blocks, block)
	}
	meta := rawdb.ReadUBTUpdateQueueMeta(db)
	if meta == nil || meta.Count != 3 || meta.Oldest != 1 || meta.Newest != 3 {
		t.Fatalf("unexpected meta after enqueue: %+v", meta)
	}
	sc.deleteQueueEntry(blocks[0].NumberU64(), blocks[0].Hash())
	meta = rawdb.ReadUBTUpdateQueueMeta(db)
	if meta == nil || meta.Count != 2 || meta.Oldest != 2 || meta.Newest != 3 {
		t.Fatalf("unexpected meta after delete oldest: %+v", meta)
	}
	sc.deleteQueueEntry(blocks[2].NumberU64(), blocks[2].Hash())
	meta = rawdb.ReadUBTUpdateQueueMeta(db)
	if meta == nil || meta.Count != 1 || meta.Oldest != 2 || meta.Newest != 2 {
		t.Fatalf("unexpected meta after delete newest: %+v", meta)
	}
	sc.deleteQueueEntry(blocks[1].NumberU64(), blocks[1].Hash())
	meta = rawdb.ReadUBTUpdateQueueMeta(db)
	if meta == nil || meta.Count != 0 || meta.Oldest != 0 || meta.Newest != 0 {
		t.Fatalf("unexpected meta after delete all: %+v", meta)
	}
}

func TestUBTQueueReset(t *testing.T) {
	db := rawdb.NewMemoryDatabase()
	sc := &UBTSidecar{
		enabled:    true,
		converting: true,
		chainDB:    db,
		queueLimit: 0,
	}
	for i := uint64(1); i <= 2; i++ {
		block := types.NewBlockWithHeader(&types.Header{Number: new(big.Int).SetUint64(i)})
		if err := sc.EnqueueUpdate(block, &state.StateUpdate{}); err != nil {
			t.Fatalf("enqueue failed: %v", err)
		}
	}
	sc.queueMu.Lock()
	if err := sc.resetQueueLocked(); err != nil {
		sc.queueMu.Unlock()
		t.Fatalf("reset failed: %v", err)
	}
	sc.queueMu.Unlock()
	meta := rawdb.ReadUBTUpdateQueueMeta(db)
	if meta != nil {
		t.Fatalf("unexpected meta after reset: %+v", meta)
	}
}
