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
	"math/rand"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/ethereum/go-ethereum/trie/bintrie"
	"github.com/ethereum/go-ethereum/trie/trienode"
	"github.com/ethereum/go-ethereum/triedb"
	"github.com/ethereum/go-ethereum/triedb/pathdb"
)

const ubtConversionLogInterval = 200_000

// Maximum number of consecutive iterator-open failures before giving up.
const maxIteratorRetries = 20

const (
	retryableErrNotConstructed = "snapshot is not constructed"
	retryableErrWaitingForSync = "waiting for sync"
	retryableUnknownLayerPrefix = "unknown layer: "
)

// isRetryableIterError returns true for transient iterator errors that
// should be retried with a fresh root rather than aborting conversion.
func isRetryableIterError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, pathdb.ErrSnapshotStale) {
		return true
	}
	msg := err.Error()
	return msg == retryableErrNotConstructed ||
		msg == retryableErrWaitingForSync ||
		// "unknown layer: ..." happens when a diff layer is evicted
		// between reading the head root and opening the iterator.
		strings.HasPrefix(msg, retryableUnknownLayerPrefix)
}

// readHeadStateRef reads the current head block's state reference from the
// chain database. This is used during conversion retries to obtain a fresh
// root that is likely still present in the pathdb layer tree.
func (sc *UBTSidecar) readHeadStateRef() (root common.Hash, num uint64, hash common.Hash, ok bool) {
	headHash := rawdb.ReadHeadBlockHash(sc.chainDB)
	if headHash == (common.Hash{}) {
		return
	}
	number, found := rawdb.ReadHeaderNumber(sc.chainDB, headHash)
	if !found {
		return
	}
	header := rawdb.ReadHeader(sc.chainDB, headHash, number)
	if header == nil {
		return
	}
	return header.Root, number, headHash, true
}

// retryBackoff sleeps for a duration that increases with the retry count,
// adding jitter to avoid thundering-herd effects.
func retryBackoff(attempt int) {
	base := time.Duration(attempt) * 2 * time.Second
	if base > 30*time.Second {
		base = 30 * time.Second
	}
	jitter := time.Duration(rand.Int63n(int64(time.Second)))
	time.Sleep(base + jitter)
}

// BeginConversion transitions the sidecar into converting mode.
func (sc *UBTSidecar) BeginConversion(root common.Hash, blockNum uint64, blockHash common.Hash) bool {
	sc.mu.Lock()
	if !sc.enabled || sc.converting {
		sc.mu.Unlock()
		return false
	}
	sc.converting = true
	sc.ready = false
	sc.stale = false
	sc.conversionRoot = root
	sc.conversionBlock = blockNum
	sc.conversionHash = blockHash
	sc.mu.Unlock()

	rawdb.WriteUBTConversionProgress(sc.chainDB, &rawdb.UBTConversionProgress{
		Root:      root,
		Block:     blockNum,
		BlockHash: blockHash,
		Started:   uint64(time.Now().Unix()),
	})
	if err := sc.clearUpdateQueue(); err != nil {
		log.Warn("Failed to clear UBT update queue", "err", err)
	}
	return true
}

