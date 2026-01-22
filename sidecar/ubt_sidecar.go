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
	"runtime"
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

const (
	defaultQueueLimit = uint64(100_000)
	yieldEvery        = 50_000
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
	mu      sync.RWMutex
	queueMu sync.Mutex

	enabled    bool
	converting bool
	ready      bool
	stale      bool

	currentRoot  common.Hash
	currentBlock uint64
	currentHash  common.Hash

	triedb     *triedb.Database
	chainDB    ethdb.Database
	queueLimit uint64
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
		enabled:    true,
		triedb:     triedb.NewDatabase(db, &cfg),
		chainDB:    db,
		queueLimit: defaultQueueLimit,
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
		enabled:    true,
		triedb:     tdb,
		chainDB:    db,
		queueLimit: defaultQueueLimit,
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
	return sc.enabled && sc.ready && !sc.stale
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

// TrieDB exposes the sidecar's trie database.
func (sc *UBTSidecar) TrieDB() *triedb.Database {
	return sc.triedb
}

// Close closes the sidecar trie database.
func (sc *UBTSidecar) Close() error {
	if sc.triedb == nil {
		return nil
	}
	return sc.triedb.Close()
}

// InitFromDB initializes sidecar state from database metadata.
func (sc *UBTSidecar) InitFromDB() error {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if !sc.enabled {
		return nil
	}
	if progress := rawdb.ReadUBTConversionProgress(sc.chainDB); progress != nil {
		sc.stale = true
		sc.ready = false
		sc.converting = false
		log.Warn("UBT sidecar conversion progress found; marking stale", "block", progress.Block, "hash", progress.BlockHash)
		return nil
	}
	root, block, hash, ok := rawdb.ReadUBTCurrentRoot(sc.chainDB)
	if ok {
		sc.currentRoot = root
		sc.currentBlock = block
		sc.currentHash = hash
		sc.ready = true
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

// ConvertFromMPT converts the canonical MPT state to the UBT sidecar.
func (sc *UBTSidecar) ConvertFromMPT(stateRoot common.Hash, blockNum uint64, blockHash common.Hash, mptDB *triedb.Database) error {
	if mptDB == nil {
		return errors.New("missing MPT trie database")
	}
	sc.queueMu.Lock()
	sc.mu.Lock()
	if !sc.enabled {
		sc.mu.Unlock()
		sc.queueMu.Unlock()
		return errors.New("ubt sidecar disabled")
	}
	sc.stale = false
	sc.ready = false
	sc.converting = true
	sc.mu.Unlock()
	if err := sc.resetQueueLocked(); err != nil {
		sc.queueMu.Unlock()
		return sc.fail("reset queue", err)
	}
	sc.queueMu.Unlock()
	rawdb.WriteUBTConversionProgress(sc.chainDB, &rawdb.UBTConversionProgress{
		Root:      stateRoot,
		Block:     blockNum,
		BlockHash: blockHash,
	})

	bt, err := bintrie.NewBinaryTrie(types.EmptyBinaryHash, sc.triedb)
	if err != nil {
		return sc.fail("open ubt trie", err)
	}
	accIt, err := mptDB.AccountIterator(stateRoot, common.Hash{})
	if err != nil {
		return sc.fail("account iterator", err)
	}
	defer accIt.Release()

	var processed uint64
	for accIt.Next() {
		accHash := accIt.Hash()
		addrBytes := rawdb.ReadPreimage(sc.chainDB, accHash)
		if len(addrBytes) == 0 {
			return sc.fail("missing account preimage", fmt.Errorf("account %x", accHash))
		}
		if len(addrBytes) != common.AddressLength {
			return sc.fail("invalid account preimage", fmt.Errorf("account %x", accHash))
		}
		var addr common.Address
		copy(addr[:], addrBytes)

		accData := accIt.Account()
		if len(accData) == 0 {
			continue
		}
		account, err := types.FullAccount(accData)
		if err != nil {
			return sc.fail("decode account", err)
		}
		codeLen := 0
		var code []byte
		codeHash := codeHashFromBytes(account.CodeHash)
		if codeHash != types.EmptyCodeHash {
			code = rawdb.ReadCode(sc.chainDB, codeHash)
			if len(code) == 0 {
				return sc.fail("missing code", fmt.Errorf("codehash %x", codeHash))
			}
			codeLen = len(code)
		}
		if err := bt.UpdateAccount(addr, account, codeLen); err != nil {
			return sc.fail("update account", err)
		}
		if len(code) > 0 {
			if err := bt.UpdateContractCode(addr, codeHash, code); err != nil {
				return sc.fail("update code", err)
			}
		}

		if account.Root != types.EmptyRootHash {
			stIt, err := mptDB.StorageIterator(stateRoot, accHash, common.Hash{})
			if err != nil {
				return sc.fail("storage iterator", err)
			}
			for stIt.Next() {
				slotHash := stIt.Hash()
				slotPreimage := rawdb.ReadPreimage(sc.chainDB, slotHash)
				if len(slotPreimage) == 0 {
					stIt.Release()
					return sc.fail("missing storage preimage", fmt.Errorf("slot %x", slotHash))
				}
				if len(slotPreimage) != common.HashLength {
					stIt.Release()
					return sc.fail("invalid storage preimage", fmt.Errorf("slot %x", slotHash))
				}
				var slotKey common.Hash
				copy(slotKey[:], slotPreimage)
				_, val, _, err := rlp.Split(stIt.Slot())
				if err != nil {
					stIt.Release()
					return sc.fail("decode storage value", err)
				}
				if err := bt.UpdateStorage(addr, slotKey.Bytes(), val); err != nil {
					stIt.Release()
					return sc.fail("update storage", err)
				}
			}
			if err := stIt.Error(); err != nil {
				stIt.Release()
				return sc.fail("storage iterator error", err)
			}
			stIt.Release()
		}
		processed++
		if processed%yieldEvery == 0 {
			runtime.Gosched()
		}
	}
	if err := accIt.Error(); err != nil {
		return sc.fail("account iterator error", err)
	}

	root, nodeset := bt.Commit(false)
	merged := trienode.NewWithNodeSet(nodeset)
	if err := sc.triedb.Update(root, types.EmptyBinaryHash, blockNum, merged, nil); err != nil {
		return sc.fail("triedb update", err)
	}
	rawdb.WriteUBTCurrentRoot(sc.chainDB, root, blockNum, blockHash)
	rawdb.WriteUBTBlockRoot(sc.chainDB, blockHash, root)
	rawdb.DeleteUBTConversionProgress(sc.chainDB)

	sc.mu.Lock()
	sc.currentRoot = root
	sc.currentBlock = blockNum
	sc.currentHash = blockHash
	sc.mu.Unlock()

	if err := sc.replayQueuedUpdates(); err != nil {
		return sc.fail("replay updates", err)
	}

	sc.mu.Lock()
	sc.converting = false
	sc.ready = true
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
	ubtUpdate := NewUBTUpdate(block, update)
	if ubtUpdate == nil {
		return nil
	}
	if err := sc.applyUBTUpdate(ubtUpdate); err != nil {
		return sc.fail("apply update", err)
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
				rawKey, err := resolveStorageKey(update.RawStorageKey, rawKeyMap, slotHash, sc.chainDB)
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
				rawKey, err := resolveStorageKey(update.RawStorageKey, rawKeyMap, slotHash, sc.chainDB)
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

func resolveStorageKey(rawStorageKey bool, rawKeyMap map[common.Hash]common.Hash, slotHash common.Hash, db ethdb.Database) (common.Hash, error) {
	if rawStorageKey {
		if rawKey, ok := rawKeyMap[slotHash]; ok {
			return rawKey, nil
		}
		return common.Hash{}, fmt.Errorf("missing raw storage key for %x", slotHash)
	}
	preimage := rawdb.ReadPreimage(db, slotHash)
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
