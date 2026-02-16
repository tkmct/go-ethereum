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
	"fmt"
	"path/filepath"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/ubtemit"
	"github.com/ethereum/go-ethereum/ethdb/leveldb"
	"github.com/ethereum/go-ethereum/ethdb/memorydb"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/trie/bintrie"
	"github.com/ethereum/go-ethereum/trie/trienode"
	"github.com/ethereum/go-ethereum/triedb"
	"github.com/ethereum/go-ethereum/triedb/pathdb"
	"github.com/holiman/uint256"
)

// Applier applies QueuedDiffV1 events to the UBT trie.
type Applier struct {
	cfg       *Config
	trieDB    *triedb.Database
	trie      *bintrie.BinaryTrie
	root      common.Hash
	diskdb    *leveldb.Database
	slotIndex *SlotIndex // Optional slot index for pre-Cancun replay correctness
}

// NewApplier creates a new UBT applier with an expected root.
// If expectedRoot is non-zero (not types.EmptyBinaryHash), it will attempt to open
// the trie with that root and fail if the root doesn't exist.
// If expectedRoot is zero/empty, it starts with an empty trie.
func NewApplier(cfg *Config, expectedRoot common.Hash) (*Applier, error) {
	// Open trie database with path scheme
	dbPath := filepath.Join(cfg.DataDir, "triedb")
	diskdb, err := leveldb.New(dbPath, 512, 256, "ubtconv/triedb", false)
	if err != nil {
		return nil, fmt.Errorf("failed to open trie DB: %w", err)
	}

	trieConfig := &triedb.Config{
		PathDB: &pathdb.Config{
			StateHistory:    cfg.TrieDBStateHistory,
			TrieCleanSize:   256 * 1024 * 1024, // 256 MB
			WriteBufferSize: 256 * 1024 * 1024, // 256 MB
		},
	}
	tdb := triedb.NewDatabase(rawdb.NewDatabase(diskdb), trieConfig)

	root := expectedRoot
	if root == (common.Hash{}) {
		// No expected root provided, use empty
		root = types.EmptyBinaryHash
	}

	// Try to open trie with the expected root
	tr, err := bintrie.NewBinaryTrie(root, tdb)
	if err != nil {
		tdb.Close() // Close trieDB which will close the underlying diskdb
		if expectedRoot != (common.Hash{}) && expectedRoot != types.EmptyBinaryHash {
			// Expected root was provided but doesn't exist - this is an error
			return nil, fmt.Errorf("failed to open trie with expected root %s: %w", expectedRoot, err)
		}
		// Fall back to empty trie only if no specific root was expected
		return nil, fmt.Errorf("failed to create binary trie: %w", err)
	}

	log.Info("Applier initialized", "root", root)

	return &Applier{
		cfg:    cfg,
		trieDB: tdb,
		trie:   tr,
		root:   root,
		diskdb: diskdb,
	}, nil
}

