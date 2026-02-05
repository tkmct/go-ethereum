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
	"encoding/json"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/triedb"
	"github.com/ethereum/go-ethereum/triedb/pathdb"
	"github.com/holiman/uint256"
)

func TestInitFromGenesisAlloc(t *testing.T) {
	chainDB := rawdb.NewMemoryDatabase()
	genesisHash := common.HexToHash("0x01")
	rawdb.WriteCanonicalHash(chainDB, genesisHash, 0)

	addr := common.HexToAddress("0x00000000000000000000000000000000000000aa")
	slot := common.HexToHash("0x01")
	alloc := types.GenesisAlloc{
		addr: {
			Balance: big.NewInt(5),
			Nonce:   7,
			Code:    []byte{0x60, 0x00},
			Storage: map[common.Hash]common.Hash{
				slot: common.HexToHash("0x1234"),
			},
		},
	}
	blob, err := marshalGenesisAlloc(alloc)
	if err != nil {
		t.Fatalf("marshal alloc failed: %v", err)
	}
	rawdb.WriteGenesisStateSpec(chainDB, genesisHash, blob)

	sc := newTestSidecarWithDB(t, chainDB)
	if err := sc.InitFromDB(); err != nil {
		t.Fatalf("init from db failed: %v", err)
	}
	if !sc.Ready() {
		t.Fatalf("expected sidecar to be ready after genesis init")
	}
	root, ok := sc.GetUBTRoot(genesisHash)
	if !ok {
		t.Fatalf("missing UBT root for genesis")
	}
	acc, err := readAccountFromTrie(sc, root, addr)
	if err != nil {
		t.Fatalf("read account failed: %v", err)
	}
	if acc == nil || acc.Nonce != 7 {
		t.Fatalf("unexpected account data: %#v", acc)
	}
	val, err := sc.ReadStorage(root, addr, slot)
	if err != nil {
		t.Fatalf("read storage failed: %v", err)
	}
	if val != common.HexToHash("0x1234") {
		t.Fatalf("unexpected storage value: %x", val)
	}
}

func TestApplyStateUpdateBalanceNonceOnly(t *testing.T) {
	chainDB := rawdb.NewMemoryDatabase()
	sc := newTestSidecarWithDB(t, chainDB)
	seedSidecar(t, sc)

	stateDB := newTestStateDB()
	addr := common.HexToAddress("0x00000000000000000000000000000000000000bb")
	sentinel := common.HexToAddress("0x00000000000000000000000000000000000000bc")
	slot := common.HexToHash("0x01")
	initialVal := common.HexToHash("0x1234")

	update1, root := commitStateUpdate(t, stateDB, types.EmptyRootHash, 1, true, func(sdb *state.StateDB) {
		sdb.SetBalance(addr, uint256.NewInt(10), tracing.BalanceChangeUnspecified)
		sdb.SetNonce(addr, 1, tracing.NonceChangeUnspecified)
		sdb.SetState(addr, slot, initialVal)
		sdb.SetBalance(sentinel, uint256.NewInt(1), tracing.BalanceChangeUnspecified)
	})
	block1 := newTestBlock(1, common.Hash{})
	if err := sc.ApplyStateUpdate(block1, update1, chainDB); err != nil {
		t.Fatalf("apply update1 failed: %v", err)
	}

	update2, _ := commitStateUpdate(t, stateDB, root, 2, true, func(sdb *state.StateDB) {
		sdb.SetBalance(addr, uint256.NewInt(20), tracing.BalanceChangeUnspecified)
		sdb.SetNonce(addr, 2, tracing.NonceChangeUnspecified)
	})
	block2 := newTestBlock(2, block1.Hash())
	if err := sc.ApplyStateUpdate(block2, update2, chainDB); err != nil {
		t.Fatalf("apply update2 failed: %v", err)
	}

	ubtRoot, ok := sc.GetUBTRoot(block2.Hash())
	if !ok {
		t.Fatalf("missing UBT root for block 2")
	}
	acc, err := readAccountFromTrie(sc, ubtRoot, addr)
	if err != nil {
		t.Fatalf("read account failed: %v", err)
	}
	if acc == nil {
		t.Fatalf("expected account to exist")
	}
	if acc.Nonce != 2 {
		t.Fatalf("unexpected nonce: %d", acc.Nonce)
	}
	if acc.Balance.Cmp(uint256.NewInt(20)) != 0 {
		t.Fatalf("unexpected balance: %s", acc.Balance)
	}
	val, err := sc.ReadStorage(ubtRoot, addr, slot)
	if err != nil {
		t.Fatalf("read storage failed: %v", err)
	}
	if val != initialVal {
		t.Fatalf("storage value changed unexpectedly: %x != %x", val, initialVal)
	}
}

