package sidecar

import (
	"context"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
)

func TestPreflightOfflineBuild(t *testing.T) {
	fixture := newConvertFromMPTBenchmarkFixture(t)
	sidecar, err := NewUBTSidecar(fixture.chainDB, fixture.chainDB, fixture.mptTrieDB)
	if err != nil {
		t.Fatal(err)
	}
	result, err := sidecar.PreflightOfflineBuild(context.Background(), fixture.chain, &UBTPreflightConfig{
		SampleAccounts: 100,
		SampleStorage:  2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.SnapshotCompleted {
		t.Fatal("expected completed snapshot")
	}
	if result.SampledAccounts != 100 {
		t.Fatalf("sampled accounts mismatch: have %d want 100", result.SampledAccounts)
	}
	if result.AccountsWithCode == 0 {
		t.Fatal("expected sampled accounts with code")
	}
	if result.AccountsWithStorage == 0 {
		t.Fatal("expected sampled accounts with storage")
	}
}

func TestPreflightOfflineBuildFullCountsMissingPreimages(t *testing.T) {
	fixture := newConvertFromMPTBenchmarkFixture(t)
	sidecar, err := NewUBTSidecar(fixture.chainDB, fixture.chainDB, fixture.mptTrieDB)
	if err != nil {
		t.Fatal(err)
	}
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
	result, err := sidecar.PreflightOfflineBuild(context.Background(), fixture.chain, &UBTPreflightConfig{
		Full: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.MissingAccountPreimages != 1 {
		t.Fatalf("missing account preimages mismatch: have %d want 1", result.MissingAccountPreimages)
	}
	if result.TotalAccounts == 0 {
		t.Fatal("expected total accounts > 0")
	}
}
