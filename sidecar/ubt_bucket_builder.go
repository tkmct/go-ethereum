package sidecar

import (
	"context"
	"encoding/binary"
	"fmt"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/trie/bintrie"
	"github.com/ethereum/go-ethereum/triedb"
	"github.com/ethereum/go-ethereum/triedb/pathdb"
	"github.com/holiman/uint256"
	"golang.org/x/sync/errgroup"
)

const (
	ubtBucketRecordLeaf       = byte(1)
	ubtBucketRecordStem       = byte(2)
	ubtBucketSpoolBatchSize   = 10_000
	ubtBucketProgressInterval = 5 * time.Second

	// ubtBucketBytesPerRecord is a rough estimate of the in-memory trie node cost
	// per spool record. Each record produces ~1 trie node (internal or leaf).
	// A binary trie node is roughly 100-200 bytes in memory including Go overhead.
	ubtBucketBytesPerRecord = 192

	// ubtBucketDefaultMemoryMB is the default memory budget for Phase 2.
	ubtBucketDefaultMemoryMB = 4096

	// ubtBucketPreimageBatchSize is the number of storage slot hashes to
	// collect before dispatching parallel preimage lookups.
	ubtBucketPreimageBatchSize = 1024
)

var ubtBucketSpoolPrefix = []byte("ubt-bucket-spool-")

type UBTBucketBuilderConfig struct {
	PrefixBits           uint8
	Workers              int
	MemoryMB             int
	PreimageFetchers     int
	SkipMissingPreimages bool
}

var defaultUBTBucketBuilderConfig = UBTBucketBuilderConfig{
	PrefixBits:       8,
	Workers:          max(1, runtime.GOMAXPROCS(0)/2),
	MemoryMB:         ubtBucketDefaultMemoryMB,
	PreimageFetchers: max(4, runtime.GOMAXPROCS(0)),
}

func (sc *UBTSidecar) BuildOfflineBucketedFromMPT(ctx context.Context, chain ChainContext, cfg *UBTBucketBuilderConfig) error {
	head := chain.HeadBlock()
	if head == nil {
		return fmt.Errorf("ubt bucket builder: head block not available")
	}
	conf := defaultUBTBucketBuilderConfig
	if cfg != nil {
		if cfg.PrefixBits != 0 {
			conf.PrefixBits = cfg.PrefixBits
		}
		if cfg.Workers != 0 {
			conf.Workers = cfg.Workers
		}
		if cfg.MemoryMB != 0 {
			conf.MemoryMB = cfg.MemoryMB
		}
		if cfg.PreimageFetchers != 0 {
			conf.PreimageFetchers = cfg.PreimageFetchers
		}
		conf.SkipMissingPreimages = cfg.SkipMissingPreimages
	}
	if conf.PrefixBits == 0 || conf.PrefixBits > 16 {
		return fmt.Errorf("ubt bucket builder: invalid prefix bits %d", conf.PrefixBits)
	}
	headRoot := chain.HeadRoot()
	bucketCount := 1 << conf.PrefixBits
	log.Info("UBT bucket builder starting", "block", head.Number.Uint64(), "root", headRoot, "prefix_bits", conf.PrefixBits, "buckets", bucketCount, "workers", conf.Workers, "memory_mb", conf.MemoryMB, "preimage_fetchers", conf.PreimageFetchers)

	sc.cleanupConversionState()
	if err := sc.resetVerkleNamespace(); err != nil {
		return sc.fail("bucket builder reset", err)
	}
	spool := rawdb.NewTable(sc.ubtDB, string(ubtBucketSpoolPrefix))
	if err := spool.DeleteRange(nil, nil); err != nil {
		return sc.fail("bucket builder spool reset", err)
	}
	bucketSizes, err := sc.partitionUBTBucketed(ctx, headRoot, conf.PrefixBits, conf.PreimageFetchers, conf.SkipMissingPreimages, spool)
	if err != nil {
		return err
	}

	verkleDB := rawdb.NewTable(sc.ubtDB, string(rawdb.VerklePrefix))
	roots, err := sc.buildUBTBuckets(ctx, conf.PrefixBits, conf.Workers, conf.MemoryMB, bucketSizes, spool, verkleDB)
	if err != nil {
		return err
	}
	root, err := finalizeUBTBucketRoots(conf.PrefixBits, roots, verkleDB)
	if err != nil {
		return sc.fail("bucket builder finalize", err)
	}
	rawdb.WriteSnapshotRoot(verkleDB, root)
	rawdb.WritePersistentStateID(verkleDB, 0)
	if err := spool.DeleteRange(nil, nil); err != nil {
		return sc.fail("bucket builder spool cleanup", err)
	}
	if err := sc.triedb.Close(); err != nil {
		log.Warn("bucket builder close old triedb failed", "err", err)
	}
	sc.triedb = triedb.NewDatabase(sc.ubtDB, &triedb.Config{IsVerkle: true, PathDB: pathdb.Defaults})
	if _, err := bintrie.NewBinaryTrie(root, sc.triedb); err != nil {
		return sc.fail("bucket builder verify root", err)
	}
	if !sc.SetReady(root, head.Number.Uint64(), head.Hash()) {
		return fmt.Errorf("ubt bucket builder: failed to transition to ready")
	}
	rawdb.WriteUBTCurrentRoot(sc.ubtDB, root, head.Number.Uint64(), head.Hash())
	rawdb.WriteUBTBlockRoot(sc.ubtDB, head.Hash(), root)
	log.Info("UBT bucket builder complete", "block", head.Number.Uint64(), "root", root)
	return nil
}

