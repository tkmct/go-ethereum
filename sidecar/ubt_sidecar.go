// Copyright 2026 The go-ethereum Authors
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
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/ethereum/go-ethereum/trie/bintrie"
	"github.com/ethereum/go-ethereum/trie/trienode"
	"github.com/ethereum/go-ethereum/triedb"
	"github.com/holiman/uint256"
)

// UBTAccount represents a decoded account from the UBT sidecar.
type UBTAccount struct {
	Balance  *uint256.Int
	Nonce    uint64
	CodeHash common.Hash
	CodeSize uint32
}

// UBTSidecar maintains a shadow UBT state alongside the canonical MPT state.
type UBTSidecar struct {
	mu sync.RWMutex

	enabled    bool
	ready      bool
	stale      bool
	converting bool

	conversionRoot  common.Hash
	conversionBlock uint64
	conversionHash  common.Hash

	currentRoot        common.Hash
	currentBlock       uint64
	currentHash        common.Hash
	lastCommittedBlock uint64
	commitInterval     uint64

	triedb    *triedb.Database
	config    *triedb.Config
	chainDB   ethdb.Database
	mptTrieDB *triedb.Database
}

// NewUBTSidecar initializes the sidecar with a dedicated verkle namespace.
func NewUBTSidecar(db ethdb.Database, base *triedb.Config) (*UBTSidecar, error) {
	if base == nil || base.PathDB == nil {
		return nil, errors.New("ubt sidecar requires path-based scheme")
	}
	cfg := *base
	cfg.HashDB = nil
	cfg.IsVerkle = true

	sc := &UBTSidecar{
		enabled: true,
		triedb:  triedb.NewDatabase(db, &cfg),
		config:  &cfg,
		chainDB: db,
	}
	return sc, nil
}

// NewUBTSidecarWithTrieDB initializes the sidecar using an existing trie database.
func NewUBTSidecarWithTrieDB(db ethdb.Database, tdb *triedb.Database) (*UBTSidecar, error) {
	if tdb == nil {
		return nil, errors.New("nil trie database")
	}
	if tdb.Scheme() != rawdb.PathScheme || !tdb.IsVerkle() {
		return nil, errors.New("ubt sidecar requires verkle/path scheme")
	}
	sc := &UBTSidecar{
		enabled: true,
		triedb:  tdb,
		chainDB: db,
	}
	return sc, nil
}

// Enabled returns whether the sidecar is enabled and not stale.
func (sc *UBTSidecar) Enabled() bool {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	return sc.enabled && !sc.stale
}

// Ready returns whether the sidecar is ready for serving UBT state.
func (sc *UBTSidecar) Ready() bool {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	return sc.enabled && sc.ready && !sc.stale && !sc.converting
}

// MarkStale forces the sidecar into the stale state so that a fresh
// conversion can be triggered (e.g. when the sidecar is far behind head).
func (sc *UBTSidecar) MarkStale() {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	sc.stale = true
	sc.ready = false
}

// Converting returns whether the sidecar is converting MPT to UBT.
func (sc *UBTSidecar) Converting() bool {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	return sc.enabled && sc.converting && !sc.stale
}

// CurrentRoot returns the current UBT root.
func (sc *UBTSidecar) CurrentRoot() common.Hash {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	return sc.currentRoot
}

// CurrentInfo returns the latest sidecar root and its associated block info.
func (sc *UBTSidecar) CurrentInfo() (common.Hash, uint64, common.Hash) {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	return sc.currentRoot, sc.currentBlock, sc.currentHash
}

// TrieDB exposes the sidecar's trie database.
func (sc *UBTSidecar) TrieDB() *triedb.Database {
	return sc.triedb
}

// SetCommitInterval configures how often UBT sidecar commits are flushed (in blocks).
func (sc *UBTSidecar) SetCommitInterval(interval uint64) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	sc.commitInterval = interval
}

