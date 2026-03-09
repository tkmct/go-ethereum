package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"time"

	"github.com/ethereum/go-ethereum/cmd/utils"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/ethdb/pebble"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/node"
	"github.com/ethereum/go-ethereum/sidecar"
	"github.com/urfave/cli/v2"
)

var (
	ubtModeFlag = &cli.StringFlag{
		Name:  "mode",
		Usage: "Offline UBT builder mode: fast or bucketed",
		Value: "fast",
	}
	ubtPrefixBitsFlag = &cli.UintFlag{
		Name:  "prefix-bits",
		Usage: "Bucket prefix bits for bucketed mode",
		Value: 8,
	}
	ubtShardBitsFlag = &cli.UintFlag{
		Name:  "shard-bits",
		Usage: "Shard bits for fast mode",
		Value: 4,
	}
	ubtPrepareWorkersFlag = &cli.IntFlag{
		Name:  "prepare-workers",
		Usage: "Number of preparation workers for fast mode",
		Value: 0,
	}
	ubtQueueSizeFlag = &cli.IntFlag{
		Name:  "queue-size",
		Usage: "Queue size for the fast builder",
		Value: 256,
	}
	ubtBucketWorkersFlag = &cli.IntFlag{
		Name:  "bucket-workers",
		Usage: "Number of parallel workers for bucketed mode",
		Value: 0,
	}
	ubtBucketMemoryFlag = &cli.IntFlag{
		Name:  "bucket-memory",
		Usage: "Memory budget in MB for bucketed mode Phase 2",
		Value: 0,
	}
	ubtPreimageFetchersFlag = &cli.IntFlag{
		Name:  "preimage-fetchers",
		Usage: "Number of parallel preimage fetchers for partition phase",
		Value: 0,
	}
	ubtSampleAccountsFlag = &cli.Uint64Flag{
		Name:  "sample-accounts",
		Usage: "Number of accounts to sample during preflight",
		Value: 1000,
	}
	ubtSampleStorageFlag = &cli.Uint64Flag{
		Name:  "sample-storage",
		Usage: "Number of storage slots to sample per sampled account",
		Value: 4,
	}
	ubtVerifyStateFlag = &cli.BoolFlag{
		Name:  "verify-state",
		Usage: "Run full pathdb state verification before preflight/build",
	}
	ubtPreflightFullFlag = &cli.BoolFlag{
		Name:  "full",
		Usage: "Scan all accounts for missing preimages instead of sampling",
	}
	ubtSkipPreflightFlag = &cli.BoolFlag{
		Name:  "skip-preflight",
		Usage: "Skip preflight before build",
	}
	ubtSkipMissingPreimagesFlag = &cli.BoolFlag{
		Name:  "ubt.skip-missing-preimages",
		Usage: "Skip accounts or storage slots with missing preimages during build",
	}
	ubtCommand = &cli.Command{
		Name:  "ubt",
		Usage: "UBT offline tooling",
		Subcommands: []*cli.Command{
			{
				Name:   "preflight",
				Usage:  "Validate that a copied MPT database is safe for offline UBT build",
				Action: ubtPreflight,
				Flags: slices.Concat(utils.NetworkFlags, utils.DatabaseFlags, []cli.Flag{
					utils.UBTDataDirFlag,
					ubtSampleAccountsFlag,
					ubtSampleStorageFlag,
					ubtVerifyStateFlag,
					ubtPreflightFullFlag,
				}),
			},
			{
				Name:   "build",
				Usage:  "Run offline UBT build into a separate UBT datadir",
				Action: ubtBuild,
				Flags: slices.Concat(utils.NetworkFlags, utils.DatabaseFlags, []cli.Flag{
					utils.UBTDataDirFlag,
					ubtModeFlag,
					ubtPrefixBitsFlag,
					ubtShardBitsFlag,
					ubtPrepareWorkersFlag,
					ubtQueueSizeFlag,
					ubtBucketWorkersFlag,
					ubtBucketMemoryFlag,
					ubtPreimageFetchersFlag,
					ubtSampleAccountsFlag,
					ubtSampleStorageFlag,
					ubtVerifyStateFlag,
					ubtPreflightFullFlag,
					ubtSkipPreflightFlag,
					ubtSkipMissingPreimagesFlag,
				}),
			},
		},
	}
)

type offlineUBTChainContext struct {
	root common.Hash
	head *types.Header
}

func (c offlineUBTChainContext) HeadRoot() common.Hash            { return c.root }
func (c offlineUBTChainContext) HeadBlock() *types.Header         { return c.head }
func (c offlineUBTChainContext) CanonicalHash(uint64) common.Hash { return common.Hash{} }

