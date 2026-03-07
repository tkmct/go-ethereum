package sidecar

import (
	"encoding/binary"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/holiman/uint256"
)

const (
	benchmarkAccounts     = 64
	benchmarkSlotsPerAcct = 8
	benchmarkCodeVariants = 4
	benchmarkTemplateSets = 32
	benchmarkCodeSize     = 4096
)

type applyUBTUpdateBenchmarkFixture struct {
	sidecar *UBTSidecar
	updates []*UBTUpdate
}

func BenchmarkApplyUBTUpdate(b *testing.B) {
	fixture := newApplyUBTUpdateBenchmarkFixture(b)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		template := fixture.updates[i%len(fixture.updates)]
		update := *template
		update.BlockNum = uint64(i + 2)
		update.BlockHash = benchmarkHash(uint64(i + 2))
		update.ParentHash = benchmarkHash(uint64(i + 1))

		if err := fixture.sidecar.applyUBTUpdate(&update); err != nil {
			b.Fatal(err)
		}
	}
}

func newApplyUBTUpdateBenchmarkFixture(tb testing.TB) *applyUBTUpdateBenchmarkFixture {
	tb.Helper()

	chainDB := rawdb.NewMemoryDatabase()
	sidecar, err := NewUBTSidecar(chainDB, nil)
	if err != nil {
		tb.Fatal(err)
	}
	sidecar.state = StateReady

	preimages := make(map[common.Hash][]byte, benchmarkAccounts+benchmarkSlotsPerAcct)
	addresses := make([]common.Address, benchmarkAccounts)
	addressHashes := make([]common.Hash, benchmarkAccounts)
	slotKeys := make([]common.Hash, benchmarkSlotsPerAcct)
	slotHashes := make([]common.Hash, benchmarkSlotsPerAcct)
	codeBlobs := make([][]byte, benchmarkCodeVariants)
	codeHashes := make([]common.Hash, benchmarkCodeVariants)

	for i := 0; i < benchmarkCodeVariants; i++ {
		code := make([]byte, benchmarkCodeSize)
		for j := range code {
			code[j] = byte(i + j + 1)
		}
		codeBlobs[i] = code
		codeHashes[i] = crypto.Keccak256Hash(code)
		rawdb.WriteCode(chainDB, codeHashes[i], code)
	}
	for i := 0; i < benchmarkSlotsPerAcct; i++ {
		slotKey := benchmarkHash(uint64(i + 1))
		slotKeys[i] = slotKey
		slotHash := crypto.Keccak256Hash(slotKey.Bytes())
		slotHashes[i] = slotHash
		preimages[slotHash] = common.CopyBytes(slotKey.Bytes())
	}
	for i := 0; i < benchmarkAccounts; i++ {
		addr := benchmarkAddress(i)
		addresses[i] = addr
		addrHash := crypto.Keccak256Hash(addr.Bytes())
		addressHashes[i] = addrHash
		preimages[addrHash] = common.CopyBytes(addr.Bytes())
	}
	rawdb.WritePreimages(chainDB, preimages)

	initial := makeBenchmarkUpdate(1, addresses, addressHashes, slotHashes, codeBlobs, codeHashes, 0)
	if err := sidecar.applyUBTUpdate(initial); err != nil {
		tb.Fatal(err)
	}
	updates := make([]*UBTUpdate, benchmarkTemplateSets)
	for i := range updates {
		updates[i] = makeBenchmarkUpdate(uint64(i+2), addresses, addressHashes, slotHashes, nil, codeHashes, i+1)
	}
	return &applyUBTUpdateBenchmarkFixture{sidecar: sidecar, updates: updates}
}

func makeBenchmarkUpdate(
	blockNum uint64,
	addresses []common.Address,
	addressHashes []common.Hash,
	slotHashes []common.Hash,
	codeBlobs [][]byte,
	codeHashes []common.Hash,
	round int,
) *UBTUpdate {
	accounts := make(map[common.Hash][]byte, len(addresses))
	accountsOrigin := make(map[common.Address][]byte, len(addresses))
	storages := make(map[common.Hash]map[common.Hash][]byte, len(addresses))
	storagesOrigin := make(map[common.Address]map[common.Hash][]byte)
	codes := make(map[common.Address][]byte)

	for i, addr := range addresses {
		codeHash := codeHashes[i%len(codeHashes)]
		acct := &types.StateAccount{
			Nonce:    uint64(round + i + 1),
			Balance:  uint256.NewInt(uint64(1_000_000 + round*97 + i)),
			CodeHash: codeHash.Bytes(),
		}
		accounts[addressHashes[i]] = types.SlimAccountRLP(*acct)
		accountsOrigin[addr] = nil

		slots := make(map[common.Hash][]byte, len(slotHashes))
		for j, slotHash := range slotHashes {
			slots[slotHash] = benchmarkHash(uint64((round + 1) * (i + 1) * (j + 1))).Bytes()
		}
		storages[addressHashes[i]] = slots

		if len(codeBlobs) > 0 {
			codes[addr] = codeBlobs[i%len(codeBlobs)]
		}
	}
	return &UBTUpdate{
		BlockNum:       blockNum,
		BlockHash:      benchmarkHash(blockNum),
		ParentHash:     benchmarkHash(blockNum - 1),
		RawStorageKey:  false,
		Accounts:       accounts,
		AccountsOrigin: accountsOrigin,
		Storages:       storages,
		StoragesOrigin: storagesOrigin,
		Codes:          codes,
	}
}

func benchmarkAddress(i int) common.Address {
	var addr common.Address
	binary.BigEndian.PutUint64(addr[12:], uint64(i+1))
	return addr
}

func benchmarkHash(v uint64) common.Hash {
	return common.BigToHash(new(big.Int).SetUint64(v))
}
