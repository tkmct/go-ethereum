package sidecar

import (
	"context"
	"encoding/binary"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/trie/bintrie"
	"github.com/ethereum/go-ethereum/trie/trienode"
	"github.com/ethereum/go-ethereum/triedb"
	"github.com/holiman/uint256"
	"golang.org/x/sync/errgroup"
)

const ubtBuilderProgressInterval = 5 * time.Second

type UBTBuilderConfig struct {
	ShardBits            uint8
	QueueSize            int
	PrepareWorkers       int
	SkipMissingPreimages bool
}

var defaultUBTBuilderConfig = UBTBuilderConfig{
	ShardBits:      4,
	QueueSize:      256,
	PrepareWorkers: max(1, runtime.GOMAXPROCS(0)/2),
}

type ubtBuilderMutation struct {
	key    []byte
	value  []byte
	stem   []byte
	values [][]byte
}

type ubtBuilderShard struct {
	trie *bintrie.Subtrie
	ch   chan ubtBuilderMutation
}

type ubtBuilderAccountJob struct {
	hash common.Hash
	data []byte
}

func (sc *UBTSidecar) BuildOfflineFromMPT(ctx context.Context, chain ChainContext, cfg *UBTBuilderConfig) error {
	head := chain.HeadBlock()
	if head == nil {
		return fmt.Errorf("ubt builder: head block not available")
	}
	conf := defaultUBTBuilderConfig
	if cfg != nil {
		if cfg.ShardBits != 0 {
			conf.ShardBits = cfg.ShardBits
		}
		if cfg.QueueSize != 0 {
			conf.QueueSize = cfg.QueueSize
		}
		if cfg.PrepareWorkers != 0 {
			conf.PrepareWorkers = cfg.PrepareWorkers
		}
		conf.SkipMissingPreimages = cfg.SkipMissingPreimages
	}
	if conf.ShardBits > 8 {
		return fmt.Errorf("ubt builder: shard bits %d unsupported", conf.ShardBits)
	}
	headRoot := chain.HeadRoot()
	log.Info("UBT offline builder starting", "block", head.Number.Uint64(), "root", headRoot, "shards", 1<<conf.ShardBits)

	sc.cleanupConversionState()
	if err := sc.resetVerkleNamespace(); err != nil {
		return sc.fail("builder reset", err)
	}

	shards := makeUBTBuilderShards(conf)
	g, gctx := errgroup.WithContext(ctx)
	for i := range shards {
		shard := shards[i]
		g.Go(func() error { return runUBTBuilderShard(gctx, shard) })
	}

	var processed atomic.Uint64
	var skippedAccounts atomic.Uint64
	var skippedSlots atomic.Uint64
	stopProgress := startUBTProgressLogger("UBT offline builder progress", &processed, nil)
	defer close(stopProgress)
	jobs := make(chan ubtBuilderAccountJob, conf.QueueSize)
	prep, prepCtx := errgroup.WithContext(gctx)
	prep.Go(func() error {
		accIt, err := sc.mptTrieDB.AccountIterator(headRoot, common.Hash{})
		if err != nil {
			return err
		}
		defer accIt.Release()
		defer close(jobs)

		for accIt.Next() {
			select {
			case <-prepCtx.Done():
				return prepCtx.Err()
			case jobs <- ubtBuilderAccountJob{hash: accIt.Hash(), data: common.CopyBytes(accIt.Account())}:
			}
		}
		return accIt.Error()
	})
	for i := 0; i < conf.PrepareWorkers; i++ {
		prep.Go(func() error {
			for job := range jobs {
				skippedAccount, skippedSlot, err := sc.prepareUBTBuilderAccount(prepCtx, shards, conf.ShardBits, headRoot, job, conf.SkipMissingPreimages)
				if err != nil {
					return err
				}
				if skippedAccount {
					skippedAccounts.Add(1)
					continue
				}
				skippedSlots.Add(skippedSlot)
				processed.Add(1)
			}
			return nil
		})
	}
	if err := prep.Wait(); err != nil {
		closeUBTBuilderShards(shards)
		_ = g.Wait()
		return sc.fail("builder prepare", err)
	}
	closeUBTBuilderShards(shards)
	if err := g.Wait(); err != nil {
		return sc.fail("builder shard apply", err)
	}

	nodeset, root, err := commitUBTBuilderShards(shards, conf.ShardBits)
	if err != nil {
		return sc.fail("builder commit shards", err)
	}
	if err := sc.triedb.Update(root, types.EmptyBinaryHash, head.Number.Uint64(), trienode.NewWithNodeSet(nodeset), triedb.NewStateSet()); err != nil {
		return sc.fail("builder triedb update", err)
	}
	if err := sc.triedb.Commit(root, false); err != nil {
		return sc.fail("builder triedb commit", err)
	}
	if !sc.SetReady(root, head.Number.Uint64(), head.Hash()) {
		return fmt.Errorf("ubt builder: failed to transition to ready")
	}
	rawdb.WriteUBTCurrentRoot(sc.chainDB, root, head.Number.Uint64(), head.Hash())
	rawdb.WriteUBTBlockRoot(sc.chainDB, head.Hash(), root)
	log.Info("UBT offline builder complete", "block", head.Number.Uint64(), "root", root, "accounts", processed.Load(), "skipped_accounts", skippedAccounts.Load(), "skipped_slots", skippedSlots.Load())
	return nil
}

