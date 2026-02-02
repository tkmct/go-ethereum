package main

import (
	"context"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/rpc"
)

func (v *Validator) Phase5_RPCConsistency(ctx context.Context, anchor *BlockAnchor) error {
	v.logAnchor(anchor)
	blockTag := v.blockTagFromAnchor(anchor)
	blockHash := anchor.Hash

	block, err := v.ref.BlockByNumber(ctx, new(big.Int).SetUint64(anchor.Number))
	if err != nil {
		return fmt.Errorf("failed to fetch anchor block: %w", err)
	}

	var sampleTx *types.Transaction
	if len(block.Transactions()) > 0 {
		sampleTx = block.Transactions()[0]
	}

	tests := []RPCTest{
		{
			Name: "debug_getUBTState",
			Run: func(ctx context.Context, anchor *BlockAnchor) error {
				addr := common.Address{}
				var state UBTStateResult
				if err := v.ubtClient.CallContext(ctx, &state, "debug_getUBTState", addr, []string{}, blockTag); err != nil {
					return err
				}
				if state.UbtRoot == (common.Hash{}) {
					return fmt.Errorf("ubt root is empty")
				}
				if state.Balance == nil {
					return fmt.Errorf("missing balance in UBT state")
				}
				refBal, err := v.ref.BalanceAtHash(ctx, addr, blockHash)
				if err != nil {
					return err
				}
				if (*big.Int)(state.Balance).Cmp(refBal) != 0 {
					return fmt.Errorf("balance mismatch: ubt=%s ref=%s", (*big.Int)(state.Balance), refBal)
				}
				refNonce, err := v.ref.NonceAtHash(ctx, addr, blockHash)
				if err != nil {
					return err
				}
				if uint64(state.Nonce) != refNonce {
					return fmt.Errorf("nonce mismatch: ubt=%d ref=%d", state.Nonce, refNonce)
				}
				return nil
			},
		},
		{
			Name: "eth_getBalance",
			Run: func(ctx context.Context, anchor *BlockAnchor) error {
				addr := common.Address{}
				ubtBal, err := v.ubt.BalanceAtHash(ctx, addr, blockHash)
				if err != nil {
					return err
				}
				refBal, err := v.ref.BalanceAtHash(ctx, addr, blockHash)
				if err != nil {
					return err
				}
				if ubtBal.Cmp(refBal) != 0 {
					return fmt.Errorf("balance mismatch: ubt=%s ref=%s", ubtBal, refBal)
				}
				return nil
			},
		},
		{
			Name: "eth_getCode",
			Run: func(ctx context.Context, anchor *BlockAnchor) error {
				addr := common.Address{}
				ubtCode, err := v.ubt.CodeAtHash(ctx, addr, blockHash)
				if err != nil {
					return err
				}
				refCode, err := v.ref.CodeAtHash(ctx, addr, blockHash)
				if err != nil {
					return err
				}
				return compareBytes(ubtCode, refCode)
			},
		},
		{
			Name: "eth_getTransactionCount",
			Run: func(ctx context.Context, anchor *BlockAnchor) error {
				addr := common.Address{}
				ubtNonce, err := v.ubt.NonceAtHash(ctx, addr, blockHash)
				if err != nil {
					return err
				}
				refNonce, err := v.ref.NonceAtHash(ctx, addr, blockHash)
				if err != nil {
					return err
				}
				if ubtNonce != refNonce {
					return fmt.Errorf("nonce mismatch: ubt=%d ref=%d", ubtNonce, refNonce)
				}
				return nil
			},
		},
		{
			Name: "eth_getStorageAt",
			Run: func(ctx context.Context, anchor *BlockAnchor) error {
				addr := common.Address{}
				key := common.Hash{}
				ubtVal, err := v.ubt.StorageAtHash(ctx, addr, key, blockHash)
				if err != nil {
					return err
				}
				refVal, err := v.ref.StorageAtHash(ctx, addr, key, blockHash)
				if err != nil {
					return err
				}
				return compareBytes(ubtVal, refVal)
			},
		},
		{
			Name: "eth_getBlockByNumber",
			Run: func(ctx context.Context, anchor *BlockAnchor) error {
				ubtBlock, err := v.ubt.BlockByNumber(ctx, new(big.Int).SetUint64(anchor.Number))
				if err != nil {
					return err
				}
				refBlock, err := v.ref.BlockByNumber(ctx, new(big.Int).SetUint64(anchor.Number))
				if err != nil {
					return err
				}
				return v.compareBlocks(ubtBlock, refBlock)
			},
		},
		{
			Name: "eth_getBlockByHash",
			Run: func(ctx context.Context, anchor *BlockAnchor) error {
				ubtBlock, err := v.ubt.BlockByHash(ctx, blockHash)
				if err != nil {
					return err
				}
				refBlock, err := v.ref.BlockByHash(ctx, blockHash)
				if err != nil {
					return err
				}
				return v.compareBlocks(ubtBlock, refBlock)
			},
		},
		{
			Name: "eth_getTransactionByHash",
			Run: func(ctx context.Context, anchor *BlockAnchor) error {
				if sampleTx == nil {
					log.Warn("No transactions in anchor block; skipping")
					return nil
				}
				ubtTx, _, err := v.ubt.TransactionByHash(ctx, sampleTx.Hash())
				if err != nil {
					return err
				}
				refTx, _, err := v.ref.TransactionByHash(ctx, sampleTx.Hash())
				if err != nil {
					return err
				}
				if ubtTx.Hash() != refTx.Hash() {
					return fmt.Errorf("tx hash mismatch")
				}
				return nil
			},
		},
		{
			Name: "eth_getTransactionReceipt",
			Run: func(ctx context.Context, anchor *BlockAnchor) error {
				if sampleTx == nil {
					log.Warn("No transactions in anchor block; skipping")
					return nil
				}
				ubtReceipt, err := v.ubt.TransactionReceipt(ctx, sampleTx.Hash())
				if err != nil {
					return err
				}
				refReceipt, err := v.ref.TransactionReceipt(ctx, sampleTx.Hash())
				if err != nil {
					return err
				}
				if ubtReceipt.Status != refReceipt.Status {
					return fmt.Errorf("receipt status mismatch")
				}
				return nil
			},
		},
		{
			Name: "eth_call",
			Run: func(ctx context.Context, anchor *BlockAnchor) error {
				msg := map[string]interface{}{
					"to":   common.Address{}.Hex(),
					"data": hexutil.Bytes{},
				}
				var ubtRes hexutil.Bytes
				if err := v.ubtClient.CallContext(ctx, &ubtRes, "eth_call", msg, blockTag); err != nil {
					return err
				}
				var refRes hexutil.Bytes
				if err := v.refClient.CallContext(ctx, &refRes, "eth_call", msg, blockTag); err != nil {
					return err
				}
				return compareBytes(ubtRes, refRes)
			},
		},
	}

	for _, test := range tests {
		if err := test.Run(ctx, anchor); err != nil {
			return fmt.Errorf("%s failed: %w", test.Name, err)
		}
		log.Info("Phase 5: RPC check passed", "name", test.Name)
	}

	if err := v.validateUBTProofs(ctx, blockTag); err != nil {
		return err
	}

	log.Info("Phase 5: RPC consistency validation passed")
	return nil
}

func (v *Validator) validateUBTProofs(ctx context.Context, blockTag rpc.BlockNumberOrHash) error {
	blockHash, err := blockHashFromTag(blockTag)
	if err != nil {
		return err
	}
	addrs, err := v.sampleAddresses(ctx, blockTag, 10)
	if err != nil {
		return err
	}
	for _, addr := range addrs {
		var proof UBTProofResult
		if err := v.ubtClient.CallContext(ctx, &proof, "debug_getUBTProof", addr, []string{}, blockTag); err != nil {
			return fmt.Errorf("failed to get UBT proof for %s: %w", addr, err)
		}
		refBalance, err := v.ref.BalanceAtHash(ctx, addr, blockHash)
		if err != nil {
			return fmt.Errorf("failed to get reference balance for %s: %w", addr, err)
		}
		if proof.Balance == nil || (*big.Int)(proof.Balance).Cmp(refBalance) != 0 {
			return fmt.Errorf("balance mismatch in proof for %s", addr)
		}
		if len(proof.AccountProof) == 0 {
			return fmt.Errorf("account proof is empty for %s", addr)
		}
	}
	log.Info("Phase 5: UBT proof validation passed", "samples", len(addrs))
	return nil
}