// ApplyDiff applies a QueuedDiffV1 to the UBT trie.
// blockNumber is used for slot index tracking (pre/post-Cancun semantics).
// Returns the new UBT root hash (note: not committed to disk yet).
func (a *Applier) ApplyDiff(diff *ubtemit.QueuedDiffV1, blockNumber ...uint64) (common.Hash, error) {
	blkNum := uint64(0)
	if len(blockNumber) > 0 {
		blkNum = blockNumber[0]
	}
	codeByAddr := make(map[common.Address][]byte, len(diff.Codes))
	for i := range diff.Codes {
		codeByAddr[diff.Codes[i].Address] = diff.Codes[i].Code
	}
	// Apply accounts
	for _, acct := range diff.Accounts {
		if acct.Alive {
			// Account exists - update it
			bal, overflow := uint256.FromBig(acct.Balance)
			if overflow {
				return common.Hash{}, fmt.Errorf("balance overflow for account %s: %s", acct.Address, acct.Balance)
			}
			if bal.BitLen() > 128 {
				return common.Hash{}, fmt.Errorf("balance exceeds UBT 128-bit limit for account %s: %s (needs %d bits)", acct.Address, acct.Balance, bal.BitLen())
			}
			stateAcct := &types.StateAccount{
				Nonce:    acct.Nonce,
				Balance:  bal,
				Root:     types.EmptyRootHash, // UBT doesn't use per-account storage roots
				CodeHash: acct.CodeHash.Bytes(),
			}
			codeLen := len(codeByAddr[acct.Address])
			if err := a.trie.UpdateAccount(acct.Address, stateAcct, codeLen); err != nil {
				return common.Hash{}, fmt.Errorf("update account %s: %w", acct.Address, err)
			}
		} else {
			// Account deleted - zero it out
			zeroAcct := &types.StateAccount{
				Nonce:    0,
				Balance:  new(uint256.Int),
				Root:     types.EmptyRootHash,
				CodeHash: types.EmptyCodeHash.Bytes(),
			}
			if err := a.trie.UpdateAccount(acct.Address, zeroAcct, 0); err != nil {
				return common.Hash{}, fmt.Errorf("delete account %s: %w", acct.Address, err)
			}
			// Clean up slot index entries for deleted account
			if a.slotIndex != nil {
				if err := a.slotIndex.DeleteSlotsForAccount(acct.Address); err != nil {
					log.Warn("Slot index cleanup failed", "addr", acct.Address, "err", err)
				}
			}
			log.Debug("UBT account deleted", "addr", acct.Address)
		}
	}

	// Apply storage
	for _, slot := range diff.Storage {
		if err := a.trie.UpdateStorage(slot.Address, slot.SlotKeyRaw.Bytes(), slot.Value.Bytes()); err != nil {
			return common.Hash{}, fmt.Errorf("update storage %s/%s: %w", slot.Address, slot.SlotKeyRaw, err)
		}
		// Track slot in index if enabled
		if a.slotIndex != nil && a.slotIndex.ShouldIndex(blkNum) {
			if err := a.slotIndex.TrackSlot(slot.Address, slot.SlotKeyRaw, blkNum); err != nil {
				log.Warn("Slot index track failed", "addr", slot.Address, "slot", slot.SlotKeyRaw, "err", err)
			}
		}
	}

	// Apply code
	for _, code := range diff.Codes {
		if err := a.trie.UpdateContractCode(code.Address, code.CodeHash, code.Code); err != nil {
			return common.Hash{}, fmt.Errorf("update code %s: %w", code.Address, err)
		}
		// Store raw code in diskdb for StateDB code lookups (used by CallUBT)
		rawdb.WriteCode(a.diskdb, code.CodeHash, code.Code)
	}

	// Return the current trie root (uncommitted)
	return a.trie.Hash(), nil
}

// Commit commits the current trie state to the database.
// It uses block=0 as a generic commit marker.
func (a *Applier) Commit() error {
	return a.CommitAt(0)
}

// CommitAt commits the current trie state to the database with the given block number.
// It uses trieDB.Update to add a new diff layer (which PathDB manages automatically,
// capping at maxDiffLayers=128). It does NOT call trieDB.Commit, which would flatten
// all diff layers to disk and destroy historical root accessibility. Use Flush() for
// periodic or shutdown persistence.
func (a *Applier) CommitAt(blockNumber uint64) error {
	// First, commit the trie to get the root and node set
	root, nodes := a.trie.Commit(false)
	if nodes == nil {
		log.Debug("No trie nodes to commit")
		a.root = root
		return nil
	}
	updates, _ := nodes.Size()
	if updates == 0 {
		log.Debug("No trie node updates to commit")
		a.root = root
		return nil
	}

	// Use triedb.Update to persist nodes as a new diff layer.
	// PathDB automatically caps diff layers at 128, flushing the oldest to disk.
	set := trienode.NewWithNodeSet(nodes)
	states := triedb.NewStateSet()
	parent := a.root
	if parent == (common.Hash{}) {
		// PathDB initializes on EmptyRootHash; map zero empty-binary root on first commit.
		parent = types.EmptyRootHash
	}
	if err := a.trieDB.Update(root, parent, blockNumber, set, states); err != nil {
		return fmt.Errorf("trie DB update: %w", err)
	}
	a.root = root

	// Reopen the trie from the new diff layer (the in-memory trie nodes were
	// collected into NodeSet by trie.Commit and the trie object is now stale).
	tr, err := bintrie.NewBinaryTrie(root, a.trieDB)
	if err != nil {
		return fmt.Errorf("reopen trie after commit: %w", err)
	}
	a.trie = tr

	log.Debug("UBT trie committed", "root", root)
	return nil
}

// Flush persists all diff layers to disk. Call this during shutdown or
// periodically for durability. Unlike CommitAt, this flattens layers and
// makes historical roots inaccessible.
func (a *Applier) Flush() error {
	if a.root == (common.Hash{}) || a.root == types.EmptyBinaryHash {
		return nil
	}
	return a.trieDB.Commit(a.root, false)
}