func (sc *UBTSidecar) prepareUBTBuilderAccount(ctx context.Context, shards []ubtBuilderShard, shardBits uint8, headRoot common.Hash, job ubtBuilderAccountJob, skipMissing bool) (bool, uint64, error) {
	addrPreimage := rawdb.ReadPreimage(sc.chainDB, job.hash)
	if len(addrPreimage) == 0 {
		if skipMissing {
			return true, 0, nil
		}
		return false, 0, fmt.Errorf("account preimage %x", job.hash)
	}
	addr := common.BytesToAddress(addrPreimage)
	acct, err := types.FullAccount(job.data)
	if err != nil {
		return false, 0, err
	}
	codeLen := 0
	codeHash := common.BytesToHash(acct.CodeHash)
	if codeHash != types.EmptyCodeHash {
		code := rawdb.ReadCode(sc.chainDB, codeHash)
		if len(code) != 0 {
			codeLen = len(code)
			if err := routeUBTCode(shards, shardBits, addr, bintrie.ChunkifyCode(code)); err != nil {
				return false, 0, err
			}
		}
	}
	if err := routeUBTAccount(shards, shardBits, addr, acct, codeLen); err != nil {
		return false, 0, err
	}
	if acct.Root == types.EmptyRootHash {
		return false, 0, nil
	}
	storIt, err := sc.mptTrieDB.StorageIterator(headRoot, job.hash, common.Hash{})
	if err != nil {
		return false, 0, err
	}
	defer storIt.Release()
	var skippedSlots uint64
	for storIt.Next() {
		select {
		case <-ctx.Done():
			return false, skippedSlots, ctx.Err()
		default:
		}
		slotHash := storIt.Hash()
		slotPreimage := rawdb.ReadPreimage(sc.chainDB, slotHash)
		if len(slotPreimage) == 0 {
			if skipMissing {
				skippedSlots++
				continue
			}
			return false, skippedSlots, fmt.Errorf("slot preimage %x", slotHash)
		}
		if err := routeUBTStorage(shards, shardBits, addr, slotPreimage, storIt.Slot()); err != nil {
			return false, skippedSlots, err
		}
	}
	return false, skippedSlots, storIt.Error()
}

func makeUBTBuilderShards(cfg UBTBuilderConfig) []ubtBuilderShard {
	count := 1 << cfg.ShardBits
	shards := make([]ubtBuilderShard, count)
	for i := range shards {
		shards[i] = ubtBuilderShard{
			trie: bintrie.NewSubtrie(builderShardPath(uint8(i), cfg.ShardBits)),
			ch:   make(chan ubtBuilderMutation, cfg.QueueSize),
		}
	}
	return shards
}

func closeUBTBuilderShards(shards []ubtBuilderShard) {
	for i := range shards {
		close(shards[i].ch)
	}
}

func runUBTBuilderShard(ctx context.Context, shard ubtBuilderShard) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case m, ok := <-shard.ch:
			if !ok {
				return nil
			}
			var err error
			if m.stem != nil {
				err = shard.trie.UpdateStem(m.stem, m.values)
			} else {
				err = shard.trie.Insert(m.key, m.value)
			}
			if err != nil {
				return err
			}
		}
	}
}

func routeUBTAccount(shards []ubtBuilderShard, shardBits uint8, addr common.Address, acct *types.StateAccount, codeLen int) error {
	var (
		values    = make([][]byte, bintrie.StemNodeWidth)
		basicData [bintrie.HashSize]byte
		stemKey   = bintrie.GetBinaryTreeKey(addr, make([]byte, bintrie.HashSize))
	)
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
	values[bintrie.BasicDataLeafKey] = append([]byte(nil), basicData[:]...)
	values[bintrie.CodeHashLeafKey] = append([]byte(nil), acct.CodeHash...)
	shard := builderShardIndex(stemKey, shardBits)
	shards[shard].ch <- ubtBuilderMutation{stem: append([]byte(nil), stemKey[:bintrie.StemSize]...), values: values}
	return nil
}

