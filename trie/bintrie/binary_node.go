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
	"crypto/sha256"
	"errors"

	"github.com/ethereum/go-ethereum/common"
)

type (
	NodeFlushFn    func([]byte, BinaryNode)
	NodeResolverFn func([]byte, common.Hash) ([]byte, error)
)

// zero is the zero value for a 32-byte array.
var zero [32]byte

const (
	StemNodeWidth = 256 // Number of child per leaf node
	StemSize      = 31  // Number of bytes to travel before reaching a group of leaves
	NodeTypeBytes = 1   // Size of node type prefix in serialization
	HashSize      = 32  // Size of a hash in bytes
	BitmapSize    = 32  // Size of the bitmap in a stem node
)

const (
	nodeTypeStem = iota + 1 // Stem node, contains a stem and a bitmap of values
	nodeTypeInternal
)

// BinaryNode is an interface for a binary trie node.
type BinaryNode interface {
	Get([]byte, NodeResolverFn) ([]byte, error)
	Insert([]byte, []byte, NodeResolverFn, int) (BinaryNode, error)
	Copy() BinaryNode
	Hash() common.Hash
	GetValuesAtStem([]byte, NodeResolverFn) ([][]byte, error)
	InsertValuesAtStem([]byte, [][]byte, NodeResolverFn, int) (BinaryNode, error)
	CollectNodes([]byte, NodeFlushFn) error

	toDot(parent, path string) string
	GetHeight() int
}

// SerializeNode serializes a binary trie node into a byte slice.
func SerializeNode(node BinaryNode) []byte {
	serialized, _ := SerializeNodeAndHash(node)
	return serialized
}

// SerializeNodeAndHash serializes a node and computes its hash in one pass.
func SerializeNodeAndHash(node BinaryNode) ([]byte, common.Hash) {
	switch n := (node).(type) {
	case *InternalNode:
		// InternalNode: 1 byte type + 32 bytes left hash + 32 bytes right hash
		var serialized [NodeTypeBytes + HashSize + HashSize]byte
		serialized[0] = nodeTypeInternal
		leftHash := n.left.Hash()
		rightHash := n.right.Hash()
		copy(serialized[1:33], leftHash[:])
		copy(serialized[33:65], rightHash[:])
		return serialized[:], common.Hash(sha256.Sum256(serialized[1:]))
	case *StemNode:
		// StemNode: 1 byte type + 31 bytes stem + 32 bytes bitmap + 256*32 bytes values
		var serialized [NodeTypeBytes + StemSize + BitmapSize + StemNodeWidth*HashSize]byte
		var data [StemNodeWidth]common.Hash
		serialized[0] = nodeTypeStem
		copy(serialized[NodeTypeBytes:NodeTypeBytes+StemSize], n.Stem)
		bitmap := serialized[NodeTypeBytes+StemSize : NodeTypeBytes+StemSize+BitmapSize]
		offset := NodeTypeBytes + StemSize + BitmapSize
		for i, v := range n.Values {
			if v != nil {
				data[i] = common.Hash(sha256.Sum256(v))
				bitmap[i/8] |= 1 << (7 - (i % 8))
				copy(serialized[offset:offset+HashSize], v)
				offset += HashSize
			}
		}
		var branchBuf [HashSize * 2]byte
		for level := 1; level <= 8; level++ {
			for i := range StemNodeWidth / (1 << level) {
				if data[i*2] == (common.Hash{}) && data[i*2+1] == (common.Hash{}) {
					data[i] = common.Hash{}
					continue
				}
				copy(branchBuf[:HashSize], data[i*2][:])
				copy(branchBuf[HashSize:], data[i*2+1][:])
				data[i] = common.Hash(sha256.Sum256(branchBuf[:]))
			}
		}
		var rootBuf [StemSize + 1 + HashSize]byte
		copy(rootBuf[:StemSize], n.Stem)
		rootBuf[StemSize] = 0
		copy(rootBuf[StemSize+1:], data[0][:])
		return serialized[:offset], common.Hash(sha256.Sum256(rootBuf[:]))
	default:
		panic("invalid node type")
	}
}

var invalidSerializedLength = errors.New("invalid serialized node length")

// DeserializeNode deserializes a binary trie node from a byte slice.
func DeserializeNode(serialized []byte, depth int) (BinaryNode, error) {
	if len(serialized) == 0 {
		return Empty{}, nil
	}

	switch serialized[0] {
	case nodeTypeInternal:
		if len(serialized) != 65 {
			return nil, invalidSerializedLength
		}
		return &InternalNode{
			depth:     depth,
			left:      HashedNode(common.BytesToHash(serialized[1:33])),
			right:     HashedNode(common.BytesToHash(serialized[33:65])),
			hashDirty: true,
		}, nil
	case nodeTypeStem:
		if len(serialized) < 64 {
			return nil, invalidSerializedLength
		}
		var values [StemNodeWidth][]byte
		bitmap := serialized[NodeTypeBytes+StemSize : NodeTypeBytes+StemSize+BitmapSize]
		offset := NodeTypeBytes + StemSize + BitmapSize

		for i := range StemNodeWidth {
			if bitmap[i/8]>>(7-(i%8))&1 == 1 {
				if len(serialized) < offset+HashSize {
					return nil, invalidSerializedLength
				}
				values[i] = serialized[offset : offset+HashSize]
				offset += HashSize
			}
		}
		return &StemNode{
			Stem:      serialized[NodeTypeBytes : NodeTypeBytes+StemSize],
			Values:    values[:],
			depth:     depth,
			hashDirty: true,
		}, nil
	default:
		return nil, errors.New("invalid node type")
	}
}

// ToDot converts the binary trie to a DOT language representation. Useful for debugging.
func ToDot(root BinaryNode) string {
	return root.toDot("", "")
}
