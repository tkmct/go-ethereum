package sidecar

import (
	"context"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/triedb"
	"github.com/ethereum/go-ethereum/triedb/pathdb"
	"github.com/holiman/uint256"
)

func TestBuildOfflineBucketedFromMPTMatchesConvert(t *testing.T) {
	convertFixture := newConvertFromMPTBenchmarkFixture(t)
	convertSidecar, err := NewUBTSidecar(convertFixture.chainDB, convertFixture.chainDB, convertFixture.mptTrieDB)
	if err != nil {
		t.Fatal(err)
	}
	if !convertSidecar.BeginConversion() {
		t.Fatal("failed to begin convert conversion")
	}
	if err := convertSidecar.ConvertFromMPT(context.Background(), convertFixture.chain); err != nil {
		t.Fatal(err)
	}
	convertRoot := convertSidecar.CurrentRoot()

	bucketFixture := newConvertFromMPTBenchmarkFixture(t)
	bucketSidecar, err := NewUBTSidecar(bucketFixture.chainDB, bucketFixture.chainDB, bucketFixture.mptTrieDB)
	if err != nil {
		t.Fatal(err)
	}
	if !bucketSidecar.BeginConversion() {
		t.Fatal("failed to begin bucket conversion")
	}
	if err := bucketSidecar.BuildOfflineBucketedFromMPT(context.Background(), bucketFixture.chain, nil); err != nil {
		t.Fatal(err)
	}
	if root := bucketSidecar.CurrentRoot(); root != convertRoot {
		t.Fatalf("root mismatch: convert=%x bucket=%x", convertRoot, root)
	}
}

func TestBucketedBuilderSeparateUBTDBNoChainWrites(t *testing.T) {
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
	codeAddr := benchmarkAddress(100)
	st.AddBalance(codeAddr, uint256.NewInt(50000), tracing.BalanceChangeUnspecified)
	st.SetCode(codeAddr, []byte{1, 2, 3, 4}, tracing.CodeChangeUnspecified)
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
	chainDB := &snapshotDB{Database: innerChainDB}
	ubtDB := rawdb.NewMemoryDatabase()
	sc, err := NewUBTSidecar(chainDB, ubtDB, mptTrieDB)
	if err != nil {
		t.Fatal(err)
	}
	if !sc.BeginConversion() {
		t.Fatal("failed to begin conversion")
	}
	head := &types.Header{Number: big.NewInt(1), Root: root}
	chain := convertBenchmarkChainContext{root: root, head: head}
	chainDB.puts = 0
	if err := sc.BuildOfflineBucketedFromMPT(context.Background(), chain, nil); err != nil {
		t.Fatal(err)
	}
	if chainDB.puts > 0 {
		t.Fatalf("chainDB was mutated during bucketed build: %d puts", chainDB.puts)
	}
}

func TestBuildOfflineBucketedFromMPTSkipMissingPreimages(t *testing.T) {
	fixture := newConvertFromMPTBenchmarkFixture(t)
	accIt, err := fixture.mptTrieDB.AccountIterator(fixture.chain.root, common.Hash{})
	if err != nil {
		t.Fatal(err)
	}
	if !accIt.Next() {
		t.Fatal("no accounts in fixture")
	}
	accountHash := accIt.Hash()
	accIt.Release()
	if err := fixture.chainDB.Delete(append(rawdb.PreimagePrefix, accountHash.Bytes()...)); err != nil {
		t.Fatal(err)
	}
	sidecar, err := NewUBTSidecar(fixture.chainDB, fixture.chainDB, fixture.mptTrieDB)
	if err != nil {
		t.Fatal(err)
	}
	if !sidecar.BeginConversion() {
		t.Fatal("failed to begin bucket conversion")
	}
	if err := sidecar.BuildOfflineBucketedFromMPT(context.Background(), fixture.chain, &UBTBucketBuilderConfig{SkipMissingPreimages: true}); err != nil {
		t.Fatal(err)
	}
	if sidecar.CurrentRoot() == (common.Hash{}) {
		t.Fatal("expected non-empty root")
	}
}
