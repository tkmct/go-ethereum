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

package pathdb

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/trie/bintrie"
	"github.com/ethereum/go-ethereum/trie/trienode"
)

// ubtConverter handles background conversion from MPT flat state to UBT.
type ubtConverter struct {
	db     *Database      // pathdb backend
	diskdb ethdb.Database // underlying disk database

	root     common.Hash                   // MPT state root to convert
	progress *rawdb.UBTConversionProgress  // conversion progress

	bt *bintrie.BinaryTrie // BinaryTrie instance for building UBT

	stopCh chan struct{} // signal to stop worker
	doneCh chan struct{} // closed when worker exits

	batchSize int // accounts per commit (default 1000)

	lock sync.Mutex
}

// newUBTConverter creates a new converter, loading existing progress from disk.
func newUBTConverter(db *Database, diskdb ethdb.Database, root common.Hash, batchSize int) *ubtConverter {
	if batchSize <= 0 {
		batchSize = 1000
	}

	progress := rawdb.ReadUBTConversionStatus(diskdb)
	if progress == nil {
		progress = &rawdb.UBTConversionProgress{
			Version:   1,
			Stage:     rawdb.UBTStageIdle,
			StateRoot: root,
			UpdatedAt: uint64(time.Now().Unix()),
		}
	}

	return &ubtConverter{
		db:        db,
		diskdb:    diskdb,
		root:      root,
		progress:  progress,
		stopCh:    make(chan struct{}),
		doneCh:    make(chan struct{}),
		batchSize: batchSize,
	}
}

// start begins the background conversion worker.
func (c *ubtConverter) start() error {
	c.lock.Lock()
	defer c.lock.Unlock()

	if c.progress.Stage == rawdb.UBTStageRunning {
		return errors.New("UBT conversion already running")
	}

	c.progress.Stage = rawdb.UBTStageRunning
	c.progress.UpdatedAt = uint64(time.Now().Unix())
	c.saveProgress()

	go c.run()
	return nil
}

// stop gracefully stops the conversion worker and waits for it to exit.
func (c *ubtConverter) stop() {
	c.lock.Lock()
	if c.progress.Stage != rawdb.UBTStageRunning {
		c.lock.Unlock()
		return
	}
	c.lock.Unlock()

	close(c.stopCh)
	<-c.doneCh
}

// status returns a copy of the current conversion progress.
func (c *ubtConverter) status() *rawdb.UBTConversionProgress {
	c.lock.Lock()
	defer c.lock.Unlock()
	return c.copyProgress()
}

// copyProgress returns a deep copy of the progress.
func (c *ubtConverter) copyProgress() *rawdb.UBTConversionProgress {
	if c.progress == nil {
		return nil
	}
	return &rawdb.UBTConversionProgress{
		Version:         c.progress.Version,
		Stage:           c.progress.Stage,
		StateRoot:       c.progress.StateRoot,
		UbtRoot:         c.progress.UbtRoot,
		NextAccountHash: c.progress.NextAccountHash,
		CurrentAccount:  c.progress.CurrentAccount,
		NextStorageHash: c.progress.NextStorageHash,
		AccountsDone:    c.progress.AccountsDone,
		SlotsDone:       c.progress.SlotsDone,
		LastError:       c.progress.LastError,
		UpdatedAt:       c.progress.UpdatedAt,
	}
}

// run is the main worker loop that performs the conversion.
func (c *ubtConverter) run() {
	defer close(c.doneCh)

	// Initialize BinaryTrie - start fresh with empty root
	var err error
	c.bt, err = bintrie.NewBinaryTrie(types.EmptyBinaryHash, nil)
	if err != nil {
		c.fail(fmt.Errorf("failed to create binary trie: %w", err))
		return
	}

	// Create AccountIterator starting from saved progress
	acctIter, err := c.db.AccountIterator(c.root, c.progress.NextAccountHash)
	if err != nil {
		c.fail(fmt.Errorf("failed to create account iterator: %w", err))
		return
	}
	defer acctIter.Release()

	processed := 0
	for acctIter.Next() {
		select {
		case <-c.stopCh:
			c.saveProgress()
			log.Info("UBT conversion stopped", "accounts", c.progress.AccountsDone, "slots", c.progress.SlotsDone)
			return
		default:
		}

		accountHash := acctIter.Hash()

		// Lookup preimage: hash → address
		addrBytes := rawdb.ReadPreimage(c.diskdb, accountHash)
		if len(addrBytes) != common.AddressLength {
			c.fail(fmt.Errorf("missing preimage for account %x", accountHash))
			return
		}
		addr := common.BytesToAddress(addrBytes)

		// Decode account data from slim RLP format
		accData := acctIter.Account()
		acc, err := types.FullAccount(accData)
		if err != nil {
			c.fail(fmt.Errorf("failed to decode account %x: %w", accountHash, err))
			return
		}

		// Get code length if account has code
		codeLen := 0
		if !bytes.Equal(acc.CodeHash, types.EmptyCodeHash[:]) {
			code := rawdb.ReadCode(c.diskdb, common.BytesToHash(acc.CodeHash))
			codeLen = len(code)
		}

		// Insert account into BinaryTrie
		if err := c.bt.UpdateAccount(addr, acc, codeLen); err != nil {
			c.fail(fmt.Errorf("failed to update account %x: %w", addr, err))
			return
		}

		// Process storage slots if account has storage
		if acc.Root != types.EmptyRootHash {
			if err := c.processStorage(accountHash, addr); err != nil {
				c.fail(err)
				return
			}
		}

		c.lock.Lock()
		c.progress.AccountsDone++
		c.progress.NextAccountHash = incHash(accountHash)
		c.lock.Unlock()

		processed++

		// Commit batch when threshold reached
		if processed >= c.batchSize {
			if err := c.commit(); err != nil {
				c.fail(fmt.Errorf("failed to commit batch: %w", err))
				return
			}
			processed = 0
		}
	}

	// Check for iterator errors
	if err := acctIter.Error(); err != nil {
		c.fail(fmt.Errorf("account iterator error: %w", err))
		return
	}

	// Final commit
	if err := c.commit(); err != nil {
		c.fail(fmt.Errorf("failed to commit final batch: %w", err))
		return
	}

	// Mark as completed
	c.lock.Lock()
	c.progress.Stage = rawdb.UBTStageDone
	c.progress.UpdatedAt = uint64(time.Now().Unix())
	c.lock.Unlock()
	c.saveProgress()

	log.Info("UBT conversion completed",
		"accounts", c.progress.AccountsDone,
		"slots", c.progress.SlotsDone,
		"ubtRoot", c.progress.UbtRoot)
}

