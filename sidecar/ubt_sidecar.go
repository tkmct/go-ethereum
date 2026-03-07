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

package sidecar

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/trie/bintrie"
	"github.com/ethereum/go-ethereum/trie/trienode"
	"github.com/ethereum/go-ethereum/triedb"
	"github.com/ethereum/go-ethereum/triedb/pathdb"
	"github.com/holiman/uint256"
)

// SidecarState represents the current state of the UBT sidecar.
type SidecarState uint8

const (
	StateDisabled   SidecarState = iota // --ubt not set
	StateStale                          // UBT state missing or corrupted, conversion needed
	StateConverting                     // MPT→UBT conversion in progress
	StateReady                          // UBT state valid, following chain
)

// The pathdb layer stack keeps up to 128 in-memory diff layers.
// We use the same window to search for a recoverable recent UBT root.
const ubtRecoverySearchWindow = 128

const ubtAutoConvertCooldown = 30 * time.Second

// String returns the string representation of a SidecarState.
func (s SidecarState) String() string {
	switch s {
	case StateDisabled:
		return "disabled"
	case StateStale:
		return "stale"
	case StateConverting:
		return "converting"
	case StateReady:
		return "ready"
	default:
		return "unknown"
	}
}

// ChainContext provides chain information needed by the sidecar.
type ChainContext interface {
	HeadRoot() common.Hash
	HeadBlock() *types.Header
	CanonicalHash(uint64) common.Hash
}

// UBTSidecar manages a shadow UBT (Unified Binary Trie) state alongside MPT.
type UBTSidecar struct {
	mu sync.RWMutex

	// State machine
	state SidecarState

	// Conversion control
	convertCtx    context.Context
	convertCancel context.CancelFunc
	convertWg     sync.WaitGroup
	autoConvertMu sync.Mutex
	lastAutoStart time.Time

	// Current UBT state
	currentRoot  common.Hash
	currentBlock uint64
	currentHash  common.Hash

	// Databases
	triedb    *triedb.Database // UBT-specific (verkle namespace)
	chainDB   ethdb.Database   // Main chain DB for metadata and code
	mptTrieDB *triedb.Database // MPT trie DB for preimage resolution and iterators
}

// NewUBTSidecar creates a new UBT sidecar instance.
func NewUBTSidecar(chainDB ethdb.Database, mptTrieDB *triedb.Database) (*UBTSidecar, error) {
	// Create UBT trie database using verkle namespace with path scheme
	ubtTrieDB := triedb.NewDatabase(chainDB, &triedb.Config{
		IsVerkle: true,
		PathDB:   pathdb.Defaults,
	})
	return &UBTSidecar{
		state:     StateStale,
		triedb:    ubtTrieDB,
		chainDB:   chainDB,
		mptTrieDB: mptTrieDB,
	}, nil
}

// InitFromDB initializes the sidecar state from persisted metadata.
// Must be called before blockchain block processing begins.
func (sc *UBTSidecar) InitFromDB() error {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	// Check for persisted current root
	root, block, blockHash, ok := rawdb.ReadUBTCurrentRoot(sc.chainDB)
	if !ok {
		// No persisted state - check for conversion progress
		if rawdb.ReadUBTConversionProgress(sc.chainDB) != nil {
			log.Info("UBT sidecar found conversion progress, will resume")
		}
		sc.state = StateStale
		return nil
	}

	// Try to open the trie at the persisted root
	_, err := bintrie.NewBinaryTrie(root, sc.triedb)
	if err == nil {
		// Success: journal replay was good
		sc.currentRoot = root
		sc.currentBlock = block
		sc.currentHash = blockHash
		sc.state = StateReady
		log.Info("UBT sidecar ready", "block", block, "root", root)
		return nil
	}
	log.Warn("UBT sidecar current root not openable, trying disk root fallback", "root", root, "err", err)

	// Fallback: scan recent canonical blocks for an openable root.
	// Use subtraction-free comparison to avoid unsigned underflow when block < window.
	for num := block; num > 0 && block-num < uint64(ubtRecoverySearchWindow); num-- {
		hash := rawdb.ReadCanonicalHash(sc.chainDB, num)
		if hash == (common.Hash{}) {
			continue
		}
		ubtRoot, found := rawdb.ReadUBTBlockRoot(sc.chainDB, hash)
		if !found || ubtRoot == (common.Hash{}) || ubtRoot == types.EmptyBinaryHash {
			continue
		}
		// Verify we can open this root.
		if _, err := bintrie.NewBinaryTrie(ubtRoot, sc.triedb); err == nil {
			sc.currentRoot = ubtRoot
			sc.currentBlock = num
			sc.currentHash = hash
			sc.state = StateReady
			log.Info("UBT sidecar recovered via recent root", "block", num, "root", ubtRoot)
			return nil
		}
	}

	sc.state = StateStale
	log.Warn("UBT sidecar going stale, will reconvert")
	return nil
}

