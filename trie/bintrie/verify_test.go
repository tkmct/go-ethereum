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

package bintrie

import (
	"errors"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethdb/memorydb"
)

// buildTestTrie creates a small binary trie with two entries in different stems
// so the proof path traverses internal nodes. Returns the trie and its root hash.
func buildTestTrie(t *testing.T) (*BinaryTrie, common.Hash) {
	t.Helper()
	root := NewBinaryNode()

	// Two keys whose first bit differs, creating an internal node at depth 0.
	key1 := common.HexToHash("0000000000000000000000000000000000000000000000000000000000000001")
	val1 := common.HexToHash("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	key2 := common.HexToHash("8000000000000000000000000000000000000000000000000000000000000001")
	val2 := common.HexToHash("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")

	var err error
	root, err = root.Insert(key1.Bytes(), val1.Bytes(), nil, 0)
	if err != nil {
		t.Fatalf("insert key1: %v", err)
	}
	root, err = root.Insert(key2.Bytes(), val2.Bytes(), nil, 0)
	if err != nil {
		t.Fatalf("insert key2: %v", err)
	}

	tr := &BinaryTrie{root: root}
	return tr, tr.Hash()
}

// generateProof uses BinaryTrie.Prove to produce a memorydb populated with proof nodes.
func generateProof(t *testing.T, tr *BinaryTrie, key []byte) *memorydb.Database {
	t.Helper()
	proofDb := memorydb.New()
	if err := tr.Prove(key, proofDb); err != nil {
		t.Fatalf("Prove: %v", err)
	}
	return proofDb
}

func TestVerifyProof_InclusionProof(t *testing.T) {
	tr, root := buildTestTrie(t)

	key := common.HexToHash("0000000000000000000000000000000000000000000000000000000000000001")
	proofDb := generateProof(t, tr, key.Bytes())

	value, err := VerifyProof(root, key.Bytes(), proofDb)
	if err != nil {
		t.Fatalf("VerifyProof: %v", err)
	}
	if value == nil {
		t.Fatal("expected non-nil value for inclusion proof")
	}
	expected := common.HexToHash("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if common.BytesToHash(value) != expected {
		t.Fatalf("value mismatch: got %x, want %x", value, expected)
	}
}

func TestVerifyProof_AbsenceProof(t *testing.T) {
	tr, root := buildTestTrie(t)

	// Key that shares stem with key1 but uses a different sub-index with no value.
	key := common.HexToHash("00000000000000000000000000000000000000000000000000000000000000FF")
	proofDb := generateProof(t, tr, key.Bytes())

	value, err := VerifyProof(root, key.Bytes(), proofDb)
	if err != nil {
		t.Fatalf("VerifyProof: %v", err)
	}
	if value != nil {
		t.Fatalf("expected nil value for absence proof, got %x", value)
	}
}

func TestVerifyProof_TamperedNode(t *testing.T) {
	tr, root := buildTestTrie(t)

	key := common.HexToHash("0000000000000000000000000000000000000000000000000000000000000001")
	proofDb := generateProof(t, tr, key.Bytes())

	// Tamper with one proof node by flipping a byte.
	it := proofDb.NewIterator(nil, nil)
	defer it.Release()
	if !it.Next() {
		t.Fatal("proof db is empty")
	}
	tamperedKey := common.CopyBytes(it.Key())
	tamperedVal := common.CopyBytes(it.Value())
	it.Release()

	// Flip a byte in the middle of the serialized node.
	if len(tamperedVal) > 10 {
		tamperedVal[10] ^= 0xFF
	} else {
		tamperedVal[0] ^= 0xFF
	}

	// Build new proof db with tampered node.
	newProofDb := memorydb.New()
	it2 := proofDb.NewIterator(nil, nil)
	defer it2.Release()
	for it2.Next() {
		k := common.CopyBytes(it2.Key())
		v := common.CopyBytes(it2.Value())
		if string(k) == string(tamperedKey) {
			v = tamperedVal
		}
		if err := newProofDb.Put(k, v); err != nil {
			t.Fatalf("put: %v", err)
		}
	}

	_, err := VerifyProof(root, key.Bytes(), newProofDb)
	if err == nil {
		t.Fatal("expected error for tampered proof node")
	}
	// Tampered data may cause either a hash mismatch or a deserialization error.
	if !errors.Is(err, ErrProofHashMismatch) && !errors.Is(err, ErrProofMalformedNode) {
		t.Fatalf("expected ErrProofHashMismatch or ErrProofMalformedNode, got: %v", err)
	}
}

func TestVerifyProof_IncompleteProof(t *testing.T) {
	tr, root := buildTestTrie(t)

	key := common.HexToHash("0000000000000000000000000000000000000000000000000000000000000001")
	proofDb := generateProof(t, tr, key.Bytes())

	// Count proof nodes and remove the last one (leaf/stem node).
	// The trie has internal + stem nodes; removing any should break verification.
	it := proofDb.NewIterator(nil, nil)
	var allKeys [][]byte
	for it.Next() {
		allKeys = append(allKeys, common.CopyBytes(it.Key()))
	}
	it.Release()

	if len(allKeys) < 2 {
		t.Skipf("proof has only %d node(s), need at least 2 to remove one", len(allKeys))
	}

	// Remove the last node (likely the stem node).
	newProofDb := memorydb.New()
	for _, k := range allKeys[:len(allKeys)-1] {
		val, _ := proofDb.Get(k)
		if err := newProofDb.Put(k, val); err != nil {
			t.Fatalf("put: %v", err)
		}
	}

	_, err := VerifyProof(root, key.Bytes(), newProofDb)
	if err == nil {
		t.Fatal("expected error for incomplete proof")
	}
	if !errors.Is(err, ErrProofMissingNode) {
		t.Fatalf("expected ErrProofMissingNode, got: %v", err)
	}
}

func TestVerifyProof_MalformedNode(t *testing.T) {
	tr, root := buildTestTrie(t)

	key := common.HexToHash("0000000000000000000000000000000000000000000000000000000000000001")

	// Insert garbage bytes under the root hash.
	proofDb := memorydb.New()
	if err := proofDb.Put(root.Bytes(), []byte{0xDE, 0xAD}); err != nil {
		t.Fatalf("put: %v", err)
	}
	_ = tr // just to keep the trie alive

	_, err := VerifyProof(root, key.Bytes(), proofDb)
	if err == nil {
		t.Fatal("expected error for malformed proof node")
	}
	if !errors.Is(err, ErrProofMalformedNode) && !errors.Is(err, ErrProofHashMismatch) {
		t.Fatalf("expected ErrProofMalformedNode or ErrProofHashMismatch, got: %v", err)
	}
}

func TestVerifyProof_InvalidKeyLength(t *testing.T) {
	root := common.HexToHash("0000000000000000000000000000000000000000000000000000000000000001")
	proofDb := memorydb.New()

	tests := []struct {
		name   string
		keyLen int
	}{
		{"too short", 16},
		{"too long", 64},
		{"empty", 0},
		{"one byte", 1},
		{"31 bytes", 31},
		{"33 bytes", 33},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := make([]byte, tt.keyLen)
			_, err := VerifyProof(root, key, proofDb)
			if err == nil {
				t.Fatal("expected error for invalid key length")
			}
			if !errors.Is(err, ErrProofInvalidKeyLength) {
				t.Fatalf("expected ErrProofInvalidKeyLength, got: %v", err)
			}
		})
	}
}

func TestVerifyProof_EmptyRoot(t *testing.T) {
	proofDb := memorydb.New()
	key := make([]byte, HashSize)

	value, err := VerifyProof(common.Hash{}, key, proofDb)
	if err != nil {
		t.Fatalf("expected no error for empty root, got: %v", err)
	}
	if value != nil {
		t.Fatalf("expected nil value for empty root, got %x", value)
	}
}