// Close closes the sidecar trie database.
func (sc *UBTSidecar) Close() error {
	if sc.triedb == nil {
		return nil
	}
	return sc.triedb.Close()
}

// SetMPTTrieDB configures the MPT trie database used to resolve preimages.
func (sc *UBTSidecar) SetMPTTrieDB(db *triedb.Database) {
	sc.mu.Lock()
	sc.mptTrieDB = db
	sc.mu.Unlock()
}

func (sc *UBTSidecar) preimage(hash common.Hash) []byte {
	sc.mu.RLock()
	tdb := sc.mptTrieDB
	sc.mu.RUnlock()
	if tdb != nil {
		if preimage := tdb.Preimage(hash); len(preimage) > 0 {
			return preimage
		}
	}
	return rawdb.ReadPreimage(sc.chainDB, hash)
}

// InitFromDB initializes sidecar state from database metadata.
func (sc *UBTSidecar) InitFromDB() error {
	sc.mu.Lock()
	if !sc.enabled {
		sc.mu.Unlock()
		return nil
	}
	sc.mu.Unlock()

	root, block, hash, ok := rawdb.ReadUBTCurrentRoot(sc.chainDB)
	if ok {
		if err := sc.ensureRootAvailable(root); err == nil {
			sc.mu.Lock()
			sc.currentRoot = root
			sc.currentBlock = block
			sc.currentHash = hash
			sc.ready = true
			sc.stale = false
			sc.converting = false
			sc.mu.Unlock()
		} else {
			log.Warn("UBT sidecar current root unavailable", "block", block, "hash", hash, "err", err)
			sc.mu.Lock()
			sc.ready = false
			sc.stale = true
			sc.converting = false
			sc.mu.Unlock()
		}
	}
	if root, block, hash, ok := rawdb.ReadUBTCommittedRoot(sc.chainDB); ok {
		sc.mu.Lock()
		sc.lastCommittedBlock = block
		sc.mu.Unlock()
		if !sc.Ready() {
			if err := sc.ensureRootAvailable(root); err == nil {
				sc.mu.Lock()
				sc.currentRoot = root
				sc.currentBlock = block
				sc.currentHash = hash
				sc.mu.Unlock()
				log.Warn("UBT sidecar recovered to committed root; conversion required", "block", block, "hash", hash)
			} else {
				log.Warn("UBT sidecar committed root unavailable", "block", block, "hash", hash, "err", err)
			}
		}
	}
	if ok {
		return nil
	}
	progress, err := rawdb.ReadUBTConversionProgress(sc.chainDB)
	if err != nil {
		log.Warn("UBT sidecar failed to read conversion progress", "err", err)
	}
	if progress == nil {
		if root, has, err := sc.verkleDiskRoot(); err != nil {
			log.Warn("UBT sidecar failed to read verkle root", "err", err)
		} else if has {
			log.Warn("UBT sidecar verkle data present without metadata; clearing", "root", root)
			if err := sc.resetVerkleTrie(); err != nil {
				log.Warn("Failed to reset UBT sidecar trie", "err", err)
			}
		}
	}

	genesisHash := rawdb.ReadCanonicalHash(sc.chainDB, 0)
	if genesisHash == (common.Hash{}) {
		_ = sc.fail("genesis hash", errors.New("missing genesis hash"))
		return nil
	}
	alloc, err := readGenesisAlloc(sc.chainDB, genesisHash)
	if err != nil {
		_ = sc.fail("genesis alloc", err)
		return nil
	}
	if err := sc.buildFromGenesis(genesisHash, alloc); err != nil {
		return nil
	}
	return nil
}

// GetUBTRoot returns the UBT root for the given block hash if present.
func (sc *UBTSidecar) GetUBTRoot(blockHash common.Hash) (common.Hash, bool) {
	return rawdb.ReadUBTBlockRoot(sc.chainDB, blockHash)
}

