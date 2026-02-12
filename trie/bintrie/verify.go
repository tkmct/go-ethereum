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
	"bytes"
	"errors"
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethdb"
)

var (
	// ErrProofMissingNode is returned when a required proof node is not found in the database.
	ErrProofMissingNode = errors.New("proof node not found")

	// ErrProofMalformedNode is returned when a proof node cannot be deserialized.
	ErrProofMalformedNode = errors.New("malformed proof node")

	// ErrProofHashMismatch is returned when a proof node's hash doesn't match the expected hash.
	ErrProofHashMismatch = errors.New("proof node hash mismatch")

	// ErrProofInvalidKeyLength is returned when the key length doesn't match the expected size.
	ErrProofInvalidKeyLength = errors.New("invalid proof key length")
)

// VerifyProof verifies a Merkle proof for the given key against the expected root hash.
// The proofDb contains the proof nodes keyed by their hash.
// Returns the value at the key (nil for absence proofs), or an error if verification fails.
func VerifyProof(rootHash common.Hash, key []byte, proofDb ethdb.KeyValueReader) ([]byte, error) {
	if len(key) != HashSize {
		return nil, fmt.Errorf("%w: got %d, expected %d", ErrProofInvalidKeyLength, len(key), HashSize)
	}
	if rootHash == (common.Hash{}) {
		return nil, nil // empty trie
	}

	// Start from the root
	currentHash := rootHash
	depth := 0

	for {
		// Fetch the node from the proof database
		nodeData, err := proofDb.Get(currentHash[:])
		if err != nil {
			return nil, fmt.Errorf("%w for hash %s at depth %d: %v", ErrProofMissingNode, currentHash, depth, err)
		}

		node, err := DeserializeNode(nodeData, depth)
		if err != nil {
			return nil, fmt.Errorf("%w at depth %d: %v", ErrProofMalformedNode, depth, err)
		}

		// Verify the node's hash matches the expected hash.
		// This prevents forged proofs where attacker-supplied nodes
		// have correct structure but incorrect content.
		nodeHash := node.Hash()
		if nodeHash != currentHash {
			return nil, fmt.Errorf("%w at depth %d: expected %s, got %s", ErrProofHashMismatch, depth, currentHash, nodeHash)
		}

		switch n := node.(type) {
		case *InternalNode:
			// Follow the key bit to determine which child to visit
			bit := key[depth/8] >> (7 - (depth % 8)) & 1
			if bit == 0 {
				childHash := n.left.Hash()
				if childHash == (common.Hash{}) {
					return nil, nil // absence proof: empty child
				}
				currentHash = childHash
			} else {
				childHash := n.right.Hash()
				if childHash == (common.Hash{}) {
					return nil, nil // absence proof: empty child
				}
				currentHash = childHash
			}
			depth++

		case *StemNode:
			// Check if the stem matches the key prefix
			if !bytes.Equal(n.Stem, key[:StemSize]) {
				// Different stem - absence proof
				return nil, nil
			}
			// The stem matches. Look up the value at the sub-index.
			subIndex := int(key[StemSize])
			if subIndex >= len(n.Values) || n.Values[subIndex] == nil {
				return nil, nil // absence proof: no value at sub-index
			}
			return n.Values[subIndex], nil

		case Empty:
			return nil, nil // absence proof

		default:
			return nil, fmt.Errorf("unexpected node type %T at depth %d", node, depth)
		}
	}
}
