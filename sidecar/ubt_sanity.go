// Copyright 2026 The go-ethereum Authors
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

package sidecar

import (
	"bytes"
	"fmt"
	"sort"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"
)

const ubtSanityLogLimit = 20

// SanityCheck compares touched MPT keys with UBT sidecar values for a block.
func (sc *UBTSidecar) SanityCheck(block *types.Block, update *state.StateUpdate, statedb *state.StateDB) {
	if block == nil || update == nil || statedb == nil {
		return
	}
	if sc == nil || !sc.Ready() {
		return
	}
	ubtRoot, ok := sc.GetUBTRoot(block.Hash())
	if !ok {
		log.Error("UBT sanity: missing UBT root", "block", block.NumberU64(), "hash", block.Hash())
		return
	}
	accounts := update.AccountsOrigin()
	storages := update.StoragesOrigin()

	addrSet := make(map[common.Address]struct{})
	for addr := range accounts {
		addrSet[addr] = struct{}{}
	}
	for addr := range storages {
		addrSet[addr] = struct{}{}
	}
	if len(addrSet) == 0 {
		return
	}
	addrs := make([]common.Address, 0, len(addrSet))
	for addr := range addrSet {
		addrs = append(addrs, addr)
	}
	sort.Slice(addrs, func(i, j int) bool {
		return bytes.Compare(addrs[i][:], addrs[j][:]) < 0
	})

	mismatches := 0
	rawStorageKey := update.RawStorageKey()

	for _, addr := range addrs {
		ubtAcc, err := sc.ReadAccount(ubtRoot, addr)
		if err != nil {
			mismatches++
			ubtSanityLog(mismatches, "UBT sanity: account read failed", "block", block.NumberU64(), "hash", block.Hash(), "address", addr, "err", err)
			continue
		}
		ubtExists := ubtAcc != nil
		mptExists := statedb.Exist(addr)
		if mptExists != ubtExists {
			mismatches++
			ubtSanityLog(mismatches, "UBT sanity: account existence mismatch", "block", block.NumberU64(), "hash", block.Hash(), "address", addr, "mpt", mptExists, "ubt", ubtExists)
		}

		mptBalance := statedb.GetBalance(addr)
		ubtBalance := common.U2560
		ubtNonce := uint64(0)
		ubtCodeHash := common.Hash{}
		ubtCodeSize := uint32(0)
		if ubtAcc != nil {
			if ubtAcc.Balance != nil {
				ubtBalance = ubtAcc.Balance
			}
			ubtNonce = ubtAcc.Nonce
			ubtCodeHash = ubtAcc.CodeHash
			ubtCodeSize = ubtAcc.CodeSize
		}

		if mptBalance.Cmp(ubtBalance) != 0 {
			mismatches++
			ubtSanityLog(mismatches, "UBT sanity: balance mismatch", "block", block.NumberU64(), "hash", block.Hash(), "address", addr, "mpt", mptBalance.String(), "ubt", ubtBalance.String())
		}
		mptNonce := statedb.GetNonce(addr)
		if mptNonce != ubtNonce {
			mismatches++
			ubtSanityLog(mismatches, "UBT sanity: nonce mismatch", "block", block.NumberU64(), "hash", block.Hash(), "address", addr, "mpt", mptNonce, "ubt", ubtNonce)
		}
		mptCodeHash := statedb.GetCodeHash(addr)
		if mptCodeHash != ubtCodeHash {
			mismatches++
			ubtSanityLog(mismatches, "UBT sanity: code hash mismatch", "block", block.NumberU64(), "hash", block.Hash(), "address", addr, "mpt", mptCodeHash, "ubt", ubtCodeHash)
		}
		mptCodeSize := uint32(statedb.GetCodeSize(addr))
		if mptCodeSize != ubtCodeSize {
			mismatches++
			ubtSanityLog(mismatches, "UBT sanity: code size mismatch", "block", block.NumberU64(), "hash", block.Hash(), "address", addr, "mpt", mptCodeSize, "ubt", ubtCodeSize)
		}

		slots, ok := storages[addr]
		if !ok {
			continue
		}
		slotHashes := make([]common.Hash, 0, len(slots))
		for slotHash := range slots {
			slotHashes = append(slotHashes, slotHash)
		}
		sort.Slice(slotHashes, func(i, j int) bool {
			return bytes.Compare(slotHashes[i][:], slotHashes[j][:]) < 0
		})
		for _, slotHash := range slotHashes {
			rawKey, err := sc.sanityResolveStorageKey(rawStorageKey, slotHash)
			if err != nil {
				mismatches++
				ubtSanityLog(mismatches, "UBT sanity: storage key resolve failed", "block", block.NumberU64(), "hash", block.Hash(), "address", addr, "slot", slotHash, "err", err)
				continue
			}
			mptVal := statedb.GetState(addr, rawKey)
			ubtVal, err := sc.ReadStorage(ubtRoot, addr, rawKey)
			if err != nil {
				mismatches++
				ubtSanityLog(mismatches, "UBT sanity: storage read failed", "block", block.NumberU64(), "hash", block.Hash(), "address", addr, "slot", rawKey, "err", err)
				continue
			}
			if mptVal != ubtVal {
				mismatches++
				ubtSanityLog(mismatches, "UBT sanity: storage mismatch", "block", block.NumberU64(), "hash", block.Hash(), "address", addr, "slot", rawKey, "mpt", mptVal, "ubt", ubtVal)
			}
		}
	}

	if mismatches > 0 {
		log.Error("UBT sanity: mismatches detected", "block", block.NumberU64(), "hash", block.Hash(), "count", mismatches)
	}
}

func (sc *UBTSidecar) sanityResolveStorageKey(rawStorageKey bool, slotHash common.Hash) (common.Hash, error) {
	if rawStorageKey {
		return slotHash, nil
	}
	preimage := sc.preimage(slotHash)
	if len(preimage) == 0 {
		return common.Hash{}, fmt.Errorf("missing storage preimage for %x", slotHash)
	}
	if len(preimage) != common.HashLength {
		return common.Hash{}, fmt.Errorf("invalid storage preimage for %x", slotHash)
	}
	var rawKey common.Hash
	copy(rawKey[:], preimage)
	return rawKey, nil
}

func ubtSanityLog(mismatches int, msg string, ctx ...interface{}) {
	if mismatches <= ubtSanityLogLimit {
		log.Error(msg, ctx...)
	}
}
