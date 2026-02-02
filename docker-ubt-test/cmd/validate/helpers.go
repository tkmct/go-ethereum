package main

import (
	"context"
	"fmt"
	"math/big"
	"math/rand"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/rpc"
	"golang.org/x/sync/errgroup"
)

func randomHash(rng *rand.Rand) common.Hash {
	var h common.Hash
	_, _ = rng.Read(h[:])
	return h
}

func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

func waitForBlock(ctx context.Context, client *rpc.Client, target uint64) error {
	for {
		var num hexutil.Uint64
		if err := client.CallContext(ctx, &num, "eth_blockNumber"); err != nil {
			return err
		}
		if uint64(num) >= target {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func compareBytes(a, b []byte) error {
	if len(a) != len(b) {
		return fmt.Errorf("length mismatch: %d != %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			return fmt.Errorf("byte mismatch at %d", i)
		}
	}
	return nil
}

func (v *Validator) compareAccount(ctx context.Context, blockTag rpc.BlockNumberOrHash, addr common.Address, refAcc state.DumpAccount) error {
	blockHash, err := blockHashFromTag(blockTag)
	if err != nil {
		return err
	}
	ubtBalance, err := v.ubt.BalanceAtHash(ctx, addr, blockHash)
	if err != nil {
		return fmt.Errorf("ubt balance read failed: %w", err)
	}
	refBalance, ok := new(big.Int).SetString(refAcc.Balance, 10)
	if !ok {
		return fmt.Errorf("failed to parse reference balance: %s", refAcc.Balance)
	}
	if ubtBalance.Cmp(refBalance) != 0 {
		return fmt.Errorf("balance mismatch: ubt=%s ref=%s", ubtBalance, refBalance)
	}

	ubtNonce, err := v.ubt.NonceAtHash(ctx, addr, blockHash)
	if err != nil {
		return fmt.Errorf("ubt nonce read failed: %w", err)
	}
	if ubtNonce != refAcc.Nonce {
		return fmt.Errorf("nonce mismatch: ubt=%d ref=%d", ubtNonce, refAcc.Nonce)
	}

	ubtCode, err := v.ubt.CodeAtHash(ctx, addr, blockHash)
	if err != nil {
		return fmt.Errorf("ubt code read failed: %w", err)
	}
	if err := compareBytes(ubtCode, refAcc.Code); err != nil {
		return fmt.Errorf("code mismatch: %w", err)
	}

	return nil
}

func (v *Validator) compareStorage(ctx context.Context, blockTag rpc.BlockNumberOrHash, addr common.Address, maxSlots int) error {
	blockHash, err := blockHashFromTag(blockTag)
	if err != nil {
		return err
	}
	startKey := common.Hash{}
	var result StorageRangeResult
	err = v.refClient.CallContext(ctx, &result, "debug_storageRangeAt", blockTag, 0, addr, startKey[:], maxSlots)
	if err != nil {
		if strings.Contains(err.Error(), "preimage") {
			log.Warn("Skipping storage sampling due to missing preimages", "address", addr, "err", err)
			return nil
		}
		return fmt.Errorf("debug_storageRangeAt failed: %w", err)
	}
	for _, entry := range result.Storage {
		if entry.Key == nil {
			continue
		}
		key := *entry.Key

		ubtVal, err := v.ubt.StorageAtHash(ctx, addr, key, blockHash)
		if err != nil {
			return fmt.Errorf("ubt storage read failed: %w", err)
		}
		refVal, err := v.ref.StorageAtHash(ctx, addr, key, blockHash)
		if err != nil {
			return fmt.Errorf("ref storage read failed: %w", err)
		}
		if err := compareBytes(ubtVal, refVal); err != nil {
			return fmt.Errorf("storage mismatch: key=%s: %w", key, err)
		}
	}
	return nil
}

func (v *Validator) compareBlocks(ubt, ref *types.Block) error {
	if ubt.Hash() != ref.Hash() {
		return fmt.Errorf("block hash mismatch: ubt=%s ref=%s", ubt.Hash(), ref.Hash())
	}
	if ubt.NumberU64() != ref.NumberU64() {
		return fmt.Errorf("block number mismatch: ubt=%d ref=%d", ubt.NumberU64(), ref.NumberU64())
	}
	if ubt.ParentHash() != ref.ParentHash() {
		return fmt.Errorf("parent hash mismatch: ubt=%s ref=%s", ubt.ParentHash(), ref.ParentHash())
	}
	if ubt.ReceiptHash() != ref.ReceiptHash() {
		return fmt.Errorf("receipt root mismatch: ubt=%s ref=%s", ubt.ReceiptHash(), ref.ReceiptHash())
	}
	return nil
}

func (v *Validator) compareAccountValuesParallel(ctx context.Context, blockTag rpc.BlockNumberOrHash, accounts map[common.Address]state.DumpAccount, cfg SamplingConfig) error {
	g, gctx := errgroup.WithContext(ctx)
	if cfg.BatchSize > 0 {
		g.SetLimit(cfg.BatchSize)
	}
	for addr, acc := range accounts {
		addr := addr
		acc := acc
		g.Go(func() error {
			if err := v.compareAccount(gctx, blockTag, addr, acc); err != nil {
				return fmt.Errorf("account %s: %w", addr, err)
			}
			if cfg.StorageSlotsPerContract > 0 && v.isContract(acc) {
				return v.compareStorage(gctx, blockTag, addr, cfg.StorageSlotsPerContract)
			}
			return nil
		})
	}
	return g.Wait()
}

func (v *Validator) sampleAddresses(ctx context.Context, blockTag rpc.BlockNumberOrHash, count int) ([]common.Address, error) {
	var dump state.Dump
	err := v.refClient.CallContext(ctx, &dump, "debug_accountRange", blockTag, common.Hash{}, count, false, true, false)
	if err != nil {
		return nil, err
	}
	addrs := make([]common.Address, 0, count)
	for addrStr, acc := range dump.Accounts {
		if acc.Address != nil {
			addrs = append(addrs, *acc.Address)
			continue
		}
		if strings.HasPrefix(addrStr, "pre(") {
			continue
		}
		addrs = append(addrs, common.HexToAddress(addrStr))
		if len(addrs) >= count {
			break
		}
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("no sample addresses available")
	}
	log.Info("Sample addresses collected", "count", len(addrs))
	return addrs, nil
}
