package main

import (
	"context"
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/rpc"
)

func (v *Validator) Phase0_PreconditionCheck(ctx context.Context, anchor *BlockAnchor) error {
	var clientVersion string
	if err := v.ubtClient.CallContext(ctx, &clientVersion, "web3_clientVersion"); err != nil {
		return fmt.Errorf("UBT node not reachable: %w", err)
	}
	if err := v.refClient.CallContext(ctx, &clientVersion, "web3_clientVersion"); err != nil {
		return fmt.Errorf("reference node not reachable: %w", err)
	}

	ubtBlock, err := v.ubt.BlockNumber(ctx)
	if err != nil {
		return fmt.Errorf("failed to get UBT block number: %w", err)
	}
	refBlock, err := v.ref.BlockNumber(ctx)
	if err != nil {
		return fmt.Errorf("failed to get reference block number: %w", err)
	}
	if abs(int64(ubtBlock)-int64(refBlock)) > 100 {
		return fmt.Errorf("block height mismatch: UBT=%d Ref=%d", ubtBlock, refBlock)
	}

	blockTag := v.blockTagFromAnchor(anchor)
	v.logAnchor(anchor)

	var accounts state.Dump
	if err := v.refClient.CallContext(ctx, &accounts, "debug_accountRange", blockTag, common.Hash{}, 1, false, true, false); err != nil {
		return fmt.Errorf("reference node does not support debug_accountRange: %w", err)
	}

	if err := v.checkPreimageSupport(ctx, v.ubtClient, "UBT", blockTag); err != nil {
		log.Warn("UBT node preimage check failed", "err", err)
	}
	if err := v.checkPreimageSupport(ctx, v.refClient, "Reference", blockTag); err != nil {
		log.Warn("Reference node preimage check failed - storage sampling may not work", "err", err)
	}

	if err := v.checkSidecarUBTRoot(ctx, anchor); err != nil {
		return err
	}

	log.Info("Phase 0: Precondition check passed", "anchor", anchor.Number)
	return nil
}

func (v *Validator) checkPreimageSupport(ctx context.Context, client *rpc.Client, name string, blockTag rpc.BlockNumberOrHash) error {
	var accounts state.Dump
	if err := client.CallContext(ctx, &accounts, "debug_accountRange", blockTag, common.Hash{}, 1, false, true, false); err != nil {
		return fmt.Errorf("%s node: cannot get address hash for preimage test: %w", name, err)
	}
	for _, acc := range accounts.Accounts {
		if len(acc.AddressHash) == 0 {
			continue
		}
		var preimage hexutil.Bytes
		hash := common.BytesToHash(acc.AddressHash)
		if err := client.CallContext(ctx, &preimage, "debug_preimage", hash); err != nil {
			return fmt.Errorf("%s node: debug_preimage failed for address hash: %w", name, err)
		}
		log.Info("Preimage support confirmed", "node", name)
		return nil
	}
	log.Warn("Could not verify preimage support - no address hash available", "node", name)
	return nil
}

func (v *Validator) checkSidecarUBTRoot(ctx context.Context, anchor *BlockAnchor) error {
	blockTag := v.blockTagFromAnchor(anchor)
	var proof UBTProofResult
	if err := v.ubtClient.CallContext(ctx, &proof, "debug_getUBTProof", common.Address{}, []string{}, blockTag); err != nil {
		return fmt.Errorf("debug_getUBTProof failed (sidecar not ready?): %w", err)
	}
	if proof.UbtRoot == (common.Hash{}) {
		return fmt.Errorf("UBT root is empty (sidecar not ready?)")
	}
	block, err := v.ubt.BlockByHash(ctx, anchor.Hash)
	if err == nil && block != nil {
		if proof.StateRoot != block.Root() {
			return fmt.Errorf("state root mismatch for anchor: proof=%s headerRoot=%s", proof.StateRoot, block.Root())
		}
	}
	return nil
}