// OpenBinaryTrie opens the UBT trie at the given root.
func (sc *UBTSidecar) OpenBinaryTrie(root common.Hash) (*bintrie.BinaryTrie, error) {
	return bintrie.NewBinaryTrie(root, sc.triedb)
}

// ReadAccount reads account data from the UBT sidecar.
func (sc *UBTSidecar) ReadAccount(root common.Hash, address common.Address) (*UBTAccount, error) {
	bt, err := sc.OpenBinaryTrie(root)
	if err != nil {
		return nil, err
	}
	acc, err := bt.GetAccount(address)
	if err != nil {
		return nil, err
	}
	if acc == nil {
		return nil, nil
	}
	codeSize, err := readCodeSize(bt, address)
	if err != nil {
		return nil, err
	}
	return &UBTAccount{
		Balance:  acc.Balance,
		Nonce:    acc.Nonce,
		CodeHash: codeHashFromBytes(acc.CodeHash),
		CodeSize: codeSize,
	}, nil
}

// ReadStorage reads a storage slot value from the UBT sidecar.
func (sc *UBTSidecar) ReadStorage(root common.Hash, address common.Address, key common.Hash) (common.Hash, error) {
	bt, err := sc.OpenBinaryTrie(root)
	if err != nil {
		return common.Hash{}, err
	}
	value, err := bt.GetStorage(address, key.Bytes())
	if err != nil {
		return common.Hash{}, err
	}
	if len(value) == 0 {
		return common.Hash{}, nil
	}
	return common.BytesToHash(value), nil
}

func readGenesisAlloc(db ethdb.KeyValueReader, genesisHash common.Hash) (types.GenesisAlloc, error) {
	blob := rawdb.ReadGenesisStateSpec(db, genesisHash)
	if len(blob) == 0 {
		return nil, errors.New("genesis state missing from db")
	}
	var alloc types.GenesisAlloc
	if err := alloc.UnmarshalJSON(blob); err != nil {
		return nil, fmt.Errorf("decode genesis alloc: %w", err)
	}
	return alloc, nil
}

func (sc *UBTSidecar) buildFromGenesis(genesisHash common.Hash, alloc types.GenesisAlloc) error {
	bt, err := bintrie.NewBinaryTrie(types.EmptyBinaryHash, sc.triedb)
	if err != nil {
		return sc.fail("open ubt trie", err)
	}
	for addr, account := range alloc {
		balance := uint256.NewInt(0)
		if account.Balance != nil {
			balance = uint256.MustFromBig(account.Balance)
		}
		codeLen := len(account.Code)
		codeHash := types.EmptyCodeHash
		if codeLen > 0 {
			codeHash = crypto.Keccak256Hash(account.Code)
		}
		stateAcc := &types.StateAccount{
			Nonce:    account.Nonce,
			Balance:  balance,
			Root:     types.EmptyRootHash,
			CodeHash: codeHash.Bytes(),
		}
		if err := bt.UpdateAccount(addr, stateAcc, codeLen); err != nil {
			return sc.fail("update account", err)
		}
		if codeLen > 0 {
			if err := bt.UpdateContractCode(addr, codeHash, account.Code); err != nil {
				return sc.fail("update code", err)
			}
		}
		for key, value := range account.Storage {
			if value == (common.Hash{}) {
				continue
			}
			raw := value.Bytes()
			raw = bytes.TrimLeft(raw, "\x00")
			if len(raw) == 0 {
				continue
			}
			if err := bt.UpdateStorage(addr, key.Bytes(), raw); err != nil {
				return sc.fail("update storage", err)
			}
		}
	}
	root, nodeset := bt.Commit(false)
	if root == types.EmptyBinaryHash {
		rawdb.WriteUBTCurrentRoot(sc.chainDB, root, 0, genesisHash)
		rawdb.WriteUBTBlockRoot(sc.chainDB, genesisHash, root)
		rawdb.WriteUBTCommittedRoot(sc.chainDB, root, 0, genesisHash)
		sc.mu.Lock()
		sc.currentRoot = root
		sc.currentBlock = 0
		sc.currentHash = genesisHash
		sc.lastCommittedBlock = 0
		sc.ready = true
		sc.stale = false
		sc.mu.Unlock()
		return nil
	}
	merged := trienode.NewWithNodeSet(nodeset)
	if err := sc.triedb.Update(root, types.EmptyBinaryHash, 0, merged, triedb.NewStateSet()); err != nil {
		return sc.fail("triedb update", err)
	}
	if err := sc.triedb.Commit(root, false); err != nil {
		return sc.fail("commit ubt trie", err)
	}
	rawdb.WriteUBTCurrentRoot(sc.chainDB, root, 0, genesisHash)
	rawdb.WriteUBTBlockRoot(sc.chainDB, genesisHash, root)
	rawdb.WriteUBTCommittedRoot(sc.chainDB, root, 0, genesisHash)

	sc.mu.Lock()
	sc.currentRoot = root
	sc.currentBlock = 0
	sc.currentHash = genesisHash
	sc.lastCommittedBlock = 0
	sc.ready = true
	sc.stale = false
	sc.mu.Unlock()
	return nil
}

