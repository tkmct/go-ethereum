// Copyright 2025 The go-ethereum Authors
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

package pathdb

import (
	"bytes"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/trie/trienode"
	"github.com/holiman/uint256"
)

func TestNewUBTConverter(t *testing.T) {
	diskdb := rawdb.NewMemoryDatabase()
	root := common.HexToHash("0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef")

	conv := newUBTConverter(nil, diskdb, root, 0)

	if conv == nil {
		t.Fatal("newUBTConverter returned nil")
	}
	if conv.batchSize != 1000 {
		t.Errorf("expected default batchSize 1000, got %d", conv.batchSize)
	}
	if conv.root != root {
		t.Errorf("expected root %x, got %x", root, conv.root)
	}
	if conv.progress.Stage != rawdb.UBTStageIdle {
		t.Errorf("expected stage Idle, got %d", conv.progress.Stage)
	}
	if conv.progress.AccountsDone != 0 {
		t.Errorf("expected AccountsDone 0, got %d", conv.progress.AccountsDone)
	}
	if conv.progress.SlotsDone != 0 {
		t.Errorf("expected SlotsDone 0, got %d", conv.progress.SlotsDone)
	}
}

func TestNewUBTConverterCustomBatchSize(t *testing.T) {
	diskdb := rawdb.NewMemoryDatabase()
	root := common.HexToHash("0x1234")

	conv := newUBTConverter(nil, diskdb, root, 500)

	if conv.batchSize != 500 {
		t.Errorf("expected batchSize 500, got %d", conv.batchSize)
	}
}

func TestNewUBTConverterResumesProgress(t *testing.T) {
	diskdb := rawdb.NewMemoryDatabase()
	root := common.HexToHash("0x1234")

	savedProgress := &rawdb.UBTConversionProgress{
		Version:         1,
		Stage:           rawdb.UBTStageRunning,
		StateRoot:       root,
		AccountsDone:    100,
		SlotsDone:       500,
		NextAccountHash: common.HexToHash("0xabcd"),
		UpdatedAt:       uint64(time.Now().Unix()),
	}
	rawdb.WriteUBTConversionStatus(diskdb, savedProgress)

	conv := newUBTConverter(nil, diskdb, root, 0)

	if conv.progress.Stage != rawdb.UBTStageRunning {
		t.Errorf("expected stage Running, got %d", conv.progress.Stage)
	}
	if conv.progress.AccountsDone != 100 {
		t.Errorf("expected AccountsDone 100, got %d", conv.progress.AccountsDone)
	}
	if conv.progress.SlotsDone != 500 {
		t.Errorf("expected SlotsDone 500, got %d", conv.progress.SlotsDone)
	}
	if conv.progress.NextAccountHash != common.HexToHash("0xabcd") {
		t.Errorf("expected NextAccountHash 0xabcd, got %x", conv.progress.NextAccountHash)
	}
}

func TestUBTConverterStatus(t *testing.T) {
	diskdb := rawdb.NewMemoryDatabase()
	root := common.HexToHash("0x1234")

	conv := newUBTConverter(nil, diskdb, root, 0)

	status1 := conv.status()
	if status1 == nil {
		t.Fatal("status returned nil")
	}

	status1.AccountsDone = 999
	status1.Stage = rawdb.UBTStageDone

	status2 := conv.status()
	if status2.AccountsDone != 0 {
		t.Errorf("status modification affected original: AccountsDone = %d", status2.AccountsDone)
	}
	if status2.Stage != rawdb.UBTStageIdle {
		t.Errorf("status modification affected original: Stage = %d", status2.Stage)
	}
}

func TestIncHash(t *testing.T) {
	tests := []struct {
		name     string
		input    common.Hash
		expected common.Hash
	}{
		{
			name:     "zero hash",
			input:    common.Hash{},
			expected: common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000001"),
		},
		{
			name:     "increment last byte",
			input:    common.HexToHash("0x00000000000000000000000000000000000000000000000000000000000000ff"),
			expected: common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000100"),
		},
		{
			name:     "increment middle",
			input:    common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000001234"),
			expected: common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000001235"),
		},
		{
			name:     "cascade overflow",
			input:    common.HexToHash("0x000000000000000000000000000000000000000000000000000000000000ffff"),
			expected: common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000010000"),
		},
		{
			name:     "max hash wraps to zero",
			input:    common.HexToHash("0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"),
			expected: common.Hash{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := incHash(tc.input)
			if got != tc.expected {
				t.Errorf("expected %x, got %x", tc.expected, got)
			}
		})
	}
}