// ConvertFromMPT rebuilds the UBT sidecar by iterating the MPT state
// snapshot. It uses pathdb's flat snapshot iterators (AccountIterator /
// StorageIterator) which are resilient to diff-layer eviction: most data
// comes from the persistent disk iterator which never goes stale. When
// the in-memory buffer portion does go stale the iterator is restarted
// from the last successfully processed account with a fresh head root.
//
// Correctness: the update queue captures every block from conversion start
// with absolute account/storage values. Any account read at a stale root
// is corrected during queue replay.
func (sc *UBTSidecar) ConvertFromMPT(root common.Hash, blockNum uint64, blockHash common.Hash, mptDB state.Database) error {
	if mptDB == nil {
		return sc.fail("convert init", errors.New("missing state database"))
	}
	if !sc.Converting() {
		if !sc.BeginConversion(root, blockNum, blockHash) {
			return errors.New("conversion already running")
		}
	}

	tdb := mptDB.TrieDB()
	log.Info("Starting UBT sidecar conversion (snapshot)", "block", blockNum, "hash", blockHash)

	bt, err := bintrie.NewBinaryTrie(types.EmptyBinaryHash, sc.triedb)
	if err != nil {
		return sc.fail("open ubt trie", err)
	}

	var (
		lastSeek       common.Hash // resume point (zero = start)
		done           bool        // true when all accounts have been processed
		accounts       uint64
		currentRoot    = root
		openRetries    int
	)

	for !done {
		accIt, err := tdb.AccountIterator(currentRoot, lastSeek)
		if err != nil {
			if !isRetryableIterError(err) || openRetries >= maxIteratorRetries {
				return sc.fail("open account iterator", err)
			}
			openRetries++
			log.Warn("UBT conversion: account iterator open failed, retrying",
				"err", err, "attempt", openRetries, "accounts", accounts)
			retryBackoff(openRetries)

			newRoot, _, _, ok := sc.readHeadStateRef()
			if !ok {
				return sc.fail("open account iterator", fmt.Errorf("cannot read head state: %w", err))
			}
			currentRoot = newRoot
			continue
		}
		openRetries = 0 // reset on successful open

		stale := false
		for accIt.Next() {
			accountHash := accIt.Hash()
			slimData := common.CopyBytes(accIt.Account())

			// Check for stale error from Account() returning nil.
			if slimData == nil {
				if itErr := accIt.Error(); itErr != nil {
					if isRetryableIterError(itErr) {
						stale = true
					} else {
						accIt.Release()
						return sc.fail("iterate account iterator", itErr)
					}
					break
				}
				continue // nil account = deleted entry, skip
			}

			data, err := types.FullAccount(slimData)
			if err != nil {
				accIt.Release()
				return sc.fail("decode account", err)
			}

			// Resolve address preimage.
			addrBytes := tdb.Preimage(accountHash)
			if len(addrBytes) == 0 {
				addrBytes = rawdb.ReadPreimage(sc.chainDB, accountHash)
			}
			if len(addrBytes) == 0 {
				accIt.Release()
				return sc.fail("address preimage", fmt.Errorf("missing preimage for %x", accountHash))
			}
			if len(addrBytes) != common.AddressLength {
				accIt.Release()
				return sc.fail("address preimage", fmt.Errorf("invalid address preimage length %d for %x", len(addrBytes), accountHash))
			}

			addr := common.BytesToAddress(addrBytes)
			codeHash := codeHashFromBytes(data.CodeHash)
			codeLen := 0
			if codeHash != types.EmptyCodeHash {
				code := rawdb.ReadCode(sc.chainDB, codeHash)
				if len(code) == 0 {
					accIt.Release()
					return sc.fail("account code", fmt.Errorf("missing code for %x", codeHash))
				}
				codeLen = len(code)
			}
			if err := bt.UpdateAccount(addr, data, codeLen); err != nil {
				accIt.Release()
				return sc.fail("update account", err)
			}
			if codeLen > 0 {
				code := rawdb.ReadCode(sc.chainDB, codeHash)
				if err := bt.UpdateContractCode(addr, codeHash, code); err != nil {
					accIt.Release()
					return sc.fail("update code", err)
				}
			}

			// Iterate storage for this account.
			if data.Root != types.EmptyRootHash {
				storageStale, err := sc.convertAccountStorage(tdb, bt, currentRoot, accountHash, addr)
				if err != nil {
					accIt.Release()
					return err
				}
				if storageStale {
					// Storage went stale mid-account. We break out of the
					// account loop WITHOUT advancing lastSeek so this
					// account will be re-processed from scratch on retry
					// (UpdateAccount is idempotent, storage re-insertion
					// overwrites prior values).
					stale = true
					break
				}
			}

			// Account fully processed; advance seek past it.
			lastSeek = accountHash
			accounts++
			if accounts%ubtConversionLogInterval == 0 {
				log.Info("UBT conversion progress", "accounts", accounts, "lastHash", accountHash)
			}
		}

		// Check iterator error (may be set even when Next() returned false).
		if !stale {
			if itErr := accIt.Error(); itErr != nil {
				if isRetryableIterError(itErr) {
					stale = true
				} else {
					accIt.Release()
					return sc.fail("iterate account iterator", itErr)
				}
			}
		}
		accIt.Release()

		if !stale {
			done = true
			break
		}

		// Stale — get fresh root and resume.
		newRoot, newNum, _, ok := sc.readHeadStateRef()
		if !ok {
			return sc.fail("resume conversion", errors.New("cannot read head state after stale"))
		}
		log.Info("UBT conversion resuming after stale",
			"accounts", accounts, "prevRoot", currentRoot, "newRoot", newRoot, "newBlock", newNum)
		currentRoot = newRoot
		// Advance lastSeek past the last fully-processed account.
		// If lastSeek is zero (no accounts processed yet), keep it zero.
		if lastSeek != (common.Hash{}) {
			next := incrementHash(lastSeek)
			if next == (common.Hash{}) {
				// Wrapped around 0xff..ff → 0x00..00, meaning all accounts done.
				done = true
				break
			}
			lastSeek = next
		}
	}

	// Commit the UBT trie. At this point the UBT represents a mixed-root
	// state that does not correspond to any single block. We deliberately
	// do NOT write block↔root mappings here; those are deferred until the
	// queue replay brings the UBT to a canonical block.
	newRoot, nodeset := bt.Commit(false)
	merged := trienode.NewWithNodeSet(nodeset)
	if err := sc.triedb.Update(newRoot, types.EmptyBinaryHash, blockNum, merged, triedb.NewStateSet()); err != nil {
		return sc.fail("update ubt trie", err)
	}

	// Write current root with conversion-start metadata. This is an
	// intermediate bookmark — not a valid block↔root mapping. The
	// correct mappings are written by the queue replay below.
	rawdb.WriteUBTCurrentRoot(sc.chainDB, newRoot, blockNum, blockHash)
	sc.mu.Lock()
	sc.currentRoot = newRoot
	sc.currentBlock = blockNum
	sc.currentHash = blockHash
	sc.mu.Unlock()

	if err := sc.triedb.Commit(newRoot, false); err != nil {
		return sc.fail("commit ubt trie", err)
	}
	rawdb.WriteUBTCommittedRoot(sc.chainDB, newRoot, blockNum, blockHash)
	sc.mu.Lock()
	sc.lastCommittedBlock = blockNum
	sc.mu.Unlock()

	if err := sc.replayUpdateQueue(); err != nil {
		return sc.fail("replay updates", err)
	}
	// If conversion completed with an empty replay queue, there may be no mapping
	// for the conversion-start block yet. Populate it now for debug/proof APIs.
	currentRoot, currentBlock, currentHash := sc.CurrentInfo()
	if _, ok := rawdb.ReadUBTBlockRoot(sc.chainDB, blockHash); !ok {
		rawdb.WriteUBTBlockRoot(sc.chainDB, blockHash, currentRoot)
	}
	rawdb.DeleteUBTConversionProgress(sc.chainDB)
	sc.mu.Lock()
	sc.converting = false
	sc.ready = true
	sc.stale = false
	sc.mu.Unlock()
	log.Info("UBT sidecar conversion completed", "accounts", accounts, "block", currentBlock, "hash", currentHash)
	return nil
}

