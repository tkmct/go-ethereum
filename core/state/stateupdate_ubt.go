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
	"errors"
	"fmt"
	"math/big"
	"sort"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/ubtemit"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rlp"
)

// ErrRawStorageKeyMissing indicates that the update cannot be converted to UBT
// because raw storage keys are unavailable for at least one touched slot.
var ErrRawStorageKeyMissing = errors.New("ErrRawStorageKeyMissing")

// UBTStorageKeyLookup resolves a raw storage slot key from (address, slotHash).
// It is used for pre-Cancun conversion where state updates are keyed by slot hash.
type UBTStorageKeyLookup func(common.Address, common.Hash) (common.Hash, bool)

// ToUBTDiff converts the internal stateUpdate to a QueuedDiffV1 for UBT emission.
// This is the orchestration boundary where unexported state types are converted
// to exported ubtemit types.
func (sc *stateUpdate) ToUBTDiff() (*ubtemit.QueuedDiffV1, error) {
	return sc.toUBTDiff(nil)
}

// ToUBTDiffWithStorageKeyLookup converts state updates to UBT diff and uses the
// provided lookup when raw storage keys are not directly available.
func (sc *stateUpdate) ToUBTDiffWithStorageKeyLookup(lookup UBTStorageKeyLookup) (*ubtemit.QueuedDiffV1, error) {
	return sc.toUBTDiff(lookup)
}

func (sc *stateUpdate) toUBTDiff(lookup UBTStorageKeyLookup) (*ubtemit.QueuedDiffV1, error) {
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
	for addr, slots := range sc.storagesOrigin {
		addrHash := crypto.Keccak256Hash(addr.Bytes())
		newSlots := sc.storages[addrHash]

		for slotKey := range slots {
			var (
				rawKey    common.Hash
				hashedKey common.Hash
			)
			if sc.rawStorageKey {
				rawKey = slotKey
				hashedKey = crypto.Keccak256Hash(rawKey.Bytes())
			} else {
				hashedKey = slotKey
				var ok bool
				rawKey, ok = sc.resolveRawStorageKey(addr, hashedKey, lookup)
				if !ok {
					return nil, fmt.Errorf("%w: missing storage key preimage for addr=%s slotHash=%s", ErrRawStorageKeyMissing, addr, hashedKey)
				}
			}
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

func (sc *stateUpdate) resolveRawStorageKey(addr common.Address, slotHash common.Hash, lookup UBTStorageKeyLookup) (common.Hash, bool) {
	if slots := sc.storageSlotPreimages[addr]; slots != nil {
		if raw, ok := slots[slotHash]; ok {
			return raw, true
		}
	}
	if lookup != nil {
		return lookup(addr, slotHash)
	}
	return common.Hash{}, false
}