// State returns the current sidecar state.
func (sc *UBTSidecar) State() SidecarState {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	return sc.state
}

// CurrentRoot returns the current UBT root hash.
func (sc *UBTSidecar) CurrentRoot() common.Hash {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	return sc.currentRoot
}

// CurrentInfo returns the current UBT root, block number, and block hash.
func (sc *UBTSidecar) CurrentInfo() (common.Hash, uint64, common.Hash) {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	return sc.currentRoot, sc.currentBlock, sc.currentHash
}

// GetUBTRoot returns the UBT root for a given block hash.
func (sc *UBTSidecar) GetUBTRoot(blockHash common.Hash) (common.Hash, bool) {
	return rawdb.ReadUBTBlockRoot(sc.chainDB, blockHash)
}

// OpenBinaryTrie opens a BinaryTrie at the given root.
func (sc *UBTSidecar) OpenBinaryTrie(root common.Hash) (*bintrie.BinaryTrie, error) {
	return bintrie.NewBinaryTrie(root, sc.triedb)
}

// TrieDB returns the sidecar's trie database.
func (sc *UBTSidecar) TrieDB() *triedb.Database {
	return sc.triedb
}

// HandleStateUpdate routes a state update to the UBT sidecar based on its current state.
func (sc *UBTSidecar) HandleStateUpdate(block *types.Block, update *state.StateUpdate, chain ChainContext) {
	switch sc.State() {
	case StateReady:
		if err := sc.ApplyStateUpdate(block, update); err != nil {
			log.Error("Failed to apply UBT state update", "block", block.NumberU64(), "err", err)
			sc.MaybeStartAutoConvert("apply update", chain)
		}
	case StateConverting:
		if err := sc.EnqueueUpdate(block, update); err != nil {
			log.Error("Failed to enqueue UBT sidecar update", "block", block.NumberU64(), "err", err)
		}
	default:
		sc.MaybeStartAutoConvert("sidecar stale", chain)
	}
}

// MaybeStartAutoConvert attempts to start MPT->UBT conversion with rate limiting.
func (sc *UBTSidecar) MaybeStartAutoConvert(reason string, chain ChainContext) {
	sc.autoConvertMu.Lock()
	defer sc.autoConvertMu.Unlock()

	if time.Since(sc.lastAutoStart) < ubtAutoConvertCooldown {
		return
	}
	sc.lastAutoStart = time.Now()

	if !sc.BeginConversion() {
		return
	}
	log.Info("Starting UBT auto-conversion", "reason", reason)

	sc.ConvertWg().Add(1)
	go func() {
		defer sc.ConvertWg().Done()
		if err := sc.ConvertFromMPT(sc.ConvertCtx(), chain); err != nil {
			log.Error("UBT conversion failed", "err", err)
			sc.MaybeStartAutoConvert("conversion failed", chain)
		}
	}()
}

// ApplyStateUpdate applies a block's state changes to the UBT trie.
// Only valid when state is Ready.
func (sc *UBTSidecar) ApplyStateUpdate(block *types.Block, update *state.StateUpdate) error {
	ubtUpdate := NewUBTUpdate(block, update)
	return sc.applyUBTUpdate(ubtUpdate)
}