func TestApplyStateUpdateStorageUpdateDelete(t *testing.T) {
	chainDB := rawdb.NewMemoryDatabase()
	sc := newTestSidecarWithDB(t, chainDB)
	seedSidecar(t, sc)

	stateDB := newTestStateDB()
	addr := common.HexToAddress("0x00000000000000000000000000000000000000cc")
	sentinel := common.HexToAddress("0x00000000000000000000000000000000000000cd")
	slotA := common.HexToHash("0x01")
	slotB := common.HexToHash("0x02")
	valA := common.HexToHash("0xaaaa")
	valB := common.HexToHash("0xbbbb")

	update1, root := commitStateUpdate(t, stateDB, types.EmptyRootHash, 1, true, func(sdb *state.StateDB) {
		sdb.SetBalance(addr, uint256.NewInt(1), tracing.BalanceChangeUnspecified)
		sdb.SetState(addr, slotA, valA)
		sdb.SetState(addr, slotB, valB)
		sdb.SetBalance(sentinel, uint256.NewInt(1), tracing.BalanceChangeUnspecified)
	})
	block1 := newTestBlock(1, common.Hash{})
	if err := sc.ApplyStateUpdate(block1, update1, chainDB); err != nil {
		t.Fatalf("apply update1 failed: %v", err)
	}

	update2, _ := commitStateUpdate(t, stateDB, root, 2, true, func(sdb *state.StateDB) {
		sdb.SetState(addr, slotA, common.HexToHash("0xcccc"))
		sdb.SetState(addr, slotB, common.Hash{})
	})
	block2 := newTestBlock(2, block1.Hash())
	if err := sc.ApplyStateUpdate(block2, update2, chainDB); err != nil {
		t.Fatalf("apply update2 failed: %v", err)
	}

	ubtRoot, ok := sc.GetUBTRoot(block2.Hash())
	if !ok {
		t.Fatalf("missing UBT root for block 2")
	}
	val, err := sc.ReadStorage(ubtRoot, addr, slotA)
	if err != nil {
		t.Fatalf("read storage A failed: %v", err)
	}
	if val != common.HexToHash("0xcccc") {
		t.Fatalf("unexpected storage A value: %x", val)
	}
	val, err = sc.ReadStorage(ubtRoot, addr, slotB)
	if err != nil {
		t.Fatalf("read storage B failed: %v", err)
	}
	if val != (common.Hash{}) {
		t.Fatalf("expected storage B to be deleted, got: %x", val)
	}
}

func TestApplyStateUpdateAccountDeletion(t *testing.T) {
	chainDB := rawdb.NewMemoryDatabase()
	sc := newTestSidecarWithDB(t, chainDB)
	seedSidecar(t, sc)

	stateDB := newTestStateDB()
	addr := common.HexToAddress("0x00000000000000000000000000000000000000dd")
	sentinel := common.HexToAddress("0x00000000000000000000000000000000000000de")

	update1, root := commitStateUpdate(t, stateDB, types.EmptyRootHash, 1, true, func(sdb *state.StateDB) {
		sdb.SetBalance(addr, uint256.NewInt(5), tracing.BalanceChangeUnspecified)
		sdb.SetNonce(addr, 3, tracing.NonceChangeUnspecified)
		sdb.SetBalance(sentinel, uint256.NewInt(1), tracing.BalanceChangeUnspecified)
	})
	block1 := newTestBlock(1, common.Hash{})
	if err := sc.ApplyStateUpdate(block1, update1, chainDB); err != nil {
		t.Fatalf("apply update1 failed: %v", err)
	}

	update2, _ := commitStateUpdate(t, stateDB, root, 2, true, func(sdb *state.StateDB) {
		sdb.SelfDestruct(addr)
	})
	block2 := newTestBlock(2, block1.Hash())
	if err := sc.ApplyStateUpdate(block2, update2, chainDB); err != nil {
		t.Fatalf("apply update2 failed: %v", err)
	}

	ubtRoot, ok := sc.GetUBTRoot(block2.Hash())
	if !ok {
		t.Fatalf("missing UBT root for block 2")
	}
	acc, err := readAccountFromTrie(sc, ubtRoot, addr)
	if err != nil {
		t.Fatalf("read account failed: %v", err)
	}
	if acc != nil {
		t.Fatalf("expected account deletion, got account with balance %s", acc.Balance)
	}
}