func TestUBTConverterStartSetsRunning(t *testing.T) {
	diskdb := rawdb.NewMemoryDatabase()
	root := common.HexToHash("0x1234")

	conv := newUBTConverter(nil, diskdb, root, 0)

	if conv.progress.Stage != rawdb.UBTStageIdle {
		t.Fatalf("expected initial stage Idle, got %d", conv.progress.Stage)
	}

	conv.lock.Lock()
	conv.progress.Stage = rawdb.UBTStageRunning
	conv.lock.Unlock()

	status := conv.status()
	if status.Stage != rawdb.UBTStageRunning {
		t.Errorf("expected stage Running, got %d", status.Stage)
	}
}

func TestUBTConverterStopOnIdleIsNoop(t *testing.T) {
	diskdb := rawdb.NewMemoryDatabase()
	root := common.HexToHash("0x1234")

	conv := newUBTConverter(nil, diskdb, root, 0)

	conv.stop()
}

func TestUBTConverterStartTwiceFails(t *testing.T) {
	diskdb := rawdb.NewMemoryDatabase()
	root := common.HexToHash("0x1234")

	conv := newUBTConverter(nil, diskdb, root, 0)

	conv.lock.Lock()
	conv.progress.Stage = rawdb.UBTStageRunning
	conv.lock.Unlock()

	err := conv.start()
	if err == nil {
		t.Error("expected error when starting already-running converter")
	}
}

// TestUBTConverterE2E tests the full conversion flow with actual state data.
func TestUBTConverterE2E(t *testing.T) {
	diskdb := rawdb.NewMemoryDatabase()

	// Create test addresses and their hashes
	addr1 := common.HexToAddress("0x1111111111111111111111111111111111111111")
	addr2 := common.HexToAddress("0x2222222222222222222222222222222222222222")
	accountHash1 := crypto.Keccak256Hash(addr1.Bytes())
	accountHash2 := crypto.Keccak256Hash(addr2.Bytes())

	// Write preimages for accounts
	rawdb.WritePreimages(diskdb, map[common.Hash][]byte{
		accountHash1: addr1.Bytes(),
		accountHash2: addr2.Bytes(),
	})

	// Verify preimages are readable
	preimage1 := rawdb.ReadPreimage(diskdb, accountHash1)
	if !bytes.Equal(preimage1, addr1.Bytes()) {
		t.Fatalf("preimage1 mismatch: got %x, want %x", preimage1, addr1.Bytes())
	}
	preimage2 := rawdb.ReadPreimage(diskdb, accountHash2)
	if !bytes.Equal(preimage2, addr2.Bytes()) {
		t.Fatalf("preimage2 mismatch: got %x, want %x", preimage2, addr2.Bytes())
	}

	// Test progress state transitions
	progress := &rawdb.UBTConversionProgress{
		Version:   1,
		Stage:     rawdb.UBTStageIdle,
		StateRoot: common.HexToHash("0xabcd"),
	}
	rawdb.WriteUBTConversionStatus(diskdb, progress)

	read := rawdb.ReadUBTConversionStatus(diskdb)
	if read.Stage != rawdb.UBTStageIdle {
		t.Errorf("expected Idle stage, got %d", read.Stage)
	}

	// Transition to Running
	progress.Stage = rawdb.UBTStageRunning
	rawdb.WriteUBTConversionStatus(diskdb, progress)

	read = rawdb.ReadUBTConversionStatus(diskdb)
	if read.Stage != rawdb.UBTStageRunning {
		t.Errorf("expected Running stage, got %d", read.Stage)
	}

	// Transition to Done
	progress.Stage = rawdb.UBTStageDone
	progress.AccountsDone = 100
	progress.SlotsDone = 500
	rawdb.WriteUBTConversionStatus(diskdb, progress)

	read = rawdb.ReadUBTConversionStatus(diskdb)
	if read.Stage != rawdb.UBTStageDone {
		t.Errorf("expected Done stage, got %d", read.Stage)
	}
	if read.AccountsDone != 100 {
		t.Errorf("expected 100 accounts, got %d", read.AccountsDone)
	}
	if read.SlotsDone != 500 {
		t.Errorf("expected 500 slots, got %d", read.SlotsDone)
	}
}

