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
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/trie/bintrie"
	"github.com/ethereum/go-ethereum/trie/trienode"
	"github.com/ethereum/go-ethereum/triedb"
	"github.com/ethereum/go-ethereum/triedb/pathdb"
)

// Conversion tuning constants.
const (
	conversionBatchSize    = 500_000    // Accounts per batch commit
	maxMutationsPerBatch   = 10_000_000 // Maximum trie mutations before forced commit
	staleRetryInitialWait  = 1 * time.Second
	staleRetryMultiplier   = 2
	staleRetryMaxWait      = 60 * time.Second
	staleRetryTotalTimeout = 30 * time.Minute
)

const pathdbSnapshotStaleMessage = "layer stale"

// ConvertFromMPT performs the full MPT to UBT conversion.
//
// The caller must have already called BeginConversion() to transition the
// sidecar into StateConverting. This method runs the account scan, commits
// intermediate batches, replays the update queue, and finally sets the
// sidecar to StateReady.
//
// On context cancellation (e.g. reorg or shutdown), progress is persisted
// so conversion can be resumed. On stale iterator errors, exponential
// backoff is used before returning to StateStale.
func (sc *UBTSidecar) ConvertFromMPT(ctx context.Context, chain ChainContext) error {
	head := chain.HeadBlock()
	if head == nil {
		log.Debug("UBT conversion skipped: head block not available (sync in progress)")
		sc.mu.Lock()
		sc.state = StateStale
		sc.mu.Unlock()
		return nil
	}
	headRoot := chain.HeadRoot()

	log.Info("UBT conversion starting", "block", head.Number.Uint64(), "root", headRoot)

	// Check for existing conversion progress to decide resume vs fresh start.
	progress := rawdb.ReadUBTConversionProgress(sc.chainDB)

	var (
		startKey  common.Hash
		processed uint64
		t         *bintrie.BinaryTrie
		err       error
	)

	if progress != nil {
		// Resume: try to open the trie at the last committed intermediate root.
		resumeRoot := progress.LastCommittedRoot
		if resumeRoot != (common.Hash{}) && resumeRoot != types.EmptyBinaryHash {
			t, err = bintrie.NewBinaryTrie(resumeRoot, sc.triedb)
			if err != nil {
				log.Warn("UBT conversion resume failed to open committed root, starting fresh",
					"root", resumeRoot, "err", err)
				progress = nil // fall through to fresh start
			} else {
				startKey = progress.LastProcessedAccountKey
				processed = progress.ProcessedAccounts
				log.Info("UBT conversion resuming",
					"from", startKey, "processed", processed, "root", resumeRoot)
			}
		} else {
			log.Warn("UBT conversion resume found no committed root, starting fresh")
			progress = nil // fall through to fresh start
		}
	}

	if progress == nil {
		// Fresh start: clean up any stale queue/progress from a previous
		// attempt (e.g. after reorg), then reset the verkle namespace.
		sc.cleanupConversionState()
		if err := sc.resetVerkleNamespace(); err != nil {
			return sc.fail("conversion reset", err)
		}
		t, err = bintrie.NewBinaryTrie(types.EmptyBinaryHash, sc.triedb)
		if err != nil {
			return sc.fail("conversion new trie", err)
		}
		startKey = common.Hash{}
		processed = 0

		// Persist initial conversion progress.
		rawdb.WriteUBTConversionProgress(sc.chainDB, &rawdb.UBTConversionProgress{
			Root:              headRoot,
			Block:             head.Number.Uint64(),
			BlockHash:         head.Hash(),
			LastCommittedRoot: types.EmptyBinaryHash,
			Started:           uint64(time.Now().Unix()),
		})
	}

	// Phase 1: Account scan with stale retry.
	var lastRoot common.Hash
	_, lastRoot, processed, err = sc.scanAccounts(ctx, t, headRoot, startKey, processed, head, chain)
	if err != nil {
		return err // fail() already called or context cancelled
	}

	// Phase 2: Replay the update queue.
	result, err := sc.replayUpdateQueue(ctx, lastRoot, head.Number.Uint64(), head.Hash(), chain)
	if err != nil {
		return err
	}

	// Phase 3: Finalize — use the replay tip (not the stale head from conversion start).
	// SetReady is conditional: if a concurrent HandleReorg already moved us to
	// Stale, we must not overwrite that transition.
	if !sc.SetReady(result.root, result.blockNum, result.blockHash) {
		log.Warn("UBT conversion completed but sidecar state changed (reorg?)")
		return nil
	}
	rawdb.WriteUBTCurrentRoot(sc.chainDB, result.root, result.blockNum, result.blockHash)
	rawdb.WriteUBTBlockRoot(sc.chainDB, result.blockHash, result.root)
	rawdb.DeleteUBTConversionProgress(sc.chainDB)
	rawdb.DeleteUBTUpdateQueueMeta(sc.chainDB)

	log.Info("UBT conversion complete",
		"block", result.blockNum, "root", result.root, "accounts", processed)
	return nil
}

