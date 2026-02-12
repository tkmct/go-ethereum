// Copyright 2024 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package state

import (
	"bytes"
	"fmt"
	"math/big"
	"sort"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/ubtemit"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rlp"
)

// ToUBTDiff converts the internal stateUpdate to a QueuedDiffV1 for UBT emission.
// This is the orchestration boundary where unexported state types are converted
// to exported ubtemit types.
//
// IMPORTANT: This method requires rawStorageKey=true on the stateUpdate for
// correct UBT conversion. If raw keys are unavailable, this is an invariant violation.
func (sc *stateUpdate) ToUBTDiff() (*ubtemit.QueuedDiffV1, error) {
	diff := &ubtemit.QueuedDiffV1{
		OriginRoot: sc.originRoot,
		Root:       sc.root,
	}

	// Convert accounts
	for addr := range sc.accountsOrigin {
		addrHash := crypto.Keccak256Hash(addr.Bytes())
		newData := sc.accounts[addrHash]

		entry := ubtemit.AccountEntry{
			Address: addr,
			Alive:   len(newData) > 0,
		}

		if len(newData) > 0 {
			acct, err := types.FullAccount(newData)
			if err != nil {
				return nil, err
			}
			entry.Nonce = acct.Nonce
			entry.Balance = acct.Balance.ToBig()
			entry.CodeHash = common.BytesToHash(acct.CodeHash)
		} else {
			entry.Balance = new(big.Int)
		}

		diff.Accounts = append(diff.Accounts, entry)
	}
	// Sort accounts by address
	sort.Slice(diff.Accounts, func(i, j int) bool {
		return bytes.Compare(diff.Accounts[i].Address[:], diff.Accounts[j].Address[:]) < 0
	})

	// Convert storage slots
	if !sc.rawStorageKey {
		// Pre-Cancun blocks lack raw storage keys - this is expected behavior.
		// UBT diffs will resume once Cancun is activated and raw keys become available.
		return nil, fmt.Errorf("UBT diff conversion requires raw storage keys (pre-Cancun block, UBT diffs will resume at Cancun activation)")
	}
	for addr, slots := range sc.storagesOrigin {
		addrHash := crypto.Keccak256Hash(addr.Bytes())
		newSlots := sc.storages[addrHash]

		for rawKey := range slots {
			hashedKey := crypto.Keccak256Hash(rawKey.Bytes())
			newValue := newSlots[hashedKey]

			var value common.Hash
			if len(newValue) > 0 {
				_, content, _, err := rlp.Split(newValue)
				if err != nil {
					return nil, fmt.Errorf("decode storage value: %w", err)
				}
				value = common.BytesToHash(content)
			}

			diff.Storage = append(diff.Storage, ubtemit.StorageEntry{
				Address:    addr,
				SlotKeyRaw: rawKey,
				Value:      value,
			})
		}
	}
	// Sort storage by (address, slotKeyRaw)
	sort.Slice(diff.Storage, func(i, j int) bool {
		cmp := bytes.Compare(diff.Storage[i].Address[:], diff.Storage[j].Address[:])
		if cmp != 0 {
			return cmp < 0
		}
		return bytes.Compare(diff.Storage[i].SlotKeyRaw[:], diff.Storage[j].SlotKeyRaw[:]) < 0
	})

	// Convert codes
	for addr, code := range sc.codes {
		diff.Codes = append(diff.Codes, ubtemit.CodeEntry{
			Address:  addr,
			CodeHash: code.hash,
			Code:     code.blob,
		})
	}
	// Sort codes by address
	sort.Slice(diff.Codes, func(i, j int) bool {
		return bytes.Compare(diff.Codes[i].Address[:], diff.Codes[j].Address[:]) < 0
	})

	return diff, nil
}