// storageSlotEntry holds a storage slot with its prefetched preimage.
type storageSlotEntry struct {
	hash     common.Hash
	preimage []byte // nil if missing
	value    []byte
}

// spoolRecord is a pre-computed bucket record ready to be written to the spool.
type spoolRecord struct {
	key    []byte
	record []byte
}

// partitionAccountJob is sent from the account iterator to storage workers.
type partitionAccountJob struct {
	accountHash common.Hash
	addr        common.Address
	storageRoot common.Hash
}

func (sc *UBTSidecar) partitionUBTBucketed(ctx context.Context, headRoot common.Hash, prefixBits uint8, preimageFetchers int, skipMissing bool, spool ethdb.Database) ([]uint64, error) {
	fail := func(msg string, err error) ([]uint64, error) {
		return nil, sc.fail(msg, err)
	}
	accIt, err := sc.mptTrieDB.AccountIterator(headRoot, common.Hash{})
	if err != nil {
		return fail("bucket builder account iterator", err)
	}
	defer accIt.Release()

	bucketCount := 1 << prefixBits
	seqs := make(map[uint16]uint64)
	batch := spool.NewBatch()

	var (
		accounts        atomic.Uint64
		storageSlots    atomic.Uint64
		skippedAccounts atomic.Uint64
		skippedSlots    atomic.Uint64
		records         int
		start           = time.Now()
	)

	// Channel for storage workers to send pre-computed records to the spool writer.
	recordCh := make(chan []spoolRecord, preimageFetchers*2)

	// Channel for account jobs that need storage processing.
	storageJobs := make(chan partitionAccountJob, preimageFetchers)

	// Progress logger.
	stopProgress := make(chan struct{})
	go func() {
		ticker := time.NewTicker(ubtBucketProgressInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stopProgress:
				return
			case <-ticker.C:
				elapsed := time.Since(start)
				acc := accounts.Load()
				slots := storageSlots.Load()
				accRate := float64(acc) / elapsed.Seconds()
				slotRate := float64(slots) / elapsed.Seconds()
				log.Info("UBT bucket builder partition progress",
					"accounts", acc,
					"storage_slots", slots,
					"skipped_accounts", skippedAccounts.Load(),
					"skipped_slots", skippedSlots.Load(),
					"elapsed", elapsed.Truncate(time.Second),
					"accounts/s", fmt.Sprintf("%.0f", accRate),
					"slots/s", fmt.Sprintf("%.0f", slotRate),
				)
			}
		}
	}()

	// Storage workers: process storage slots for accounts in parallel.
	storageGroup, storageCtx := errgroup.WithContext(ctx)
	for range preimageFetchers {
		storageGroup.Go(func() error {
			return sc.partitionStorageWorker(storageCtx, headRoot, prefixBits, preimageFetchers, skipMissing, storageJobs, recordCh, &storageSlots, &skippedSlots)
		})
	}

	// Writer goroutine: single goroutine writes all records to spool (seqs is not concurrent-safe).
	var writerErr error
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for recs := range recordCh {
			for _, r := range recs {
				bucket := bucketIndex(r.key, prefixBits)
				seq := seqs[bucket]
				seqs[bucket] = seq + 1
				if err := batch.Put(bucketRecordKey(bucket, seq), r.record); err != nil {
					writerErr = err
					return
				}
				records++
			}
			if records >= ubtBucketSpoolBatchSize {
				if batch.ValueSize() > 0 {
					if err := batch.Write(); err != nil {
						writerErr = err
						return
					}
					batch.Reset()
					records = 0
				}
			}
		}
		// Final flush.
		if batch.ValueSize() > 0 {
			if err := batch.Write(); err != nil {
				writerErr = err
			}
		}
	}()

	// Producer: iterate accounts, process headers inline, dispatch storage jobs.
	var iterErr error
	for accIt.Next() {
		if ctx.Err() != nil {
			iterErr = ctx.Err()
			break
		}
		accountHash := accIt.Hash()
		accountData := common.CopyBytes(accIt.Account())
		addrPreimage := rawdb.ReadPreimage(sc.chainDB, accountHash)
		if len(addrPreimage) == 0 {
			if skipMissing {
				skippedAccounts.Add(1)
				continue
			}
			iterErr = fmt.Errorf("bucket builder address preimage: account hash %x", accountHash)
			break
		}
		addr := common.BytesToAddress(addrPreimage)
		acct, err := types.FullAccount(accountData)
		if err != nil {
			iterErr = fmt.Errorf("bucket builder decode account: %w", err)
			break
		}
		// Partition account header and code — produce records inline (fast).
		codeLen := 0
		codeHash := common.BytesToHash(acct.CodeHash)
		if codeHash != types.EmptyCodeHash {
			if code := rawdb.ReadCode(sc.chainDB, codeHash); len(code) != 0 {
				codeLen = len(code)
				codeRecs := buildCodeRecords(addr, bintrie.ChunkifyCode(code))
				recordCh <- codeRecs
			}
		}
		recordCh <- buildAccountRecord(addr, acct, codeLen)
		accounts.Add(1)

		// Dispatch storage processing to workers if account has storage.
		if acct.Root != types.EmptyRootHash {
			select {
			case storageJobs <- partitionAccountJob{
				accountHash: accountHash,
				addr:        addr,
				storageRoot: acct.Root,
			}:
			case <-ctx.Done():
				iterErr = ctx.Err()
			}
			if iterErr != nil {
				break
			}
		}
	}
	if err := accIt.Error(); err != nil && iterErr == nil {
		iterErr = err
	}
	// Signal storage workers that no more jobs are coming.
	close(storageJobs)
	// Wait for all storage workers to finish.
	storageErr := storageGroup.Wait()
	// Signal writer that no more records are coming.
	close(recordCh)
	// Wait for writer to finish.
	<-writerDone

	close(stopProgress)

	if iterErr != nil {
		return fail("bucket builder iterate", iterErr)
	}
	if storageErr != nil {
		return fail("bucket builder storage", storageErr)
	}
	if writerErr != nil {
		return fail("bucket builder spool write", writerErr)
	}

	// Convert seqs map to bucket sizes slice.
	bucketSizes := make([]uint64, bucketCount)
	for b, n := range seqs {
		bucketSizes[b] = n
	}
	elapsed := time.Since(start)
	log.Info("UBT bucket builder partition complete",
		"accounts", accounts.Load(),
		"storage_slots", storageSlots.Load(),
		"skipped_accounts", skippedAccounts.Load(),
		"skipped_slots", skippedSlots.Load(),
		"elapsed", elapsed.Truncate(time.Second),
	)
	return bucketSizes, nil
}

