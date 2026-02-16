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
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/ubtemit"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/ethereum/go-ethereum/trie/bintrie"
)

// Validator cross-checks UBT state against the canonical MPT state via geth RPC.
type Validator struct {
	reader  *OutboxReader // reuse existing RPC connection to geth
	timeout time.Duration
}

var errHistoricalStateUnavailable = errors.New("historical state unavailable")

// NewValidator creates a new Validator using the outbox reader's RPC connection.
func NewValidator(reader *OutboxReader) *Validator {
	return &Validator{
		reader:  reader,
		timeout: 30 * time.Second,
	}
}

// maxValidationSample limits the number of accounts validated per block.
const maxValidationSample = 10
const strictValidationBatchSize = 256

// ValidateBlock cross-checks a sample of accounts from the UBT trie against the
// canonical MPT state at the given block number. The addresses slice contains
// accounts touched in recent diffs; a sample is drawn from them.
func (v *Validator) ValidateBlock(trie *bintrie.BinaryTrie, blockNumber uint64, ubtRoot common.Hash, addresses []common.Address) error {
	if len(addresses) == 0 {
		log.Debug("UBT validation: no accounts to validate", "block", blockNumber)
		return nil
	}

	// Sample at most maxValidationSample addresses
	sample := addresses
	if len(sample) > maxValidationSample {
		sample = sample[:maxValidationSample]
	}

	client, err := v.reader.getClient()
	if err != nil {
		return fmt.Errorf("get RPC client for validation: %w", err)
	}

	blockNr := rpc.BlockNumberOrHashWithNumber(rpc.BlockNumber(blockNumber))
	var mismatches int

	for _, addr := range sample {
		if err := v.validateAccount(client, addr, blockNr, trie); err != nil {
			if isHistoricalStateUnavailableRPC(err) {
				log.Debug("UBT validation skipped: historical state unavailable", "block", blockNumber)
				return nil
			}
			log.Warn("UBT validation mismatch", "addr", addr, "block", blockNumber, "err", err)
			mismatches++
		}
	}

	if mismatches > 0 {
		validationMismatches.Inc(int64(mismatches))
		return fmt.Errorf("UBT validation found %d mismatches at block %d (root %s)", mismatches, blockNumber, ubtRoot)
	}

	validationChecksTotal.Inc(1)
	log.Info("UBT validation passed", "block", blockNumber, "root", ubtRoot, "accounts", len(sample))
	return nil
}

// ValidateStrict validates ALL accounts, storage, and code in the diff against MPT via RPC.
func (v *Validator) ValidateStrict(trie *bintrie.BinaryTrie, blockNumber uint64, diff *ubtemit.QueuedDiffV1) error {
	client, err := v.reader.getClient()
	if err != nil {
		return fmt.Errorf("get RPC client for strict validation: %w", err)
	}

	blockNr := rpc.BlockNumberOrHashWithNumber(rpc.BlockNumber(blockNumber))
	var mismatches int

	if err := v.validateAccountsStrictBatch(client, trie, blockNr, blockNumber, diff.Accounts, &mismatches); err != nil {
		if isHistoricalStateUnavailableRPC(err) {
			return fmt.Errorf("%w at block %d", errHistoricalStateUnavailable, blockNumber)
		}
		return err
	}
	if err := v.validateStorageStrictBatch(client, trie, blockNr, blockNumber, diff.Storage, &mismatches); err != nil {
		if isHistoricalStateUnavailableRPC(err) {
			return fmt.Errorf("%w at block %d", errHistoricalStateUnavailable, blockNumber)
		}
		return err
	}
	if err := v.validateCodeStrictBatch(client, trie, blockNr, blockNumber, diff.Codes, &mismatches); err != nil {
		if isHistoricalStateUnavailableRPC(err) {
			return fmt.Errorf("%w at block %d", errHistoricalStateUnavailable, blockNumber)
		}
		return err
	}

	if mismatches > 0 {
		validationMismatches.Inc(int64(mismatches))
		return fmt.Errorf("strict validation found %d mismatches at block %d", mismatches, blockNumber)
	}

	validationChecksTotal.Inc(1)
	log.Debug("Strict validation passed", "block", blockNumber, "accounts", len(diff.Accounts), "storage", len(diff.Storage), "codes", len(diff.Codes))
	return nil
}

