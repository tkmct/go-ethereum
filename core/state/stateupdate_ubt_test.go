// Copyright 2024 The go-ethereum Authors
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

package state

import (
	"bytes"
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/ethereum/go-ethereum/trie/trienode"
	"github.com/holiman/uint256"
)

// TestToUBTDiff_BasicAccountUpdate verifies conversion of a simple account update.
func TestToUBTDiff_BasicAccountUpdate(t *testing.T) {
	addr := common.HexToAddress("0xaaaa")
	addrHash := crypto.Keccak256Hash(addr.Bytes())

	// Create a slim-RLP encoded account
	acct := &types.SlimAccount{
		Nonce:   5,
		Balance: uint256.NewInt(1000),
	}
	newData, _ := rlp.EncodeToBytes(acct)
	oldData, _ := rlp.EncodeToBytes(&types.SlimAccount{
		Nonce:   4,
		Balance: uint256.NewInt(800),
	})

	updates := map[common.Hash]*accountUpdate{
		addrHash: {
			address:              addr,
			data:                 newData,
			origin:               oldData,
			storages:             make(map[common.Hash][]byte),
			storagesOriginByKey:  make(map[common.Hash][]byte),
			storagesOriginByHash: make(map[common.Hash][]byte),
		},
	}

	su := newStateUpdate(true, common.HexToHash("0x01"), common.HexToHash("0x02"), 1,
		make(map[common.Hash]*accountDelete), updates, trienode.NewMergedNodeSet())

	diff, err := su.ToUBTDiff()
	if err != nil {
		t.Fatal(err)
	}
	if diff.OriginRoot != common.HexToHash("0x01") {
		t.Fatal("origin root mismatch")
	}
	if diff.Root != common.HexToHash("0x02") {
		t.Fatal("root mismatch")
	}
	if len(diff.Accounts) != 1 {
		t.Fatalf("expected 1 account, got %d", len(diff.Accounts))
	}
	if !diff.Accounts[0].Alive {
		t.Fatal("expected account to be alive")
	}
	if diff.Accounts[0].Nonce != 5 {
		t.Fatalf("nonce mismatch: got %d, want 5", diff.Accounts[0].Nonce)
	}
	if diff.Accounts[0].Balance.Cmp(big.NewInt(1000)) != 0 {
		t.Fatalf("balance mismatch: got %v, want 1000", diff.Accounts[0].Balance)
	}
	if diff.Accounts[0].Address != addr {
		t.Fatalf("address mismatch: got %v, want %v", diff.Accounts[0].Address, addr)
	}
}

// TestToUBTDiff_AccountDeletion verifies conversion of an account deletion.
func TestToUBTDiff_AccountDeletion(t *testing.T) {
	addr := common.HexToAddress("0xbbbb")
	addrHash := crypto.Keccak256Hash(addr.Bytes())

	oldData, _ := rlp.EncodeToBytes(&types.SlimAccount{
		Nonce:   1,
		Balance: uint256.NewInt(500),
	})

	deletes := map[common.Hash]*accountDelete{
		addrHash: {
			address:        addr,
			origin:         oldData,
			storages:       make(map[common.Hash][]byte),
			storagesOrigin: make(map[common.Hash][]byte),
		},
	}

	su := newStateUpdate(true, common.HexToHash("0x01"), common.HexToHash("0x02"), 1,
		deletes, make(map[common.Hash]*accountUpdate), trienode.NewMergedNodeSet())

	diff, err := su.ToUBTDiff()
	if err != nil {
		t.Fatal(err)
	}
	if len(diff.Accounts) != 1 {
		t.Fatalf("expected 1 account, got %d", len(diff.Accounts))
	}
	if diff.Accounts[0].Alive {
		t.Fatal("deleted account should not be alive")
	}
	if diff.Accounts[0].Address != addr {
		t.Fatalf("address mismatch: got %v, want %v", diff.Accounts[0].Address, addr)
	}
	// Deleted account should have zero balance
	if diff.Accounts[0].Balance.Sign() != 0 {
		t.Fatalf("deleted account balance should be zero, got %v", diff.Accounts[0].Balance)
	}
}

