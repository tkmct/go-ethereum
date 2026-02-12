// Copyright 2024 The go-ethereum Authors
// This file is part of go-ethereum.
//
// go-ethereum is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// go-ethereum is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with go-ethereum. If not, see <http://www.gnu.org/licenses/>.

package main

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/state/snapshot"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/trie/bintrie"
	"github.com/ethereum/go-ethereum/triedb"
)

// ubtStateDatabase implements state.Database for UBT trie access.
// It provides the necessary adapters for creating a state.StateDB
// backed by a BinaryTrie.
type ubtStateDatabase struct {
	trieDB *triedb.Database
	diskdb ethdb.KeyValueStore
}

// newUBTStateDatabase creates a new state.Database adapter for the UBT.
func newUBTStateDatabase(trieDB *triedb.Database, diskdb ethdb.KeyValueStore) *ubtStateDatabase {
	return &ubtStateDatabase{
		trieDB: trieDB,
		diskdb: diskdb,
	}
}

// Reader returns a state reader for the given root.
func (db *ubtStateDatabase) Reader(root common.Hash) (state.Reader, error) {
	tr, err := bintrie.NewBinaryTrie(root, db.trieDB)
	if err != nil {
		return nil, err
	}
	return &ubtReader{trie: tr, diskdb: db.diskdb}, nil
}

// OpenTrie opens the main account trie at the given root.
func (db *ubtStateDatabase) OpenTrie(root common.Hash) (state.Trie, error) {
	return bintrie.NewBinaryTrie(root, db.trieDB)
}

// OpenStorageTrie returns the same trie since UBT uses a unified key space.
func (db *ubtStateDatabase) OpenStorageTrie(stateRoot common.Hash, address common.Address, root common.Hash, self state.Trie) (state.Trie, error) {
	return self, nil
}

// TrieDB returns the underlying trie database.
func (db *ubtStateDatabase) TrieDB() *triedb.Database {
	return db.trieDB
}

// Snapshot returns nil since snapshots are not used in the UBT daemon.
func (db *ubtStateDatabase) Snapshot() *snapshot.Tree {
	return nil
}

// ubtReader implements state.Reader (ContractCodeReader + StateReader)
// for reading state from a BinaryTrie.
type ubtReader struct {
	trie   *bintrie.BinaryTrie
	diskdb ethdb.KeyValueStore
}

// Account retrieves the account at the given address.
func (r *ubtReader) Account(addr common.Address) (*types.StateAccount, error) {
	return r.trie.GetAccount(addr)
}

// Storage retrieves the storage value at the given address and slot.
func (r *ubtReader) Storage(addr common.Address, slot common.Hash) (common.Hash, error) {
	val, err := r.trie.GetStorage(addr, slot.Bytes())
	if err != nil {
		return common.Hash{}, err
	}
	if val == nil {
		return common.Hash{}, nil
	}
	return common.BytesToHash(val), nil
}

// Code retrieves the contract code by address and code hash.
func (r *ubtReader) Code(addr common.Address, codeHash common.Hash) ([]byte, error) {
	if codeHash == types.EmptyCodeHash || codeHash == (common.Hash{}) {
		return nil, nil
	}
	code := rawdb.ReadCodeWithPrefix(r.diskdb, codeHash)
	if code != nil {
		return code, nil
	}
	// Fallback: reconstruct from trie chunks
	return r.trie.GetCode(addr)
}

// CodeSize returns the size of the contract code.
func (r *ubtReader) CodeSize(addr common.Address, codeHash common.Hash) (int, error) {
	code, err := r.Code(addr, codeHash)
	if err != nil {
		return 0, err
	}
	return len(code), nil
}

// Has returns whether the contract code exists.
func (r *ubtReader) Has(addr common.Address, codeHash common.Hash) bool {
	if codeHash == types.EmptyCodeHash || codeHash == (common.Hash{}) {
		return false
	}
	if rawdb.HasCodeWithPrefix(r.diskdb, codeHash) {
		return true
	}
	// Fallback: check if code can be reconstructed from trie chunks
	code, err := r.trie.GetCode(addr)
	return err == nil && code != nil
}

// Ensure interfaces are satisfied at compile time.
var (
	_ state.Database = (*ubtStateDatabase)(nil)
	_ state.Reader   = (*ubtReader)(nil)
)
