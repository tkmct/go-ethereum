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
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

// memoryProofDb is an in-memory key-value store for proof nodes
type memoryProofDb struct {
	nodes map[string][]byte
}

func newMemoryProofDb() *memoryProofDb {
	return &memoryProofDb{
		nodes: make(map[string][]byte),
	}
}

func (m *memoryProofDb) Put(key []byte, value []byte) error {
	m.nodes[string(key)] = bytes.Clone(value)
	return nil
}

func (m *memoryProofDb) Delete(key []byte) error {
	delete(m.nodes, string(key))
	return nil
}

func (m *memoryProofDb) Has(key []byte) (bool, error) {
	_, ok := m.nodes[string(key)]
	return ok, nil
}

func (m *memoryProofDb) Get(key []byte) ([]byte, error) {
	if val, ok := m.nodes[string(key)]; ok {
		return val, nil
	}
	return nil, nil
}

func TestProve_ExistingKey(t *testing.T) {
	// Build a simple trie with one key-value pair
	root := NewBinaryNode()
	key := common.HexToHash("0000000000000000000000000000000000000000000000000000000000000001")
	value := common.HexToHash("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")

	var err error
	root, err = root.Insert(key.Bytes(), value.Bytes(), nil, 0)
	if err != nil {
		t.Fatalf("Failed to insert: %v", err)
	}

	// Create a trie wrapper (without database for simplicity)
	trie := &BinaryTrie{
		root:   root,
		reader: nil,
	}

	// Generate proof
	proofDb := newMemoryProofDb()
	if err := trie.Prove(key.Bytes(), proofDb); err != nil {
		t.Fatalf("Prove failed: %v", err)
	}

	// Verify that the proof contains at least one node (the stem node)
	if len(proofDb.nodes) == 0 {
		t.Fatal("Proof is empty")
	}

	// The root hash should be in the proof
	rootHash := trie.Hash()
	if _, ok := proofDb.nodes[string(rootHash[:])]; !ok {
		t.Errorf("Proof does not contain root node")
	}

	t.Logf("Proof contains %d nodes", len(proofDb.nodes))
}

func TestProve_NonExistingKey(t *testing.T) {
	// Build a trie with one key
	root := NewBinaryNode()
	key1 := common.HexToHash("0000000000000000000000000000000000000000000000000000000000000001")
	value1 := common.HexToHash("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")

	var err error
	root, err = root.Insert(key1.Bytes(), value1.Bytes(), nil, 0)
	if err != nil {
		t.Fatalf("Failed to insert: %v", err)
	}

	trie := &BinaryTrie{
		root:   root,
		reader: nil,
	}

	// Try to prove a different key that doesn't exist
	key2 := common.HexToHash("1000000000000000000000000000000000000000000000000000000000000001")
	proofDb := newMemoryProofDb()

	if err := trie.Prove(key2.Bytes(), proofDb); err != nil {
		t.Fatalf("Prove failed for non-existing key: %v", err)
	}

	// Absence proof should still contain nodes (at least the nodes on the path)
	t.Logf("Absence proof contains %d nodes", len(proofDb.nodes))
}

func TestProve_EmptyTrie(t *testing.T) {
	// Empty trie
	root := NewBinaryNode()
	trie := &BinaryTrie{
		root:   root,
		reader: nil,
	}

	// Prove against empty trie
	key := common.HexToHash("0000000000000000000000000000000000000000000000000000000000000001")
	proofDb := newMemoryProofDb()

	if err := trie.Prove(key.Bytes(), proofDb); err != nil {
		t.Fatalf("Prove failed on empty trie: %v", err)
	}

	// Empty trie should produce an empty proof
	if len(proofDb.nodes) != 0 {
		t.Errorf("Expected empty proof for empty trie, got %d nodes", len(proofDb.nodes))
	}
}

func TestProve_InvalidKeyLength(t *testing.T) {
	root := NewBinaryNode()
	trie := &BinaryTrie{
		root:   root,
		reader: nil,
	}

	// Test with wrong key lengths
	testCases := []struct {
		name   string
		keyLen int
	}{
		{"too short", 16},
		{"too long", 64},
		{"empty", 0},
		{"one byte", 1},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			key := make([]byte, tc.keyLen)
			proofDb := newMemoryProofDb()

			err := trie.Prove(key, proofDb)
			if err == nil {
				t.Errorf("Expected error for key length %d, got nil", tc.keyLen)
			}
		})
	}
}