// applyUBTUpdate applies a UBTUpdate to the current trie.
// Steps per spec (section 6.3):
//  1. Open BinaryTrie at currentRoot
//  2. Process deletions: zero-out account basic data and code hash leaves
//  3. Process updates: UpdateAccount → UpdateContractCode → UpdateStorage
//  4. Commit: trie.Commit() → triedb.Update() (auto-cap handles disk flush)
//  5. Persist metadata: WriteUBTCurrentRoot, WriteUBTBlockRoot
func (sc *UBTSidecar) applyUBTUpdate(update *UBTUpdate) error {
	// Open trie at current root
	sc.mu.RLock()
	root := sc.currentRoot
	sc.mu.RUnlock()

	t, err := bintrie.NewBinaryTrie(root, sc.triedb)
	if err != nil {
		return sc.fail("open trie", err)
	}

	// Build resolver caches to avoid O(n²) scanning with keccak256.
	resolver := update.buildResolver(sc.chainDB)
	codeSizeByHash := make(map[common.Hash]int, len(update.Codes))

	// Step 1: Process deletions (accounts with nil data).
	// BinaryTrie.DeleteAccount is a no-op, so we explicitly zero the account's
	// basic-data and code-hash leaf values via UpdateAccount with a zeroed state.
	for addrHash, data := range update.Accounts {
		if data != nil {
			continue
		}
		addr, err := resolver.resolveAddress(addrHash)
		if err != nil {
			return sc.fail("resolve deleted address", err)
		}
		zeroAcct := &types.StateAccount{
			Nonce:    0,
			Balance:  new(uint256.Int),
			CodeHash: types.EmptyCodeHash.Bytes(),
		}
		if err := t.UpdateAccount(addr, zeroAcct, 0); err != nil {
			return sc.fail("delete account", err)
		}
		// Apply zeroed storage slots for the deleted account. The state update
		// includes these as part of the account wipe, but Step 2 skips nil
		// accounts, so we must handle them here.
		if slots, ok := update.Storages[addrHash]; ok {
			for slotHash, value := range slots {
				slotKey, err := resolver.resolveStorageKey(slotHash)
				if err != nil {
					return sc.fail("resolve deleted storage key", err)
				}
				if err := t.UpdateStorage(addr, slotKey, value); err != nil {
					return sc.fail("clear deleted storage", err)
				}
			}
		}
	}

	// Step 2: Process updates (accounts with non-nil data).
	for addrHash, data := range update.Accounts {
		if data == nil {
			continue // already handled as deletion
		}
		addr, err := resolver.resolveAddress(addrHash)
		if err != nil {
			return sc.fail("resolve address", err)
		}
		// Decode slim-RLP account data
		acct, err := types.FullAccount(data)
		if err != nil {
			return sc.fail("decode account", err)
		}
		codeHash := common.BytesToHash(acct.CodeHash)
		// Determine code length for UpdateAccount
		codeLen := 0
		if code, ok := update.Codes[addr]; ok {
			codeLen = len(code)
			codeSizeByHash[codeHash] = codeLen
		} else if codeHash != types.EmptyCodeHash {
			if cached, ok := codeSizeByHash[codeHash]; ok {
				codeLen = cached
			} else {
				codeLen = len(rawdb.ReadCode(sc.chainDB, codeHash))
				codeSizeByHash[codeHash] = codeLen
			}
		}
		if err := t.UpdateAccount(addr, acct, codeLen); err != nil {
			return sc.fail("update account", err)
		}
		// Update contract code if changed
		if code, ok := update.Codes[addr]; ok {
			if err := t.UpdateContractCode(addr, codeHash, code); err != nil {
				return sc.fail("update code", err)
			}
		}
		// Update storage slots
		if slots, ok := update.Storages[addrHash]; ok {
			for slotHash, value := range slots {
				slotKey, err := resolver.resolveStorageKey(slotHash)
				if err != nil {
					return sc.fail("resolve storage key", err)
				}
				if err := t.UpdateStorage(addr, slotKey, value); err != nil {
					return sc.fail("update storage", err)
				}
			}
		}
	}

	// Step 3: Commit trie changes → triedb.Update (auto-cap handles disk flush)
	newRoot, nodeset := t.Commit(false)
	if nodeset != nil {
		stateSet := &triedb.StateSet{
			Accounts:       update.Accounts,
			AccountsOrigin: update.AccountsOrigin,
			Storages:       update.Storages,
			StoragesOrigin: update.StoragesOrigin,
			RawStorageKey:  update.RawStorageKey,
		}
		if err := sc.triedb.Update(newRoot, root, update.BlockNum, trienode.NewWithNodeSet(nodeset), stateSet); err != nil {
			return sc.fail("triedb update", err)
		}
	}

	// Step 4: Persist metadata
	sc.mu.Lock()
	sc.currentRoot = newRoot
	sc.currentBlock = update.BlockNum
	sc.currentHash = update.BlockHash
	sc.mu.Unlock()

	rawdb.WriteUBTCurrentRoot(sc.chainDB, newRoot, update.BlockNum, update.BlockHash)
	rawdb.WriteUBTBlockRoot(sc.chainDB, update.BlockHash, newRoot)

	return nil
}