// TestUBTConverterWithRealState tests the conversion with actual pathdb state.
// Note: This test uses isVerkle=false because verkle mode disables snapshot
// generation, which prevents iterator access. In production, the converter
// will run after snapshot sync is complete.
func TestUBTConverterWithRealState(t *testing.T) {
	// Create a pathdb with test state
	// Use isVerkle=false to allow snapshot iteration in tests
	config := &Config{
		NoAsyncGeneration: true,
	}
	diskdb := rawdb.NewMemoryDatabase()
	db := New(diskdb, config, false) // isVerkle=false for testing

	// Create test addresses
	addr1 := common.HexToAddress("0x1111111111111111111111111111111111111111")
	addr2 := common.HexToAddress("0x2222222222222222222222222222222222222222")
	accountHash1 := crypto.Keccak256Hash(addr1.Bytes())
	accountHash2 := crypto.Keccak256Hash(addr2.Bytes())

	// Write preimages for accounts
	rawdb.WritePreimages(diskdb, map[common.Hash][]byte{
		accountHash1: addr1.Bytes(),
		accountHash2: addr2.Bytes(),
	})

	// Create account data (slim RLP format for snapshot)
	acc1 := &types.StateAccount{
		Nonce:    1,
		Balance:  uint256.NewInt(1000),
		Root:     types.EmptyRootHash,
		CodeHash: types.EmptyCodeHash[:],
	}
	acc2 := &types.StateAccount{
		Nonce:    2,
		Balance:  uint256.NewInt(2000),
		Root:     types.EmptyRootHash,
		CodeHash: types.EmptyCodeHash[:],
	}

	// Create state set for accounts
	accounts := map[common.Hash][]byte{
		accountHash1: types.SlimAccountRLP(*acc1),
		accountHash2: types.SlimAccountRLP(*acc2),
	}

	// Update pathdb with state - use EmptyRootHash as parent for non-verkle
	stateRoot := common.HexToHash("0x02")
	stateSet := NewStateSetWithOrigin(accounts, nil, nil, nil, false)
	err := db.Update(stateRoot, types.EmptyRootHash, 0, trienode.NewMergedNodeSet(), stateSet)
	if err != nil {
		t.Fatalf("failed to update db: %v", err)
	}

	// Create converter
	conv := newUBTConverter(db, diskdb, stateRoot, 10)

	// Verify initial state
	status := conv.status()
	if status.Stage != rawdb.UBTStageIdle {
		t.Errorf("expected initial stage Idle, got %d", status.Stage)
	}
	if status.AccountsDone != 0 {
		t.Errorf("expected 0 accounts done initially, got %d", status.AccountsDone)
	}

	// Verify that the converter was created with correct settings
	if conv.batchSize != 10 {
		t.Errorf("expected batchSize 10, got %d", conv.batchSize)
	}
	if conv.root != stateRoot {
		t.Errorf("expected root %x, got %x", stateRoot, conv.root)
	}

	// Verify account iterator works with the state
	acctIter, err := db.AccountIterator(stateRoot, common.Hash{})
	if err != nil {
		t.Fatalf("failed to create account iterator: %v", err)
	}
	defer acctIter.Release()

	accountCount := 0
	for acctIter.Next() {
		accountCount++
	}
	if accountCount != 2 {
		t.Errorf("expected 2 accounts, got %d", accountCount)
	}
}

// TestUBTConverterProgressPersistence tests that progress is correctly persisted and loaded.
func TestUBTConverterProgressPersistence(t *testing.T) {
	diskdb := rawdb.NewMemoryDatabase()
	root := common.HexToHash("0x1234")

	// Create initial progress
	progress := &rawdb.UBTConversionProgress{
		Version:         1,
		Stage:           rawdb.UBTStageRunning,
		StateRoot:       root,
		UbtRoot:         common.HexToHash("0x5678"),
		NextAccountHash: common.HexToHash("0xaaaa"),
		CurrentAccount:  common.HexToHash("0xbbbb"),
		NextStorageHash: common.HexToHash("0xcccc"),
		AccountsDone:    50,
		SlotsDone:       200,
		LastError:       "",
		UpdatedAt:       uint64(time.Now().Unix()),
	}
	rawdb.WriteUBTConversionStatus(diskdb, progress)

	// Create converter - should load existing progress
	conv := newUBTConverter(nil, diskdb, root, 0)

	status := conv.status()
	if status.Stage != rawdb.UBTStageRunning {
		t.Errorf("expected stage Running, got %d", status.Stage)
	}
	if status.AccountsDone != 50 {
		t.Errorf("expected 50 accounts done, got %d", status.AccountsDone)
	}
	if status.SlotsDone != 200 {
		t.Errorf("expected 200 slots done, got %d", status.SlotsDone)
	}
	if status.NextAccountHash != common.HexToHash("0xaaaa") {
		t.Errorf("expected NextAccountHash 0xaaaa, got %x", status.NextAccountHash)
	}
	if status.UbtRoot != common.HexToHash("0x5678") {
		t.Errorf("expected UbtRoot 0x5678, got %x", status.UbtRoot)
	}
}

