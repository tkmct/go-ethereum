package sidecar

import (
	"context"
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
)

type UBTPreflightConfig struct {
	SampleAccounts uint64
	SampleStorage  uint64
	VerifyState    bool
	Full           bool
}

type UBTPreflightResult struct {
	HeadBlock               uint64
	HeadRoot                common.Hash
	SnapshotCompleted       bool
	SampledAccounts         uint64
	SampledSlots            uint64
	AccountsWithCode        uint64
	AccountsWithStorage     uint64
	TotalAccounts           uint64
	MissingAccountPreimages uint64
	MissingStoragePreimages uint64
}

var defaultUBTPreflightConfig = UBTPreflightConfig{
	SampleAccounts: 1000,
	SampleStorage:  4,
	VerifyState:    false,
}

func (sc *UBTSidecar) PreflightOfflineBuild(ctx context.Context, chain ChainContext, cfg *UBTPreflightConfig) (*UBTPreflightResult, error) {
	head := chain.HeadBlock()
	if head == nil {
		return nil, fmt.Errorf("ubt preflight: head block not available")
	}
	conf := defaultUBTPreflightConfig
	if cfg != nil {
		if cfg.SampleAccounts != 0 {
			conf.SampleAccounts = cfg.SampleAccounts
		}
		if cfg.SampleStorage != 0 {
			conf.SampleStorage = cfg.SampleStorage
		}
		conf.VerifyState = cfg.VerifyState
		conf.Full = cfg.Full
	}
	root := chain.HeadRoot()
	result := &UBTPreflightResult{
		HeadBlock:         head.Number.Uint64(),
		HeadRoot:          root,
		SnapshotCompleted: sc.mptTrieDB.SnapshotCompleted(),
	}
	if !result.SnapshotCompleted {
		return result, fmt.Errorf("ubt preflight: mpt snapshot not completed")
	}
	if conf.VerifyState {
		if err := sc.mptTrieDB.VerifyState(root); err != nil {
			return result, fmt.Errorf("ubt preflight: verify state: %w", err)
		}
	}
	accIt, err := sc.mptTrieDB.AccountIterator(root, common.Hash{})
	if err != nil {
		return result, fmt.Errorf("ubt preflight: account iterator: %w", err)
	}
	defer accIt.Release()

	for accIt.Next() && (conf.Full || result.SampledAccounts < conf.SampleAccounts) {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		default:
		}
		accountHash := accIt.Hash()
		accountData := accIt.Account()
		result.TotalAccounts++
		preimage := rawdb.ReadPreimage(sc.chainDB, accountHash)
		if len(preimage) == 0 {
			result.MissingAccountPreimages++
			if conf.Full {
				continue
			}
			return result, fmt.Errorf("ubt preflight: missing account preimage %x", accountHash)
		}
		acct, err := types.FullAccount(accountData)
		if err != nil {
			return result, fmt.Errorf("ubt preflight: decode account %x: %w", accountHash, err)
		}
		if codeHash := common.BytesToHash(acct.CodeHash); codeHash != types.EmptyCodeHash {
			if len(rawdb.ReadCode(sc.chainDB, codeHash)) == 0 {
				return result, fmt.Errorf("ubt preflight: missing code %x", codeHash)
			}
			result.AccountsWithCode++
		}
		if acct.Root != types.EmptyRootHash {
			result.AccountsWithStorage++
			storIt, err := sc.mptTrieDB.StorageIterator(root, accountHash, common.Hash{})
			if err != nil {
				return result, fmt.Errorf("ubt preflight: storage iterator %x: %w", accountHash, err)
			}
			var sampled uint64
			for storIt.Next() && sampled < conf.SampleStorage {
				slotHash := storIt.Hash()
				if len(rawdb.ReadPreimage(sc.chainDB, slotHash)) == 0 {
					result.MissingStoragePreimages++
					if !conf.Full {
						storIt.Release()
						return result, fmt.Errorf("ubt preflight: missing storage preimage %x", slotHash)
					}
				} else {
					sampled++
					result.SampledSlots++
				}
			}
			err = storIt.Error()
			storIt.Release()
			if err != nil {
				return result, fmt.Errorf("ubt preflight: iterate storage %x: %w", accountHash, err)
			}
		}
		result.SampledAccounts++
	}
	if err := accIt.Error(); err != nil {
		return result, fmt.Errorf("ubt preflight: iterate accounts: %w", err)
	}
	return result, nil
}
