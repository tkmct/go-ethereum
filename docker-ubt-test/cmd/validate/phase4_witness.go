package main

import (
	"context"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/stateless"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/log"
)

func (v *Validator) Phase4_WitnessValidation(ctx context.Context, anchor *BlockAnchor, blocks int) error {
	if blocks <= 0 {
		return nil
	}
	chainConfig, err := v.requireChainConfig()
	if err != nil {
		return err
	}

	start := anchor.Number
	if uint64(blocks) <= anchor.Number {
		start = anchor.Number - uint64(blocks) + 1
	} else {
		start = 0
	}

	for bn := start; bn <= anchor.Number; bn++ {
		block, err := v.ubt.BlockByNumber(ctx, new(big.Int).SetUint64(bn))
		if err != nil {
			return fmt.Errorf("failed to fetch block %d: %w", bn, err)
		}
		blockTag := v.blockTagFromHash(block.Hash())

		var ext stateless.ExtUBTWitness
		if err := v.ubtClient.CallContext(ctx, &ext, "debug_executionWitnessUBT", blockTag); err != nil {
			return fmt.Errorf("debug_executionWitnessUBT failed for block %d: %w", bn, err)
		}
		witness, err := stateless.NewWitnessFromUBTWitness(&ext)
		if err != nil {
			return fmt.Errorf("failed to parse witness for block %d: %w", bn, err)
		}
		if witness == nil {
			return fmt.Errorf("empty witness for block %d", bn)
		}

		stateRoot, receiptRoot, err := core.ExecuteStatelessWithPathDB(chainConfig, vm.Config{}, block, witness, true)
		if err != nil {
			return fmt.Errorf("ExecuteStateless failed for block %d: %w", bn, err)
		}
		if receiptRoot != block.ReceiptHash() {
			return fmt.Errorf("receipt root mismatch for block %d: computed=%s header=%s", bn, receiptRoot, block.ReceiptHash())
		}

		var proof UBTProofResult
		if err := v.ubtClient.CallContext(ctx, &proof, "debug_getUBTProof", common.Address{}, []string{}, blockTag); err != nil {
			return fmt.Errorf("debug_getUBTProof failed for block %d: %w", bn, err)
		}
		if proof.UbtRoot != stateRoot {
			return fmt.Errorf("UBT root mismatch for block %d: computed=%s proof=%s", bn, stateRoot, proof.UbtRoot)
		}
		log.Info("Phase 4 progress", "block", bn)
	}
	log.Info("Phase 4: Witness validation complete", "blocks", blocks)
	return nil
}