func (v *Validator) validateAccountsStrictBatch(client *rpc.Client, trie *bintrie.BinaryTrie, blockNr rpc.BlockNumberOrHash, blockNumber uint64, accounts []ubtemit.AccountEntry, mismatches *int) error {
	addrs := make([]common.Address, 0, len(accounts))
	for _, acct := range accounts {
		if acct.Alive {
			addrs = append(addrs, acct.Address)
		}
	}
	for i := 0; i < len(addrs); i += strictValidationBatchSize {
		end := i + strictValidationBatchSize
		if end > len(addrs) {
			end = len(addrs)
		}
		chunk := addrs[i:end]

		balances := make([]hexutil.Big, len(chunk))
		balanceReq := make([]rpc.BatchElem, len(chunk))
		for j, addr := range chunk {
			balanceReq[j] = rpc.BatchElem{
				Method: "eth_getBalance",
				Args:   []any{addr, blockNr},
				Result: &balances[j],
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), v.timeout)
		err := client.BatchCallContext(ctx, balanceReq)
		cancel()
		if err != nil {
			return fmt.Errorf("batch eth_getBalance: %w", err)
		}

		nonces := make([]hexutil.Uint64, len(chunk))
		nonceReq := make([]rpc.BatchElem, len(chunk))
		for j, addr := range chunk {
			nonceReq[j] = rpc.BatchElem{
				Method: "eth_getTransactionCount",
				Args:   []any{addr, blockNr},
				Result: &nonces[j],
			}
		}
		ctx, cancel = context.WithTimeout(context.Background(), v.timeout)
		err = client.BatchCallContext(ctx, nonceReq)
		cancel()
		if err != nil {
			return fmt.Errorf("batch eth_getTransactionCount: %w", err)
		}

		for j, addr := range chunk {
			if balanceReq[j].Error != nil {
				return balanceReq[j].Error
			}
			if nonceReq[j].Error != nil {
				return nonceReq[j].Error
			}
			ubtAcct, err := trie.GetAccount(addr)
			if err != nil {
				log.Warn("Strict validation: account read failed", "addr", addr, "block", blockNumber, "err", err)
				*mismatches = *mismatches + 1
				continue
			}
			var ubtBal *big.Int
			var ubtNonce uint64
			if ubtAcct != nil {
				ubtBal = ubtAcct.Balance.ToBig()
				ubtNonce = ubtAcct.Nonce
			} else {
				ubtBal = new(big.Int)
			}
			if balances[j].ToInt().Cmp(ubtBal) != 0 {
				log.Warn("Strict validation: account balance mismatch", "addr", addr, "block", blockNumber, "mpt", balances[j].ToInt(), "ubt", ubtBal)
				*mismatches = *mismatches + 1
				continue
			}
			if uint64(nonces[j]) != ubtNonce {
				log.Warn("Strict validation: account nonce mismatch", "addr", addr, "block", blockNumber, "mpt", uint64(nonces[j]), "ubt", ubtNonce)
				*mismatches = *mismatches + 1
			}
		}
	}
	return nil
}

func (v *Validator) validateStorageStrictBatch(client *rpc.Client, trie *bintrie.BinaryTrie, blockNr rpc.BlockNumberOrHash, blockNumber uint64, storage []ubtemit.StorageEntry, mismatches *int) error {
	for i := 0; i < len(storage); i += strictValidationBatchSize {
		end := i + strictValidationBatchSize
		if end > len(storage) {
			end = len(storage)
		}
		chunk := storage[i:end]

		values := make([]common.Hash, len(chunk))
		req := make([]rpc.BatchElem, len(chunk))
		for j, slot := range chunk {
			req[j] = rpc.BatchElem{
				Method: "eth_getStorageAt",
				Args:   []any{slot.Address, slot.SlotKeyRaw, blockNr},
				Result: &values[j],
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), v.timeout)
		err := client.BatchCallContext(ctx, req)
		cancel()
		if err != nil {
			return fmt.Errorf("batch eth_getStorageAt: %w", err)
		}

		for j, slot := range chunk {
			if req[j].Error != nil {
				return req[j].Error
			}
			ubtValue, err := trie.GetStorage(slot.Address, slot.SlotKeyRaw.Bytes())
			if err != nil {
				log.Warn("Strict validation: storage read failed", "addr", slot.Address, "slot", slot.SlotKeyRaw, "block", blockNumber, "err", err)
				*mismatches = *mismatches + 1
				continue
			}
			if values[j] != common.BytesToHash(ubtValue) {
				log.Warn("Strict validation: storage mismatch", "addr", slot.Address, "slot", slot.SlotKeyRaw, "block", blockNumber, "mpt", values[j], "ubt", common.BytesToHash(ubtValue))
				*mismatches = *mismatches + 1
			}
		}
	}
	return nil
}

func (v *Validator) validateCodeStrictBatch(client *rpc.Client, trie *bintrie.BinaryTrie, blockNr rpc.BlockNumberOrHash, blockNumber uint64, codes []ubtemit.CodeEntry, mismatches *int) error {
	for i := 0; i < len(codes); i += strictValidationBatchSize {
		end := i + strictValidationBatchSize
		if end > len(codes) {
			end = len(codes)
		}
		chunk := codes[i:end]

		codeRes := make([]hexutil.Bytes, len(chunk))
		req := make([]rpc.BatchElem, len(chunk))
		for j, code := range chunk {
			req[j] = rpc.BatchElem{
				Method: "eth_getCode",
				Args:   []any{code.Address, blockNr},
				Result: &codeRes[j],
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), v.timeout)
		err := client.BatchCallContext(ctx, req)
		cancel()
		if err != nil {
			return fmt.Errorf("batch eth_getCode: %w", err)
		}

		for j, code := range chunk {
			if req[j].Error != nil {
				return req[j].Error
			}
			ubtCode, err := trie.GetCode(code.Address)
			if err != nil {
				log.Warn("Strict validation: code read failed", "addr", code.Address, "block", blockNumber, "err", err)
				*mismatches = *mismatches + 1
				continue
			}
			mptCode := codeRes[j]
			if len(mptCode) != len(ubtCode) {
				log.Warn("Strict validation: code length mismatch", "addr", code.Address, "block", blockNumber, "mptLen", len(mptCode), "ubtLen", len(ubtCode))
				*mismatches = *mismatches + 1
				continue
			}
			match := true
			for k := range mptCode {
				if mptCode[k] != ubtCode[k] {
					match = false
					break
				}
			}
			if !match {
				log.Warn("Strict validation: code mismatch", "addr", code.Address, "block", blockNumber)
				*mismatches = *mismatches + 1
			}
		}
	}
	return nil
}