// processStorage iterates over an account's storage slots and inserts them into the BinaryTrie.
func (c *ubtConverter) processStorage(accountHash common.Hash, addr common.Address) error {
	storageIter, err := c.db.StorageIterator(c.root, accountHash, c.progress.NextStorageHash)
	if err != nil {
		return fmt.Errorf("failed to create storage iterator for %x: %w", accountHash, err)
	}
	defer storageIter.Release()

	c.lock.Lock()
	c.progress.CurrentAccount = accountHash
	c.lock.Unlock()

	for storageIter.Next() {
		select {
		case <-c.stopCh:
			return nil // Interrupted, progress will be saved
		default:
		}

		slotHash := storageIter.Hash()
		value := storageIter.Slot()

		// Lookup preimage: slotHash → raw key
		slotKey := rawdb.ReadPreimage(c.diskdb, slotHash)
		if len(slotKey) != common.HashLength {
			return fmt.Errorf("missing preimage for slot %x of account %x", slotHash, accountHash)
		}

		// Insert storage slot into BinaryTrie
		if err := c.bt.UpdateStorage(addr, slotKey, value); err != nil {
			return fmt.Errorf("failed to update storage %x for account %x: %w", slotHash, addr, err)
		}

		c.lock.Lock()
		c.progress.SlotsDone++
		c.progress.NextStorageHash = incHash(slotHash)
		c.lock.Unlock()
	}

	// Check for iterator errors
	if err := storageIter.Error(); err != nil {
		return fmt.Errorf("storage iterator error for account %x: %w", accountHash, err)
	}

	// Account's storage processing complete, reset for next account
	c.lock.Lock()
	c.progress.CurrentAccount = common.Hash{}
	c.progress.NextStorageHash = common.Hash{}
	c.lock.Unlock()

	return nil
}

// commit persists the current BinaryTrie state and updates progress.
func (c *ubtConverter) commit() error {
	newRoot, nodeset := c.bt.Commit(false)

	// Store nodes to disk via batch write
	// UBT nodes are stored under VerklePrefix namespace which is already
	// set up in pathdb.Database.diskdb when isVerkle=true
	if nodeset != nil {
		updates, _ := nodeset.Size()
		if updates > 0 {
			batch := c.db.diskdb.NewBatch()
			nodeset.ForEachWithOrder(func(path string, node *trienode.Node) {
				if node.IsDeleted() {
					rawdb.DeleteAccountTrieNode(batch, []byte(path))
				} else {
					rawdb.WriteAccountTrieNode(batch, []byte(path), node.Blob)
				}
			})
			if err := batch.Write(); err != nil {
				return fmt.Errorf("failed to write UBT nodes: %w", err)
			}
		}
	}

	c.lock.Lock()
	c.progress.UbtRoot = newRoot
	c.progress.UpdatedAt = uint64(time.Now().Unix())
	c.lock.Unlock()

	c.saveProgress()

	log.Debug("UBT conversion batch committed",
		"root", newRoot,
		"accounts", c.progress.AccountsDone,
		"slots", c.progress.SlotsDone)

	return nil
}

// fail marks the conversion as failed and saves the error.
func (c *ubtConverter) fail(err error) {
	c.lock.Lock()
	c.progress.Stage = rawdb.UBTStageFailed
	c.progress.LastError = err.Error()
	c.progress.UpdatedAt = uint64(time.Now().Unix())
	c.lock.Unlock()

	c.saveProgress()

	log.Error("UBT conversion failed", "error", err)
}

// saveProgress persists the current progress to disk.
func (c *ubtConverter) saveProgress() {
	c.lock.Lock()
	progress := c.copyProgress()
	c.lock.Unlock()

	rawdb.WriteUBTConversionStatus(c.diskdb, progress)
}

// incHash returns the next hash in lexicographical order (plus one).
func incHash(h common.Hash) common.Hash {
	for i := len(h) - 1; i >= 0; i-- {
		h[i]++
		if h[i] != 0 {
			break
		}
	}
	return h
}