// TestToUBTDiff_StorageUpdate verifies conversion of storage slot updates.
func TestToUBTDiff_StorageUpdate(t *testing.T) {
	addr := common.HexToAddress("0xcccc")
	addrHash := crypto.Keccak256Hash(addr.Bytes())
	rawKey := common.HexToHash("0x0123456789abcdef")
	hashedKey := crypto.Keccak256Hash(rawKey.Bytes())

	// RLP-encode a storage value (prefix-zero-trimmed)
	newVal, _ := rlp.EncodeToBytes(common.HexToHash("0xff").Bytes())
	oldVal, _ := rlp.EncodeToBytes(common.HexToHash("0xee").Bytes())

	acctData, _ := rlp.EncodeToBytes(&types.SlimAccount{
		Nonce:   1,
		Balance: uint256.NewInt(100),
	})
	oldAcctData, _ := rlp.EncodeToBytes(&types.SlimAccount{
		Balance: uint256.NewInt(100),
	})

	updates := map[common.Hash]*accountUpdate{
		addrHash: {
			address:              addr,
			data:                 acctData,
			origin:               oldAcctData,
			storages:             map[common.Hash][]byte{hashedKey: newVal},
			storagesOriginByKey:  map[common.Hash][]byte{rawKey: oldVal},
			storagesOriginByHash: map[common.Hash][]byte{hashedKey: oldVal},
		},
	}

	su := newStateUpdate(true, common.HexToHash("0x01"), common.HexToHash("0x02"), 1,
		make(map[common.Hash]*accountDelete), updates, trienode.NewMergedNodeSet())

	diff, err := su.ToUBTDiff()
	if err != nil {
		t.Fatal(err)
	}
	if len(diff.Storage) != 1 {
		t.Fatalf("expected 1 storage entry, got %d", len(diff.Storage))
	}
	if diff.Storage[0].SlotKeyRaw != rawKey {
		t.Fatalf("raw key mismatch: got %v, want %v", diff.Storage[0].SlotKeyRaw, rawKey)
	}
	if diff.Storage[0].Address != addr {
		t.Fatalf("address mismatch: got %v, want %v", diff.Storage[0].Address, addr)
	}
	// The value should be decoded from RLP
	expectedValue := common.HexToHash("0xff")
	if diff.Storage[0].Value != expectedValue {
		t.Fatalf("value mismatch: got %v, want %v", diff.Storage[0].Value, expectedValue)
	}
}

// TestToUBTDiff_StorageDeletion verifies storage slot deletion (value becomes zero).
func TestToUBTDiff_StorageDeletion(t *testing.T) {
	addr := common.HexToAddress("0xdddd")
	addrHash := crypto.Keccak256Hash(addr.Bytes())
	rawKey := common.HexToHash("0x01")
	hashedKey := crypto.Keccak256Hash(rawKey.Bytes())

	oldVal, _ := rlp.EncodeToBytes(common.HexToHash("0xabcd").Bytes())

	acctData, _ := rlp.EncodeToBytes(&types.SlimAccount{
		Nonce:   1,
		Balance: uint256.NewInt(100),
	})

	updates := map[common.Hash]*accountUpdate{
		addrHash: {
			address:              addr,
			data:                 acctData,
			origin:               acctData,
			storages:             map[common.Hash][]byte{hashedKey: nil}, // nil means deleted
			storagesOriginByKey:  map[common.Hash][]byte{rawKey: oldVal},
			storagesOriginByHash: map[common.Hash][]byte{hashedKey: oldVal},
		},
	}

	su := newStateUpdate(true, common.HexToHash("0x01"), common.HexToHash("0x02"), 1,
		make(map[common.Hash]*accountDelete), updates, trienode.NewMergedNodeSet())

	diff, err := su.ToUBTDiff()
	if err != nil {
		t.Fatal(err)
	}
	if len(diff.Storage) != 1 {
		t.Fatalf("expected 1 storage entry, got %d", len(diff.Storage))
	}
	// Deleted storage should have zero value
	if diff.Storage[0].Value != (common.Hash{}) {
		t.Fatalf("deleted storage value should be zero, got %v", diff.Storage[0].Value)
	}
}

