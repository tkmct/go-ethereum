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
	"fmt"
	"math/big"
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

// NewValidator creates a new Validator using the outbox reader's RPC connection.
func NewValidator(reader *OutboxReader) *Validator {
	return &Validator{
		reader:  reader,
		timeout: 30 * time.Second,
	}
}

// maxValidationSample limits the number of accounts validated per block.
const maxValidationSample = 10

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

	// Validate all accounts
	for _, acct := range diff.Accounts {
		if !acct.Alive {
			continue // skip deleted accounts
		}
		if err := v.validateAccount(client, acct.Address, blockNr, trie); err != nil {
			log.Warn("Strict validation: account mismatch", "addr", acct.Address, "block", blockNumber, "err", err)
			mismatches++
		}
	}

	// Validate all storage slots
	for _, slot := range diff.Storage {
		if err := v.validateStorage(client, slot.Address, slot.SlotKeyRaw, blockNumber, trie); err != nil {
			log.Warn("Strict validation: storage mismatch", "addr", slot.Address, "slot", slot.SlotKeyRaw, "block", blockNumber, "err", err)
			mismatches++
		}
	}

	// Validate all code entries
	for _, code := range diff.Codes {
		if err := v.validateCode(client, code.Address, blockNumber, trie); err != nil {
			log.Warn("Strict validation: code mismatch", "addr", code.Address, "block", blockNumber, "err", err)
			mismatches++
		}
	}

	if mismatches > 0 {
		validationMismatches.Inc(int64(mismatches))
		return fmt.Errorf("strict validation found %d mismatches at block %d", mismatches, blockNumber)
	}

	validationChecksTotal.Inc(1)
	log.Debug("Strict validation passed", "block", blockNumber, "accounts", len(diff.Accounts), "storage", len(diff.Storage), "codes", len(diff.Codes))
	return nil
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