func TestApplyStateUpdateUsesTrieDBPreimages(t *testing.T) {
	chainDB := rawdb.NewMemoryDatabase()
	cfg := *triedb.HashDefaults
	cfg.Preimages = true
	mptTrieDB := triedb.NewDatabase(chainDB, &cfg)

	sc := newTestSidecarWithDB(t, chainDB)
	sc.SetMPTTrieDB(mptTrieDB)
	seedSidecar(t, sc)

	stateDB := state.NewDatabase(mptTrieDB, nil)
	addr := common.HexToAddress("0x00000000000000000000000000000000000000ee")
	sentinel := common.HexToAddress("0x00000000000000000000000000000000000000ef")
	slot := common.HexToHash("0x01")
	val := common.HexToHash("0x9999")

	update, _ := commitStateUpdate(t, stateDB, types.EmptyRootHash, 1, false, func(sdb *state.StateDB) {
		sdb.SetBalance(addr, uint256.NewInt(1), tracing.BalanceChangeUnspecified)
		sdb.SetState(addr, slot, val)
		sdb.SetBalance(sentinel, uint256.NewInt(1), tracing.BalanceChangeUnspecified)
	})

	slotHash := crypto.Keccak256Hash(slot.Bytes())
	if preimage := rawdb.ReadPreimage(chainDB, slotHash); preimage != nil {
		t.Fatalf("expected no preimage on disk, got %x", preimage)
	}

	block := newTestBlock(1, common.Hash{})
	if err := sc.ApplyStateUpdate(block, update, chainDB); err != nil {
		t.Fatalf("apply update failed: %v", err)
	}

	ubtRoot, ok := sc.GetUBTRoot(block.Hash())
	if !ok {
		t.Fatalf("missing UBT root for block 1")
	}
	got, err := sc.ReadStorage(ubtRoot, addr, slot)
	if err != nil {
		t.Fatalf("read storage failed: %v", err)
	}
	if got != val {
		t.Fatalf("unexpected storage value: %x", got)
	}
}

func newTestSidecarWithDB(t *testing.T, db ethdb.Database) *UBTSidecar {
	t.Helper()
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

func marshalGenesisAlloc(alloc types.GenesisAlloc) ([]byte, error) {
	raw := make(map[common.UnprefixedAddress]types.Account, len(alloc))
	for addr, account := range alloc {
		raw[common.UnprefixedAddress(addr)] = account
	}
	return json.Marshal(raw)
}

func seedSidecar(t *testing.T, sc *UBTSidecar) common.Hash {
	t.Helper()
	genesisHash := common.HexToHash("0x01")
	if err := sc.buildFromGenesis(genesisHash, types.GenesisAlloc{}); err != nil {
		t.Fatalf("seed sidecar failed: %v", err)
	}
	return genesisHash
}

func newTestStateDB() *state.CachingDB {
	tdb := triedb.NewDatabase(rawdb.NewMemoryDatabase(), triedb.HashDefaults)
	return state.NewDatabase(tdb, nil)
}

func newTestBlock(num uint64, parent common.Hash) *types.Block {
	return types.NewBlockWithHeader(&types.Header{
		Number:     new(big.Int).SetUint64(num),
		ParentHash: parent,
	})
}

func commitStateUpdate(t *testing.T, db *state.CachingDB, root common.Hash, blockNum uint64, noStorageWiping bool, mutate func(*state.StateDB)) (*state.StateUpdate, common.Hash) {
	t.Helper()
	sdb, err := state.New(root, db)
	if err != nil {
		t.Fatalf("state init failed: %v", err)
	}
	mutate(sdb)
	newRoot, update, err := sdb.CommitWithUpdate(blockNum, true, noStorageWiping)
	if err != nil {
		t.Fatalf("commit failed: %v", err)
	}
	if update == nil {
		t.Fatalf("missing state update")
	}
	return update, newRoot
}

func readAccountFromTrie(sc *UBTSidecar, root common.Hash, addr common.Address) (*types.StateAccount, error) {
	bt, err := sc.OpenBinaryTrie(root)
	if err != nil {
		return nil, err
	}
	return bt.GetAccount(addr)
}
