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
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/rlp"
)

// rlpOutboxEnvelope is the RLP-encodable representation of OutboxEnvelope.
type rlpOutboxEnvelope struct {
	Seq         uint64
	Version     uint16
	Kind        string
	BlockNumber uint64
	BlockHash   common.Hash
	ParentHash  common.Hash
	Timestamp   uint64
	Payload     []byte
}

// rlpAccountEntry is the RLP-encodable representation of AccountEntry.
type rlpAccountEntry struct {
	Address  common.Address
	Nonce    uint64
	Balance  *big.Int
	CodeHash common.Hash
	Alive    bool
}

// rlpStorageEntry is the RLP-encodable representation of StorageEntry.
type rlpStorageEntry struct {
	Address    common.Address
	SlotKeyRaw common.Hash
	Value      common.Hash
}

// rlpCodeEntry is the RLP-encodable representation of CodeEntry.
type rlpCodeEntry struct {
	Address  common.Address
	CodeHash common.Hash
	Code     []byte
}

// rlpQueuedDiffV1 is the RLP-encodable representation of QueuedDiffV1.
type rlpQueuedDiffV1 struct {
	OriginRoot common.Hash
	Root       common.Hash
	Accounts   []rlpAccountEntry
	Storage    []rlpStorageEntry
	Codes      []rlpCodeEntry
}

// rlpReorgMarkerV1 is the RLP-encodable representation of ReorgMarkerV1.
type rlpReorgMarkerV1 struct {
	FromBlockNumber      uint64
	FromBlockHash        common.Hash
	ToBlockNumber        uint64
	ToBlockHash          common.Hash
	CommonAncestorNumber uint64
	CommonAncestorHash   common.Hash
}

// EncodeEnvelope encodes an OutboxEnvelope to RLP bytes.
func EncodeEnvelope(env *OutboxEnvelope) ([]byte, error) {
	rlpEnv := rlpOutboxEnvelope{
		Seq:         env.Seq,
		Version:     env.Version,
		Kind:        env.Kind,
		BlockNumber: env.BlockNumber,
		BlockHash:   env.BlockHash,
		ParentHash:  env.ParentHash,
		Timestamp:   env.Timestamp,
		Payload:     env.Payload,
	}
	return rlp.EncodeToBytes(&rlpEnv)
}

// DecodeEnvelope decodes RLP bytes to an OutboxEnvelope.
func DecodeEnvelope(data []byte) (*OutboxEnvelope, error) {
	var rlpEnv rlpOutboxEnvelope
	if err := rlp.DecodeBytes(data, &rlpEnv); err != nil {
		return nil, err
	}

	// Validate version
	if rlpEnv.Version != EnvelopeVersionV1 {
		return nil, fmt.Errorf("unsupported envelope version: %d", rlpEnv.Version)
	}

	// Validate kind
	if rlpEnv.Kind != KindDiff && rlpEnv.Kind != KindReorg {
		return nil, fmt.Errorf("invalid envelope kind: %s", rlpEnv.Kind)
	}

	return &OutboxEnvelope{
		Seq:         rlpEnv.Seq,
		Version:     rlpEnv.Version,
		Kind:        rlpEnv.Kind,
		BlockNumber: rlpEnv.BlockNumber,
		BlockHash:   rlpEnv.BlockHash,
		ParentHash:  rlpEnv.ParentHash,
		Timestamp:   rlpEnv.Timestamp,
		Payload:     rlpEnv.Payload,
	}, nil
}

