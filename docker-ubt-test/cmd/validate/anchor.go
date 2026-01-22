package main

import (
	"context"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/rpc"
)

func (v *Validator) getAnchorBlock(ctx context.Context) (*BlockAnchor, error) {
	// Priority: finalized > safe > latest-32
	block, err := v.getBlockSummary(ctx, rpc.FinalizedBlockNumber)
	if err == nil && block != nil {
		return &BlockAnchor{Number: uint64(*block.Number), Hash: block.Hash}, nil
	}
	block, err = v.getBlockSummary(ctx, rpc.SafeBlockNumber)
	if err == nil && block != nil {
		return &BlockAnchor{Number: uint64(*block.Number), Hash: block.Hash}, nil
	}

	latest, err := v.ref.BlockNumber(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get latest block number: %w", err)
	}
	if latest < 32 {
		latest = 32
	}
	safeNum := latest - 32
	bn := new(big.Int).SetUint64(safeNum)
	blockByNum, err := v.ref.BlockByNumber(ctx, bn)
	if err != nil {
		return nil, fmt.Errorf("failed to get block %d: %w", safeNum, err)
	}
	log.Info("Anchor block fallback to latest-32", "number", blockByNum.NumberU64(), "hash", blockByNum.Hash())
	return &BlockAnchor{Number: blockByNum.NumberU64(), Hash: blockByNum.Hash()}, nil
}

func (v *Validator) getBlockSummary(ctx context.Context, blockNum rpc.BlockNumber) (*BlockSummary, error) {
	var block BlockSummary
	if err := v.refClient.CallContext(ctx, &block, "eth_getBlockByNumber", blockNum, false); err != nil {
		return nil, err
	}
	if block.Number == nil || block.Hash == (common.Hash{}) {
		return nil, nil
	}
	return &block, nil
}

func (v *Validator) blockTagFromAnchor(anchor *BlockAnchor) rpc.BlockNumberOrHash {
	return rpc.BlockNumberOrHashWithHash(anchor.Hash, false)
}

func (v *Validator) blockTagFromHash(hash common.Hash) rpc.BlockNumberOrHash {
	return rpc.BlockNumberOrHashWithHash(hash, false)
}

func blockHashFromTag(tag rpc.BlockNumberOrHash) (common.Hash, error) {
	if h, ok := tag.Hash(); ok {
		return h, nil
	}
	return common.Hash{}, fmt.Errorf("block tag does not contain hash")
}

func blockNumberToHex(num uint64) hexutil.Uint64 {
	return hexutil.Uint64(num)
}
