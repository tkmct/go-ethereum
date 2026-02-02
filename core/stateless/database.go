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

package stateless

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethdb"
)

// MakeHashDB imports tries, codes and block hashes from a witness into a new
// hash-based memory db. We could eventually rewrite this into a pathdb, but
// simple is better for now.
//
// Note, this hashdb approach is quite strictly self-validating:
//   - Headers are persisted keyed by hash, so blockhash will error on junk
//   - Codes are persisted keyed by hash, so bytecode lookup will error on junk
//   - Trie nodes are persisted keyed by hash, so trie expansion will error on junk
//
// Acceleration structures built would need to explicitly validate the witness.
func (w *Witness) MakeHashDB() ethdb.Database {
	var (
		memdb  = rawdb.NewMemoryDatabase()
		hasher = crypto.NewKeccakState()
		hash   = make([]byte, 32)
	)
	// Inject all the "block hashes" (i.e. headers) into the ephemeral database
	for _, header := range w.Headers {
		rawdb.WriteHeader(memdb, header)
	}
	// Inject all the bytecodes into the ephemeral database
	for code := range w.Codes {
		blob := []byte(code)

		hasher.Reset()
		hasher.Write(blob)
		hasher.Read(hash)

		rawdb.WriteCode(memdb, common.BytesToHash(hash), blob)
	}
	// Inject all the MPT trie nodes into the ephemeral database
	for node := range w.State {
		blob := []byte(node)

		hasher.Reset()
		hasher.Write(blob)
		hasher.Read(hash)

		rawdb.WriteLegacyTrieNode(memdb, common.BytesToHash(hash), blob)
	}
	return memdb
}

// MakePathDB imports binary trie nodes, codes and block hashes from a witness
// into a new path-based memory db for UBT/PathScheme support.
//
// PathDB wraps the database with VerklePrefix, so nodes must be stored with
// the prefix already applied to be accessible through the wrapped interface.
func (w *Witness) MakePathDB() ethdb.Database {
	memdb := rawdb.NewMemoryDatabase()
	verkleDB := rawdb.NewTable(memdb, string(rawdb.VerklePrefix))
	hasher := crypto.NewKeccakState()
	hash := make([]byte, 32)

	// Inject all the "block hashes" (i.e. headers) into the ephemeral database
	for _, header := range w.Headers {
		rawdb.WriteHeader(memdb, header)
	}

	// Inject all the bytecodes into the ephemeral database
	for code := range w.Codes {
		blob := []byte(code)

		hasher.Reset()
		hasher.Write(blob)
		hasher.Read(hash)

		rawdb.WriteCode(memdb, common.BytesToHash(hash), blob)
	}

	// For UBT/PathScheme, use StatePaths if available (preserves paths)
	// The root node of the trie is always located at the empty path. We must
	// load it from this specific path to avoid ambiguity in case of hash collisions.
	if rootBlob, ok := w.StatePaths[""]; ok {
		// Write the root node at the special 'nil' path for pathdb.
		rawdb.WriteAccountTrieNode(verkleDB, nil, rootBlob)
	}

	// Inject all other trie nodes into the ephemeral database.
	for pathStr, blob := range w.StatePaths {
		if pathStr != "" {
			rawdb.WriteAccountTrieNode(verkleDB, []byte(pathStr), blob)
		}
	}

	return memdb
}