func isHistoricalStateUnavailableRPC(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "historical state") && strings.Contains(msg, "is not available")
}

// validateStorage compares a single storage slot value between MPT and UBT.
func (v *Validator) validateStorage(client *rpc.Client, addr common.Address, slot common.Hash, blockNumber uint64, trie *bintrie.BinaryTrie) error {
	ctx, cancel := context.WithTimeout(context.Background(), v.timeout)
	defer cancel()

	// Fetch MPT storage
	var mptValue common.Hash
	blockNr := rpc.BlockNumberOrHashWithNumber(rpc.BlockNumber(blockNumber))
	if err := client.CallContext(ctx, &mptValue, "eth_getStorageAt", addr, slot, blockNr); err != nil {
		return fmt.Errorf("eth_getStorageAt: %w", err)
	}

	// Get UBT storage
	ubtValue, err := trie.GetStorage(addr, slot.Bytes())
	if err != nil {
		return fmt.Errorf("UBT GetStorage: %w", err)
	}

	ubtHash := common.BytesToHash(ubtValue)
	if mptValue != ubtHash {
		return fmt.Errorf("storage mismatch for %s slot %s: MPT=%s UBT=%s", addr, slot, mptValue, ubtHash)
	}
	return nil
}

// validateCode compares contract code between MPT and UBT.
func (v *Validator) validateCode(client *rpc.Client, addr common.Address, blockNumber uint64, trie *bintrie.BinaryTrie) error {
	ctx, cancel := context.WithTimeout(context.Background(), v.timeout)
	defer cancel()

	// Fetch MPT code
	var mptCode hexutil.Bytes
	blockNr := rpc.BlockNumberOrHashWithNumber(rpc.BlockNumber(blockNumber))
	if err := client.CallContext(ctx, &mptCode, "eth_getCode", addr, blockNr); err != nil {
		return fmt.Errorf("eth_getCode: %w", err)
	}

	// Get UBT code
	ubtCode, err := trie.GetCode(addr)
	if err != nil {
		return fmt.Errorf("UBT GetCode: %w", err)
	}

	if len(mptCode) == 0 && len(ubtCode) == 0 {
		return nil
	}
	if len(mptCode) != len(ubtCode) {
		return fmt.Errorf("code length mismatch for %s: MPT=%d UBT=%d", addr, len(mptCode), len(ubtCode))
	}

	for i := range mptCode {
		if mptCode[i] != ubtCode[i] {
			return fmt.Errorf("code content mismatch for %s at byte %d", addr, i)
		}
	}
	return nil
}

// validateAccount checks a single account's balance and nonce against the geth MPT state.
func (v *Validator) validateAccount(client *rpc.Client, addr common.Address, blockNr rpc.BlockNumberOrHash, trie *bintrie.BinaryTrie) error {
	ctx, cancel := context.WithTimeout(context.Background(), v.timeout)
	defer cancel()

	// Fetch MPT balance
	var mptBalance hexutil.Big
	if err := client.CallContext(ctx, &mptBalance, "eth_getBalance", addr, blockNr); err != nil {
		return fmt.Errorf("eth_getBalance: %w", err)
	}

	// Get UBT account
	ubtAcct, err := trie.GetAccount(addr)
	if err != nil {
		return fmt.Errorf("UBT GetAccount: %w", err)
	}

	// Compare balance
	var ubtBalance *big.Int
	if ubtAcct != nil {
		ubtBalance = ubtAcct.Balance.ToBig()
	} else {
		ubtBalance = new(big.Int)
	}
	if mptBalance.ToInt().Cmp(ubtBalance) != 0 {
		return fmt.Errorf("balance mismatch for %s: MPT=%s UBT=%s", addr, mptBalance.ToInt(), ubtBalance)
	}

	// Fetch and compare nonce
	var mptNonce hexutil.Uint64
	if err := client.CallContext(ctx, &mptNonce, "eth_getTransactionCount", addr, blockNr); err != nil {
		return fmt.Errorf("eth_getTransactionCount: %w", err)
	}
	var ubtNonce uint64
	if ubtAcct != nil {
		ubtNonce = ubtAcct.Nonce
	}
	if uint64(mptNonce) != ubtNonce {
		return fmt.Errorf("nonce mismatch for %s: MPT=%d UBT=%d", addr, uint64(mptNonce), ubtNonce)
	}

	return nil
}
