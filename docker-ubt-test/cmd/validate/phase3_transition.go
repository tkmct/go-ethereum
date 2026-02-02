package main

import (
	"context"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/rpc"
)

func (v *Validator) Phase3_TransitionValidation(ctx context.Context, anchor *BlockAnchor, blocks int, cfg SamplingConfig) error {
	if blocks <= 0 {
		return nil
	}
	v.logAnchor(anchor)

	start := anchor.Number
	if uint64(blocks) < start {
		start = anchor.Number - uint64(blocks) + 1
	}

	for bn := start; bn <= anchor.Number; bn++ {
		block, err := v.ref.BlockByNumber(ctx, new(big.Int).SetUint64(bn))
		if err != nil {
			return fmt.Errorf("failed to fetch block %d: %w", bn, err)
		}
		blockTag := v.blockTagFromHash(block.Hash())

		var addrs []common.Address
		var rpcErr error
		rpcErr = v.refClient.CallContext(ctx, &addrs, "debug_getModifiedAccountsByNumber", bn)
		if rpcErr != nil {
			log.Warn("debug_getModifiedAccountsByNumber failed, falling back", "block", bn, "err", rpcErr)
			addrs, rpcErr = v.extractBlockModifiedAddresses(block)
		}
		if rpcErr != nil {
			return fmt.Errorf("failed to derive modified accounts for block %d: %w", bn, rpcErr)
		}

		for _, addr := range addrs {
			if err := v.compareAccountValues(ctx, blockTag, addr); err != nil {
				return fmt.Errorf("block %d addr %s: %w", bn, addr, err)
			}
		}
		log.Info("Phase 3 progress", "block", bn, "addresses", len(addrs))
	}

	log.Info("Phase 3: Transition validation passed", "blocks", blocks)
	return nil
}

func (v *Validator) compareAccountValues(ctx context.Context, blockTag rpc.BlockNumberOrHash, addr common.Address) error {
	blockHash, err := blockHashFromTag(blockTag)
	if err != nil {
		return err
	}
	ubtBalance, err := v.ubt.BalanceAtHash(ctx, addr, blockHash)
	if err != nil {
		return fmt.Errorf("ubt balance read failed: %w", err)
	}
	refBalance, err := v.ref.BalanceAtHash(ctx, addr, blockHash)
	if err != nil {
		return fmt.Errorf("ref balance read failed: %w", err)
	}
	if ubtBalance.Cmp(refBalance) != 0 {
		return fmt.Errorf("balance mismatch: ubt=%s ref=%s", ubtBalance, refBalance)
	}

	ubtNonce, err := v.ubt.NonceAtHash(ctx, addr, blockHash)
	if err != nil {
		return fmt.Errorf("ubt nonce read failed: %w", err)
	}
	refNonce, err := v.ref.NonceAtHash(ctx, addr, blockHash)
	if err != nil {
		return fmt.Errorf("ref nonce read failed: %w", err)
	}
	if ubtNonce != refNonce {
		return fmt.Errorf("nonce mismatch: ubt=%d ref=%d", ubtNonce, refNonce)
	}

	ubtCode, err := v.ubt.CodeAtHash(ctx, addr, blockHash)
	if err != nil {
		return fmt.Errorf("ubt code read failed: %w", err)
	}
	refCode, err := v.ref.CodeAtHash(ctx, addr, blockHash)
	if err != nil {
		return fmt.Errorf("ref code read failed: %w", err)
	}
	if err := compareBytes(ubtCode, refCode); err != nil {
		return fmt.Errorf("code mismatch: %w", err)
	}

	return nil
}

func (v *Validator) extractBlockModifiedAddresses(block *types.Block) ([]common.Address, error) {
	chainConfig, err := v.requireChainConfig()
	if err != nil {
		return nil, err
	}
	signer := types.MakeSigner(chainConfig, block.Number(), block.Time())

	addrSet := make(map[common.Address]struct{})
	addrSet[block.Coinbase()] = struct{}{}

	for _, tx := range block.Transactions() {
		from, err := types.Sender(signer, tx)
		if err == nil {
			addrSet[from] = struct{}{}
		}
		if tx.To() != nil {
			addrSet[*tx.To()] = struct{}{}
		}
		for _, entry := range tx.AccessList() {
			addrSet[entry.Address] = struct{}{}
		}
	}

	if withdrawals := block.Withdrawals(); withdrawals != nil {
		for _, w := range withdrawals {
			addrSet[w.Address] = struct{}{}
		}
	}

	for _, addr := range chainConfig.ActiveSystemContracts(block.Time()) {
		addrSet[addr] = struct{}{}
	}

	addrs := make([]common.Address, 0, len(addrSet))
	for addr := range addrSet {
		addrs = append(addrs, addr)
	}
	return addrs, nil
}
