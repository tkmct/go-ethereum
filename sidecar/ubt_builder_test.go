package sidecar

import (
	"context"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
)

func TestBuildOfflineFromMPTMatchesConvert(t *testing.T) {
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

	builderFixture := newConvertFromMPTBenchmarkFixture(t)
	builderSidecar, err := NewUBTSidecar(builderFixture.chainDB, builderFixture.chainDB, builderFixture.mptTrieDB)
	if err != nil {
		t.Fatal(err)
	}
	if !builderSidecar.BeginConversion() {
		t.Fatal("failed to begin builder conversion")
	}
	if err := builderSidecar.BuildOfflineFromMPT(context.Background(), builderFixture.chain, nil); err != nil {
		t.Fatal(err)
	}
	builderRoot := builderSidecar.CurrentRoot()

	if convertRoot != builderRoot {
		t.Fatalf("root mismatch: convert=%x builder=%x", convertRoot, builderRoot)
	}
}

func TestBuildOfflineFromMPTSkipMissingPreimages(t *testing.T) {
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
		t.Fatal("failed to begin builder conversion")
	}
	if err := sidecar.BuildOfflineFromMPT(context.Background(), fixture.chain, &UBTBuilderConfig{SkipMissingPreimages: true}); err != nil {
		t.Fatal(err)
	}
	if sidecar.CurrentRoot() == (common.Hash{}) {
		t.Fatal("expected non-empty root")
	}
}
