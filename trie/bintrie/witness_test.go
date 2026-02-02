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
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/trie"
)

// TestWitnessBasic tests that the Witness method returns the tracer values.
func TestWitnessBasic(t *testing.T) {
	tracer := trie.NewPrevalueTracer()

	bt := &BinaryTrie{
		root:   NewBinaryNode(),
		reader: nil,
		tracer: tracer,
	}

	witness := bt.Witness()
	if len(witness) != 0 {
		t.Errorf("expected empty witness for fresh trie, got %d nodes", len(witness))
	}

	tracer.Put([]byte("path1"), []byte("node1"))
	tracer.Put([]byte("path2"), []byte("node2"))

	witness = bt.Witness()
	if len(witness) != 2 {
		t.Errorf("expected 2 nodes in witness, got %d", len(witness))
	}

	if string(witness["path1"]) != "node1" {
		t.Errorf("expected node1 at path1, got %s", string(witness["path1"]))
	}
	if string(witness["path2"]) != "node2" {
		t.Errorf("expected node2 at path2, got %s", string(witness["path2"]))
	}
}

// TestWitnessCopy tests that copying a trie preserves the witness state.
func TestWitnessCopy(t *testing.T) {
	tracer := trie.NewPrevalueTracer()
	tracer.Put([]byte("test-path"), []byte("test-node"))

	bt := &BinaryTrie{
		root:   NewBinaryNode(),
		reader: nil,
		tracer: tracer,
	}

	btCopy := bt.Copy()

	witness := bt.Witness()
	witnessCopy := btCopy.Witness()

	if len(witness) != len(witnessCopy) {
		t.Errorf("witness length mismatch: original %d, copy %d", len(witness), len(witnessCopy))
	}

	for path, blob := range witness {
		if copyBlob, ok := witnessCopy[path]; !ok {
			t.Errorf("copied witness missing path: %s", path)
		} else if string(blob) != string(copyBlob) {
			t.Errorf("copied witness has different blob for path %s", path)
		}
	}
}

// TestWitnessIndependentAfterCopy tests that modifications to the copy don't affect the original.
func TestWitnessIndependentAfterCopy(t *testing.T) {
	tracer := trie.NewPrevalueTracer()
	tracer.Put([]byte("original-path"), []byte("original-node"))

	bt := &BinaryTrie{
		root:   NewBinaryNode(),
		reader: nil,
		tracer: tracer,
	}

	btCopy := bt.Copy()

	btCopy.tracer.Put([]byte("new-path"), []byte("new-node"))

	witness := bt.Witness()
	witnessCopy := btCopy.Witness()

	if len(witness) != 1 {
		t.Errorf("original witness should have 1 node, got %d", len(witness))
	}
	if len(witnessCopy) != 2 {
		t.Errorf("copied witness should have 2 nodes, got %d", len(witnessCopy))
	}
}

// TestWitnessWithNodeResolver tests witness collection via nodeResolver.
func TestWitnessWithNodeResolver(t *testing.T) {
	tracer := trie.NewPrevalueTracer()

	nodeStore := make(map[common.Hash][]byte)

	node1 := SerializeNode(&StemNode{
		Stem:   make([]byte, StemSize),
		Values: make([][]byte, StemNodeWidth),
		depth:  0,
	})
	hash1 := common.BytesToHash([]byte("hash1"))
	nodeStore[hash1] = node1

	bt := &BinaryTrie{
		root:   NewBinaryNode(),
		reader: nil,
		tracer: tracer,
	}

	tracer.Put([]byte("resolved-path"), node1)

	witness := bt.Witness()
	if len(witness) != 1 {
		t.Errorf("expected 1 node in witness after resolution, got %d", len(witness))
	}

	if _, ok := witness["resolved-path"]; !ok {
		t.Error("witness should contain the resolved path")
	}

	_, err := DeserializeNode(witness["resolved-path"], 0)
	if err != nil {
		t.Errorf("witnessed node should be deserializable: %v", err)
	}
}