// EncodeDiff encodes a QueuedDiffV1 to RLP bytes.
func EncodeDiff(diff *QueuedDiffV1) ([]byte, error) {
	rlpDiff := rlpQueuedDiffV1{
		OriginRoot: diff.OriginRoot,
		Root:       diff.Root,
		Accounts:   make([]rlpAccountEntry, len(diff.Accounts)),
		Storage:    make([]rlpStorageEntry, len(diff.Storage)),
		Codes:      make([]rlpCodeEntry, len(diff.Codes)),
	}

	for i, acc := range diff.Accounts {
		rlpDiff.Accounts[i] = rlpAccountEntry{
			Address:  acc.Address,
			Nonce:    acc.Nonce,
			Balance:  acc.Balance,
			CodeHash: acc.CodeHash,
			Alive:    acc.Alive,
		}
	}

	for i, stor := range diff.Storage {
		rlpDiff.Storage[i] = rlpStorageEntry{
			Address:    stor.Address,
			SlotKeyRaw: stor.SlotKeyRaw,
			Value:      stor.Value,
		}
	}

	for i, code := range diff.Codes {
		rlpDiff.Codes[i] = rlpCodeEntry{
			Address:  code.Address,
			CodeHash: code.CodeHash,
			Code:     code.Code,
		}
	}

	return rlp.EncodeToBytes(&rlpDiff)
}

// DecodeDiff decodes RLP bytes to a QueuedDiffV1.
func DecodeDiff(data []byte) (*QueuedDiffV1, error) {
	var rlpDiff rlpQueuedDiffV1
	if err := rlp.DecodeBytes(data, &rlpDiff); err != nil {
		return nil, err
	}

	diff := &QueuedDiffV1{
		OriginRoot: rlpDiff.OriginRoot,
		Root:       rlpDiff.Root,
		Accounts:   make([]AccountEntry, len(rlpDiff.Accounts)),
		Storage:    make([]StorageEntry, len(rlpDiff.Storage)),
		Codes:      make([]CodeEntry, len(rlpDiff.Codes)),
	}

	for i, acc := range rlpDiff.Accounts {
		diff.Accounts[i] = AccountEntry{
			Address:  acc.Address,
			Nonce:    acc.Nonce,
			Balance:  acc.Balance,
			CodeHash: acc.CodeHash,
			Alive:    acc.Alive,
		}
	}

	for i, stor := range rlpDiff.Storage {
		diff.Storage[i] = StorageEntry{
			Address:    stor.Address,
			SlotKeyRaw: stor.SlotKeyRaw,
			Value:      stor.Value,
		}
	}

	for i, code := range rlpDiff.Codes {
		diff.Codes[i] = CodeEntry{
			Address:  code.Address,
			CodeHash: code.CodeHash,
			Code:     code.Code,
		}
	}

	return diff, nil
}

// EncodeReorgMarker encodes a ReorgMarkerV1 to RLP bytes.
func EncodeReorgMarker(marker *ReorgMarkerV1) ([]byte, error) {
	rlpMarker := rlpReorgMarkerV1{
		FromBlockNumber:      marker.FromBlockNumber,
		FromBlockHash:        marker.FromBlockHash,
		ToBlockNumber:        marker.ToBlockNumber,
		ToBlockHash:          marker.ToBlockHash,
		CommonAncestorNumber: marker.CommonAncestorNumber,
		CommonAncestorHash:   marker.CommonAncestorHash,
	}
	return rlp.EncodeToBytes(&rlpMarker)
}

// DecodeReorgMarker decodes RLP bytes to a ReorgMarkerV1.
func DecodeReorgMarker(data []byte) (*ReorgMarkerV1, error) {
	var rlpMarker rlpReorgMarkerV1
	if err := rlp.DecodeBytes(data, &rlpMarker); err != nil {
		return nil, err
	}

	return &ReorgMarkerV1{
		FromBlockNumber:      rlpMarker.FromBlockNumber,
		FromBlockHash:        rlpMarker.FromBlockHash,
		ToBlockNumber:        rlpMarker.ToBlockNumber,
		ToBlockHash:          rlpMarker.ToBlockHash,
		CommonAncestorNumber: rlpMarker.CommonAncestorNumber,
		CommonAncestorHash:   rlpMarker.CommonAncestorHash,
	}, nil
}
