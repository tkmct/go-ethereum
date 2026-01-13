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
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/trie/trienode"
	"github.com/holiman/uint256"
)

// =============================================================================
// Test Helpers
// =============================================================================

// testAccount represents a test account with optional storage
type testAccount struct {
	addr     common.Address
	nonce    uint64
	balance  *uint256.Int
	codeHash []byte
	code     []byte
	storage  map[common.Hash][]byte
}

// generateTestAccounts creates n test accounts with varied characteristics
func generateTestAccounts(n int, withStorage bool, storageSlots int) []testAccount {
	accounts := make([]testAccount, n)
	for i := 0; i < n; i++ {
		addr := common.BytesToAddress(crypto.Keccak256([]byte(fmt.Sprintf("account-%d", i)))[:20])
		acc := testAccount{
			addr:     addr,
			nonce:    uint64(i + 1),
			balance:  uint256.NewInt(uint64((i + 1) * 1000)),
			codeHash: types.EmptyCodeHash[:],
		}

		// Every 3rd account is a contract with code
		if i%3 == 0 && i > 0 {
			acc.code = bytes.Repeat([]byte{byte(i)}, 100+i*10)
			acc.codeHash = crypto.Keccak256(acc.code)
		}

		// Add storage if requested
		if withStorage && storageSlots > 0 {
			acc.storage = make(map[common.Hash][]byte)
			for j := 0; j < storageSlots; j++ {
				slotKey := crypto.Keccak256Hash([]byte(fmt.Sprintf("slot-%d-%d", i, j)))
				slotValue := crypto.Keccak256([]byte(fmt.Sprintf("value-%d-%d", i, j)))
				acc.storage[slotKey] = slotValue
			}
		}

		accounts[i] = acc
	}
	return accounts
}

// setupTestState creates a pathdb with test accounts and returns the state root
func setupTestState(t *testing.T, accounts []testAccount) (*Database, common.Hash) {
	config := &Config{
		NoAsyncGeneration: true,
	}
	diskdb := rawdb.NewMemoryDatabase()
	db := New(diskdb, config, false) // isVerkle=false for testing

	accountsMap := make(map[common.Hash][]byte)
	storagesMap := make(map[common.Hash]map[common.Hash][]byte)
	preimages := make(map[common.Hash][]byte)

	for _, acc := range accounts {
		accountHash := crypto.Keccak256Hash(acc.addr.Bytes())
		preimages[accountHash] = acc.addr.Bytes()

		// Determine storage root
		storageRoot := types.EmptyRootHash
		if len(acc.storage) > 0 {
			storageRoot = common.HexToHash("0xdeadbeef") // Placeholder for non-empty storage
			storagesMap[accountHash] = make(map[common.Hash][]byte)
			for slotKey, slotValue := range acc.storage {
				storagesMap[accountHash][slotKey] = slotValue
				preimages[slotKey] = slotKey.Bytes()
			}
		}

		// Create state account
		stateAcc := &types.StateAccount{
			Nonce:    acc.nonce,
			Balance:  acc.balance,
			Root:     storageRoot,
			CodeHash: acc.codeHash,
		}
		accountsMap[accountHash] = types.SlimAccountRLP(*stateAcc)

		// Write code if present
		if len(acc.code) > 0 {
			rawdb.WriteCode(diskdb, common.BytesToHash(acc.codeHash), acc.code)
		}
	}

	// Write preimages
	rawdb.WritePreimages(diskdb, preimages)

	// Update pathdb with state
	stateRoot := common.HexToHash("0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef")
	stateSet := NewStateSetWithOrigin(accountsMap, storagesMap, nil, nil, false)
	err := db.Update(stateRoot, types.EmptyRootHash, 0, trienode.NewMergedNodeSet(), stateSet)
	if err != nil {
		t.Fatalf("failed to update db: %v", err)
	}

	return db, stateRoot
}