// partitionStorageWorker reads account jobs from the channel and processes
// each account's storage slots with parallel preimage fetching. Computed
// records are sent to recordCh for the spool writer.
func (sc *UBTSidecar) partitionStorageWorker(
	ctx context.Context,
	headRoot common.Hash,
	prefixBits uint8,
	fetchers int,
	skipMissing bool,
	jobs <-chan partitionAccountJob,
	recordCh chan<- []spoolRecord,
	slotsCounter *atomic.Uint64,
	skippedCounter *atomic.Uint64,
) error {
	slotBuf := make([]storageSlotEntry, 0, ubtBucketPreimageBatchSize)

	for job := range jobs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		storIt, err := sc.mptTrieDB.StorageIterator(headRoot, job.accountHash, common.Hash{})
		if err != nil {
			return err
		}
		var logged time.Time
		var accountSlots uint64
		for storIt.Next() {
			if ctx.Err() != nil {
				storIt.Release()
				return ctx.Err()
			}
			slotBuf = append(slotBuf, storageSlotEntry{
				hash:  storIt.Hash(),
				value: common.CopyBytes(storIt.Slot()),
			})
			if len(slotBuf) >= ubtBucketPreimageBatchSize {
				recs, skipped, err := sc.resolveAndBuildStorageRecords(slotBuf, prefixBits, fetchers, skipMissing, job.addr)
				if err != nil {
					storIt.Release()
					return err
				}
				if len(recs) > 0 {
					recordCh <- recs
				}
				slotsCounter.Add(uint64(len(recs)))
				skippedCounter.Add(skipped)
				accountSlots += uint64(len(recs))
				slotBuf = slotBuf[:0]
				if time.Since(logged) >= ubtBucketProgressInterval {
					log.Info("UBT bucket builder storage progress", "account", job.addr, "slots", accountSlots)
					logged = time.Now()
				}
			}
		}
		iterErr := storIt.Error()
		storIt.Release()
		if iterErr != nil {
			return iterErr
		}
		// Process remaining slots.
		if len(slotBuf) > 0 {
			recs, skipped, err := sc.resolveAndBuildStorageRecords(slotBuf, prefixBits, fetchers, skipMissing, job.addr)
			if err != nil {
				return err
			}
			if len(recs) > 0 {
				recordCh <- recs
			}
			slotsCounter.Add(uint64(len(recs)))
			skippedCounter.Add(skipped)
			accountSlots += uint64(len(recs))
			slotBuf = slotBuf[:0]
		}
		if accountSlots >= ubtBucketPreimageBatchSize {
			log.Info("UBT bucket builder storage complete", "account", job.addr, "slots", accountSlots)
		}
	}
	return nil
}

