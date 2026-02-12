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

package ubtemit

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// Event envelope versioning
const (
	EnvelopeVersionV1 uint16 = 1

	KindDiff  = "diff"
	KindReorg = "reorg"
)

// OutboxEnvelope is the durable event envelope wrapping all outbox events.
type OutboxEnvelope struct {
	Seq         uint64      // Strictly monotonic sequence number
	Version     uint16      // Envelope version (currently V1)
	Kind        string      // Event kind: "diff" or "reorg"
	BlockNumber uint64
	BlockHash   common.Hash
	ParentHash  common.Hash
	Timestamp   uint64 // Unix timestamp of emission
	Payload     []byte // RLP-encoded inner event (QueuedDiffV1 or ReorgMarkerV1)
}

// QueuedDiffV1 represents a canonical block state diff for UBT conversion.
// All slices are sorted for deterministic encoding.
type QueuedDiffV1 struct {
	OriginRoot common.Hash    // Pre-state MPT root
	Root       common.Hash    // Post-state MPT root
	Accounts   []AccountEntry // Sorted by address bytes
	Storage    []StorageEntry // Sorted by (address, slotKeyRaw)
	Codes      []CodeEntry    // Sorted by address bytes
}

// AccountEntry represents an account state change.
type AccountEntry struct {
	Address  common.Address
	Nonce    uint64
	Balance  *big.Int
	CodeHash common.Hash
	// Alive indicates whether the account exists post-state.
	// When false, the account should be zeroed/deleted in UBT.
	Alive bool
}

// StorageEntry represents a storage slot change.
// SlotKeyRaw is the raw (unhashed) storage key - critical for UBT which uses raw keys.
type StorageEntry struct {
	Address    common.Address
	SlotKeyRaw common.Hash // Raw (unhashed) storage slot key
	Value      common.Hash // Post-state value (zero means deleted)
}

// CodeEntry represents a contract code change.
type CodeEntry struct {
	Address  common.Address
	CodeHash common.Hash
	Code     []byte
}

// ReorgMarkerV1 signals a canonical chain reorganization.
type ReorgMarkerV1 struct {
	FromBlockNumber      uint64
	FromBlockHash        common.Hash
	ToBlockNumber        uint64
	ToBlockHash          common.Hash
	CommonAncestorNumber uint64
	CommonAncestorHash   common.Hash
}

// BlockReplayer is the interface for replaying canonical blocks.
type BlockReplayer interface {
	CurrentBlock() *types.Header
	GetBlockByNumber(number uint64) *types.Block
	GetCanonicalHash(number uint64) common.Hash
	ReplayBlock(block *types.Block) (*QueuedDiffV1, error)
}

// IsZeroAccount returns true if the account entry represents a deleted/zero account.
// A zero account is one that is marked as not alive OR has nonce=0, balance=0, and empty code.
func (a *AccountEntry) IsZeroAccount() bool {
	return !a.Alive || (a.Nonce == 0 &&
		(a.Balance == nil || a.Balance.Sign() == 0) &&
		(a.CodeHash == common.Hash{} || a.CodeHash == types.EmptyCodeHash))
}

// NewDeletedAccountEntry creates an AccountEntry for a deleted account.
// This represents the proper zero state for an account in UBT.
func NewDeletedAccountEntry(addr common.Address) AccountEntry {
	return AccountEntry{
		Address:  addr,
		Nonce:    0,
		Balance:  new(big.Int),
		CodeHash: types.EmptyCodeHash,
		Alive:    false,
	}
}

// DeletedAccountCount returns the number of accounts marked for deletion.
func (d *QueuedDiffV1) DeletedAccountCount() int {
	count := 0
	for _, a := range d.Accounts {
		if !a.Alive {
			count++
		}
	}
	return count
}

// ActiveAccountCount returns the number of accounts that are active post-state.
func (d *QueuedDiffV1) ActiveAccountCount() int {
	count := 0
	for _, a := range d.Accounts {
		if a.Alive {
			count++
		}
	}
	return count
}