// TestToUBTDiff_RawKeyRequired verifies that conversion fails without raw storage keys.
func TestToUBTDiff_RawKeyRequired(t *testing.T) {
	addr := common.HexToAddress("0xaaaa")
	addrHash := crypto.Keccak256Hash(addr.Bytes())
	hashedKey := common.HexToHash("0x01")

	acctData, _ := rlp.EncodeToBytes(&types.SlimAccount{Balance: uint256.NewInt(100)})

	updates := map[common.Hash]*accountUpdate{
		addrHash: {
			address: addr,
			data:    acctData,
			origin:  acctData,
			storages: map[common.Hash][]byte{
				hashedKey: []byte{0x01},
			},
			storagesOriginByKey: make(map[common.Hash][]byte),
			storagesOriginByHash: map[common.Hash][]byte{
				hashedKey: []byte{0x01},
			},
		},
	}

	// rawStorageKey=false should fail when storage exists
	su := newStateUpdate(false, common.Hash{}, common.Hash{}, 1,
		make(map[common.Hash]*accountDelete), updates, trienode.NewMergedNodeSet())

	_, err := su.ToUBTDiff()
	if err == nil {
		t.Fatal("expected error for non-raw storage keys")
	}
	if !errors.Is(err, ErrRawStorageKeyMissing) {
		t.Fatalf("expected ErrRawStorageKeyMissing, got: %v", err)
	}
	if err.Error() == "ErrRawStorageKeyMissing" {
		t.Fatalf("unexpected error message: %v", err)
	}
}

// TestToUBTDiff_SortedAccounts verifies accounts are sorted by address.
func TestToUBTDiff_SortedAccounts(t *testing.T) {
	addrs := []common.Address{
		common.HexToAddress("0xcccc"),
		common.HexToAddress("0xaaaa"),
		common.HexToAddress("0xbbbb"),
	}

	updates := make(map[common.Hash]*accountUpdate)
	for _, addr := range addrs {
		addrHash := crypto.Keccak256Hash(addr.Bytes())
		acctData, _ := rlp.EncodeToBytes(&types.SlimAccount{
			Nonce:   1,
			Balance: uint256.NewInt(100),
		})
		updates[addrHash] = &accountUpdate{
			address:              addr,
			data:                 acctData,
			origin:               acctData,
			storages:             make(map[common.Hash][]byte),
			storagesOriginByKey:  make(map[common.Hash][]byte),
			storagesOriginByHash: make(map[common.Hash][]byte),
		}
	}

	su := newStateUpdate(true, common.Hash{}, common.Hash{}, 1,
		make(map[common.Hash]*accountDelete), updates, trienode.NewMergedNodeSet())

	diff, err := su.ToUBTDiff()
	if err != nil {
		t.Fatal(err)
	}
	if len(diff.Accounts) != 3 {
		t.Fatalf("expected 3 accounts, got %d", len(diff.Accounts))
	}

	// Verify sorted by address bytes
	for i := 1; i < len(diff.Accounts); i++ {
		if bytes.Compare(diff.Accounts[i-1].Address[:], diff.Accounts[i].Address[:]) >= 0 {
			t.Fatalf("accounts not sorted at index %d: %v >= %v",
				i, diff.Accounts[i-1].Address, diff.Accounts[i].Address)
		}
	}
}