// scanAccounts iterates all accounts in the MPT snapshot, inserts them into
// the binary trie along with their code and storage, and commits in batches.
func (sc *UBTSidecar) scanAccounts(
	ctx context.Context,
	t *bintrie.BinaryTrie,
	headRoot common.Hash,
	startKey common.Hash,
	processed uint64,
	head *types.Header,
	chain ChainContext,
) (*bintrie.BinaryTrie, common.Hash, uint64, error) {
	batchCount := uint64(0)
	mutationCount := uint64(0)
	lastRoot := t.Hash()

	for {
		// Create account iterator from the MPT trie database.
		accIt, err := sc.mptTrieDB.AccountIterator(headRoot, startKey)
		if err != nil {
			return nil, common.Hash{}, 0, sc.fail("conversion account iterator", err)
		}

		iterErr := sc.iterateAccounts(ctx, t, accIt, headRoot, head, &startKey, &processed, &batchCount, &mutationCount, &lastRoot)
		accIt.Release()

		if iterErr == nil {
			// Iteration completed successfully. Final commit for remaining mutations.
			var commitErr error
			lastRoot, commitErr = sc.commitConversionBatch(t, head, startKey, processed)
			if commitErr != nil {
				return nil, common.Hash{}, 0, commitErr
			}
			return t, lastRoot, processed, nil
		}

		// Check for context cancellation first.
		if ctx.Err() != nil {
			log.Info("UBT conversion cancelled, progress saved", "processed", processed)
			return nil, common.Hash{}, 0, ctx.Err()
		}

		// Check for stale snapshot error.
		if !isPathdbSnapshotStale(iterErr) {
			return nil, common.Hash{}, 0, sc.fail("conversion iterate", iterErr)
		}

		// Stale error: commit what we have, then retry with backoff.
		lastRoot, err = sc.commitConversionBatch(t, head, startKey, processed)
		if err != nil {
			return nil, common.Hash{}, 0, err
		}
		mutationCount = 0

		// Exponential backoff retry for stale snapshots.
		if err := sc.waitForStaleRetry(ctx); err != nil {
			// Timeout or cancelled: save progress, return to stale.
			log.Warn("UBT conversion stale retry exhausted", "processed", processed)
			sc.mu.Lock()
			sc.state = StateStale
			sc.mu.Unlock()
			return nil, common.Hash{}, 0, fmt.Errorf("stale retry timeout after %v: %w", staleRetryTotalTimeout, err)
		}

		// Retry: refresh headRoot from chain (snapshot may have advanced) and reopen trie.
		headRoot = chain.HeadRoot()
		t, err = bintrie.NewBinaryTrie(lastRoot, sc.triedb)
		if err != nil {
			return nil, common.Hash{}, 0, sc.fail("conversion reopen after stale", err)
		}
		log.Info("UBT conversion retrying after stale", "from", startKey, "headRoot", headRoot, "processed", processed)
	}
}