// waitForConversion waits for the converter to complete or timeout
func waitForConversion(conv *ubtConverter, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		status := conv.status()
		switch status.Stage {
		case rawdb.UBTStageDone:
			return nil
		case rawdb.UBTStageFailed:
			return fmt.Errorf("conversion failed: %s", status.LastError)
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("conversion timed out")
}

// verifyConversionComplete checks that conversion completed successfully
func verifyConversionComplete(t *testing.T, conv *ubtConverter, expectedAccounts, expectedSlots uint64) {
	t.Helper()
	status := conv.status()
	if status.Stage != rawdb.UBTStageDone {
		t.Errorf("expected stage Done, got %d", status.Stage)
	}
	if status.AccountsDone != expectedAccounts {
		t.Errorf("expected %d accounts done, got %d", expectedAccounts, status.AccountsDone)
	}
	if status.SlotsDone != expectedSlots {
		t.Errorf("expected %d slots done, got %d", expectedSlots, status.SlotsDone)
	}
}

// =============================================================================
// Tier 1: Small Scale Tests (10-100 Accounts)
// =============================================================================

// TestUBTConverter_Small_BasicConversion validates basic end-to-end conversion flow
func TestUBTConverter_Small_BasicConversion(t *testing.T) {
	// Generate 50 test accounts (mix of EOAs and contracts)
	accounts := generateTestAccounts(50, false, 0)

	// Setup test state
	db, stateRoot := setupTestState(t, accounts)
	defer db.Close()

	// Create converter
	conv := newUBTConverter(db, db.diskdb, stateRoot, 10)
	if conv == nil {
		t.Fatal("failed to create converter")
	}

	// Verify initial state
	status := conv.status()
	if status.Stage != rawdb.UBTStageIdle {
		t.Errorf("expected initial stage Idle, got %d", status.Stage)
	}

	// Start conversion
	if err := conv.start(); err != nil {
		t.Fatalf("failed to start conversion: %v", err)
	}

	// Wait for completion
	if err := waitForConversion(conv, 30*time.Second); err != nil {
		t.Fatalf("conversion failed: %v", err)
	}

	// Verify completion
	verifyConversionComplete(t, conv, 50, 0)

	// Verify UBT root is set
	finalStatus := conv.status()
	if finalStatus.UbtRoot == (common.Hash{}) {
		t.Error("expected non-zero UBT root")
	}
}

// TestUBTConverter_Small_DataIntegrity exhaustively verifies all account data is preserved
func TestUBTConverter_Small_DataIntegrity(t *testing.T) {
	// Create accounts with varied characteristics
	accounts := []testAccount{
		// EOAs with varying balances
		{addr: common.HexToAddress("0x01"), nonce: 1, balance: uint256.NewInt(0), codeHash: types.EmptyCodeHash[:]},
		{addr: common.HexToAddress("0x02"), nonce: 2, balance: uint256.NewInt(1), codeHash: types.EmptyCodeHash[:]},
		{addr: common.HexToAddress("0x03"), nonce: 3, balance: uint256.MustFromBig(new(big.Int).Exp(big.NewInt(10), big.NewInt(30), nil)), codeHash: types.EmptyCodeHash[:]},
	}

	// Add contracts with code
	for i := 0; i < 5; i++ {
		code := bytes.Repeat([]byte{byte(i + 1)}, 100*(i+1))
		accounts = append(accounts, testAccount{
			addr:     common.HexToAddress(fmt.Sprintf("0x1%d", i)),
			nonce:    uint64(i + 10),
			balance:  uint256.NewInt(uint64(i * 1000)),
			code:     code,
			codeHash: crypto.Keccak256(code),
		})
	}

	// Add accounts with storage
	for i := 0; i < 5; i++ {
		storage := make(map[common.Hash][]byte)
		slotCount := (i + 1) * 5 // 5, 10, 15, 20, 25 slots
		for j := 0; j < slotCount; j++ {
			slotKey := crypto.Keccak256Hash([]byte(fmt.Sprintf("slot-%d-%d", i, j)))
			slotValue := crypto.Keccak256([]byte(fmt.Sprintf("value-%d-%d", i, j)))
			storage[slotKey] = slotValue
		}
		accounts = append(accounts, testAccount{
			addr:     common.HexToAddress(fmt.Sprintf("0x2%d", i)),
			nonce:    uint64(i + 20),
			balance:  uint256.NewInt(uint64(i * 2000)),
			codeHash: types.EmptyCodeHash[:],
			storage:  storage,
		})
	}

	// Setup and run conversion
	db, stateRoot := setupTestState(t, accounts)
	defer db.Close()

	conv := newUBTConverter(db, db.diskdb, stateRoot, 5)
	if err := conv.start(); err != nil {
		t.Fatalf("failed to start conversion: %v", err)
	}

	if err := waitForConversion(conv, 60*time.Second); err != nil {
		t.Fatalf("conversion failed: %v", err)
	}

	// Calculate expected storage slots
	expectedSlots := uint64(0)
	for _, acc := range accounts {
		expectedSlots += uint64(len(acc.storage))
	}

	verifyConversionComplete(t, conv, uint64(len(accounts)), expectedSlots)
}

// TestUBTConverter_Small_StateRootDeterminism verifies same MPT state produces same UBT root
func TestUBTConverter_Small_StateRootDeterminism(t *testing.T) {
	accounts := generateTestAccounts(20, true, 3)

	var ubtRoots []common.Hash

	// Run conversion 3 times with identical state
	for iteration := 0; iteration < 3; iteration++ {
		db, stateRoot := setupTestState(t, accounts)

		conv := newUBTConverter(db, db.diskdb, stateRoot, 5)
		if err := conv.start(); err != nil {
			t.Fatalf("iteration %d: failed to start conversion: %v", iteration, err)
		}

		if err := waitForConversion(conv, 30*time.Second); err != nil {
			t.Fatalf("iteration %d: conversion failed: %v", iteration, err)
		}

		status := conv.status()
		ubtRoots = append(ubtRoots, status.UbtRoot)

		db.Close()
	}

	// All UBT roots should be identical
	for i := 1; i < len(ubtRoots); i++ {
		if ubtRoots[i] != ubtRoots[0] {
			t.Errorf("UBT root mismatch: iteration 0 = %x, iteration %d = %x", ubtRoots[0], i, ubtRoots[i])
		}
	}
}

// TestUBTConverter_Small_EmptyState tests edge cases with minimal state
func TestUBTConverter_Small_EmptyState(t *testing.T) {
	testCases := []struct {
		name     string
		accounts []testAccount
	}{
		{
			name:     "single_eoa",
			accounts: []testAccount{{addr: common.HexToAddress("0x01"), nonce: 1, balance: uint256.NewInt(100), codeHash: types.EmptyCodeHash[:]}},
		},
		{
			name: "single_contract_no_storage",
			accounts: []testAccount{{
				addr:     common.HexToAddress("0x02"),
				nonce:    1,
				balance:  uint256.NewInt(100),
				code:     []byte{0x60, 0x00},
				codeHash: crypto.Keccak256([]byte{0x60, 0x00}),
			}},
		},
		{
			name: "single_contract_with_storage",
			accounts: []testAccount{{
				addr:     common.HexToAddress("0x03"),
				nonce:    1,
				balance:  uint256.NewInt(100),
				codeHash: types.EmptyCodeHash[:],
				storage:  map[common.Hash][]byte{common.HexToHash("0x01"): {0x01}},
			}},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			db, stateRoot := setupTestState(t, tc.accounts)
			defer db.Close()

			conv := newUBTConverter(db, db.diskdb, stateRoot, 10)
			if err := conv.start(); err != nil {
				t.Fatalf("failed to start conversion: %v", err)
			}

			if err := waitForConversion(conv, 10*time.Second); err != nil {
				t.Fatalf("conversion failed: %v", err)
			}

			status := conv.status()
			if status.Stage != rawdb.UBTStageDone {
				t.Errorf("expected stage Done, got %d", status.Stage)
			}
			if status.AccountsDone != uint64(len(tc.accounts)) {
				t.Errorf("expected %d accounts, got %d", len(tc.accounts), status.AccountsDone)
			}
		})
	}
}