// convertAccountStorage iterates storage for a single account using the
// pathdb StorageIterator. Returns (storageStale, error). If storageStale
// is true, the caller should restart this account from scratch with a
// fresh root.
func (sc *UBTSidecar) convertAccountStorage(
	tdb *triedb.Database,
	bt *bintrie.BinaryTrie,
	root common.Hash,
	accountHash common.Hash,
	addr common.Address,
) (storageStale bool, err error) {
	stIt, err := tdb.StorageIterator(root, accountHash, common.Hash{})
	if err != nil {
		if isRetryableIterError(err) {
			return true, nil // signal storage-stale to caller
		}
		return false, sc.fail("open storage iterator", err)
	}
	defer stIt.Release()

	for stIt.Next() {
		slotHash := stIt.Hash()
		slotValue := common.CopyBytes(stIt.Slot())

		if slotValue == nil {
			if itErr := stIt.Error(); itErr != nil {
				if isRetryableIterError(itErr) {
					return true, nil // stale mid-storage
				}
				return false, sc.fail("iterate storage iterator", itErr)
			}
			continue // deleted slot
		}
		if len(slotValue) == 0 {
			continue
		}

		// Resolve storage key preimage.
		rawKey := tdb.Preimage(slotHash)
		if len(rawKey) == 0 {
			rawKey = rawdb.ReadPreimage(sc.chainDB, slotHash)
		}
		if len(rawKey) == 0 {
			return false, sc.fail("storage preimage", fmt.Errorf("missing storage preimage for %x", slotHash))
		}
		if len(rawKey) != common.HashLength {
			return false, sc.fail("storage preimage", fmt.Errorf("invalid storage preimage length %d for %x", len(rawKey), slotHash))
		}
		if err := bt.UpdateStorage(addr, rawKey, slotValue); err != nil {
			return false, sc.fail("update storage", err)
		}
	}
	if itErr := stIt.Error(); itErr != nil {
		if isRetryableIterError(itErr) {
			return true, nil // stale after loop
		}
		return false, sc.fail("iterate storage iterator", itErr)
	}
	return false, nil
}

