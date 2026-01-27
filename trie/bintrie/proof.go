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
	"fmt"

	"github.com/ethereum/go-ethereum/common"
)

// Proof returns a Merkle proof for the given key in the UBT. The proof format is:
//   - siblings (from root to leaf), each 32-byte hash
//   - if a stem node is reached: the stem (31 bytes) followed by 256 values
//   - if the key is missing and no stem is reached: only the siblings are returned
func (t *BinaryTrie) Proof(key []byte) ([][]byte, error) {
	if len(key) != HashSize {
		return nil, fmt.Errorf("invalid key length: %d", len(key))
	}
	return t.proofByTraversal(key)
}

func (t *BinaryTrie) proofByTraversal(key []byte) ([][]byte, error) {
	node := t.root
	siblings := make([][]byte, 0, 32)

	for {
		switch n := node.(type) {
		case Empty:
			return siblings, nil
		case *StemNode:
			proof := make([][]byte, 0, len(siblings)+1+StemNodeWidth)
			proof = append(proof, siblings...)
			proof = append(proof, n.Stem)
			proof = append(proof, n.Values...)
			return proof, nil
		case *InternalNode:
			bit := key[n.depth/8] >> (7 - (n.depth % 8)) & 1
			var child, sibling BinaryNode
			if bit == 0 {
				child, sibling = n.left, n.right
			} else {
				child, sibling = n.right, n.left
			}
			if sibling == nil {
				siblings = append(siblings, common.Hash{}.Bytes())
			} else {
				siblings = append(siblings, sibling.Hash().Bytes())
			}
			if child == nil {
				return siblings, nil
			}
			if hn, ok := child.(HashedNode); ok {
				path, err := keyToPath(n.depth, key[:StemSize])
				if err != nil {
					return nil, err
				}
				data, err := t.nodeResolver(path, common.Hash(hn))
				if err != nil {
					return nil, err
				}
				resolved, err := DeserializeNode(data, n.depth+1)
				if err != nil {
					return nil, err
				}
				child = resolved
			}
			node = child
		case HashedNode:
			data, err := t.nodeResolver(nil, common.Hash(n))
			if err != nil {
				return nil, err
			}
			resolved, err := DeserializeNode(data, 0)
			if err != nil {
				return nil, err
			}
			node = resolved
		default:
			return nil, errInvalidRootType
		}
	}
}