// iterateAccounts processes accounts from the iterator, inserting each into the
// binary trie along with code and storage. Returns nil on successful exhaustion
// of the iterator, or an error (including stale snapshot errors).
func (sc *UBTSidecar) iterateAccounts(
	ctx context.Context,
	t *bintrie.BinaryTrie,
	accIt pathdb.AccountIterator,
	headRoot common.Hash,
	head *types.Header,
	startKey *common.Hash,
	processed *uint64,
	batchCount *uint64,
	mutationCount *uint64,
	lastRoot *common.Hash,
) error {
	for accIt.Next() {
		// Check context cancellation periodically.
		if ctx.Err() != nil {
			return ctx.Err()
		}

		accountHash := accIt.Hash()
		accountData := accIt.Account()

		// Resolve the full address from preimage.
		preimage := rawdb.ReadPreimage(sc.chainDB, accountHash)
		if len(preimage) == 0 {
			return fmt.Errorf("preimage not found for account hash %x", accountHash)
		}
		addr := common.BytesToAddress(preimage)

		// Decode the slim-RLP encoded account.
		acct, err := types.FullAccount(accountData)
		if err != nil {
			return fmt.Errorf("decode account %x: %w", accountHash, err)
		}

		// Determine code and code length.
		var code []byte
		codeHash := common.BytesToHash(acct.CodeHash)
		if codeHash != types.EmptyCodeHash {
			code = rawdb.ReadCode(sc.chainDB, codeHash)
		}

		// Insert account into binary trie.
		if err := t.UpdateAccount(addr, acct, len(code)); err != nil {
			return fmt.Errorf("update account %x: %w", addr, err)
		}
		*mutationCount++

		// Insert contract code if present.
		if len(code) > 0 {
			if err := t.UpdateContractCode(addr, codeHash, code); err != nil {
				return fmt.Errorf("update code %x: %w", addr, err)
			}
			*mutationCount++
		}

		// Iterate and insert storage slots for this account.
		if err := sc.convertAccountStorage(ctx, t, headRoot, accountHash, addr, mutationCount); err != nil {
			return err
		}

		*processed++
		*batchCount++
		*startKey = accountHash

		// Batch commit check.
		if *batchCount >= conversionBatchSize || *mutationCount >= maxMutationsPerBatch {
			var err error
			*lastRoot, err = sc.commitConversionBatch(t, head, *startKey, *processed)
			if err != nil {
				return err
			}
			*batchCount = 0
			*mutationCount = 0

			// Check context after commit.
			if ctx.Err() != nil {
				return ctx.Err()
			}
		}
	}
	return accIt.Error()
}

// convertAccountStorage iterates all storage slots for a single account
// and inserts them into the binary trie.
func (sc *UBTSidecar) convertAccountStorage(
	ctx context.Context,
	t *bintrie.BinaryTrie,
	headRoot common.Hash,
	accountHash common.Hash,
	addr common.Address,
	mutationCount *uint64,
) error {
	storIt, err := sc.mptTrieDB.StorageIterator(headRoot, accountHash, common.Hash{})
	if err != nil {
		return fmt.Errorf("storage iterator for %x: %w", accountHash, err)
	}
	defer storIt.Release()

	for storIt.Next() {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		slotHash := storIt.Hash()
		slotValue := storIt.Slot()

		// Resolve the raw storage key from preimage.
		slotPreimage := rawdb.ReadPreimage(sc.chainDB, slotHash)
		if len(slotPreimage) == 0 {
			return fmt.Errorf("storage preimage not found for slot hash %x (account %x)", slotHash, accountHash)
		}

		if err := t.UpdateStorage(addr, slotPreimage, slotValue); err != nil {
			return fmt.Errorf("update storage %x slot %x: %w", addr, slotHash, err)
		}
		*mutationCount++
	}
	return storIt.Error()
}