// TestToUBTDiff_SortedStorage verifies storage entries are sorted by (address, slotKeyRaw).
func TestToUBTDiff_SortedStorage(t *testing.T) {
	addr1 := common.HexToAddress("0xbbbb")
	addr2 := common.HexToAddress("0xaaaa")

	// Create storage for two accounts with multiple slots each
	updates := make(map[common.Hash]*accountUpdate)

	// Account 1 with two storage slots
	addr1Hash := crypto.Keccak256Hash(addr1.Bytes())
	rawKey1a := common.HexToHash("0x02")
	rawKey1b := common.HexToHash("0x01")
	hashedKey1a := crypto.Keccak256Hash(rawKey1a.Bytes())
	hashedKey1b := crypto.Keccak256Hash(rawKey1b.Bytes())

	val1, _ := rlp.EncodeToBytes(common.HexToHash("0xff").Bytes())
	val2, _ := rlp.EncodeToBytes(common.HexToHash("0xee").Bytes())

	acctData1, _ := rlp.EncodeToBytes(&types.SlimAccount{Balance: uint256.NewInt(100)})

	updates[addr1Hash] = &accountUpdate{
		address: addr1,
		data:    acctData1,
		origin:  acctData1,
		storages: map[common.Hash][]byte{
			hashedKey1a: val1,
			hashedKey1b: val2,
		},
		storagesOriginByKey: map[common.Hash][]byte{
			rawKey1a: val1,
			rawKey1b: val2,
		},
		storagesOriginByHash: map[common.Hash][]byte{
			hashedKey1a: val1,
			hashedKey1b: val2,
		},
	}

	// Account 2 with one storage slot
	addr2Hash := crypto.Keccak256Hash(addr2.Bytes())
	rawKey2 := common.HexToHash("0x03")
	hashedKey2 := crypto.Keccak256Hash(rawKey2.Bytes())

	acctData2, _ := rlp.EncodeToBytes(&types.SlimAccount{Balance: uint256.NewInt(200)})

	updates[addr2Hash] = &accountUpdate{
		address: addr2,
		data:    acctData2,
		origin:  acctData2,
		storages: map[common.Hash][]byte{
			hashedKey2: val1,
		},
		storagesOriginByKey: map[common.Hash][]byte{
			rawKey2: val1,
		},
		storagesOriginByHash: map[common.Hash][]byte{
			hashedKey2: val1,
		},
	}

	su := newStateUpdate(true, common.Hash{}, common.Hash{}, 1,
		make(map[common.Hash]*accountDelete), updates, trienode.NewMergedNodeSet())

	diff, err := su.ToUBTDiff()
	if err != nil {
		t.Fatal(err)
	}
	if len(diff.Storage) != 3 {
		t.Fatalf("expected 3 storage entries, got %d", len(diff.Storage))
	}

	// Verify sorted by (address, slotKeyRaw)
	for i := 1; i < len(diff.Storage); i++ {
		prev := diff.Storage[i-1]
		curr := diff.Storage[i]

		cmpAddr := bytes.Compare(prev.Address[:], curr.Address[:])
		if cmpAddr > 0 {
			t.Fatalf("storage not sorted by address at index %d", i)
		}
		if cmpAddr == 0 {
			if bytes.Compare(prev.SlotKeyRaw[:], curr.SlotKeyRaw[:]) >= 0 {
				t.Fatalf("storage not sorted by slot key at index %d", i)
			}
		}
	}
}

// TestToUBTDiff_CodeChange verifies contract code changes are properly converted.
func TestToUBTDiff_CodeChange(t *testing.T) {
	addr := common.HexToAddress("0xeeee")
	addrHash := crypto.Keccak256Hash(addr.Bytes())
	code := []byte{0x60, 0x00, 0x60, 0x00, 0xf3} // simple bytecode: PUSH1 0 PUSH1 0 RETURN
	codeHash := crypto.Keccak256Hash(code)

	acctData, _ := rlp.EncodeToBytes(&types.SlimAccount{
		Nonce:    1,
		Balance:  uint256.NewInt(500),
		CodeHash: codeHash.Bytes(),
	})
	oldData, _ := rlp.EncodeToBytes(&types.SlimAccount{Balance: uint256.NewInt(500)})

	updates := map[common.Hash]*accountUpdate{
		addrHash: {
			address: addr,
			data:    acctData,
			origin:  oldData,
			code: &contractCode{
				hash: codeHash,
				blob: code,
			},
			storages:             make(map[common.Hash][]byte),
			storagesOriginByKey:  make(map[common.Hash][]byte),
			storagesOriginByHash: make(map[common.Hash][]byte),
		},
	}

	su := newStateUpdate(true, common.Hash{}, common.Hash{}, 1,
		make(map[common.Hash]*accountDelete), updates, trienode.NewMergedNodeSet())

	diff, err := su.ToUBTDiff()
	if err != nil {
		t.Fatal(err)
	}
	if len(diff.Codes) != 1 {
		t.Fatalf("expected 1 code entry, got %d", len(diff.Codes))
	}
	if diff.Codes[0].CodeHash != codeHash {
		t.Fatalf("code hash mismatch: got %v, want %v", diff.Codes[0].CodeHash, codeHash)
	}
	if !bytes.Equal(diff.Codes[0].Code, code) {
		t.Fatalf("code bytes mismatch: got %x, want %x", diff.Codes[0].Code, code)
	}
	if diff.Codes[0].Address != addr {
		t.Fatalf("address mismatch: got %v, want %v", diff.Codes[0].Address, addr)
	}
}