func ubtPreflight(ctx *cli.Context) error {
	stack, _ := makeConfigNode(ctx)
	defer stack.Close()

	chainDB := utils.MakeChainDatabase(ctx, stack, true)
	defer chainDB.Close()

	head := rawdb.ReadHeadBlock(chainDB)
	if head == nil {
		return errors.New("no head block found")
	}
	triedb := utils.MakeTrieDatabase(ctx, stack, chainDB, true, true, false)
	defer triedb.Close()

	side, err := sidecar.NewUBTSidecar(chainDB, chainDB, triedb)
	if err != nil {
		return err
	}
	chain := offlineUBTChainContext{root: head.Root(), head: head.Header()}
	res, err := side.PreflightOfflineBuild(context.Background(), chain, &sidecar.UBTPreflightConfig{
		SampleAccounts: ctx.Uint64(ubtSampleAccountsFlag.Name),
		SampleStorage:  ctx.Uint64(ubtSampleStorageFlag.Name),
		VerifyState:    ctx.Bool(ubtVerifyStateFlag.Name),
		Full:           ctx.Bool(ubtPreflightFullFlag.Name),
	})
	if err != nil {
		return err
	}
	missingPct := 0.0
	if res.TotalAccounts > 0 {
		missingPct = float64(res.MissingAccountPreimages) * 100 / float64(res.TotalAccounts)
	}
	log.Info("UBT preflight ok",
		"block", res.HeadBlock,
		"root", res.HeadRoot,
		"snapshotCompleted", res.SnapshotCompleted,
		"sampledAccounts", res.SampledAccounts,
		"sampledSlots", res.SampledSlots,
		"totalAccounts", res.TotalAccounts,
		"accountsWithCode", res.AccountsWithCode,
		"accountsWithStorage", res.AccountsWithStorage,
		"missingAccountPreimages", res.MissingAccountPreimages,
		"missingAccountPreimagesPct", fmt.Sprintf("%.4f", missingPct),
		"missingStoragePreimages", res.MissingStoragePreimages,
	)
	if res.MissingAccountPreimages > 0 && ctx.Bool(ubtPreflightFullFlag.Name) {
		return fmt.Errorf("ubt preflight: found %d missing account preimages (%.4f%%)", res.MissingAccountPreimages, missingPct)
	}
	return nil
}

func ubtBuild(ctx *cli.Context) error {
	ubtPath := ctx.String(utils.UBTDataDirFlag.Name)
	if ubtPath == "" {
		return errors.New("--ubt.datadir is required for offline build")
	}
	stack, _ := makeConfigNode(ctx)
	defer stack.Close()

	chainDB := utils.MakeChainDatabase(ctx, stack, true)
	defer chainDB.Close()

	head := rawdb.ReadHeadBlock(chainDB)
	if head == nil {
		return errors.New("no head block found")
	}
	triedb := utils.MakeTrieDatabase(ctx, stack, chainDB, true, true, false)
	defer triedb.Close()

	ubtDB, err := openUBTDatabase(stack, ubtPath)
	if err != nil {
		return err
	}
	defer ubtDB.Close()

	side, err := sidecar.NewUBTSidecar(chainDB, ubtDB, triedb)
	if err != nil {
		return err
	}
	chain := offlineUBTChainContext{root: head.Root(), head: head.Header()}
	if !ctx.Bool(ubtSkipPreflightFlag.Name) {
		if _, err := side.PreflightOfflineBuild(context.Background(), chain, &sidecar.UBTPreflightConfig{
			SampleAccounts: ctx.Uint64(ubtSampleAccountsFlag.Name),
			SampleStorage:  ctx.Uint64(ubtSampleStorageFlag.Name),
			VerifyState:    ctx.Bool(ubtVerifyStateFlag.Name),
			Full:           ctx.Bool(ubtPreflightFullFlag.Name),
		}); err != nil {
			return err
		}
	}
	if !side.BeginConversion() {
		return errors.New("failed to begin conversion")
	}
	start := time.Now()
	switch ctx.String(ubtModeFlag.Name) {
	case "fast":
		err = side.BuildOfflineFromMPT(context.Background(), chain, &sidecar.UBTBuilderConfig{
			ShardBits:            uint8(ctx.Uint(ubtShardBitsFlag.Name)),
			QueueSize:            ctx.Int(ubtQueueSizeFlag.Name),
			PrepareWorkers:       ctx.Int(ubtPrepareWorkersFlag.Name),
			SkipMissingPreimages: ctx.Bool(ubtSkipMissingPreimagesFlag.Name),
		})
	case "bucketed":
		err = side.BuildOfflineBucketedFromMPT(context.Background(), chain, &sidecar.UBTBucketBuilderConfig{
			PrefixBits:           uint8(ctx.Uint(ubtPrefixBitsFlag.Name)),
			Workers:              ctx.Int(ubtBucketWorkersFlag.Name),
			MemoryMB:             ctx.Int(ubtBucketMemoryFlag.Name),
			PreimageFetchers:     ctx.Int(ubtPreimageFetchersFlag.Name),
			SkipMissingPreimages: ctx.Bool(ubtSkipMissingPreimagesFlag.Name),
		})
	default:
		return fmt.Errorf("unknown ubt mode %q", ctx.String(ubtModeFlag.Name))
	}
	if err != nil {
		return err
	}
	root, block, hash := side.CurrentInfo()
	log.Info("UBT build complete", "mode", ctx.String(ubtModeFlag.Name), "block", block, "hash", hash, "root", root, "elapsed", time.Since(start))
	return nil
}

func openUBTDatabase(stack *node.Node, path string) (ethdb.Database, error) {
	if !filepath.IsAbs(path) {
		path = stack.ResolvePath(path)
	}
	kvstore, err := pebble.New(path, 256, 64, "eth/db/ubt/", false)
	if err != nil {
		return nil, err
	}
	return rawdb.NewDatabase(kvstore), nil
}