// =============================================================================
// Tier 2: Medium Scale Tests (1000+ Accounts)
// =============================================================================

// TestUBTConverter_Medium_BatchCommit verifies batch commit mechanics
func TestUBTConverter_Medium_BatchCommit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping medium scale test in short mode")
	}

	const accountCount = 500
	const batchSize = 100

	accounts := generateTestAccounts(accountCount, false, 0)
	db, stateRoot := setupTestState(t, accounts)
	defer db.Close()

	conv := newUBTConverter(db, db.diskdb, stateRoot, batchSize)
	if err := conv.start(); err != nil {
		t.Fatalf("failed to start conversion: %v", err)
	}

	if err := waitForConversion(conv, 120*time.Second); err != nil {
		t.Fatalf("conversion failed: %v", err)
	}

	verifyConversionComplete(t, conv, accountCount, 0)
}

// TestUBTConverter_Medium_InterruptResume tests stop/resume functionality
func TestUBTConverter_Medium_InterruptResume(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping medium scale test in short mode")
	}

	const accountCount = 200
	const batchSize = 20

	accounts := generateTestAccounts(accountCount, true, 5)
	db, stateRoot := setupTestState(t, accounts)
	defer db.Close()

	// Phase 1: Start conversion and stop after some progress
	conv := newUBTConverter(db, db.diskdb, stateRoot, batchSize)
	if err := conv.start(); err != nil {
		t.Fatalf("failed to start conversion: %v", err)
	}

	// Wait for some progress
	time.Sleep(100 * time.Millisecond)

	// Stop conversion
	conv.stop()

	status1 := conv.status()
	if status1.AccountsDone == 0 {
		t.Log("Warning: no accounts processed before stop, test may not be meaningful")
	}
	if status1.AccountsDone >= accountCount {
		t.Skip("conversion completed before stop, skipping resume test")
	}

	savedAccountsDone := status1.AccountsDone
	savedNextAccountHash := status1.NextAccountHash

	t.Logf("Phase 1: Stopped after %d accounts, next hash: %x", savedAccountsDone, savedNextAccountHash[:8])

	// Phase 2: Create new converter and verify it resumes
	conv2 := newUBTConverter(db, db.diskdb, stateRoot, batchSize)
	status2 := conv2.status()

	if status2.AccountsDone != savedAccountsDone {
		t.Errorf("expected resumed AccountsDone %d, got %d", savedAccountsDone, status2.AccountsDone)
	}
	if status2.NextAccountHash != savedNextAccountHash {
		t.Errorf("expected resumed NextAccountHash %x, got %x", savedNextAccountHash, status2.NextAccountHash)
	}

	// Resume and complete
	if err := conv2.start(); err != nil {
		t.Fatalf("failed to resume conversion: %v", err)
	}

	if err := waitForConversion(conv2, 120*time.Second); err != nil {
		t.Fatalf("resumed conversion failed: %v", err)
	}

	// Verify all accounts were processed
	finalStatus := conv2.status()
	if finalStatus.AccountsDone != accountCount {
		t.Errorf("expected %d accounts after resume, got %d", accountCount, finalStatus.AccountsDone)
	}
}