// EnqueueUpdate stores a state update in the disk queue during conversion.
func (sc *UBTSidecar) EnqueueUpdate(block *types.Block, update *state.StateUpdate) error {
	ubtUpdate := NewUBTUpdate(block, update)
	data, err := ubtUpdate.EncodeRLP()
	if err != nil {
		return fmt.Errorf("ubt encode queue entry: %w", err)
	}
	rawdb.WriteUBTUpdateQueueEntry(sc.chainDB, block.NumberU64(), block.Hash(), data)

	// Update queue metadata
	start, _, ok := rawdb.ReadUBTUpdateQueueMeta(sc.chainDB)
	if !ok {
		rawdb.WriteUBTUpdateQueueMeta(sc.chainDB, block.NumberU64(), block.NumberU64())
	} else {
		rawdb.WriteUBTUpdateQueueMeta(sc.chainDB, start, block.NumberU64())
	}
	log.Debug("UBT queue enqueue", "block", block.NumberU64())
	return nil
}

// HandleReorg handles a chain reorganization.
func (sc *UBTSidecar) HandleReorg(ancestorHash common.Hash, ancestorNum uint64) error {
	// Atomically read and transition the state under the lock to prevent
	// a TOCTOU race with the conversion goroutine calling SetReady().
	sc.mu.Lock()
	currentState := sc.state

	switch currentState {
	case StateConverting:
		// Transition to Stale under lock before releasing, so a concurrent
		// SetReady() call will see Stale and not overwrite.
		sc.state = StateStale
		cancel := sc.convertCancel
		sc.mu.Unlock()

		if cancel != nil {
			cancel()
		}
		sc.convertWg.Wait()
		log.Warn("UBT sidecar reorg during conversion, going stale")
		return nil

	case StateReady:
		sc.mu.Unlock()

		// Try diff layer rollback
		ancestorUBTRoot, found := sc.GetUBTRoot(ancestorHash)
		if !found {
			return sc.fail("reorg", fmt.Errorf("ancestor UBT root not found for %x", ancestorHash))
		}
		// Verify we can open the ancestor root
		_, err := bintrie.NewBinaryTrie(ancestorUBTRoot, sc.triedb)
		if err != nil {
			return sc.fail("reorg", fmt.Errorf("cannot open ancestor trie: %w", err))
		}
		sc.mu.Lock()
		sc.currentRoot = ancestorUBTRoot
		sc.currentBlock = ancestorNum
		sc.currentHash = ancestorHash
		sc.mu.Unlock()
		rawdb.WriteUBTCurrentRoot(sc.chainDB, ancestorUBTRoot, ancestorNum, ancestorHash)
		log.Info("UBT sidecar reorg rollback", "ancestor", ancestorNum)
		return nil

	default:
		sc.mu.Unlock()
		return nil
	}
}

// BeginConversion prepares the sidecar for MPT→UBT conversion.
// Returns true if conversion was started, false if already converting or ready.
// Clears stale queue metadata and conversion progress per spec (section 6.4.1):
// after reorg, old progress/queue must be discarded for a fresh start.
func (sc *UBTSidecar) BeginConversion() bool {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	if sc.state != StateStale {
		return false
	}
	sc.state = StateConverting
	sc.convertCtx, sc.convertCancel = context.WithCancel(context.Background())
	return true
}

// cleanupConversionState removes old queue entries, queue metadata, and
// conversion progress. Must be called when starting a fresh conversion.
func (sc *UBTSidecar) cleanupConversionState() {
	start, end, ok := rawdb.ReadUBTUpdateQueueMeta(sc.chainDB)
	if ok {
		for blockNum := start; blockNum <= end; blockNum++ {
			canonHash := rawdb.ReadCanonicalHash(sc.chainDB, blockNum)
			if canonHash != (common.Hash{}) {
				rawdb.DeleteUBTUpdateQueueEntry(sc.chainDB, blockNum, canonHash)
			}
		}
		rawdb.DeleteUBTUpdateQueueMeta(sc.chainDB)
	}
	rawdb.DeleteUBTConversionProgress(sc.chainDB)
}

