package sidecar

import (
	"context"
	"math/big"
	"testing"
	"time"

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

// snapshotDB wraps a database and records all Put operations to detect mutations.
type snapshotDB struct {
	ethdb.Database
	puts int
}

func (s *snapshotDB) NewBatch() ethdb.Batch {
	return &snapshotBatch{Batch: s.Database.NewBatch(), db: s}
}

func (s *snapshotDB) NewBatchWithSize(size int) ethdb.Batch {
	return &snapshotBatch{Batch: s.Database.NewBatchWithSize(size), db: s}
}

func (s *snapshotDB) Put(key []byte, value []byte) error {
	s.puts++
	return s.Database.Put(key, value)
}

type snapshotBatch struct {
	ethdb.Batch
	db    *snapshotDB
	count int
}

func (b *snapshotBatch) Put(key []byte, value []byte) error {
	b.count++
	return b.Batch.Put(key, value)
}

func (b *snapshotBatch) Write() error {
	b.db.puts += b.count
	return b.Batch.Write()
}

// TestSeparateUBTDB verifies that when using a separate UBT database,
// conversion and block application work correctly with the UBT data
// isolated from the main chainDB (no writes to chainDB after setup).
func TestSeparateUBTDB(t *testing.T) {
	// Step 1: Create a small MPT state with preimages.
	innerChainDB := rawdb.NewMemoryDatabase()
	mptTrieDB := triedb.NewDatabase(innerChainDB, &triedb.Config{
		Preimages: true,
		PathDB:    pathdb.Defaults,
	})
	statedb := state.NewDatabase(mptTrieDB, nil)
	st, err := state.New(types.EmptyRootHash, statedb)
	if err != nil {
		t.Fatal(err)
	}

	for i := range 10 {
		addr := benchmarkAddress(i)
		st.AddBalance(addr, uint256.NewInt(uint64(i+1)*1000), tracing.BalanceChangeUnspecified)
		st.SetNonce(addr, uint64(i+1), tracing.NonceChangeUnspecified)
	}
	code := make([]byte, 64)
	for i := range code {
		code[i] = byte(i + 1)
	}
	codeAddr := benchmarkAddress(100)
	st.AddBalance(codeAddr, uint256.NewInt(50000), tracing.BalanceChangeUnspecified)
	st.SetCode(codeAddr, code, tracing.CodeChangeUnspecified)
	for slot := range 5 {
		st.SetState(codeAddr, benchmarkHash(uint64(slot+1)), benchmarkHash(uint64(slot+100)))
	}

	root, err := st.Commit(1, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := mptTrieDB.Commit(root, false); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for !mptTrieDB.SnapshotCompleted() {
		if time.Now().After(deadline) {
			t.Fatal("snapshot did not complete")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Step 2: Wrap chainDB to track mutations, create separate UBT database.
	chainDB := &snapshotDB{Database: innerChainDB}
	ubtDB := rawdb.NewMemoryDatabase()

	// Step 3: Create sidecar with separate DBs and run conversion.
	sc, err := NewUBTSidecar(chainDB, ubtDB, mptTrieDB)
	if err != nil {
		t.Fatal(err)
	}
	if err := sc.InitFromDB(); err != nil {
		t.Fatal(err)
	}
	if sc.State() != StateStale {
		t.Fatalf("expected stale state, got %s", sc.State())
	}

	// Reset put counter after sidecar init (InitFromDB only reads).
	chainDB.puts = 0

	if !sc.BeginConversion() {
		t.Fatal("failed to begin conversion")
	}
	head := &types.Header{Number: big.NewInt(1), Root: root}
	chain := convertBenchmarkChainContext{root: root, head: head}
	if err := sc.ConvertFromMPT(context.Background(), chain); err != nil {
		t.Fatal(err)
	}
	if sc.State() != StateReady {
		t.Fatalf("expected ready state, got %s", sc.State())
	}

	// Verify chainDB was NOT mutated during conversion.
	if chainDB.puts > 0 {
		t.Fatalf("chainDB was mutated during conversion: %d puts", chainDB.puts)
	}

	// Step 4: Verify the UBT state is usable.
	ubtRoot := sc.CurrentRoot()
	t.Logf("UBT root: %s", ubtRoot)
	if ubtRoot == (common.Hash{}) || ubtRoot == types.EmptyBinaryHash {
		t.Fatal("UBT root is empty after conversion")
	}
	bt, err := sc.OpenBinaryTrie(ubtRoot)
	if err != nil {
		t.Fatal("OpenBinaryTrie failed:", err)
	}
	acct, err := bt.GetAccount(benchmarkAddress(0))
	if err != nil {
		t.Fatal("GetAccount failed:", err)
	}
	if acct == nil {
		t.Fatal("account not found in UBT after conversion")
	}
	if acct.Balance.Cmp(uint256.NewInt(1000)) != 0 {
		t.Errorf("unexpected balance: got %s, want 1000", acct.Balance)
	}

	// Step 5: Simulate a block update.
	chainDB.puts = 0
	addr0 := benchmarkAddress(0)
	addr0Hash := crypto.Keccak256Hash(addr0.Bytes())
	slimData := types.SlimAccountRLP(types.StateAccount{
		Nonce:    2,
		Balance:  uint256.NewInt(2000),
		CodeHash: types.EmptyCodeHash.Bytes(),
	})
	update := &UBTUpdate{
		BlockNum:       2,
		BlockHash:      common.HexToHash("0x02"),
		ParentHash:     common.HexToHash("0x01"),
		Accounts:       map[common.Hash][]byte{addr0Hash: slimData},
		AccountsOrigin: map[common.Address][]byte{addr0: nil},
		Storages:       map[common.Hash]map[common.Hash][]byte{},
		StoragesOrigin: map[common.Address]map[common.Hash][]byte{},
		Codes:          map[common.Address][]byte{},
	}
	if err := sc.applyUBTUpdate(update); err != nil {
		t.Fatal("applyUBTUpdate failed:", err)
	}

	// Verify chainDB was NOT mutated during block update.
	if chainDB.puts > 0 {
		t.Fatalf("chainDB was mutated during block update: %d puts", chainDB.puts)
	}

	// Verify updated balance.
	newRoot := sc.CurrentRoot()
	bt2, err := sc.OpenBinaryTrie(newRoot)
	if err != nil {
		t.Fatal(err)
	}
	acct2, err := bt2.GetAccount(addr0)
	if err != nil {
		t.Fatal(err)
	}
	if acct2 == nil {
		t.Fatal("account not found after block update")
	}
	if acct2.Balance.Cmp(uint256.NewInt(2000)) != 0 {
		t.Errorf("unexpected balance after update: got %s, want 2000", acct2.Balance)
	}

	t.Log("separate DB: conversion and block update passed without chainDB mutation")
	sc.Shutdown()
}