// HandleReorg attempts to recover the sidecar state to the given ancestor.
func (sc *UBTSidecar) HandleReorg(ancestorHash common.Hash, ancestorNum uint64) error {
	if !sc.Ready() {
		return nil
	}
	root, ok := rawdb.ReadUBTBlockRoot(sc.chainDB, ancestorHash)
	if !ok {
		return sc.fail("reorg missing root", fmt.Errorf("ancestor %x", ancestorHash))
	}
	recoverable, err := sc.triedb.Recoverable(root)
	if err != nil {
		return sc.fail("reorg recoverable check", err)
	}
	if !recoverable {
		return sc.fail("reorg not recoverable", fmt.Errorf("ancestor %x", ancestorHash))
	}
	if err := sc.triedb.Recover(root); err != nil {
		return sc.fail("reorg recover", err)
	}
	rawdb.WriteUBTCurrentRoot(sc.chainDB, root, ancestorNum, ancestorHash)
	sc.mu.Lock()
	sc.currentRoot = root
	sc.currentBlock = ancestorNum
	sc.currentHash = ancestorHash
	sc.mu.Unlock()
	return nil
}

// ApplyStateUpdate applies a StateUpdate to the sidecar.
func (sc *UBTSidecar) ApplyStateUpdate(block *types.Block, update *state.StateUpdate, db ethdb.Database) error {
	if !sc.Ready() {
		return errors.New("ubt sidecar not ready")
	}
	// Drain any leftover queue items from conversion. A small number of
	// blocks may have been enqueued between the final replayUpdateQueue
	// and the converting→ready transition.
	if err := sc.drainLeftoverQueue(); err != nil {
		return sc.fail("drain leftover queue", err)
	}
	ubtUpdate := NewUBTUpdate(block, update)
	if ubtUpdate == nil {
		return nil
	}
	if err := sc.applyUBTUpdate(ubtUpdate); err != nil {
		return sc.fail("apply update", err)
	}
	if err := sc.maybeCommit(block.NumberU64(), block.Hash()); err != nil {
		return err
	}
	return nil
}

