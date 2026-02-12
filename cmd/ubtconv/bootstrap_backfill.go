// Copyright 2024 The go-ethereum Authors
// This file is part of go-ethereum.
//
// go-ethereum is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// go-ethereum is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with go-ethereum. If not, see <http://www.gnu.org/licenses/>.

package main

import (
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/holiman/uint256"
)

const accountRangePageSize = 256

// bootstrapBackfillDirect imports genesis state into UBT, then catches up existing outbox events.
func (r *Runner) bootstrapBackfillDirect() error {
	log.Info("Bootstrap mode: backfill-direct, importing genesis state")
	if err := r.importGenesisState(); err != nil {
		return err
	}

	// Probe seq=0 because ubt_latestSeq is ambiguous when outbox is empty.
	first, err := r.consumer.reader.ReadEvent(0)
	if err != nil {
		log.Warn("Backfill bootstrap skipped outbox catch-up due to seq=0 probe error; consumer loop will retry", "err", err)
		return nil
	}
	if first == nil {
		log.Info("Backfill bootstrap complete (no outbox events yet), waiting for seq=0")
		return nil
	}

	latestSeq, err := r.consumer.reader.LatestSeq()
	if err != nil {
		log.Warn("Backfill bootstrap skipped outbox catch-up due to latest-seq error; consumer loop will retry", "err", err)
		return nil
	}

	for {
		r.consumer.mu.Lock()
		targetSeq := r.consumer.processedSeq + 1
		r.consumer.mu.Unlock()
		if targetSeq > latestSeq {
			break
		}
		if err := r.consumer.ConsumeNext(); err != nil {
			log.Warn("Backfill bootstrap partial catch-up halted; consumer loop will continue with retry", "seq", targetSeq, "err", err)
			return nil
		}
	}

	// Flush any uncommitted tail so restart resumes from a durable checkpoint.
	r.consumer.mu.Lock()
	defer r.consumer.mu.Unlock()
	if r.consumer.uncommittedBlocks > 0 {
		if err := r.consumer.commit(); err != nil {
			return fmt.Errorf("backfill final commit: %w", err)
		}
	}
	r.consumer.hasState = true
	log.Info("Backfill bootstrap catch-up complete", "appliedSeq", r.consumer.state.AppliedSeq, "appliedBlock", r.consumer.state.AppliedBlock)
	return nil
}

func (r *Runner) importGenesisState() error {
	start := hexutil.Bytes{}
	prevCursor := ""
	totalAccounts := 0

	for {
		block0 := rpc.BlockNumberOrHashWithNumber(rpc.BlockNumber(0))
		dump, err := r.consumer.reader.ReadAccountRange(block0, start, accountRangePageSize, false, false, false)
		if err != nil {
			return fmt.Errorf("read genesis account range: %w", err)
		}
		if dump == nil {
			return fmt.Errorf("empty response from debug_accountRange")
		}
		applied, err := r.applyDumpPage(dump)
		if err != nil {
			return err
		}
		totalAccounts += applied

		if len(dump.Next) == 0 {
			break
		}
		cursor := hexutil.Encode(dump.Next)
		if cursor == prevCursor {
			return fmt.Errorf("non-advancing account-range cursor: %s", cursor)
		}
		prevCursor = cursor
		start = dump.Next
	}

	if err := r.consumer.applier.CommitAt(0); err != nil {
		return fmt.Errorf("commit genesis UBT state: %w", err)
	}
	root := r.consumer.applier.Root()

	r.consumer.mu.Lock()
	r.consumer.state.AppliedRoot = root
	r.consumer.state.AppliedBlock = 0
	r.consumer.pendingRoot = root
	r.consumer.pendingBlock = 0
	r.consumer.pendingBlockHash = common.Hash{}
	r.consumer.pendingParentHash = common.Hash{}
	r.consumer.mu.Unlock()

	rawdb.WriteUBTBlockRoot(r.consumer.db, 0, root)
	log.Info("Genesis state imported into UBT", "accounts", totalAccounts, "root", root)
	return nil
}

func (r *Runner) applyDumpPage(dump *rpcStateDump) (int, error) {
	applied := 0
	for addrText, account := range dump.Accounts {
		if !common.IsHexAddress(addrText) {
			// accountRange may return entries without address preimages; skip those.
			continue
		}
		addr := common.HexToAddress(addrText)

		balance, ok := new(big.Int).SetString(account.Balance, 10)
		if !ok {
			return applied, fmt.Errorf("invalid balance for %s: %q", addr, account.Balance)
		}
		uBal, overflow := uint256.FromBig(balance)
		if overflow {
			return applied, fmt.Errorf("balance overflows uint256 for %s", addr)
		}

		codeHash := common.BytesToHash(account.CodeHash)
		if len(account.Code) > 0 && codeHash == (common.Hash{}) {
			codeHash = crypto.Keccak256Hash(account.Code)
		}
		if len(account.Code) == 0 && codeHash == (common.Hash{}) {
			codeHash = types.EmptyCodeHash
		}
		stateAccount := &types.StateAccount{
			Nonce:    account.Nonce,
			Balance:  uBal,
			Root:     types.EmptyRootHash,
			CodeHash: codeHash.Bytes(),
		}
		if err := r.consumer.applier.trie.UpdateAccount(addr, stateAccount, len(account.Code)); err != nil {
			return applied, fmt.Errorf("update genesis account %s: %w", addr, err)
		}
		if len(account.Code) > 0 {
			if err := r.consumer.applier.trie.UpdateContractCode(addr, codeHash, account.Code); err != nil {
				return applied, fmt.Errorf("update genesis code %s: %w", addr, err)
			}
		}
		for slotText, valueHex := range account.Storage {
			slot := common.HexToHash(slotText)
			value, err := decodeStorageHex(valueHex)
			if err != nil {
				return applied, fmt.Errorf("decode genesis storage %s/%s: %w", addr, slotText, err)
			}
			if err := r.consumer.applier.trie.UpdateStorage(addr, slot.Bytes(), value); err != nil {
				return applied, fmt.Errorf("update genesis storage %s/%s: %w", addr, slotText, err)
			}
		}
		applied++
	}
	return applied, nil
}

func decodeStorageHex(v string) ([]byte, error) {
	value := strings.TrimPrefix(v, "0x")
	if value == "" {
		return common.Hash{}.Bytes(), nil
	}
	if len(value)%2 == 1 {
		value = "0" + value
	}
	decoded, err := hex.DecodeString(value)
	if err != nil {
		return nil, err
	}
	padded := common.BytesToHash(decoded)
	return padded.Bytes(), nil
}