// TestUBTConverterFailedState tests handling of failed conversion state.
func TestUBTConverterFailedState(t *testing.T) {
	diskdb := rawdb.NewMemoryDatabase()
	root := common.HexToHash("0x1234")

	// Create failed progress
	progress := &rawdb.UBTConversionProgress{
		Version:      1,
		Stage:        rawdb.UBTStageFailed,
		StateRoot:    root,
		AccountsDone: 25,
		SlotsDone:    100,
		LastError:    "test error message",
		UpdatedAt:    uint64(time.Now().Unix()),
	}
	rawdb.WriteUBTConversionStatus(diskdb, progress)

	// Read back and verify
	read := rawdb.ReadUBTConversionStatus(diskdb)
	if read.Stage != rawdb.UBTStageFailed {
		t.Errorf("expected stage Failed, got %d", read.Stage)
	}
	if read.LastError != "test error message" {
		t.Errorf("expected error message 'test error message', got '%s'", read.LastError)
	}
	if read.AccountsDone != 25 {
		t.Errorf("expected 25 accounts done, got %d", read.AccountsDone)
	}
}

// TestUBTConverterWithStorage tests conversion of accounts with storage.
// Note: This test uses isVerkle=false because verkle mode disables snapshot
// generation, which prevents iterator access.
func TestUBTConverterWithStorage(t *testing.T) {
	// Create a pathdb with test state including storage
	config := &Config{
		NoAsyncGeneration: true,
	}
	diskdb := rawdb.NewMemoryDatabase()
	db := New(diskdb, config, false) // isVerkle=false for testing

	// Create test address
	addr := common.HexToAddress("0x1111111111111111111111111111111111111111")
	accountHash := crypto.Keccak256Hash(addr.Bytes())

	// Create test storage keys - use simple hex hashes for iteration
	storageHash1 := common.HexToHash("0x01")
	storageHash2 := common.HexToHash("0x02")

	// Write preimages for accounts and storage keys
	rawdb.WritePreimages(diskdb, map[common.Hash][]byte{
		accountHash:  addr.Bytes(),
		storageHash1: storageHash1.Bytes(),
		storageHash2: storageHash2.Bytes(),
	})

	// Create account data with non-empty storage root
	storageRoot := common.HexToHash("0xdeadbeef")
	acc := &types.StateAccount{
		Nonce:    1,
		Balance:  uint256.NewInt(1000),
		Root:     storageRoot, // Non-empty storage
		CodeHash: types.EmptyCodeHash[:],
	}

	// Create state set for accounts
	accounts := map[common.Hash][]byte{
		accountHash: types.SlimAccountRLP(*acc),
	}

	// Create storage data
	storages := map[common.Hash]map[common.Hash][]byte{
		accountHash: {
			storageHash1: []byte{0x01, 0x02, 0x03},
			storageHash2: []byte{0x04, 0x05, 0x06},
		},
	}

	// Update pathdb with state - use EmptyRootHash as parent for non-verkle
	stateRoot := common.HexToHash("0x02")
	stateSet := NewStateSetWithOrigin(accounts, storages, nil, nil, false)
	err := db.Update(stateRoot, types.EmptyRootHash, 0, trienode.NewMergedNodeSet(), stateSet)
	if err != nil {
		t.Fatalf("failed to update db: %v", err)
	}

	// Create converter
	conv := newUBTConverter(db, diskdb, stateRoot, 10)

	// Verify initial state
	status := conv.status()
	if status.Stage != rawdb.UBTStageIdle {
		t.Errorf("expected initial stage Idle, got %d", status.Stage)
	}

	// Verify account iterator works
	acctIter, err := db.AccountIterator(stateRoot, common.Hash{})
	if err != nil {
		t.Fatalf("failed to create account iterator: %v", err)
	}
	defer acctIter.Release()

	accountCount := 0
	for acctIter.Next() {
		accountCount++
	}
	if accountCount != 1 {
		t.Errorf("expected 1 account, got %d", accountCount)
	}

	// Verify storage was set up correctly by checking iterator
	storageIter, err := db.StorageIterator(stateRoot, accountHash, common.Hash{})
	if err != nil {
		t.Fatalf("failed to create storage iterator: %v", err)
	}
	defer storageIter.Release()

	storageCount := 0
	for storageIter.Next() {
		storageCount++
	}
	if storageCount != 2 {
		t.Errorf("expected 2 storage slots, got %d", storageCount)
	}
}