// commitConversionBatch commits the trie and persists conversion progress.
func (sc *UBTSidecar) commitConversionBatch(
	t *bintrie.BinaryTrie,
	head *types.Header,
	lastKey common.Hash,
	processed uint64,
) (common.Hash, error) {
	parentRoot := t.Hash()
	newRoot, nodeset := t.Commit(false)

	if nodeset != nil {
		if err := sc.triedb.Update(newRoot, parentRoot, 0, trienode.NewWithNodeSet(nodeset), nil); err != nil {
			return common.Hash{}, sc.fail("conversion triedb update", err)
		}
	}
	if err := sc.triedb.Commit(newRoot, false); err != nil {
		return common.Hash{}, sc.fail("conversion triedb commit", err)
	}

	// Persist conversion progress.
	rawdb.WriteUBTConversionProgress(sc.chainDB, &rawdb.UBTConversionProgress{
		Root:                    head.Hash(),
		Block:                   head.Number.Uint64(),
		BlockHash:               head.Hash(),
		LastCommittedRoot:       newRoot,
		Started:                 uint64(time.Now().Unix()),
		LastProcessedAccountKey: lastKey,
		ProcessedAccounts:       processed,
	})

	log.Info("UBT conversion batch committed",
		"root", newRoot, "processed", processed, "lastKey", lastKey)

	return newRoot, nil
}

// waitForStaleRetry performs exponential backoff waiting for the stale snapshot
// to become available again. Returns nil if the wait completes within the total
// timeout, or an error if the timeout or context is exceeded.
func (sc *UBTSidecar) waitForStaleRetry(ctx context.Context) error {
	wait := staleRetryInitialWait
	deadline := time.After(staleRetryTotalTimeout)

	for {
		log.Info("UBT conversion waiting for stale snapshot retry", "wait", wait)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline:
			return fmt.Errorf("stale retry total timeout exceeded")
		case <-time.After(wait):
			// Check if the snapshot is available again by trying to create an iterator.
			it, err := sc.mptTrieDB.AccountIterator(common.Hash{}, common.Hash{})
			if it != nil {
				it.Release()
			}
			if err == nil || !isPathdbSnapshotStale(err) {
				return nil // Snapshot is available or a different error (let caller handle)
			}
		}

		// Increase wait time with exponential backoff.
		wait *= staleRetryMultiplier
		if wait > staleRetryMaxWait {
			wait = staleRetryMaxWait
		}
	}
}

func isPathdbSnapshotStale(err error) bool {
	return err != nil && strings.Contains(err.Error(), pathdbSnapshotStaleMessage)
}

// replayUpdateQueue replays queued state updates that were enqueued during
// conversion. It processes entries sequentially, verifying canonical hash
// and parent hash continuity, then deletes processed entries.
// replayResult holds the state after queue replay completes.
type replayResult struct {
	root      common.Hash
	blockNum  uint64
	blockHash common.Hash
}

