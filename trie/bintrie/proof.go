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

package bintrie

import (
	"crypto/sha256"
	"fmt"
	"sort"

	"github.com/ethereum/go-ethereum/common"
)

// ProofResult holds the output of GenerateProofWithPath.
type ProofResult struct {
	Siblings []ProofSibling
	Stem     []byte
	Values   [][]byte
	Root     common.Hash
}

// ProofSibling represents a sibling hash with the depth of the internal node.
type ProofSibling struct {
	Depth uint16
	Hash  common.Hash
}

// Proof returns a Merkle proof for the given key in the UBT. The proof format is:
//   - siblings (from root to leaf), each 32-byte hash
//   - if a stem node is reached: the stem (31 bytes) followed by 256 values
//   - if the key is missing and no stem is reached: only the siblings are returned
func (t *BinaryTrie) Proof(key []byte) ([][]byte, error) {
	if len(key) != HashSize {
		return nil, fmt.Errorf("invalid key length: %d", len(key))
	}
	siblings, stem, values, err := t.ProofWithDepth(key)
	if err != nil {
		return nil, err
	}
	proof := make([][]byte, 0, len(siblings)+1+len(values))
	for _, s := range siblings {
		proof = append(proof, s.Hash.Bytes())
	}
	if stem != nil {
		proof = append(proof, stem)
		for _, v := range values {
			proof = append(proof, v)
		}
	}
	return proof, nil
}

// ProofWithDepth returns siblings with their internal-node depths, plus the stem
// and values if a stem node is reached.
func (t *BinaryTrie) ProofWithDepth(key []byte) ([]ProofSibling, []byte, [][]byte, error) {
	node := t.root
	siblings := make([]ProofSibling, 0, 32)

	for {
		switch n := node.(type) {
		case Empty:
			return siblings, nil, nil, nil
		case *StemNode:
			return siblings, n.Stem, n.Values, nil
		case *InternalNode:
			bit := key[n.depth/8] >> (7 - (n.depth % 8)) & 1
			var child, sibling BinaryNode
			if bit == 0 {
				child, sibling = n.left, n.right
			} else {
				child, sibling = n.right, n.left
			}
			if sibling == nil {
				siblings = append(siblings, ProofSibling{Depth: uint16(n.depth), Hash: common.Hash{}})
			} else {
				siblings = append(siblings, ProofSibling{Depth: uint16(n.depth), Hash: sibling.Hash()})
			}
			if child == nil {
				return siblings, nil, nil, nil
			}
			if hn, ok := child.(HashedNode); ok {
				path, err := keyToPath(n.depth, key[:StemSize])
				if err != nil {
					return nil, nil, nil, err
				}
				data, err := t.nodeResolver(path, common.Hash(hn))
				if err != nil {
					return nil, nil, nil, err
				}
				resolved, err := DeserializeNode(data, n.depth+1)
				if err != nil {
					return nil, nil, nil, err
				}
				child = resolved
			}
			node = child
		case HashedNode:
			data, err := t.nodeResolver(nil, common.Hash(n))
			if err != nil {
				return nil, nil, nil, err
			}
			resolved, err := DeserializeNode(data, 0)
			if err != nil {
				return nil, nil, nil, err
			}
			node = resolved
		default:
			return nil, nil, nil, errInvalidRootType
		}
	}
}

// GenerateProofWithPath generates a Merkle proof with depth-annotated siblings
// and computes the proof root from the sibling path.
func GenerateProofWithPath(bt *BinaryTrie, key []byte) (*ProofResult, error) {
	siblings, stem, values, err := bt.ProofWithDepth(key)
	if err != nil {
		return nil, err
	}
	leaf := computeLeafHash(stem, values)
	root := computeRootWithPath(key, siblings, leaf)
	return &ProofResult{
		Siblings: siblings,
		Stem:     stem,
		Values:   values,
		Root:     root,
	}, nil
}

// computeLeafHash computes the hash of a stem node from its stem and values.
func computeLeafHash(stem []byte, values [][]byte) common.Hash {
	if stem == nil {
		return common.Hash{}
	}
	var data [StemNodeWidth]common.Hash
	for i, v := range values {
		if len(v) == 0 {
			continue
		}
		h := sha256.Sum256(v)
		data[i] = common.BytesToHash(h[:])
	}
	h := sha256.New()
	for level := 1; level <= 8; level++ {
		for i := 0; i < StemNodeWidth/(1<<level); i++ {
			if data[i*2] == (common.Hash{}) && data[i*2+1] == (common.Hash{}) {
				data[i] = common.Hash{}
				continue
			}
			h.Reset()
			h.Write(data[i*2][:])
			h.Write(data[i*2+1][:])
			data[i] = common.Hash(h.Sum(nil))
		}
	}
	h.Reset()
	h.Write(stem)
	h.Write([]byte{0})
	h.Write(data[0][:])
	return common.BytesToHash(h.Sum(nil))
}

// computeRootWithPath recomputes the trie root by walking the sibling path
// from leaf to root.
func computeRootWithPath(key []byte, siblings []ProofSibling, leaf common.Hash) common.Hash {
	if len(siblings) == 0 {
		return leaf
	}
	ordered := make([]ProofSibling, len(siblings))
	copy(ordered, siblings)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Depth < ordered[j].Depth })
	current := leaf
	for i := len(ordered) - 1; i >= 0; i-- {
		depth := int(ordered[i].Depth)
		bit := (key[depth/8] >> (7 - (depth % 8))) & 1
		if bit == 0 {
			current = hashPair(current, ordered[i].Hash)
		} else {
			current = hashPair(ordered[i].Hash, current)
		}
	}
	return current
}

// hashPair computes SHA-256(left || right).
func hashPair(left, right common.Hash) common.Hash {
	var data [64]byte
	copy(data[:32], left[:])
	copy(data[32:], right[:])
	sum := sha256.Sum256(data[:])
	return common.BytesToHash(sum[:])
}
