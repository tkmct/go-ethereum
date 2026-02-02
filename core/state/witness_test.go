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

package state

import (
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/stateless"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/triedb"
	"github.com/ethereum/go-ethereum/triedb/pathdb"
	"github.com/holiman/uint256"
)

func TestWitnessPathsMPT(t *testing.T) {
	db := rawdb.NewMemoryDatabase()
	tdb := triedb.NewDatabase(db, nil)
	sdb := NewDatabase(tdb, nil)
	addr := common.HexToAddress("0x00000000000000000000000000000000000000aa")

	root, err := commitWithBalance(t, sdb, types.EmptyRootHash, addr, uint256.NewInt(1))
	if err != nil {
		t.Fatalf("commit failed: %v", err)
	}
	witness := &stateless.Witness{State: make(map[string]struct{})}
	state, err := New(root, sdb)
	if err != nil {
		t.Fatalf("state init failed: %v", err)
	}
	state.StartPrefetcher("test", witness, nil)
	state.SetBalance(addr, uint256.NewInt(2), tracing.BalanceChangeUnspecified)
	if _, err := state.Commit(1, false, false); err != nil {
		t.Fatalf("commit failed: %v", err)
	}
	if len(witness.StatePaths) != 0 {
		t.Fatalf("unexpected StatePaths in MPT witness: %d", len(witness.StatePaths))
	}
	if len(witness.State) == 0 {
		t.Fatalf("expected MPT witness entries")
	}
}

func TestWitnessPathsVerkle(t *testing.T) {
	db := rawdb.NewMemoryDatabase()
	tdb := triedb.NewDatabase(db, &triedb.Config{
		IsVerkle: true,
		PathDB: &pathdb.Config{
			TrieCleanSize:     1024,
			StateCleanSize:    1024,
			WriteBufferSize:   1024,
			SnapshotNoBuild:   true,
			NoAsyncFlush:      true,
			NoAsyncGeneration: true,
		},
	})
	sdb := NewDatabase(tdb, nil)
	addr := common.HexToAddress("0x00000000000000000000000000000000000000bb")

	root, err := commitWithBalance(t, sdb, types.EmptyBinaryHash, addr, uint256.NewInt(1))
	if err != nil {
		t.Fatalf("commit failed: %v", err)
	}
	witness := &stateless.Witness{State: make(map[string]struct{})}
	state, err := New(root, sdb)
	if err != nil {
		t.Fatalf("state init failed: %v", err)
	}
	state.StartPrefetcher("test", witness, nil)
	state.SetBalance(addr, uint256.NewInt(2), tracing.BalanceChangeUnspecified)
	if _, err := state.Commit(1, false, false); err != nil {
		t.Fatalf("commit failed: %v", err)
	}
	if len(witness.StatePaths) == 0 {
		t.Fatalf("expected UBT witness paths")
	}
}

func commitWithBalance(t *testing.T, db Database, root common.Hash, addr common.Address, bal *uint256.Int) (common.Hash, error) {
	t.Helper()
	state, err := New(root, db)
	if err != nil {
		return common.Hash{}, err
	}
	state.SetBalance(addr, bal, tracing.BalanceChangeUnspecified)
	return state.Commit(1, false, false)
}