// resolveAndBuildStorageRecords fetches preimages in parallel and builds spool records.
func (sc *UBTSidecar) resolveAndBuildStorageRecords(
	slots []storageSlotEntry,
	prefixBits uint8,
	fetchers int,
	skipMissing bool,
	addr common.Address,
) ([]spoolRecord, uint64, error) {
	// Parallel preimage fetch.
	var wg sync.WaitGroup
	wg.Add(len(slots))
	sem := make(chan struct{}, fetchers)
	for i := range slots {
		sem <- struct{}{}
		go func(idx int) {
			defer func() { <-sem; wg.Done() }()
			slots[idx].preimage = rawdb.ReadPreimage(sc.chainDB, slots[idx].hash)
		}(i)
	}
	wg.Wait()

	// Build records.
	recs := make([]spoolRecord, 0, len(slots))
	var skipped uint64
	for i := range slots {
		if len(slots[i].preimage) == 0 {
			if skipMissing {
				skipped++
				continue
			}
			return nil, skipped, fmt.Errorf("slot preimage %x", slots[i].hash)
		}
		key := bintrie.GetBinaryTreeKeyStorageSlot(addr, slots[i].preimage)
		record := make([]byte, 1+bintrie.HashSize+bintrie.HashSize)
		record[0] = ubtBucketRecordLeaf
		copy(record[1:], key)
		if len(slots[i].value) >= bintrie.HashSize {
			copy(record[1+bintrie.HashSize:], slots[i].value[:bintrie.HashSize])
		} else {
			copy(record[1+bintrie.HashSize+bintrie.HashSize-len(slots[i].value):], slots[i].value)
		}
		recs = append(recs, spoolRecord{key: key, record: record})
	}
	return recs, skipped, nil
}

// buildAccountRecord creates spool records for an account header.
func buildAccountRecord(addr common.Address, acct *types.StateAccount, codeLen int) []spoolRecord {
	var basicData [bintrie.HashSize]byte
	stemKey := bintrie.GetBinaryTreeKey(addr, make([]byte, bintrie.HashSize))

	binary.BigEndian.PutUint32(basicData[bintrie.BasicDataCodeSizeOffset-1:], uint32(codeLen))
	binary.BigEndian.PutUint64(basicData[bintrie.BasicDataNonceOffset:], acct.Nonce)
	balance := acct.Balance
	if balance == nil {
		balance = new(uint256.Int)
	}
	balanceBytes := balance.Bytes()
	if len(balanceBytes) > 16 {
		balanceBytes = balanceBytes[16:]
	}
	copy(basicData[bintrie.HashSize-len(balanceBytes):], balanceBytes)

	record := make([]byte, 1+bintrie.StemSize+2+(1+bintrie.HashSize)*2)
	record[0] = ubtBucketRecordStem
	copy(record[1:], stemKey[:bintrie.StemSize])
	binary.BigEndian.PutUint16(record[1+bintrie.StemSize:], 2)
	offset := 1 + bintrie.StemSize + 2
	record[offset] = bintrie.BasicDataLeafKey
	offset++
	copy(record[offset:], basicData[:])
	offset += bintrie.HashSize
	record[offset] = bintrie.CodeHashLeafKey
	offset++
	copy(record[offset:], acct.CodeHash)

	return []spoolRecord{{key: stemKey, record: record}}
}

