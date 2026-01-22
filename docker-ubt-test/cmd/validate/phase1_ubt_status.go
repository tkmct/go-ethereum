package main

import (
	"context"
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/log"
)

func (v *Validator) Phase1_UBTStatusCheck(ctx context.Context, anchor *BlockAnchor) error {
	if syncing, err := v.ubt.SyncProgress(ctx); err == nil && syncing != nil {
		return fmt.Errorf("UBT node still syncing: current=%d highest=%d", syncing.CurrentBlock, syncing.HighestBlock)
	}
	blockHash := anchor.Hash
	_, err := v.ubt.BalanceAtHash(ctx, common.Address{}, blockHash)
	if err != nil {
		return fmt.Errorf("cannot read state from UBT node at block %d: %w", anchor.Number, err)
	}
	log.Info("Phase 1: UBT state readable (full sync mode)")
	return nil
}