// TestToUBTDiff_SortedCodes verifies code entries are sorted by address.
func TestToUBTDiff_SortedCodes(t *testing.T) {
	addrs := []common.Address{
		common.HexToAddress("0xcccc"),
		common.HexToAddress("0xaaaa"),
		common.HexToAddress("0xbbbb"),
	}

	updates := make(map[common.Hash]*accountUpdate)
	for i, addr := range addrs {
		addrHash := crypto.Keccak256Hash(addr.Bytes())
		code := []byte{byte(i + 0x60)}
		codeHash := crypto.Keccak256Hash(code)

		acctData, _ := rlp.EncodeToBytes(&types.SlimAccount{
			Nonce:    1,
			Balance:  uint256.NewInt(100),
			CodeHash: codeHash.Bytes(),
		})

		updates[addrHash] = &accountUpdate{
			address: addr,
			data:    acctData,
			origin:  acctData,
			code: &contractCode{
				hash: codeHash,
				blob: code,
			},
			storages:             make(map[common.Hash][]byte),
			storagesOriginByKey:  make(map[common.Hash][]byte),
			storagesOriginByHash: make(map[common.Hash][]byte),
		}
	}

	su := newStateUpdate(true, common.Hash{}, common.Hash{}, 1,
		make(map[common.Hash]*accountDelete), updates, trienode.NewMergedNodeSet())

	diff, err := su.ToUBTDiff()
	if err != nil {
		t.Fatal(err)
	}
	if len(diff.Codes) != 3 {
		t.Fatalf("expected 3 code entries, got %d", len(diff.Codes))
	}

	// Verify sorted by address
	for i := 1; i < len(diff.Codes); i++ {
		if bytes.Compare(diff.Codes[i-1].Address[:], diff.Codes[i].Address[:]) >= 0 {
			t.Fatalf("codes not sorted at index %d", i)
		}
	}
}

// TestToUBTDiff_EmptyRoots verifies handling of empty/zero roots.
func TestToUBTDiff_EmptyRoots(t *testing.T) {
	addr := common.HexToAddress("0xaaaa")
	addrHash := crypto.Keccak256Hash(addr.Bytes())

	acctData, _ := rlp.EncodeToBytes(&types.SlimAccount{
		Nonce:   1,
		Balance: uint256.NewInt(100),
	})

	updates := map[common.Hash]*accountUpdate{
		addrHash: {
			address:              addr,
			data:                 acctData,
			origin:               acctData,
			storages:             make(map[common.Hash][]byte),
			storagesOriginByKey:  make(map[common.Hash][]byte),
			storagesOriginByHash: make(map[common.Hash][]byte),
		},
	}

	su := newStateUpdate(true, common.Hash{}, common.Hash{}, 1,
		make(map[common.Hash]*accountDelete), updates, trienode.NewMergedNodeSet())

	diff, err := su.ToUBTDiff()
	if err != nil {
		t.Fatal(err)
	}
	if diff.OriginRoot != (common.Hash{}) {
		t.Fatalf("expected zero origin root, got %v", diff.OriginRoot)
	}
	if diff.Root != (common.Hash{}) {
		t.Fatalf("expected zero root, got %v", diff.Root)
	}
}