func TestProve_MultipleKeys(t *testing.T) {
	// Build a trie with multiple keys to create internal nodes
	root := NewBinaryNode()

	keys := []common.Hash{
		common.HexToHash("0000000000000000000000000000000000000000000000000000000000000001"),
		common.HexToHash("0000000000000000000000000000000000000000000000000000000000000002"),
		common.HexToHash("8000000000000000000000000000000000000000000000000000000000000001"), // Different first bit
		common.HexToHash("4000000000000000000000000000000000000000000000000000000000000001"), // Different second bit
	}

	values := []common.Hash{
		common.HexToHash("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
		common.HexToHash("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"),
		common.HexToHash("cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"),
		common.HexToHash("dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"),
	}

	var err error
	for i := range keys {
		root, err = root.Insert(keys[i].Bytes(), values[i].Bytes(), nil, 0)
		if err != nil {
			t.Fatalf("Failed to insert key %d: %v", i, err)
		}
	}

	trie := &BinaryTrie{
		root:   root,
		reader: nil,
	}

	// Prove each key's existence
	for i, key := range keys {
		t.Run(key.Hex(), func(t *testing.T) {
			proofDb := newMemoryProofDb()

			if err := trie.Prove(key.Bytes(), proofDb); err != nil {
				t.Fatalf("Prove failed for key %d: %v", i, err)
			}

			if len(proofDb.nodes) == 0 {
				t.Errorf("Proof is empty for key %d", i)
			}

			t.Logf("Key %d proof contains %d nodes", i, len(proofDb.nodes))
		})
	}
}

func TestProve_VerifyProofStructure(t *testing.T) {
	// Build a trie with internal nodes
	root := NewBinaryNode()

	key1 := common.HexToHash("0000000000000000000000000000000000000000000000000000000000000001")
	value1 := common.HexToHash("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	key2 := common.HexToHash("8000000000000000000000000000000000000000000000000000000000000001")
	value2 := common.HexToHash("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")

	var err error
	root, err = root.Insert(key1.Bytes(), value1.Bytes(), nil, 0)
	if err != nil {
		t.Fatalf("Failed to insert key1: %v", err)
	}
	root, err = root.Insert(key2.Bytes(), value2.Bytes(), nil, 0)
	if err != nil {
		t.Fatalf("Failed to insert key2: %v", err)
	}

	trie := &BinaryTrie{
		root:   root,
		reader: nil,
	}

	// Generate proof for key1
	proofDb := newMemoryProofDb()
	if err := trie.Prove(key1.Bytes(), proofDb); err != nil {
		t.Fatalf("Prove failed: %v", err)
	}

	// Verify proof structure - each node should be deserializable
	for hash, encoded := range proofDb.nodes {
		if len(encoded) == 0 {
			t.Errorf("Empty encoded node for hash %x", hash)
			continue
		}

		// Try to deserialize to verify it's a valid node
		_, err := DeserializeNode(encoded, 0)
		if err != nil {
			t.Errorf("Failed to deserialize node with hash %x: %v", hash, err)
		}
	}

	// Should contain at least the internal node and the stem node
	if len(proofDb.nodes) < 2 {
		t.Errorf("Expected at least 2 nodes in proof (internal + stem), got %d", len(proofDb.nodes))
	}
}

func TestProve_ColocatedValues(t *testing.T) {
	// Build a trie with colocated values (same stem, different last byte)
	root := NewBinaryNode()

	// These keys share the same first 31 bytes (stem) but differ in the last byte
	key1 := common.HexToHash("0000000000000000000000000000000000000000000000000000000000000001")
	value1 := common.HexToHash("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	key2 := common.HexToHash("0000000000000000000000000000000000000000000000000000000000000002")
	value2 := common.HexToHash("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")

	var err error
	root, err = root.Insert(key1.Bytes(), value1.Bytes(), nil, 0)
	if err != nil {
		t.Fatalf("Failed to insert key1: %v", err)
	}
	root, err = root.Insert(key2.Bytes(), value2.Bytes(), nil, 0)
	if err != nil {
		t.Fatalf("Failed to insert key2: %v", err)
	}

	trie := &BinaryTrie{
		root:   root,
		reader: nil,
	}

	// Both keys should produce the same proof (just the stem node)
	proofDb1 := newMemoryProofDb()
	if err := trie.Prove(key1.Bytes(), proofDb1); err != nil {
		t.Fatalf("Prove failed for key1: %v", err)
	}

	proofDb2 := newMemoryProofDb()
	if err := trie.Prove(key2.Bytes(), proofDb2); err != nil {
		t.Fatalf("Prove failed for key2: %v", err)
	}

	// Both proofs should have the same nodes
	if len(proofDb1.nodes) != len(proofDb2.nodes) {
		t.Errorf("Colocated values should have same proof size, got %d and %d",
			len(proofDb1.nodes), len(proofDb2.nodes))
	}

	t.Logf("Colocated values proof contains %d nodes", len(proofDb1.nodes))
}