func (sc *UBTSidecar) replayUpdateQueue(
	ctx context.Context,
	currentRoot common.Hash,
	currentBlock uint64,
	currentHash common.Hash,
	chain ChainContext,
) (*replayResult, error) {
	start, end, ok := rawdb.ReadUBTUpdateQueueMeta(sc.chainDB)
	if !ok {
		log.Info("UBT conversion: no queued updates to replay")
		return &replayResult{root: currentRoot, blockNum: currentBlock, blockHash: currentHash}, nil
	}

	log.Info("UBT conversion replaying update queue", "start", start, "end", end)

	// Update the sidecar's current root so applyUBTUpdate can read it.
	sc.mu.Lock()
	sc.currentRoot = currentRoot
	sc.currentBlock = currentBlock
	sc.currentHash = currentHash
	sc.mu.Unlock()

	// lastParentHash tracks parent hash continuity across replayed blocks.
	// Initialize to currentHash so the first replayed block's parent is validated
	// against the conversion tip (Fix #8: validate first parent link).
	lastParentHash := currentHash

	for blockNum := start; blockNum <= end; blockNum++ {
		if ctx.Err() != nil {
			log.Info("UBT queue replay cancelled", "at", blockNum)
			sc.mu.RLock()
			res := &replayResult{root: sc.currentRoot, blockNum: sc.currentBlock, blockHash: sc.currentHash}
			sc.mu.RUnlock()
			return res, ctx.Err()
		}

		// Skip blocks already covered by conversion.
		if blockNum <= currentBlock {
			// Clean up the entry.
			canonHash := chain.CanonicalHash(blockNum)
			if canonHash != (common.Hash{}) {
				rawdb.DeleteUBTUpdateQueueEntry(sc.chainDB, blockNum, canonHash)
			}
			continue
		}

		// Get canonical hash for this block number.
		canonHash := chain.CanonicalHash(blockNum)
		if canonHash == (common.Hash{}) {
			return nil, sc.fail("queue replay",
				fmt.Errorf("canonical hash not found for block %d", blockNum))
		}

		// Read the queued entry.
		data := rawdb.ReadUBTUpdateQueueEntry(sc.chainDB, blockNum, canonHash)
		if len(data) == 0 {
			// Entry might have been for a non-canonical hash; skip it.
			log.Debug("UBT queue replay: no entry for canonical block", "block", blockNum)
			continue
		}

		// Decode the update.
		update, err := DecodeUBTUpdate(data)
		if err != nil {
			return nil, sc.fail("queue replay decode",
				fmt.Errorf("block %d: %w", blockNum, err))
		}

		// Verify canonical hash matches.
		if update.BlockHash != canonHash {
			return nil, sc.fail("queue replay",
				fmt.Errorf("block %d hash mismatch: queued %x, canonical %x",
					blockNum, update.BlockHash, canonHash))
		}

		// Verify parent hash continuity.
		if lastParentHash != (common.Hash{}) && update.ParentHash != lastParentHash {
			return nil, sc.fail("queue replay",
				fmt.Errorf("block %d parent hash mismatch: expected %x, got %x",
					blockNum, lastParentHash, update.ParentHash))
		}

		// Apply the update.
		if err := sc.applyUBTUpdate(update); err != nil {
			return nil, fmt.Errorf("queue replay apply block %d: %w", blockNum, err)
		}

		lastParentHash = update.BlockHash

		// Delete the processed queue entry.
		rawdb.DeleteUBTUpdateQueueEntry(sc.chainDB, blockNum, canonHash)

		if blockNum%1000 == 0 {
			log.Info("UBT queue replay progress", "block", blockNum, "end", end)
		}
	}

	sc.mu.RLock()
	res := &replayResult{root: sc.currentRoot, blockNum: sc.currentBlock, blockHash: sc.currentHash}
	sc.mu.RUnlock()

	log.Info("UBT queue replay complete", "root", res.root, "block", res.blockNum)
	return res, nil
}

// resetVerkleNamespace clears the verkle namespace in the database and
// recreates the UBT trie database. This is used for fresh conversions.
func (sc *UBTSidecar) resetVerkleNamespace() error {
	log.Info("UBT conversion: resetting verkle namespace")

	// Close the existing triedb before wiping.
	if err := sc.triedb.Close(); err != nil {
		log.Warn("UBT conversion: failed to close old triedb", "err", err)
	}

	// Delete all data under the verkle prefix.
	verkleTable := rawdb.NewTable(sc.chainDB, string(rawdb.VerklePrefix))
	if err := verkleTable.DeleteRange(nil, nil); err != nil {
		return fmt.Errorf("verkle namespace reset: %w", err)
	}

	// Recreate the UBT trie database.
	sc.triedb = triedb.NewDatabase(sc.chainDB, &triedb.Config{
		IsVerkle: true,
		PathDB:   pathdb.Defaults,
	})

	log.Info("UBT conversion: verkle namespace reset complete")
	return nil
}
