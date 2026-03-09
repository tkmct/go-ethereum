package bintrie

import (
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/trie/trienode"
)

// Subtrie builds a disjoint subtree rooted at a fixed prefix depth.
type Subtrie struct {
	depth int
	path  []byte
	root  BinaryNode
}

// NewSubtrie creates an empty subtree rooted at the provided bit prefix.
func NewSubtrie(path []byte) *Subtrie {
	return &Subtrie{
		depth: len(path),
		path:  append([]byte(nil), path...),
		root:  Empty{},
	}
}

// Insert adds a single leaf update into the subtree.
func (s *Subtrie) Insert(key, value []byte) error {
	if !hasPathPrefix(key, s.path) {
		return fmt.Errorf("subtrie insert prefix mismatch")
	}
	root, err := s.root.Insert(key, value, nil, s.depth)
	if err != nil {
		return err
	}
	s.root = root
	return nil
}

// UpdateStem adds a full stem update into the subtree.
func (s *Subtrie) UpdateStem(stem []byte, values [][]byte) error {
	if !hasPathPrefix(stem, s.path) {
		return fmt.Errorf("subtrie stem prefix mismatch")
	}
	root, err := s.root.InsertValuesAtStem(stem, values, nil, s.depth)
	if err != nil {
		return err
	}
	s.root = root
	return nil
}

// UpdateStemEntries applies a sparse stem update into the subtree.
func (s *Subtrie) UpdateStemEntries(stem []byte, positions []byte, values [][]byte) error {
	var full [StemNodeWidth][]byte
	for i, pos := range positions {
		full[pos] = values[i]
	}
	return s.UpdateStem(stem, full[:])
}

// Commit collects subtree nodes with fully-qualified paths.
func (s *Subtrie) Commit() (common.Hash, *trienode.NodeSet, error) {
	hash := s.root.Hash()
	if hash == (common.Hash{}) {
		return hash, trienode.NewNodeSet(common.Hash{}), nil
	}
	nodeset := trienode.NewNodeSet(common.Hash{})
	if err := s.root.CollectNodes(s.path, func(path []byte, node BinaryNode) {
		blob, hash := SerializeNodeAndHash(node)
		nodeset.AddNodeWithPrev(path, hash, blob, nil)
	}); err != nil {
		return common.Hash{}, nil, err
	}
	return hash, nodeset, nil
}

func hasPathPrefix(key []byte, path []byte) bool {
	for i, bit := range path {
		if ((key[i/8] >> (7 - (i % 8))) & 1) != bit {
			return false
		}
	}
	return true
}

// SerializeInternalNode serializes an internal node referencing hashed children.
func SerializeInternalNode(depth int, left, right common.Hash) ([]byte, common.Hash) {
	node := &InternalNode{
		left:      HashedNode(left),
		right:     HashedNode(right),
		depth:     depth,
		hashDirty: true,
	}
	return SerializeNodeAndHash(node)
}

// FlushTo streams all subtree nodes to the provided sink.
func (s *Subtrie) FlushTo(sink func(path []byte, blob []byte, hash common.Hash) error) (common.Hash, error) {
	hash := s.root.Hash()
	if hash == (common.Hash{}) {
		return hash, nil
	}
	var sinkErr error
	if err := s.root.CollectNodes(s.path, func(path []byte, node BinaryNode) {
		if sinkErr != nil {
			return
		}
		blob, hash := SerializeNodeAndHash(node)
		if err := sink(path, blob, hash); err != nil {
			sinkErr = err
		}
	}); err != nil {
		return common.Hash{}, err
	}
	if sinkErr != nil {
		return common.Hash{}, sinkErr
	}
	return hash, nil
}
