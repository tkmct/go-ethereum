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

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/ethereum/go-ethereum/triedb"
	"github.com/ethereum/go-ethereum/triedb/pathdb"
	"github.com/holiman/uint256"
)

func TestApplyUBTUpdateDeterministicOrder(t *testing.T) {
	addrA := common.HexToAddress("0x00000000000000000000000000000000000000aa")
	addrB := common.HexToAddress("0x00000000000000000000000000000000000000bb")
	seed := buildTestUpdate(1, []common.Address{addrA, addrB}, common.Address{})
	update1 := buildTestUpdate(2, []common.Address{addrA, addrB}, addrA)
	update2 := buildTestUpdate(2, []common.Address{addrB, addrA}, addrA)

	sc1 := newTestSidecar(t)
	sc2 := newTestSidecar(t)

	if err := sc1.applyUBTUpdate(seed); err != nil {
		t.Fatalf("apply seed failed: %v", err)
	}
	if err := sc2.applyUBTUpdate(seed); err != nil {
		t.Fatalf("apply seed failed: %v", err)
	}
	if err := sc1.applyUBTUpdate(update1); err != nil {
		t.Fatalf("apply update1 failed: %v", err)
	}
	if err := sc2.applyUBTUpdate(update2); err != nil {
		t.Fatalf("apply update2 failed: %v", err)
	}
	if sc1.CurrentRoot() != sc2.CurrentRoot() {
		t.Fatalf("root mismatch: %x != %x", sc1.CurrentRoot(), sc2.CurrentRoot())
	}
}

func newTestSidecar(t *testing.T) *UBTSidecar {
	t.Helper()
	db := rawdb.NewMemoryDatabase()
	cfg := &triedb.Config{
		IsVerkle: true,
		PathDB: &pathdb.Config{
			TrieCleanSize:       1024,
			StateCleanSize:      1024,
			WriteBufferSize:     1024,
			SnapshotNoBuild:     true,
			NoAsyncFlush:        true,
			NoAsyncGeneration:   true,
			EnableStateIndexing: false,
		},
	}
	tdb := triedb.NewDatabase(db, cfg)
	return &UBTSidecar{
		enabled:     true,
		triedb:      tdb,
		chainDB:     db,
		currentRoot: types.EmptyBinaryHash,
	}
}

func buildTestUpdate(blockNum uint64, order []common.Address, deleteAddr common.Address) *UBTUpdate {
	update := &UBTUpdate{
		BlockNum:       blockNum,
		BlockHash:      types.NewBlockWithHeader(&types.Header{Number: new(big.Int).SetUint64(blockNum)}).Hash(),
		ParentHash:     common.Hash{},
		RawStorageKey:  true,
		Accounts:       make(map[common.Hash][]byte),
		AccountsOrigin: make(map[common.Address][]byte),
		Storages:       make(map[common.Hash]map[common.Hash][]byte),
		StoragesOrigin: make(map[common.Address]map[common.Hash][]byte),
		Codes:          make(map[common.Address][]byte),
	}
	rawKey := common.HexToHash("0x01")
	slotHash := crypto.Keccak256Hash(rawKey.Bytes())
	slotVal, _ := rlp.EncodeToBytes([]byte{0x01})
	account := types.StateAccount{
		Nonce:    1,
		Balance:  uint256.NewInt(1),
		Root:     types.EmptyRootHash,
		CodeHash: types.EmptyCodeHash.Bytes(),
	}
	for _, addr := range order {
		update.AccountsOrigin[addr] = nil
		addrHash := crypto.Keccak256Hash(addr.Bytes())
		if addr == deleteAddr {
			update.Accounts[addrHash] = nil
			update.Storages[addrHash] = map[common.Hash][]byte{
				slotHash: slotVal,
			}
			update.StoragesOrigin[addr] = map[common.Hash][]byte{
				rawKey: nil,
			}
			continue
		}
		update.Accounts[addrHash] = types.SlimAccountRLP(account)
		update.Storages[addrHash] = map[common.Hash][]byte{
			slotHash: slotVal,
		}
		update.StoragesOrigin[addr] = map[common.Hash][]byte{
			rawKey: nil,
		}
	}
	return update
}