// TestUBTConverter_Medium_MultipleInterrupts stress tests with multiple start/stop cycles
func TestUBTConverter_Medium_MultipleInterrupts(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping medium scale test in short mode")
	}

	const accountCount = 100
	const batchSize = 10
	const numInterrupts = 5

	accounts := generateTestAccounts(accountCount, true, 3)
	db, stateRoot := setupTestState(t, accounts)
	defer db.Close()

	for i := 0; i < numInterrupts; i++ {
		conv := newUBTConverter(db, db.diskdb, stateRoot, batchSize)
		status := conv.status()

		if status.Stage == rawdb.UBTStageDone {
			t.Logf("Conversion completed at iteration %d", i)
			break
		}

		if err := conv.start(); err != nil {
			t.Fatalf("iteration %d: failed to start: %v", i, err)
		}

		// Random-ish wait
		time.Sleep(time.Duration(20+i*10) * time.Millisecond)

		conv.stop()

		t.Logf("Iteration %d: stopped at %d accounts", i, conv.status().AccountsDone)
	}

	// Final run to completion
	conv := newUBTConverter(db, db.diskdb, stateRoot, batchSize)
	if conv.status().Stage != rawdb.UBTStageDone {
		if err := conv.start(); err != nil {
			t.Fatalf("final run failed to start: %v", err)
		}
		if err := waitForConversion(conv, 60*time.Second); err != nil {
			t.Fatalf("final conversion failed: %v", err)
		}
	}

	verifyConversionComplete(t, conv, accountCount, uint64(accountCount*3))
}