// incrementHash returns h+1 treating h as a 256-bit big-endian integer.
// Returns the zero hash on overflow (0xff..ff + 1 wraps to 0x00..00).
func incrementHash(h common.Hash) common.Hash {
	var result common.Hash
	copy(result[:], h[:])
	for i := common.HashLength - 1; i >= 0; i-- {
		result[i]++
		if result[i] != 0 {
			return result
		}
	}
	return common.Hash{} // overflow
}

// EnqueueUpdate appends a UBT update to the conversion queue.
func (sc *UBTSidecar) EnqueueUpdate(block *types.Block, update *state.StateUpdate) error {
	if block == nil {
		return errors.New("missing block")
	}
	if !sc.Converting() {
		return nil
	}
	var ubtUpdate *UBTUpdate
	if update == nil {
		ubtUpdate = NewEmptyUBTUpdate(block)
	} else {
		ubtUpdate = NewUBTUpdate(block, update)
	}
	if ubtUpdate == nil {
		return nil
	}
	blob, err := rlp.EncodeToBytes(ubtUpdate)
	if err != nil {
		return err
	}
	key := ubtUpdateQueueKey(ubtUpdate.BlockNum, ubtUpdate.BlockHash)
	if err := sc.chainDB.Put(key, blob); err != nil {
		return err
	}
	return sc.updateQueueMeta(ubtUpdate.BlockNum)
}

func (sc *UBTSidecar) replayUpdateQueue() error {
	for {
		meta, err := rawdb.ReadUBTUpdateQueueMeta(sc.chainDB)
		if err != nil {
			return err
		}
		if meta == nil {
			return nil
		}
		if err := sc.replayQueueUpTo(meta.End); err != nil {
			return err
		}
		if err := sc.recomputeQueueMeta(); err != nil {
			return err
		}
		meta, err = rawdb.ReadUBTUpdateQueueMeta(sc.chainDB)
		if err != nil {
			return err
		}
		if meta == nil {
			return nil
		}
	}
}

func (sc *UBTSidecar) replayQueueUpTo(end uint64) error {
	it := sc.chainDB.NewIterator(rawdb.UBTUpdateQueuePrefix, nil)
	defer it.Release()

	sc.mu.RLock()
	expectedParent := sc.currentHash
	currentBlock := sc.currentBlock
	sc.mu.RUnlock()

	var deletes [][]byte
	for it.Next() {
		blockNum, _, ok := parseUBTUpdateQueueKey(it.Key())
		if !ok {
			continue
		}
		if blockNum > end {
			break
		}
		var update UBTUpdate
		if err := rlp.DecodeBytes(it.Value(), &update); err != nil {
			return err
		}
		// drop stale entries
		if update.BlockNum <= currentBlock {
			deletes = append(deletes, append([]byte{}, it.Key()...))
			continue
		}
		canonical := rawdb.ReadCanonicalHash(sc.chainDB, update.BlockNum)
		if canonical == (common.Hash{}) || canonical != update.BlockHash {
			deletes = append(deletes, append([]byte{}, it.Key()...))
			continue
		}
		if update.ParentHash != expectedParent {
			return fmt.Errorf("ubt update queue gap at block %d", update.BlockNum)
		}
		if update.Empty() {
			sc.applyNoopUpdate(update.BlockNum, update.BlockHash)
		} else {
			if err := sc.applyUBTUpdate(&update); err != nil {
				return err
			}
		}
		if err := sc.maybeCommit(update.BlockNum, update.BlockHash); err != nil {
			return err
		}
		expectedParent = update.BlockHash
		currentBlock = update.BlockNum
		deletes = append(deletes, append([]byte{}, it.Key()...))
	}
	if err := it.Error(); err != nil {
		return err
	}
	if len(deletes) == 0 {
		return nil
	}
	batch := sc.chainDB.NewBatch()
	for _, key := range deletes {
		_ = batch.Delete(key)
	}
	if err := batch.Write(); err != nil {
		return err
	}
	return nil
}

