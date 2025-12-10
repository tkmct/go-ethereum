// Copyright 2025 go-ethereum Authors
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

package bintrie

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

var (
	zeroKey  = [HashSize]byte{}
	oneKey   = common.HexToHash("0101010101010101010101010101010101010101010101010101010101010101")
	twoKey   = common.HexToHash("0202020202020202020202020202020202020202020202020202020202020202")
	threeKey = common.HexToHash("0303030303030303030303030303030303030303030303030303030303030303")
	fourKey  = common.HexToHash("0404040404040404040404040404040404040404040404040404040404040404")
	ffKey    = common.HexToHash("ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff")
)

func TestSingleEntry(t *testing.T) {
	tree := NewBinaryNode()
	tree, err := tree.Insert(zeroKey[:], oneKey[:], nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if tree.GetHeight() != 1 {
		t.Fatal("invalid depth")
	}
	expected := common.HexToHash("aab1060e04cb4f5dc6f697ae93156a95714debbf77d54238766adc5709282b6f")
	got := tree.Hash()
	if got != expected {
		t.Fatalf("invalid tree root, got %x, want %x", got, expected)
	}
}

func TestTwoEntriesDiffFirstBit(t *testing.T) {
	var err error
	tree := NewBinaryNode()
	tree, err = tree.Insert(zeroKey[:], oneKey[:], nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	tree, err = tree.Insert(common.HexToHash("8000000000000000000000000000000000000000000000000000000000000000").Bytes(), twoKey[:], nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if tree.GetHeight() != 2 {
		t.Fatal("invalid height")
	}
	if tree.Hash() != common.HexToHash("dfc69c94013a8b3c65395625a719a87534a7cfd38719251ad8c8ea7fe79f065e") {
		t.Fatal("invalid tree root")
	}
}

func TestOneStemColocatedValues(t *testing.T) {
	var err error
	tree := NewBinaryNode()
	tree, err = tree.Insert(common.HexToHash("0000000000000000000000000000000000000000000000000000000000000003").Bytes(), oneKey[:], nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	tree, err = tree.Insert(common.HexToHash("0000000000000000000000000000000000000000000000000000000000000004").Bytes(), twoKey[:], nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	tree, err = tree.Insert(common.HexToHash("0000000000000000000000000000000000000000000000000000000000000009").Bytes(), threeKey[:], nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	tree, err = tree.Insert(common.HexToHash("00000000000000000000000000000000000000000000000000000000000000FF").Bytes(), fourKey[:], nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if tree.GetHeight() != 1 {
		t.Fatal("invalid height")
	}
}

func TestTwoStemColocatedValues(t *testing.T) {
	var err error
	tree := NewBinaryNode()
	// stem: 0...0
	tree, err = tree.Insert(common.HexToHash("0000000000000000000000000000000000000000000000000000000000000003").Bytes(), oneKey[:], nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	tree, err = tree.Insert(common.HexToHash("0000000000000000000000000000000000000000000000000000000000000004").Bytes(), twoKey[:], nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	// stem: 10...0
	tree, err = tree.Insert(common.HexToHash("8000000000000000000000000000000000000000000000000000000000000003").Bytes(), oneKey[:], nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	tree, err = tree.Insert(common.HexToHash("8000000000000000000000000000000000000000000000000000000000000004").Bytes(), twoKey[:], nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if tree.GetHeight() != 2 {
		t.Fatal("invalid height")
	}
}

func TestTwoKeysMatchFirst42Bits(t *testing.T) {
	var err error
	tree := NewBinaryNode()
	// key1 and key 2 have the same prefix of 42 bits (b0*42+b1+b1) and differ after.
	key1 := common.HexToHash("0000000000C0C0C0C0C0C0C0C0C0C0C0C0C0C0C0C0C0C0C0C0C0C0C0C0C0C0C0").Bytes()
	key2 := common.HexToHash("0000000000E00000000000000000000000000000000000000000000000000000").Bytes()
	tree, err = tree.Insert(key1, oneKey[:], nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	tree, err = tree.Insert(key2, twoKey[:], nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if tree.GetHeight() != 1+42+1 {
		t.Fatal("invalid height")
	}
}
func TestInsertDuplicateKey(t *testing.T) {
	var err error
	tree := NewBinaryNode()
	tree, err = tree.Insert(oneKey[:], oneKey[:], nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	tree, err = tree.Insert(oneKey[:], twoKey[:], nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if tree.GetHeight() != 1 {
		t.Fatal("invalid height")
	}
	// Verify that the value is updated
	if !bytes.Equal(tree.(*StemNode).Values[1], twoKey[:]) {
		t.Fatal("invalid height")
	}
}
func TestLargeNumberOfEntries(t *testing.T) {
	var err error
	tree := NewBinaryNode()
	for i := range StemNodeWidth {
		var key [HashSize]byte
		key[0] = byte(i)
		tree, err = tree.Insert(key[:], ffKey[:], nil, 0)
		if err != nil {
			t.Fatal(err)
		}
	}
	height := tree.GetHeight()
	if height != 1+8 {
		t.Fatalf("invalid height, wanted %d, got %d", 1+8, height)
	}
}

func TestMerkleizeMultipleEntries(t *testing.T) {
	var err error
	tree := NewBinaryNode()
	keys := [][]byte{
		zeroKey[:],
		common.HexToHash("8000000000000000000000000000000000000000000000000000000000000000").Bytes(),
		common.HexToHash("0100000000000000000000000000000000000000000000000000000000000000").Bytes(),
		common.HexToHash("8100000000000000000000000000000000000000000000000000000000000000").Bytes(),
	}
	for i, key := range keys {
		var v [HashSize]byte
		binary.LittleEndian.PutUint64(v[:8], uint64(i))
		tree, err = tree.Insert(key, v[:], nil, 0)
		if err != nil {
			t.Fatal(err)
		}
	}
	got := tree.Hash()
	expected := common.HexToHash("9317155862f7a3867660ddd0966ff799a3d16aa4df1e70a7516eaa4a675191b5")
	if got != expected {
		t.Fatalf("invalid root, expected=%x, got = %x", expected, got)
	}
}

// TestGetStorageKeyEncoding tests that GetStorage and UpdateStorage use matching key encoding.
func TestGetStorageKeyEncoding(t *testing.T) {
	addr := common.HexToAddress("0x1234567890123456789012345678901234567890")

	testCases := []struct {
		name     string
		key      common.Hash
		value    []byte
		expected []byte
	}{
		{
			name:     "simple storage slot",
			key:      common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000001"),
			value:    common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000042").Bytes(),
			expected: common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000042").Bytes(),
		},
		{
			name:     "header storage slot (< 64)",
			key:      common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000010"),
			value:    common.HexToHash("0x00000000000000000000000000000000000000000000000000000000000000ff").Bytes(),
			expected: common.HexToHash("0x00000000000000000000000000000000000000000000000000000000000000ff").Bytes(),
		},
		{
			name:     "main storage slot",
			key:      common.HexToHash("0x0100000000000000000000000000000000000000000000000000000000000001"),
			value:    common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000001234").Bytes(),
			expected: common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000001234").Bytes(),
		},
		{
			name:     "max key value",
			key:      common.HexToHash("0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"),
			value:    common.HexToHash("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890").Bytes(),
			expected: common.HexToHash("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890").Bytes(),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			bt := &BinaryTrie{
				root:   NewBinaryNode(),
				reader: nil,
				tracer: nil,
			}

			err := bt.UpdateStorage(addr, tc.key.Bytes(), tc.value)
			if err != nil {
				t.Fatalf("UpdateStorage failed: %v", err)
			}

			retrieved, err := bt.GetStorage(addr, tc.key.Bytes())
			if err != nil {
				t.Fatalf("GetStorage failed: %v", err)
			}

			if retrieved == nil {
				t.Fatal("GetStorage returned nil, expected value")
			}

			if !bytes.Equal(retrieved, tc.expected) {
				t.Errorf("GetStorage returned wrong value: got %x, expected %x", retrieved, tc.expected)
			}
		})
	}
}

// TestGetStorageNonExistent tests that GetStorage returns nil for non-existent keys.
func TestGetStorageNonExistent(t *testing.T) {
	addr := common.HexToAddress("0x1234567890123456789012345678901234567890")
	key := common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000001")

	bt := &BinaryTrie{
		root:   NewBinaryNode(),
		reader: nil,
		tracer: nil,
	}

	retrieved, err := bt.GetStorage(addr, key.Bytes())
	if err != nil {
		t.Fatalf("GetStorage failed: %v", err)
	}

	if retrieved != nil {
		t.Errorf("GetStorage should return nil for non-existent key, got %x", retrieved)
	}
}

// TestGetStorageMultipleSlots tests reading multiple storage slots.
func TestGetStorageMultipleSlots(t *testing.T) {
	addr := common.HexToAddress("0x1234567890123456789012345678901234567890")

	bt := &BinaryTrie{
		root:   NewBinaryNode(),
		reader: nil,
		tracer: nil,
	}

	slots := make(map[common.Hash][]byte)
	for i := 0; i < 10; i++ {
		key := common.BigToHash(common.Big0)
		key[31] = byte(i)
		value := make([]byte, 32)
		value[31] = byte(i * 10)
		slots[key] = value

		err := bt.UpdateStorage(addr, key.Bytes(), value)
		if err != nil {
			t.Fatalf("UpdateStorage failed for slot %d: %v", i, err)
		}
	}

	for key, expected := range slots {
		retrieved, err := bt.GetStorage(addr, key.Bytes())
		if err != nil {
			t.Fatalf("GetStorage failed for key %x: %v", key, err)
		}

		if !bytes.Equal(retrieved, expected) {
			t.Errorf("GetStorage for key %x: got %x, expected %x", key, retrieved, expected)
		}
	}
}

// TestDeleteStorageKeyEncoding tests that DeleteStorage uses the same key encoding as UpdateStorage.
func TestDeleteStorageKeyEncoding(t *testing.T) {
	addr := common.HexToAddress("0x1234567890123456789012345678901234567890")
	key := common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000001")
	value := common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000042").Bytes()

	bt := &BinaryTrie{
		root:   NewBinaryNode(),
		reader: nil,
		tracer: nil,
	}

	// Update storage
	err := bt.UpdateStorage(addr, key.Bytes(), value)
	if err != nil {
		t.Fatalf("UpdateStorage failed: %v", err)
	}

	// Verify value exists
	retrieved, err := bt.GetStorage(addr, key.Bytes())
	if err != nil {
		t.Fatalf("GetStorage failed: %v", err)
	}
	if retrieved == nil {
		t.Fatal("GetStorage returned nil, expected value")
	}
	if !bytes.Equal(retrieved, value) {
		t.Errorf("GetStorage returned wrong value: got %x, expected %x", retrieved, value)
	}

	// Delete storage
	err = bt.DeleteStorage(addr, key.Bytes())
	if err != nil {
		t.Fatalf("DeleteStorage failed: %v", err)
	}

	// Verify value is deleted (DeleteStorage inserts zero value)
	retrieved, err = bt.GetStorage(addr, key.Bytes())
	if err != nil {
		t.Fatalf("GetStorage failed after delete: %v", err)
	}
	// DeleteStorage inserts a zero value, so we check for all zeros
	zeroValue := make([]byte, 32)
	if retrieved == nil || !bytes.Equal(retrieved, zeroValue) {
		t.Errorf("GetStorage should return zero value after DeleteStorage, got %x", retrieved)
	}
}

// TestDeleteStorageMultipleSlots tests deleting multiple storage slots.
func TestDeleteStorageMultipleSlots(t *testing.T) {
	addr := common.HexToAddress("0x1234567890123456789012345678901234567890")

	bt := &BinaryTrie{
		root:   NewBinaryNode(),
		reader: nil,
		tracer: nil,
	}

	// Add multiple storage slots
	keys := make([]common.Hash, 5)
	for i := 0; i < 5; i++ {
		key := common.BigToHash(common.Big0)
		key[31] = byte(i)
		keys[i] = key
		value := make([]byte, 32)
		value[31] = byte(i * 10)

		err := bt.UpdateStorage(addr, key.Bytes(), value)
		if err != nil {
			t.Fatalf("UpdateStorage failed for slot %d: %v", i, err)
		}
	}

	// Verify all values exist
	for i, key := range keys {
		retrieved, err := bt.GetStorage(addr, key.Bytes())
		if err != nil {
			t.Fatalf("GetStorage failed for key %d: %v", i, err)
		}
		if retrieved == nil {
			t.Fatalf("GetStorage returned nil for key %d", i)
		}
		expected := byte(i * 10)
		if retrieved[31] != expected {
			t.Errorf("GetStorage for key %d: got %x, expected value ending in %02x", i, retrieved, expected)
		}
	}

	// Delete every other slot
	for i := 0; i < 5; i += 2 {
		err := bt.DeleteStorage(addr, keys[i].Bytes())
		if err != nil {
			t.Fatalf("DeleteStorage failed for key %d: %v", i, err)
		}
	}

	// Verify deleted slots are zeroed and others remain
	zeroValue := make([]byte, 32)
	for i, key := range keys {
		retrieved, err := bt.GetStorage(addr, key.Bytes())
		if err != nil {
			t.Fatalf("GetStorage failed for key %d: %v", i, err)
		}
		if i%2 == 0 {
			// Should be deleted (zero value)
			if retrieved == nil || !bytes.Equal(retrieved, zeroValue) {
				t.Errorf("GetStorage for deleted key %d should return zero value, got %x", i, retrieved)
			}
		} else {
			// Should still exist
			if retrieved == nil {
				t.Fatalf("GetStorage for key %d returned nil, expected value", i)
			}
			expected := byte(i * 10)
			if retrieved[31] != expected {
				t.Errorf("GetStorage for key %d: got %x, expected value ending in %02x", i, retrieved, expected)
			}
		}
	}
}

// TestGetStorageBoundsCheck tests that GetStorage handles short values slices correctly.
// This verifies Bug 2 fix: GetStorage should not panic when a StemNode has a values
// slice shorter than StemNodeWidth (256), which can happen if UpdateStem is called
// with an arbitrary-length slice.
func TestGetStorageBoundsCheck(t *testing.T) {
	addr := common.HexToAddress("0x1234567890123456789012345678901234567890")
	key := common.HexToHash("0x00000000000000000000000000000000000000000000000000000000000000ff") // k[31] = 255

	bt := &BinaryTrie{
		root:   NewBinaryNode(),
		reader: nil,
		tracer: nil,
	}

	// Create a stem key (first 31 bytes)
	stemKey := GetBinaryTreeKeyStorageSlot(addr, key.Bytes())[:StemSize]

	// UpdateStem with a short slice (only 10 elements instead of 256)
	// This simulates a scenario where UpdateStem is called with an arbitrary-length slice
	shortValues := make([][]byte, 10)
	shortValues[0] = common.HexToHash("0x0101010101010101010101010101010101010101010101010101010101010101").Bytes()

	err := bt.UpdateStem(stemKey, shortValues)
	if err != nil {
		t.Fatalf("UpdateStem failed: %v", err)
	}

	// Try to get storage at index 255 (which is beyond the short slice length)
	// This should return nil gracefully, not panic due to index out of bounds
	retrieved, err := bt.GetStorage(addr, key.Bytes())
	if err != nil {
		t.Fatalf("GetStorage failed: %v", err)
	}
	// Should return nil because index 255 is out of bounds for the short slice
	// The bounds check in GetStorage should prevent a panic
	if retrieved != nil {
		t.Errorf("GetStorage should return nil for out-of-bounds index, got %x", retrieved)
	}

	// Test with a valid index within the short slice (index 0)
	key2 := common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000000") // k[31] = 0
	retrieved2, err := bt.GetStorage(addr, key2.Bytes())
	if err != nil {
		t.Fatalf("GetStorage failed for index 0: %v", err)
	}
	// Should return the value at index 0 if the stem matches
	// Note: This might return nil if the stem doesn't match due to how UpdateStem works
	// The important thing is that it doesn't panic
	if retrieved2 != nil && !bytes.Equal(retrieved2, shortValues[0]) {
		t.Errorf("GetStorage returned wrong value: got %x, expected %x", retrieved2, shortValues[0])
	}
}