func routeUBTCode(shards []ubtBuilderShard, shardBits uint8, addr common.Address, chunks bintrie.ChunkedCode) error {
	var values [][]byte
	for i, chunknr := 0, uint64(0); i < len(chunks); i, chunknr = i+bintrie.HashSize, chunknr+1 {
		groupOffset := (chunknr + 128) % bintrie.StemNodeWidth
		if groupOffset == 0 || chunknr == 0 {
			values = make([][]byte, bintrie.StemNodeWidth)
		}
		values[groupOffset] = chunks[i : i+bintrie.HashSize]
		if groupOffset == bintrie.StemNodeWidth-1 || len(chunks)-i <= bintrie.HashSize {
			var offset [bintrie.HashSize]byte
			binary.BigEndian.PutUint64(offset[24:], chunknr-groupOffset+128)
			stemKey := bintrie.GetBinaryTreeKey(addr, offset[:])
			shard := builderShardIndex(stemKey, shardBits)
			shards[shard].ch <- ubtBuilderMutation{stem: append([]byte(nil), stemKey[:bintrie.StemSize]...), values: cloneStemValues(values)}
		}
	}
	return nil
}

func routeUBTStorage(shards []ubtBuilderShard, shardBits uint8, addr common.Address, slotKey, slotValue []byte) error {
	key := bintrie.GetBinaryTreeKeyStorageSlot(addr, slotKey)
	var value [bintrie.HashSize]byte
	if len(slotValue) >= bintrie.HashSize {
		copy(value[:], slotValue[:bintrie.HashSize])
	} else {
		copy(value[bintrie.HashSize-len(slotValue):], slotValue)
	}
	shard := builderShardIndex(key, shardBits)
	shards[shard].ch <- ubtBuilderMutation{key: key, value: append([]byte(nil), value[:]...)}
	return nil
}

func commitUBTBuilderShards(shards []ubtBuilderShard, shardBits uint8) (*trienode.NodeSet, common.Hash, error) {
	set := trienode.NewNodeSet(common.Hash{})
	roots := make([]common.Hash, len(shards))
	var mu sync.Mutex
	g := new(errgroup.Group)
	for i := range shards {
		i := i
		g.Go(func() error {
			root, nodeset, err := shards[i].trie.Commit()
			if err != nil {
				return err
			}
			mu.Lock()
			defer mu.Unlock()
			roots[i] = root
			return set.MergeDisjoint(nodeset)
		})
	}
	if err := g.Wait(); err != nil {
		return nil, common.Hash{}, err
	}
	root := buildUBTBuilderTop(set, roots, shardBits, nil, 0)
	return set, root, nil
}

func buildUBTBuilderTop(set *trienode.NodeSet, roots []common.Hash, shardBits uint8, path []byte, index int) common.Hash {
	if len(path) == int(shardBits) {
		return roots[index]
	}
	left := buildUBTBuilderTop(set, roots, shardBits, appendBit(path, 0), index<<1)
	right := buildUBTBuilderTop(set, roots, shardBits, appendBit(path, 1), index<<1|1)
	if left == (common.Hash{}) && right == (common.Hash{}) {
		return common.Hash{}
	}
	blob, hash := bintrie.SerializeInternalNode(len(path), left, right)
	set.AddNodeWithPrev(path, hash, blob, nil)
	return hash
}

func appendBit(path []byte, bit byte) []byte {
	next := make([]byte, len(path)+1)
	copy(next, path)
	next[len(path)] = bit
	return next
}

func builderShardIndex(key []byte, bits uint8) int {
	var shard int
	for i := 0; i < int(bits); i++ {
		shard = shard<<1 | int((key[i/8]>>(7-(i%8)))&1)
	}
	return shard
}

func builderShardPath(index uint8, bits uint8) []byte {
	path := make([]byte, bits)
	for i := 0; i < int(bits); i++ {
		path[i] = (index >> (bits - uint8(i) - 1)) & 1
	}
	return path
}

func cloneStemValues(values [][]byte) [][]byte {
	cloned := make([][]byte, len(values))
	copy(cloned, values)
	return cloned
}

func startUBTProgressLogger(msg string, accounts *atomic.Uint64, buckets *atomic.Uint64) chan struct{} {
	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(ubtBuilderProgressInterval)
		defer ticker.Stop()

		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				ctx := []any{"accounts", accounts.Load()}
				if buckets != nil {
					ctx = append(ctx, "buckets", buckets.Load())
				}
				log.Info(msg, ctx...)
			}
		}
	}()
	return stop
}