func (sc *UBTSidecar) applyUBTUpdate(update *UBTUpdate) error {
	if update == nil {
		return nil
	}
	sc.mu.RLock()
	parentRoot := sc.currentRoot
	sc.mu.RUnlock()

	bt, err := sc.OpenBinaryTrie(parentRoot)
	if err != nil {
		return err
	}
	addrs := make([]common.Address, 0, len(update.AccountsOrigin))
	for addr := range update.AccountsOrigin {
		addrs = append(addrs, addr)
	}
	sort.Slice(addrs, func(i, j int) bool {
		return bytes.Compare(addrs[i][:], addrs[j][:]) < 0
	})
	deletions := make([]common.Address, 0, len(addrs))
	updates := make([]common.Address, 0, len(addrs))
	for _, addr := range addrs {
		addrHash := crypto.Keccak256Hash(addr.Bytes())
		data, ok := update.Accounts[addrHash]
		if !ok {
			return fmt.Errorf("account %x not found in update", addr)
		}
		if data == nil {
			deletions = append(deletions, addr)
		} else {
			updates = append(updates, addr)
		}
	}
	for _, addr := range deletions {
		addrHash := crypto.Keccak256Hash(addr.Bytes())
		var rawKeyMap map[common.Hash]common.Hash
		if update.RawStorageKey {
			if originSlots, ok := update.StoragesOrigin[addr]; ok {
				rawKeyMap = make(map[common.Hash]common.Hash, len(originSlots))
				for rawKey := range originSlots {
					rawKeyMap[crypto.Keccak256Hash(rawKey.Bytes())] = rawKey
				}
			}
		}
		if slots, ok := update.Storages[addrHash]; ok {
			slotHashes := make([]common.Hash, 0, len(slots))
			for slotHash := range slots {
				slotHashes = append(slotHashes, slotHash)
			}
			sort.Slice(slotHashes, func(i, j int) bool {
				return bytes.Compare(slotHashes[i][:], slotHashes[j][:]) < 0
			})
			for _, slotHash := range slotHashes {
				rawKey, err := sc.resolveStorageKey(update.RawStorageKey, rawKeyMap, slotHash)
				if err != nil {
					return err
				}
				if err := bt.DeleteStorage(addr, rawKey.Bytes()); err != nil {
					return err
				}
			}
		}
		codeSize, err := readCodeSize(bt, addr)
		if err != nil {
			return err
		}
		if codeSize > 0 {
			if err := bt.DeleteContractCode(addr, int(codeSize)); err != nil {
				return err
			}
		}
		if err := bt.MarkAccountDeleted(addr); err != nil {
			return err
		}
	}
	for _, addr := range updates {
		addrHash := crypto.Keccak256Hash(addr.Bytes())
		data := update.Accounts[addrHash]
		var rawKeyMap map[common.Hash]common.Hash
		if update.RawStorageKey {
			if originSlots, ok := update.StoragesOrigin[addr]; ok {
				rawKeyMap = make(map[common.Hash]common.Hash, len(originSlots))
				for rawKey := range originSlots {
					rawKeyMap[crypto.Keccak256Hash(rawKey.Bytes())] = rawKey
				}
			}
		}
		account, err := types.FullAccount(data)
		if err != nil {
			return err
		}
		codeHash := codeHashFromBytes(account.CodeHash)
		codeLen, err := resolveCodeLen(update, addr, codeHash, sc.chainDB)
		if err != nil {
			return err
		}
		if err := bt.UpdateAccount(addr, account, codeLen); err != nil {
			return err
		}
		if code, ok := update.Codes[addr]; ok && len(code) > 0 {
			if err := bt.UpdateContractCode(addr, codeHash, code); err != nil {
				return err
			}
		}
		if slots, ok := update.Storages[addrHash]; ok {
			slotHashes := make([]common.Hash, 0, len(slots))
			for slotHash := range slots {
				slotHashes = append(slotHashes, slotHash)
			}
			sort.Slice(slotHashes, func(i, j int) bool {
				return bytes.Compare(slotHashes[i][:], slotHashes[j][:]) < 0
			})
			for _, slotHash := range slotHashes {
				encVal := slots[slotHash]
				rawKey, err := sc.resolveStorageKey(update.RawStorageKey, rawKeyMap, slotHash)
				if err != nil {
					return err
				}
				val, err := decodeStorageValue(encVal)
				if err != nil {
					return err
				}
				if len(val) == 0 {
					if err := bt.DeleteStorage(addr, rawKey.Bytes()); err != nil {
						return err
					}
					continue
				}
				if err := bt.UpdateStorage(addr, rawKey.Bytes(), val); err != nil {
					return err
				}
			}
		}
	}

	newRoot, nodeset := bt.Commit(false)
	merged := trienode.NewWithNodeSet(nodeset)
	stateSet := buildStateSet(update)
	if err := sc.triedb.Update(newRoot, parentRoot, update.BlockNum, merged, stateSet); err != nil {
		return err
	}
	if prev, ok := rawdb.ReadUBTBlockRoot(sc.chainDB, update.BlockHash); ok && prev != newRoot {
		log.Warn("UBT block root overwrite mismatch", "block", update.BlockNum, "hash", update.BlockHash, "prev", prev, "new", newRoot)
	}
	rawdb.WriteUBTCurrentRoot(sc.chainDB, newRoot, update.BlockNum, update.BlockHash)
	rawdb.WriteUBTBlockRoot(sc.chainDB, update.BlockHash, newRoot)

	sc.mu.Lock()
	sc.currentRoot = newRoot
	sc.currentBlock = update.BlockNum
	sc.currentHash = update.BlockHash
	sc.mu.Unlock()
	return nil
}