// buildCodeRecords creates spool records for contract code chunks.
func buildCodeRecords(addr common.Address, chunks bintrie.ChunkedCode) []spoolRecord {
	var (
		recs      []spoolRecord
		positions []byte
		values    [][]byte
	)
	for i, chunknr := 0, uint64(0); i < len(chunks); i, chunknr = i+bintrie.HashSize, chunknr+1 {
		groupOffset := (chunknr + 128) % bintrie.StemNodeWidth
		if groupOffset == 0 || chunknr == 0 {
			positions = positions[:0]
			values = values[:0]
		}
		positions = append(positions, byte(groupOffset))
		values = append(values, chunks[i:i+bintrie.HashSize])
		if groupOffset == bintrie.StemNodeWidth-1 || len(chunks)-i <= bintrie.HashSize {
			var off [bintrie.HashSize]byte
			binary.BigEndian.PutUint64(off[24:], chunknr-groupOffset+128)
			stemKey := bintrie.GetBinaryTreeKey(addr, off[:])
			record := make([]byte, 1+bintrie.StemSize+2+len(positions)*(1+bintrie.HashSize))
			record[0] = ubtBucketRecordStem
			copy(record[1:], stemKey[:bintrie.StemSize])
			binary.BigEndian.PutUint16(record[1+bintrie.StemSize:], uint16(len(positions)))
			o := 1 + bintrie.StemSize + 2
			for i, pos := range positions {
				record[o] = pos
				o++
				copy(record[o:], values[i])
				o += bintrie.HashSize
			}
			recs = append(recs, spoolRecord{key: stemKey, record: record})
		}
	}
	return recs
}

// bucketJob holds the index and estimated memory cost of a single bucket.
type bucketJob struct {
	index    int
	memBytes int64
}