// SetReady transitions the sidecar from Converting to Ready state.
// Called by ConvertFromMPT after successful conversion and queue replay.
// Returns false if the state is no longer Converting (e.g. a concurrent
// HandleReorg set it to Stale), preventing a stale goroutine from
// overwriting the reorg's state transition.
func (sc *UBTSidecar) SetReady(root common.Hash, block uint64, blockHash common.Hash) bool {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	if sc.state != StateConverting {
		return false
	}
	sc.currentRoot = root
	sc.currentBlock = block
	sc.currentHash = blockHash
	sc.state = StateReady
	return true
}

// ConvertCtx returns the conversion context. Used by the conversion goroutine.
func (sc *UBTSidecar) ConvertCtx() context.Context {
	return sc.convertCtx
}

// ConvertWg returns the conversion WaitGroup. Used by the caller that starts
// the conversion goroutine to register the goroutine.
func (sc *UBTSidecar) ConvertWg() *sync.WaitGroup {
	return &sc.convertWg
}

// Shutdown gracefully stops the sidecar.
func (sc *UBTSidecar) Shutdown() {
	if sc.convertCancel != nil {
		sc.convertCancel()
	}
	sc.convertWg.Wait()

	sc.mu.RLock()
	root := sc.currentRoot
	sc.mu.RUnlock()

	if err := sc.triedb.Commit(root, false); err != nil {
		log.Error("Failed to commit UBT triedb on shutdown", "err", err)
	}
	sc.triedb.Close()
}

// fail transitions the sidecar to Stale state and logs the error.
func (sc *UBTSidecar) fail(stage string, err error) error {
	sc.mu.Lock()
	sc.state = StateStale
	sc.mu.Unlock()
	log.Error("UBT sidecar failure", "stage", stage, "err", err)
	return fmt.Errorf("ubt sidecar %s: %w", stage, err)
}

// resolverCache pre-computes hash→address and hash→storageKey lookup maps
// to avoid O(n²) scanning with keccak256 during applyUBTUpdate.
type resolverCache struct {
	addrByHash    map[common.Hash]common.Address
	storageByHash map[common.Hash][]byte
	chainDB       ethdb.Database
}

// buildResolver creates a resolverCache from the update's origin data.
func (u *UBTUpdate) buildResolver(db ethdb.Database) *resolverCache {
	rc := &resolverCache{
		addrByHash:    make(map[common.Hash]common.Address, len(u.AccountsOrigin)),
		storageByHash: make(map[common.Hash][]byte),
		chainDB:       db,
	}
	for addr := range u.AccountsOrigin {
		rc.addrByHash[crypto.Keccak256Hash(addr.Bytes())] = addr
	}
	if u.RawStorageKey {
		for _, slots := range u.StoragesOrigin {
			for rawKey := range slots {
				rc.storageByHash[crypto.Keccak256Hash(rawKey.Bytes())] = rawKey.Bytes()
			}
		}
	}
	return rc
}

// resolveAddress resolves a full address from an account hash.
// First checks the pre-built cache, then falls back to chainDB preimages.
func (rc *resolverCache) resolveAddress(addrHash common.Hash) (common.Address, error) {
	if addr, ok := rc.addrByHash[addrHash]; ok {
		return addr, nil
	}
	preimage := rawdb.ReadPreimage(rc.chainDB, addrHash)
	if len(preimage) == 0 {
		return common.Address{}, fmt.Errorf("preimage not found for account hash %x", addrHash)
	}
	return common.BytesToAddress(preimage), nil
}

// resolveStorageKey resolves raw storage key bytes from a slot hash.
// First checks the pre-built cache, then falls back to chainDB preimages.
func (rc *resolverCache) resolveStorageKey(slotHash common.Hash) ([]byte, error) {
	if raw, ok := rc.storageByHash[slotHash]; ok {
		return raw, nil
	}
	preimage := rawdb.ReadPreimage(rc.chainDB, slotHash)
	if len(preimage) == 0 {
		return nil, fmt.Errorf("storage preimage not found for slot hash %x", slotHash)
	}
	return preimage, nil
}