func (sc *UBTSidecar) fail(stage string, err error) error {
	sc.mu.Lock()
	sc.stale = true
	sc.ready = false
	sc.converting = false
	sc.mu.Unlock()
	log.Error("UBT sidecar failed", "stage", stage, "err", err)
	return err
}

func (sc *UBTSidecar) ensureRootAvailable(root common.Hash) error {
	if root == (common.Hash{}) {
		return nil
	}
	if sc.triedb == nil {
		return errors.New("ubt sidecar missing triedb")
	}
	if recoverable, err := sc.triedb.Recoverable(root); err == nil && recoverable {
		if err := sc.triedb.Recover(root); err != nil {
			return err
		}
		return nil
	}
	var lastErr error
	if _, err := sc.triedb.NodeReader(root); err == nil {
		return nil
	} else {
		lastErr = err
	}
	if _, err := sc.triedb.StateReader(root); err == nil {
		return nil
	} else {
		lastErr = err
	}
	if lastErr != nil {
		return lastErr
	}
	return errors.New("ubt root unavailable")
}

func (sc *UBTSidecar) verkleDiskRoot() (common.Hash, bool, error) {
	if sc.chainDB == nil {
		return common.Hash{}, false, errors.New("ubt sidecar missing chain db")
	}
	vdb := rawdb.NewTable(sc.chainDB, string(rawdb.VerklePrefix))
	iter := vdb.NewIterator(nil, nil)
	defer iter.Release()
	if !iter.Next() {
		if err := iter.Error(); err != nil {
			return common.Hash{}, false, err
		}
		return common.Hash{}, false, nil
	}
	blob := rawdb.ReadAccountTrieNode(vdb, nil)
	if len(blob) == 0 {
		return common.Hash{}, true, nil
	}
	node, err := bintrie.DeserializeNode(blob, 0)
	if err != nil {
		return common.Hash{}, true, err
	}
	return node.Hash(), true, nil
}

func (sc *UBTSidecar) resetVerkleTrie() error {
	if sc.config == nil {
		return errors.New("ubt sidecar missing triedb config")
	}
	vdb := rawdb.NewTable(sc.chainDB, string(rawdb.VerklePrefix))
	if err := vdb.DeleteRange(nil, nil); err != nil {
		return err
	}
	sc.mu.Lock()
	if sc.triedb != nil {
		_ = sc.triedb.Close()
	}
	sc.triedb = triedb.NewDatabase(sc.chainDB, sc.config)
	sc.stale = false
	sc.ready = false
	sc.converting = false
	sc.currentRoot = common.Hash{}
	sc.currentBlock = 0
	sc.currentHash = common.Hash{}
	sc.mu.Unlock()
	return nil
}