// TestToUBTDiff_MultipleChanges verifies complex scenario with all change types.
func TestToUBTDiff_MultipleChanges(t *testing.T) {
	// Account 1: updated with storage
	addr1 := common.HexToAddress("0xaaaa")
	addr1Hash := crypto.Keccak256Hash(addr1.Bytes())
	rawKey1 := common.HexToHash("0x01")
	hashedKey1 := crypto.Keccak256Hash(rawKey1.Bytes())
	val1, _ := rlp.EncodeToBytes(common.HexToHash("0xff").Bytes())
	acct1Data, _ := rlp.EncodeToBytes(&types.SlimAccount{
		Nonce:   10,
		Balance: uint256.NewInt(1000),
	})

	// Account 2: deleted
	addr2 := common.HexToAddress("0xbbbb")
	addr2Hash := crypto.Keccak256Hash(addr2.Bytes())
	acct2OldData, _ := rlp.EncodeToBytes(&types.SlimAccount{
		Nonce:   5,
		Balance: uint256.NewInt(500),
	})

	// Account 3: code change
	addr3 := common.HexToAddress("0xcccc")
	addr3Hash := crypto.Keccak256Hash(addr3.Bytes())
	code3 := []byte{0x60, 0x01}
	code3Hash := crypto.Keccak256Hash(code3)
	acct3Data, _ := rlp.EncodeToBytes(&types.SlimAccount{
		Nonce:    1,
		Balance:  uint256.NewInt(100),
		CodeHash: code3Hash.Bytes(),
	})

	deletes := map[common.Hash]*accountDelete{
		addr2Hash: {
			address:        addr2,
			origin:         acct2OldData,
			storages:       make(map[common.Hash][]byte),
			storagesOrigin: make(map[common.Hash][]byte),
		},
	}

	updates := map[common.Hash]*accountUpdate{
		addr1Hash: {
			address:              addr1,
			data:                 acct1Data,
			origin:               acct1Data,
			storages:             map[common.Hash][]byte{hashedKey1: val1},
			storagesOriginByKey:  map[common.Hash][]byte{rawKey1: val1},
			storagesOriginByHash: map[common.Hash][]byte{hashedKey1: val1},
		},
		addr3Hash: {
			address: addr3,
			data:    acct3Data,
			origin:  acct3Data,
			code: &contractCode{
				hash: code3Hash,
				blob: code3,
			},
			storages:             make(map[common.Hash][]byte),
			storagesOriginByKey:  make(map[common.Hash][]byte),
			storagesOriginByHash: make(map[common.Hash][]byte),
		},
	}

	su := newStateUpdate(true, common.HexToHash("0x01"), common.HexToHash("0x02"), 1,
		deletes, updates, trienode.NewMergedNodeSet())

	diff, err := su.ToUBTDiff()
	if err != nil {
		t.Fatal(err)
	}

	// Verify accounts (3 total: 1 updated, 1 deleted, 1 with code)
	if len(diff.Accounts) != 3 {
		t.Fatalf("expected 3 accounts, got %d", len(diff.Accounts))
	}

	// Find each account in the result
	var found1, found2, found3 bool
	for _, acc := range diff.Accounts {
		switch acc.Address {
		case addr1:
			found1 = true
			if !acc.Alive || acc.Nonce != 10 {
				t.Fatalf("account 1 incorrect: alive=%v, nonce=%d", acc.Alive, acc.Nonce)
			}
		case addr2:
			found2 = true
			if acc.Alive {
				t.Fatal("account 2 should be deleted")
			}
		case addr3:
			found3 = true
			if !acc.Alive || acc.Nonce != 1 {
				t.Fatalf("account 3 incorrect: alive=%v, nonce=%d", acc.Alive, acc.Nonce)
			}
		}
	}
	if !found1 || !found2 || !found3 {
		t.Fatal("not all accounts found in result")
	}

	// Verify storage (1 entry for account 1)
	if len(diff.Storage) != 1 {
		t.Fatalf("expected 1 storage entry, got %d", len(diff.Storage))
	}
	if diff.Storage[0].Address != addr1 {
		t.Fatalf("storage address mismatch: got %v, want %v", diff.Storage[0].Address, addr1)
	}

	// Verify code (1 entry for account 3)
	if len(diff.Codes) != 1 {
		t.Fatalf("expected 1 code entry, got %d", len(diff.Codes))
	}
	if diff.Codes[0].Address != addr3 {
		t.Fatalf("code address mismatch: got %v, want %v", diff.Codes[0].Address, addr3)
	}
}