// drainLeftoverQueue replays any queue items left over from conversion.
// This handles the race where blocks are enqueued between the final
// replayUpdateQueue call and the converting→ready state transition.
func (sc *UBTSidecar) drainLeftoverQueue() error {
	meta, err := rawdb.ReadUBTUpdateQueueMeta(sc.chainDB)
	if err != nil {
		return err
	}
	if meta == nil {
		return nil
	}
	log.Info("Draining leftover UBT conversion queue", "start", meta.Start, "end", meta.End)
	return sc.replayUpdateQueue()
}

func (sc *UBTSidecar) clearUpdateQueue() error {
	it := sc.chainDB.NewIterator(rawdb.UBTUpdateQueuePrefix, nil)
	defer it.Release()

	batch := sc.chainDB.NewBatch()
	for it.Next() {
		_ = batch.Delete(it.Key())
	}
	if err := it.Error(); err != nil {
		return err
	}
	if err := batch.Write(); err != nil {
		return err
	}
	rawdb.DeleteUBTUpdateQueueMeta(sc.chainDB)
	return nil
}

func (sc *UBTSidecar) updateQueueMeta(blockNum uint64) error {
	meta, err := rawdb.ReadUBTUpdateQueueMeta(sc.chainDB)
	if err != nil {
		return err
	}
	if meta == nil {
		meta = &rawdb.UBTUpdateQueueMeta{Start: blockNum, End: blockNum}
	} else {
		if blockNum < meta.Start {
			meta.Start = blockNum
		}
		if blockNum > meta.End {
			meta.End = blockNum
		}
	}
	rawdb.WriteUBTUpdateQueueMeta(sc.chainDB, meta)
	return nil
}

func (sc *UBTSidecar) recomputeQueueMeta() error {
	it := sc.chainDB.NewIterator(rawdb.UBTUpdateQueuePrefix, nil)
	defer it.Release()

	var (
		start uint64
		end   uint64
		seen  bool
	)
	for it.Next() {
		blockNum, _, ok := parseUBTUpdateQueueKey(it.Key())
		if !ok {
			continue
		}
		if !seen {
			start = blockNum
			end = blockNum
			seen = true
			continue
		}
		if blockNum < start {
			start = blockNum
		}
		if blockNum > end {
			end = blockNum
		}
	}
	if err := it.Error(); err != nil {
		return err
	}
	if !seen {
		rawdb.DeleteUBTUpdateQueueMeta(sc.chainDB)
		return nil
	}
	rawdb.WriteUBTUpdateQueueMeta(sc.chainDB, &rawdb.UBTUpdateQueueMeta{Start: start, End: end})
	return nil
}

func ubtUpdateQueueKey(blockNum uint64, blockHash common.Hash) []byte {
	key := make([]byte, len(rawdb.UBTUpdateQueuePrefix)+8+common.HashLength)
	copy(key, rawdb.UBTUpdateQueuePrefix)
	off := len(rawdb.UBTUpdateQueuePrefix)
	binary.BigEndian.PutUint64(key[off:off+8], blockNum)
	off += 8
	copy(key[off:], blockHash.Bytes())
	return key
}

func parseUBTUpdateQueueKey(key []byte) (uint64, common.Hash, bool) {
	prefix := rawdb.UBTUpdateQueuePrefix
	if !bytes.HasPrefix(key, prefix) {
		return 0, common.Hash{}, false
	}
	need := len(prefix) + 8 + common.HashLength
	if len(key) != need {
		return 0, common.Hash{}, false
	}
	start := len(prefix)
	blockNum := binary.BigEndian.Uint64(key[start : start+8])
	var hash common.Hash
	copy(hash[:], key[start+8:])
	return blockNum, hash, true
}