func (sc *UBTSidecar) applyNoopUpdate(blockNum uint64, blockHash common.Hash) {
	sc.mu.RLock()
	root := sc.currentRoot
	sc.mu.RUnlock()
	rawdb.WriteUBTCurrentRoot(sc.chainDB, root, blockNum, blockHash)
	rawdb.WriteUBTBlockRoot(sc.chainDB, blockHash, root)
	sc.mu.Lock()
	sc.currentBlock = blockNum
	sc.currentHash = blockHash
	sc.mu.Unlock()
}

func (sc *UBTSidecar) maybeCommit(blockNum uint64, blockHash common.Hash) error {
	sc.mu.RLock()
	interval := sc.commitInterval
	lastCommit := sc.lastCommittedBlock
	root := sc.currentRoot
	sc.mu.RUnlock()
	if interval == 0 {
		return nil
	}
	if lastCommit != 0 && blockNum-lastCommit < interval {
		return nil
	}
	if err := sc.triedb.Commit(root, false); err != nil {
		return sc.fail("commit", err)
	}
	rawdb.WriteUBTCommittedRoot(sc.chainDB, root, blockNum, blockHash)
	sc.mu.Lock()
	sc.lastCommittedBlock = blockNum
	sc.mu.Unlock()
	log.Info("Committed UBT sidecar state", "block", blockNum, "hash", blockHash, "root", root)
	return nil
}

func resolveCodeLen(update *UBTUpdate, addr common.Address, codeHash common.Hash, db ethdb.Database) (int, error) {
	if codeHash == types.EmptyCodeHash {
		return 0, nil
	}
	if code, ok := update.Codes[addr]; ok {
		return len(code), nil
	}
	code := rawdb.ReadCode(db, codeHash)
	if len(code) == 0 {
		return 0, fmt.Errorf("missing code for %x", codeHash)
	}
	return len(code), nil
}

func codeHashFromBytes(codeHash []byte) common.Hash {
	if len(codeHash) == 0 {
		return types.EmptyCodeHash
	}
	return common.BytesToHash(codeHash)
}

func decodeStorageValue(enc []byte) ([]byte, error) {
	if len(enc) == 0 {
		return nil, nil
	}
	_, val, _, err := rlp.Split(enc)
	if err != nil {
		return nil, err
	}
	return val, nil
}

func (sc *UBTSidecar) resolveStorageKey(rawStorageKey bool, rawKeyMap map[common.Hash]common.Hash, slotHash common.Hash) (common.Hash, error) {
	if rawStorageKey {
		if rawKey, ok := rawKeyMap[slotHash]; ok {
			return rawKey, nil
		}
		return common.Hash{}, fmt.Errorf("missing raw storage key for %x", slotHash)
	}
	preimage := sc.preimage(slotHash)
	if len(preimage) == 0 {
		return common.Hash{}, fmt.Errorf("missing storage preimage for %x", slotHash)
	}
	if len(preimage) != common.HashLength {
		return common.Hash{}, fmt.Errorf("invalid storage preimage for %x", slotHash)
	}
	var rawKey common.Hash
	copy(rawKey[:], preimage)
	return rawKey, nil
}

func buildStateSet(update *UBTUpdate) *triedb.StateSet {
	if update == nil {
		return nil
	}
	return &triedb.StateSet{
		Accounts:       update.Accounts,
		AccountsOrigin: update.AccountsOrigin,
		Storages:       update.Storages,
		StoragesOrigin: update.StoragesOrigin,
		RawStorageKey:  update.RawStorageKey,
	}
}

func readCodeSize(bt *bintrie.BinaryTrie, addr common.Address) (uint32, error) {
	basic, err := bt.GetWithHashedKey(bintrie.GetBinaryTreeKeyBasicData(addr))
	if err != nil {
		return 0, err
	}
	if len(basic) == 0 {
		return 0, nil
	}
	offset := bintrie.BasicDataCodeSizeOffset - 1
	if len(basic) < offset+4 {
		return 0, fmt.Errorf("invalid basic data length %d", len(basic))
	}
	return binary.BigEndian.Uint32(basic[offset : offset+4]), nil
}