// TestToUBTDiff_AccountWithEmptyRoot verifies accounts with empty storage root.
func TestToUBTDiff_AccountWithEmptyRoot(t *testing.T) {
	addr := common.HexToAddress("0xaaaa")
	addrHash := crypto.Keccak256Hash(addr.Bytes())

	// Account with explicit empty root (SlimAccount encodes nil root for EmptyRootHash)
	acct := &types.SlimAccount{
		Nonce:   1,
		Balance: uint256.NewInt(100),
		Root:    nil, // Empty root
	}
	acctData, _ := rlp.EncodeToBytes(acct)

	updates := map[common.Hash]*accountUpdate{
		addrHash: {
			address:              addr,
			data:                 acctData,
			origin:               acctData,
			storages:             make(map[common.Hash][]byte),
			storagesOriginByKey:  make(map[common.Hash][]byte),
			storagesOriginByHash: make(map[common.Hash][]byte),
		},
	}

	su := newStateUpdate(true, common.Hash{}, common.Hash{}, 1,
		make(map[common.Hash]*accountDelete), updates, trienode.NewMergedNodeSet())

	diff, err := su.ToUBTDiff()
	if err != nil {
		t.Fatal(err)
	}

	if len(diff.Accounts) != 1 {
		t.Fatalf("expected 1 account, got %d", len(diff.Accounts))
	}

	// Account should decode correctly with empty root
	if !diff.Accounts[0].Alive {
		t.Fatal("account should be alive")
	}
}

// TestToUBTDiff_LargeBalance verifies handling of large balance values.
func TestToUBTDiff_LargeBalance(t *testing.T) {
	addr := common.HexToAddress("0xaaaa")
	addrHash := crypto.Keccak256Hash(addr.Bytes())

	// Create a very large balance
	largeBalance := new(uint256.Int)
	largeBalance.SetBytes(common.Hex2Bytes("ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"))

	acct := &types.SlimAccount{
		Nonce:   1,
		Balance: largeBalance,
	}
	acctData, _ := rlp.EncodeToBytes(acct)

	updates := map[common.Hash]*accountUpdate{
		addrHash: {
			address:              addr,
			data:                 acctData,
			origin:               acctData,
			storages:             make(map[common.Hash][]byte),
			storagesOriginByKey:  make(map[common.Hash][]byte),
			storagesOriginByHash: make(map[common.Hash][]byte),
		},
	}

	su := newStateUpdate(true, common.Hash{}, common.Hash{}, 1,
		make(map[common.Hash]*accountDelete), updates, trienode.NewMergedNodeSet())

	diff, err := su.ToUBTDiff()
	if err != nil {
		t.Fatal(err)
	}

	if len(diff.Accounts) != 1 {
		t.Fatalf("expected 1 account, got %d", len(diff.Accounts))
	}

	// Verify large balance is preserved
	expectedBig := largeBalance.ToBig()
	if diff.Accounts[0].Balance.Cmp(expectedBig) != 0 {
		t.Fatalf("balance mismatch: got %v, want %v", diff.Accounts[0].Balance, expectedBig)
	}
}

// TestToUBTDiff_EmptyCode verifies handling of empty code hash.
func TestToUBTDiff_EmptyCode(t *testing.T) {
	addr := common.HexToAddress("0xaaaa")
	addrHash := crypto.Keccak256Hash(addr.Bytes())

	// Account with empty code hash (encoded as nil in SlimAccount)
	acct := &types.SlimAccount{
		Nonce:    1,
		Balance:  uint256.NewInt(100),
		CodeHash: nil, // Empty code hash
	}
	acctData, _ := rlp.EncodeToBytes(acct)

	updates := map[common.Hash]*accountUpdate{
		addrHash: {
			address:              addr,
			data:                 acctData,
			origin:               acctData,
			storages:             make(map[common.Hash][]byte),
			storagesOriginByKey:  make(map[common.Hash][]byte),
			storagesOriginByHash: make(map[common.Hash][]byte),
		},
	}

	su := newStateUpdate(true, common.Hash{}, common.Hash{}, 1,
		make(map[common.Hash]*accountDelete), updates, trienode.NewMergedNodeSet())

	diff, err := su.ToUBTDiff()
	if err != nil {
		t.Fatal(err)
	}

	if len(diff.Accounts) != 1 {
		t.Fatalf("expected 1 account, got %d", len(diff.Accounts))
	}

	// Verify code hash is set to EmptyCodeHash
	if diff.Accounts[0].CodeHash != types.EmptyCodeHash {
		t.Fatalf("expected empty code hash, got %v", diff.Accounts[0].CodeHash)
	}
}
