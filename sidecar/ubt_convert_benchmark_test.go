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
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/triedb"
	"github.com/ethereum/go-ethereum/triedb/pathdb"
	"github.com/holiman/uint256"
)

const (
	convertBenchmarkEOAs             = 2048
	convertBenchmarkCodeAccounts     = 256
	convertBenchmarkStorageAccounts  = 64
	convertBenchmarkStorageSlots     = 8
	convertBenchmarkCodeSize         = 4096
	convertBenchmarkSnapshotWait     = 5 * time.Second
	convertBenchmarkSnapshotInterval = 10 * time.Millisecond
)

type convertBenchmarkChainContext struct {
	root common.Hash
	head *types.Header
}

func (c convertBenchmarkChainContext) HeadRoot() common.Hash            { return c.root }
func (c convertBenchmarkChainContext) HeadBlock() *types.Header         { return c.head }
func (c convertBenchmarkChainContext) CanonicalHash(uint64) common.Hash { return common.Hash{} }

type convertFromMPTBenchmarkFixture struct {
	chainDB   ethdb.Database
	mptTrieDB *triedb.Database
	chain     convertBenchmarkChainContext
}

func BenchmarkConvertFromMPT(b *testing.B) {
	fixture := newConvertFromMPTBenchmarkFixture(b)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		sidecar, err := NewUBTSidecar(fixture.chainDB, fixture.chainDB, fixture.mptTrieDB)
		if err != nil {
			b.Fatal(err)
		}
		if !sidecar.BeginConversion() {
			b.Fatal("failed to begin conversion")
		}
		if err := sidecar.ConvertFromMPT(context.Background(), fixture.chain); err != nil {
			b.Fatal(err)
		}
		sidecar.Shutdown()
	}
}

func newConvertFromMPTBenchmarkFixture(tb testing.TB) *convertFromMPTBenchmarkFixture {
	return newScaledConvertFromMPTBenchmarkFixture(tb, convertBenchmarkEOAs, convertBenchmarkCodeAccounts, convertBenchmarkStorageAccounts)
}

func newSizedConvertFromMPTBenchmarkFixture(tb testing.TB, totalAccounts int) *convertFromMPTBenchmarkFixture {
	eoas, codeAccounts, storageAccounts := scaledConvertBenchmarkCounts(totalAccounts)
	return newScaledConvertFromMPTBenchmarkFixture(tb, eoas, codeAccounts, storageAccounts)
}

func newScaledConvertFromMPTBenchmarkFixture(tb testing.TB, eoas, codeAccounts, storageAccounts int) *convertFromMPTBenchmarkFixture {
	tb.Helper()

	chainDB := rawdb.NewMemoryDatabase()
	mptTrieDB := triedb.NewDatabase(chainDB, &triedb.Config{
		Preimages: true,
		PathDB:    pathdb.Defaults,
	})
	statedb := state.NewDatabase(mptTrieDB, nil)
	st, err := state.New(types.EmptyRootHash, statedb)
	if err != nil {
		tb.Fatal(err)
	}

	codeVariants := make([][]byte, benchmarkCodeVariants)
	for i := range codeVariants {
		code := make([]byte, convertBenchmarkCodeSize)
		for j := range code {
			code[j] = byte(i + j + 1)
		}
		codeVariants[i] = code
	}

	for i := 0; i < eoas; i++ {
		addr := benchmarkAddress(i)
		st.AddBalance(addr, uint256.NewInt(uint64(i+1)), tracing.BalanceChangeUnspecified)
		st.SetNonce(addr, uint64(i+1), tracing.NonceChangeUnspecified)
	}
	for i := 0; i < codeAccounts; i++ {
		addr := benchmarkAddress(eoas + i)
		st.AddBalance(addr, uint256.NewInt(uint64(10_000+i)), tracing.BalanceChangeUnspecified)
		st.SetNonce(addr, uint64(i+1), tracing.NonceChangeUnspecified)
		st.SetCode(addr, codeVariants[i%len(codeVariants)], tracing.CodeChangeUnspecified)
	}
	for i := 0; i < storageAccounts; i++ {
		addr := benchmarkAddress(eoas + codeAccounts + i)
		st.AddBalance(addr, uint256.NewInt(uint64(20_000+i)), tracing.BalanceChangeUnspecified)
		st.SetNonce(addr, uint64(i+1), tracing.NonceChangeUnspecified)
		st.SetCode(addr, codeVariants[i%len(codeVariants)], tracing.CodeChangeUnspecified)
		for slot := 0; slot < convertBenchmarkStorageSlots; slot++ {
			key := benchmarkHash(uint64(slot + 1))
			value := benchmarkHash(uint64((i + 1) * (slot + 1)))
			st.SetState(addr, key, value)
		}
	}

	root, err := st.Commit(1, true, false)
	if err != nil {
		tb.Fatal(err)
	}
	if err := mptTrieDB.Commit(root, false); err != nil {
		tb.Fatal(err)
	}

	deadline := time.Now().Add(convertBenchmarkSnapshotWait)
	for !mptTrieDB.SnapshotCompleted() {
		if time.Now().After(deadline) {
			tb.Fatal("mpt snapshot did not complete before benchmark")
		}
		time.Sleep(convertBenchmarkSnapshotInterval)
	}

	head := &types.Header{Number: big.NewInt(1), Root: root}
	return &convertFromMPTBenchmarkFixture{
		chainDB:   chainDB,
		mptTrieDB: mptTrieDB,
		chain: convertBenchmarkChainContext{
			root: root,
			head: head,
		},
	}
}

func scaledConvertBenchmarkCounts(totalAccounts int) (int, int, int) {
	codeAccounts := totalAccounts * convertBenchmarkCodeAccounts / (convertBenchmarkEOAs + convertBenchmarkCodeAccounts + convertBenchmarkStorageAccounts)
	storageAccounts := totalAccounts * convertBenchmarkStorageAccounts / (convertBenchmarkEOAs + convertBenchmarkCodeAccounts + convertBenchmarkStorageAccounts)
	eoas := totalAccounts - codeAccounts - storageAccounts
	return eoas, codeAccounts, storageAccounts
}