// Revert reopens the trie at the given root, discarding any uncommitted state.
// This is used for reorg recovery to roll back to a historical state.
func (a *Applier) Revert(root common.Hash) error {
	tr, err := bintrie.NewBinaryTrie(root, a.trieDB)
	if err != nil {
		return fmt.Errorf("failed to open trie at root %s: %w", root, err)
	}
	a.trie = tr
	a.root = root
	log.Info("UBT trie reverted", "root", root)
	return nil
}

// Trie returns the current BinaryTrie instance.
func (a *Applier) Trie() *bintrie.BinaryTrie {
	return a.trie
}

// TrieAt opens a read trie at a specific committed root.
func (a *Applier) TrieAt(root common.Hash) (*bintrie.BinaryTrie, error) {
	if root == (common.Hash{}) {
		root = types.EmptyBinaryHash
	}
	tr, err := bintrie.NewBinaryTrie(root, a.trieDB)
	if err != nil {
		return nil, fmt.Errorf("open trie at root %s: %w", root, err)
	}
	return tr, nil
}

// Close persists state to disk via journal and releases resources.
func (a *Applier) Close() {
	if a.trieDB != nil {
		// Journal writes all dirty state (including the root) so pathdb can
		// recover it on the next open. Flush (trieDB.Commit) only flattens
		// diff layers without writing a journal, which prevents restart.
		if a.root != (common.Hash{}) && a.root != types.EmptyBinaryHash {
			if err := a.trieDB.Journal(a.root); err != nil {
				log.Error("Failed to journal trie state", "root", a.root, "err", err)
			}
		}
		a.trieDB.Close()
	}
	if a.diskdb != nil {
		a.diskdb.Close()
	}
}

// DiskDB returns the underlying disk database for raw key-value access.
func (a *Applier) DiskDB() *leveldb.Database {
	return a.diskdb
}

// TrieDB returns the underlying trie database.
func (a *Applier) TrieDB() *triedb.Database {
	return a.trieDB
}

// Root returns the current UBT trie root hash.
func (a *Applier) Root() common.Hash {
	return a.root
}

// SetSlotIndex sets the slot index for tracking storage slot metadata.
func (a *Applier) SetSlotIndex(si *SlotIndex) {
	a.slotIndex = si
}

// SlotIndex returns the current slot index, or nil if not set.
func (a *Applier) SlotIndex() *SlotIndex {
	return a.slotIndex
}

// ValidateProofRequest checks if a proof request is valid.
func (a *Applier) ValidateProofRequest(key []byte) error {
	if len(key) != 32 {
		return fmt.Errorf("invalid key length %d, expected 32", len(key))
	}
	if a.root == (common.Hash{}) {
		return fmt.Errorf("UBT trie has no committed root")
	}
	return nil
}

// GenerateProof generates a Merkle proof for the given key against the current UBT state.
// The key should be a 32-byte hash (e.g., account address hash for account proofs).
// Returns a map of hash->serialized_node pairs forming the proof.
func (a *Applier) GenerateProof(key []byte) (map[common.Hash][]byte, error) {
	// Preserve existing behavior for "current state" proof generation:
	// require a committed, non-zero root.
	if err := a.ValidateProofRequest(key); err != nil {
		return nil, err
	}
	return a.GenerateProofAt(a.root, key)
}

// GenerateProofAt generates a Merkle proof at a specific root.
func (a *Applier) GenerateProofAt(root common.Hash, key []byte) (map[common.Hash][]byte, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("invalid key length %d, expected 32", len(key))
	}
	// Reuse the live trie only when the requested root matches the current
	// in-memory trie hash exactly (supports uncommitted-root proofs in tests).
	// For historical/committed-root proofs while newer uncommitted mutations
	// exist, open an explicit snapshot trie at the requested root.
	var (
		tr  *bintrie.BinaryTrie
		err error
	)
	if a.trie != nil && root == a.trie.Hash() {
		tr = a.trie
	} else {
		tr, err = a.TrieAt(root)
		if err != nil {
			return nil, err
		}
	}

	proofDb := memorydb.New()
	if err := tr.Prove(key, proofDb); err != nil {
		return nil, fmt.Errorf("prove failed: %w", err)
	}

	result := make(map[common.Hash][]byte)
	it := proofDb.NewIterator(nil, nil)
	defer it.Release()
	for it.Next() {
		key := common.BytesToHash(it.Key())
		val := make([]byte, len(it.Value()))
		copy(val, it.Value())
		result[key] = val
	}
	return result, nil
}