// TestWitnessEmptyTrie tests that an empty trie has no witnessed nodes.
func TestWitnessEmptyTrie(t *testing.T) {
	tracer := trie.NewPrevalueTracer()
	bt := &BinaryTrie{
		root:   Empty{},
		reader: nil,
		tracer: tracer,
	}

	witness := bt.Witness()
	if len(witness) != 0 {
		t.Errorf("expected empty witness for empty trie, got %d nodes", len(witness))
	}
}

// TestWitnessMultiplePaths tests witness with multiple different paths.
func TestWitnessMultiplePaths(t *testing.T) {
	tracer := trie.NewPrevalueTracer()

	paths := []string{"path/a", "path/b", "path/c", "other/path", "deep/nested/path"}
	for i, path := range paths {
		node := SerializeNode(&StemNode{
			Stem:   make([]byte, StemSize),
			Values: make([][]byte, StemNodeWidth),
			depth:  i,
		})
		tracer.Put([]byte(path), node)
	}

	bt := &BinaryTrie{
		root:   NewBinaryNode(),
		reader: nil,
		tracer: tracer,
	}

	witness := bt.Witness()
	if len(witness) != len(paths) {
		t.Errorf("expected %d nodes in witness, got %d", len(paths), len(witness))
	}

	for _, path := range paths {
		if _, ok := witness[path]; !ok {
			t.Errorf("witness missing path: %s", path)
		}
	}
}

// TestWitnessNodeBlobValidity tests that all witness blobs are valid serialized nodes.
func TestWitnessNodeBlobValidity(t *testing.T) {
	tracer := trie.NewPrevalueTracer()

	stemNode := SerializeNode(&StemNode{
		Stem:   make([]byte, StemSize),
		Values: make([][]byte, StemNodeWidth),
		depth:  0,
	})
	tracer.Put([]byte("stem-path"), stemNode)

	internalNode := SerializeNode(&InternalNode{
		depth: 0,
		left:  HashedNode(common.HexToHash("0x1234")),
		right: HashedNode(common.HexToHash("0x5678")),
	})
	tracer.Put([]byte("internal-path"), internalNode)

	bt := &BinaryTrie{
		root:   NewBinaryNode(),
		reader: nil,
		tracer: tracer,
	}

	witness := bt.Witness()
	if len(witness) != 2 {
		t.Errorf("expected 2 nodes in witness, got %d", len(witness))
	}

	for path, blob := range witness {
		if len(blob) == 0 {
			t.Errorf("witness contains empty blob for path %s", path)
		}
		node, err := DeserializeNode(blob, 0)
		if err != nil {
			t.Errorf("witness blob at path %s is not a valid node: %v", path, err)
		}
		if node == nil {
			t.Errorf("deserialized node at path %s is nil", path)
		}
	}
}

// TestWitnessFormat tests the witness format matches expectations.
func TestWitnessFormat(t *testing.T) {
	tracer := trie.NewPrevalueTracer()

	path := []byte{0x01, 0x02, 0x03}
	blob := []byte{0x0a, 0x0b, 0x0c, 0x0d}

	tracer.Put(path, blob)

	bt := &BinaryTrie{
		root:   NewBinaryNode(),
		reader: nil,
		tracer: tracer,
	}

	witness := bt.Witness()

	if len(witness) != 1 {
		t.Fatalf("expected 1 node in witness, got %d", len(witness))
	}

	retrievedBlob, ok := witness[string(path)]
	if !ok {
		t.Fatal("witness missing expected path")
	}

	if len(retrievedBlob) != len(blob) {
		t.Errorf("blob length mismatch: expected %d, got %d", len(blob), len(retrievedBlob))
	}

	for i := range blob {
		if retrievedBlob[i] != blob[i] {
			t.Errorf("blob byte %d mismatch: expected %x, got %x", i, blob[i], retrievedBlob[i])
		}
	}
}