func (sc *UBTSidecar) buildUBTBuckets(ctx context.Context, prefixBits uint8, workers, memoryMB int, bucketSizes []uint64, spool ethdb.Database, verkleDB ethdb.KeyValueWriter) ([]common.Hash, error) {
	count := 1 << prefixBits
	roots := make([]common.Hash, count)

	// Build job list sorted by estimated memory (largest first) to avoid tail latency.
	jobs := make([]bucketJob, count)
	var totalRecords uint64
	for i := range count {
		size := bucketSizes[i]
		totalRecords += size
		jobs[i] = bucketJob{
			index:    i,
			memBytes: int64(size) * ubtBucketBytesPerRecord,
		}
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].memBytes > jobs[j].memBytes })

	// Memory semaphore: tracks in-flight estimated memory in bytes.
	// Each goroutine acquires its estimated cost before starting and releases after flush.
	memBudget := int64(memoryMB) * 1024 * 1024
	var memInFlight atomic.Int64

	var (
		completed  atomic.Int64
		mu         sync.Mutex
		start      = time.Now()
	)
	stopProgress := make(chan struct{})
	go func() {
		ticker := time.NewTicker(ubtBucketProgressInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stopProgress:
				return
			case <-ticker.C:
				done := completed.Load()
				elapsed := time.Since(start)
				rate := float64(done) / elapsed.Seconds()
				pct := float64(done) * 100 / float64(count)
				memMB := float64(memInFlight.Load()) / (1024 * 1024)
				log.Info("UBT bucket builder build progress",
					"buckets", fmt.Sprintf("%d/%d", done, count),
					"pct", fmt.Sprintf("%.1f%%", pct),
					"mem_mb", fmt.Sprintf("%.0f/%d", memMB, memoryMB),
					"elapsed", elapsed.Truncate(time.Second),
					"buckets/s", fmt.Sprintf("%.1f", rate),
				)
			}
		}
	}()
	defer close(stopProgress)

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(workers)
	for _, job := range jobs {
		job := job
		// Wait until there is enough memory budget available.
		// Always allow at least one bucket to proceed (even if it exceeds budget)
		// to avoid deadlock on very large buckets.
		for {
			inFlight := memInFlight.Load()
			if inFlight == 0 || inFlight+job.memBytes <= memBudget {
				memInFlight.Add(job.memBytes)
				break
			}
			// Budget exceeded; wait briefly for other goroutines to finish.
			select {
			case <-gctx.Done():
				_ = g.Wait()
				return nil, gctx.Err()
			case <-time.After(50 * time.Millisecond):
			}
		}
		g.Go(func() error {
			defer memInFlight.Add(-job.memBytes)

			if gctx.Err() != nil {
				return gctx.Err()
			}
			path := bucketPath(uint16(job.index), prefixBits)
			tr := bintrie.NewSubtrie(path)
			it := spool.NewIterator(bucketPrefixKey(uint16(job.index)), nil)
			for it.Next() {
				record := it.Value()
				switch record[0] {
				case ubtBucketRecordLeaf:
					if err := tr.Insert(record[1:33], record[33:65]); err != nil {
						it.Release()
						return sc.fail("bucket builder insert leaf", err)
					}
				case ubtBucketRecordStem:
					stem := record[1 : 1+bintrie.StemSize]
					n := int(binary.BigEndian.Uint16(record[1+bintrie.StemSize : 1+bintrie.StemSize+2]))
					positions := make([]byte, n)
					values := make([][]byte, n)
					offset := 1 + bintrie.StemSize + 2
					for i := 0; i < n; i++ {
						positions[i] = record[offset]
						offset++
						values[i] = record[offset : offset+bintrie.HashSize]
						offset += bintrie.HashSize
					}
					if err := tr.UpdateStemEntries(stem, positions, values); err != nil {
						it.Release()
						return sc.fail("bucket builder insert stem", err)
					}
				default:
					it.Release()
					return sc.fail("bucket builder decode record", fmt.Errorf("unknown record kind %d", record[0]))
				}
			}
			if err := it.Error(); err != nil {
				it.Release()
				return sc.fail("bucket builder iterate bucket", err)
			}
			it.Release()
			root, err := tr.FlushTo(func(path []byte, blob []byte, hash common.Hash) error {
				mu.Lock()
				defer mu.Unlock()
				rawdb.WriteAccountTrieNode(verkleDB, path, blob)
				return nil
			})
			if err != nil {
				return sc.fail("bucket builder flush bucket", err)
			}
			roots[job.index] = root
			completed.Add(1)
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	elapsed := time.Since(start)
	log.Info("UBT bucket builder build complete", "buckets", count, "total_records", totalRecords, "elapsed", elapsed.Truncate(time.Second))
	return roots, nil
}

func finalizeUBTBucketRoots(prefixBits uint8, roots []common.Hash, verkleDB ethdb.KeyValueWriter) (common.Hash, error) {
	var build func(path []byte, index int) common.Hash
	build = func(path []byte, index int) common.Hash {
		if len(path) == int(prefixBits) {
			return roots[index]
		}
		left := build(appendBit(path, 0), index<<1)
		right := build(appendBit(path, 1), index<<1|1)
		if left == (common.Hash{}) && right == (common.Hash{}) {
			return common.Hash{}
		}
		blob, hash := bintrie.SerializeInternalNode(len(path), left, right)
		rawdb.WriteAccountTrieNode(verkleDB, path, blob)
		return hash
	}
	return build(nil, 0), nil
}

func bucketIndex(key []byte, bits uint8) uint16 {
	var bucket uint16
	for i := 0; i < int(bits); i++ {
		bucket = bucket<<1 | uint16((key[i/8]>>(7-(i%8)))&1)
	}
	return bucket
}

func bucketRecordKey(bucket uint16, seq uint64) []byte {
	key := make([]byte, 2+8)
	binary.BigEndian.PutUint16(key, bucket)
	binary.BigEndian.PutUint64(key[2:], seq)
	return key
}

func bucketPath(index uint16, bits uint8) []byte {
	path := make([]byte, bits)
	for i := 0; i < int(bits); i++ {
		path[i] = byte((index >> (int(bits) - i - 1)) & 1)
	}
	return path
}

func bucketPrefixKey(bucket uint16) []byte {
	key := make([]byte, 2)
	binary.BigEndian.PutUint16(key, bucket)
	return key
}
