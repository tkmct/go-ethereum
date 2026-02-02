package main

import (
	"context"
	"fmt"
	"math/rand"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"
)

func (v *Validator) Phase2_ValueValidation(ctx context.Context, anchor *BlockAnchor, cfg SamplingConfig) error {
	v.logAnchor(anchor)
	blockTag := v.blockTagFromAnchor(anchor)

	rng := rand.New(rand.NewSource(cfg.RandomSeed))
	sampled := 0

	for sampled < cfg.AccountCount {
		startHash := randomHash(rng)
		var accounts state.Dump
		err := v.refClient.CallContext(ctx, &accounts, "debug_accountRange", blockTag, startHash, cfg.BatchSize, false, true, false)
		if err != nil {
			return fmt.Errorf("debug_accountRange failed: %w", err)
		}

		batch := make(map[common.Address]state.DumpAccount)
		for addrStr, acc := range accounts.Accounts {
			if acc.Address != nil {
				batch[*acc.Address] = acc
				continue
			}
			if strings.HasPrefix(addrStr, "pre(") {
				continue
			}
			batch[common.HexToAddress(addrStr)] = acc
		}

		if err := v.compareAccountValuesParallel(ctx, blockTag, batch, cfg); err != nil {
			return err
		}

		sampled += len(batch)
		if sampled%1000 == 0 || sampled >= cfg.AccountCount {
			log.Info("Phase 2 progress", "sampled", sampled, "target", cfg.AccountCount)
		}
	}

	log.Info("Phase 2: Value validation passed", "sampled", sampled)
	return nil
}

func (v *Validator) isContract(acc state.DumpAccount) bool {
	if len(acc.CodeHash) == 0 {
		return false
	}
	return common.BytesToHash(acc.CodeHash) != types.EmptyCodeHash
}
