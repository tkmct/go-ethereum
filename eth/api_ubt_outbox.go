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

package eth

import (
	"context"
	"errors"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/ubtemit"
	"github.com/ethereum/go-ethereum/log"
)

// UBTOutboxAPI provides read-only access to UBT outbox events.
// The ubtconv daemon consumes events through these endpoints.
type UBTOutboxAPI struct {
	eth *Ethereum
}

// NewUBTOutboxAPI creates a new UBTOutboxAPI instance.
func NewUBTOutboxAPI(eth *Ethereum) *UBTOutboxAPI {
	return &UBTOutboxAPI{eth: eth}
}

// RPCOutboxEvent is the JSON-serializable outbox event for RPC responses.
type RPCOutboxEvent struct {
	Seq         hexutil.Uint64 `json:"seq"`
	Version     hexutil.Uint   `json:"version"`
	Kind        string         `json:"kind"`
	BlockNumber hexutil.Uint64 `json:"blockNumber"`
	BlockHash   common.Hash    `json:"blockHash"`
	ParentHash  common.Hash    `json:"parentHash"`
	Timestamp   hexutil.Uint64 `json:"timestamp"`
	Payload     hexutil.Bytes  `json:"payload"`
}

func toRPCEvent(env *ubtemit.OutboxEnvelope) *RPCOutboxEvent {
	return &RPCOutboxEvent{
		Seq:         hexutil.Uint64(env.Seq),
		Version:     hexutil.Uint(env.Version),
		Kind:        env.Kind,
		BlockNumber: hexutil.Uint64(env.BlockNumber),
		BlockHash:   env.BlockHash,
		ParentHash:  env.ParentHash,
		Timestamp:   hexutil.Uint64(env.Timestamp),
		Payload:     env.Payload,
	}
}

// GetEvent returns an outbox event by sequence number.
// Returns null if the event doesn't exist. Returns an error for real DB/decode failures.
func (api *UBTOutboxAPI) GetEvent(ctx context.Context, seq hexutil.Uint64) (*RPCOutboxEvent, error) {
	store := api.eth.OutboxStore()
	if store == nil {
		return nil, errors.New("UBT outbox not enabled")
	}
	env, err := store.Read(uint64(seq))
	if err != nil {
		if errors.Is(err, ubtemit.ErrEventNotFound) {
			return nil, nil // Not found returns null
		}
		return nil, err // Real errors propagated
	}
	return toRPCEvent(env), nil
}

// GetEvents returns outbox events in range [fromSeq, toSeq] inclusive.
// Maximum 1000 events per call to prevent abuse.
func (api *UBTOutboxAPI) GetEvents(ctx context.Context, fromSeq, toSeq hexutil.Uint64) ([]RPCOutboxEvent, error) {
	store := api.eth.OutboxStore()
	if store == nil {
		return nil, errors.New("UBT outbox not enabled")
	}
	from, to := uint64(fromSeq), uint64(toSeq)
	if from > to {
		return nil, errors.New("fromSeq must be <= toSeq")
	}
	const maxRange = 1000
	originalTo := to
	if to-from+1 > maxRange {
		to = from + maxRange - 1
		log.Debug("UBT outbox GetEvents range truncated", "from", from, "requestedTo", originalTo, "actualTo", to, "maxRange", maxRange)
	}
	envs, err := store.ReadRange(from, to)
	if err != nil {
		return nil, err
	}
	result := make([]RPCOutboxEvent, len(envs))
	for i, env := range envs {
		result[i] = *toRPCEvent(env)
	}
	return result, nil
}

// LatestSeq returns the latest outbox sequence number.
func (api *UBTOutboxAPI) LatestSeq(ctx context.Context) (hexutil.Uint64, error) {
	store := api.eth.OutboxStore()
	if store == nil {
		return 0, errors.New("UBT outbox not enabled")
	}
	return hexutil.Uint64(store.LatestSeq()), nil
}

// CompactOutboxBelow deletes outbox events below the given safe sequence number.
// This is called by the ubtconv daemon to coordinate outbox retention.
func (api *UBTOutboxAPI) CompactOutboxBelow(ctx context.Context, safeSeq hexutil.Uint64) (map[string]any, error) {
	store := api.eth.OutboxStore()
	if store == nil {
		return nil, errors.New("UBT outbox not enabled")
	}
	latest := store.LatestSeq()
	if err := ubtemit.ValidateCompactBelowBounds(uint64(safeSeq), latest); err != nil {
		return nil, err
	}
	count, err := store.CompactBelow(uint64(safeSeq))
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"deleted": count,
		"safeSeq": uint64(safeSeq),
	}, nil
}

// Status returns the UBT emitter health status.
func (api *UBTOutboxAPI) Status(ctx context.Context) (map[string]any, error) {
	store := api.eth.OutboxStore()
	if store == nil {
		return nil, errors.New("UBT outbox not enabled")
	}

	result := map[string]any{
		"enabled":   true,
		"latestSeq": hexutil.Uint64(store.LatestSeq()),
	}

	// Add emitter degraded status if service is available
	if svc := api.eth.EmitterService(); svc != nil {
		result["degraded"] = svc.IsDegraded()
	}

	return result, nil
}
