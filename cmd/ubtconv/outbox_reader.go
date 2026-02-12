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
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/ubtemit"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/rpc"
)

// rpcOutboxEvent mirrors the JSON response from the geth ubt_getEvent RPC.
type rpcOutboxEvent struct {
	Seq         hexutil.Uint64 `json:"seq"`
	Version     hexutil.Uint   `json:"version"`
	Kind        string         `json:"kind"`
	BlockNumber hexutil.Uint64 `json:"blockNumber"`
	BlockHash   common.Hash    `json:"blockHash"`
	ParentHash  common.Hash    `json:"parentHash"`
	Timestamp   hexutil.Uint64 `json:"timestamp"`
	Payload     hexutil.Bytes  `json:"payload"`
}

// rpcDumpAccount mirrors debug_accountRange account payload.
type rpcDumpAccount struct {
	Balance  string            `json:"balance"`
	Nonce    uint64            `json:"nonce"`
	CodeHash hexutil.Bytes     `json:"codeHash"`
	Code     hexutil.Bytes     `json:"code,omitempty"`
	Storage  map[string]string `json:"storage,omitempty"`
}

// rpcStateDump mirrors debug_accountRange response payload.
type rpcStateDump struct {
	Root     string                    `json:"root"`
	Accounts map[string]rpcDumpAccount `json:"accounts"`
	Next     hexutil.Bytes             `json:"next,omitempty"`
}

// toEnvelope converts the RPC event to an OutboxEnvelope.
func (r *rpcOutboxEvent) toEnvelope() *ubtemit.OutboxEnvelope {
	return &ubtemit.OutboxEnvelope{
		Seq:         uint64(r.Seq),
		Version:     uint16(r.Version),
		Kind:        r.Kind,
		BlockNumber: uint64(r.BlockNumber),
		BlockHash:   r.BlockHash,
		ParentHash:  r.ParentHash,
		Timestamp:   uint64(r.Timestamp),
		Payload:     r.Payload,
	}
}

// OutboxReader reads outbox events from geth via RPC.
type OutboxReader struct {
	endpoint       string
	client         *rpc.Client
	mu             sync.Mutex
	closed         bool
	timeout        time.Duration
	lastReconnect  time.Time
	reconnectDelay time.Duration // minimum delay between reconnection attempts
}

// NewOutboxReader creates a new outbox reader for the given RPC endpoint.
func NewOutboxReader(endpoint string) *OutboxReader {
	return &OutboxReader{
		endpoint:       endpoint,
		timeout:        30 * time.Second,
		reconnectDelay: 5 * time.Second,
	}
}

// connectLocked establishes the RPC connection. Caller must hold r.mu.
// This method does NOT sleep — callers handle throttling outside the lock.
func (r *OutboxReader) connectLocked() error {
	if r.client != nil {
		return nil
	}
	if r.closed {
		return fmt.Errorf("outbox reader is closed")
	}

	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()

	client, err := rpc.DialContext(ctx, r.endpoint)
	if err != nil {
		r.lastReconnect = time.Now()
		return fmt.Errorf("failed to connect to %s: %w", r.endpoint, err)
	}
	r.client = client
	r.lastReconnect = time.Now()
	log.Info("Connected to geth outbox RPC", "endpoint", r.endpoint)
	return nil
}

// getClient returns the current RPC client, connecting if necessary.
func (r *OutboxReader) getClient() (*rpc.Client, error) {
	// Keep lock scope narrow: only wait/retry on reconnection outside critical section.
	r.mu.Lock()
	for {
		if r.client != nil {
			c := r.client
			r.mu.Unlock()
			return c, nil
		}
		if r.closed {
			r.mu.Unlock()
			return nil, fmt.Errorf("outbox reader is closed")
		}

		timeSinceReconnect := time.Since(r.lastReconnect)
		if timeSinceReconnect < r.reconnectDelay {
			waitTime := r.reconnectDelay - timeSinceReconnect
			r.mu.Unlock()
			log.Debug("Throttling reconnection attempt", "wait", waitTime)
			time.Sleep(waitTime)
			r.mu.Lock()
			continue
		}

		err := r.connectLocked()
		c := r.client
		r.mu.Unlock()
		return c, err
	}
}

// ReadEvent reads an outbox event by sequence number.
// Returns nil if no event exists at the given sequence.
func (r *OutboxReader) ReadEvent(seq uint64) (*ubtemit.OutboxEnvelope, error) {
	client, err := r.getClient()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()

	var result *rpcOutboxEvent
	err = client.CallContext(ctx, &result, "ubt_getEvent", hexutil.Uint64(seq))
	if err != nil {
		r.resetClient(err)
		return nil, fmt.Errorf("ubt_getEvent(%d): %w", seq, err)
	}
	if result == nil {
		return nil, nil
	}
	return result.toEnvelope(), nil
}

// ReadRange reads outbox events in [fromSeq, toSeq].
func (r *OutboxReader) ReadRange(fromSeq, toSeq uint64) ([]*ubtemit.OutboxEnvelope, error) {
	client, err := r.getClient()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()

	var results []rpcOutboxEvent
	err = client.CallContext(ctx, &results, "ubt_getEvents", hexutil.Uint64(fromSeq), hexutil.Uint64(toSeq))
	if err != nil {
		r.resetClient(err)
		return nil, fmt.Errorf("ubt_getEvents(%d, %d): %w", fromSeq, toSeq, err)
	}
	envs := make([]*ubtemit.OutboxEnvelope, len(results))
	for i := range results {
		envs[i] = results[i].toEnvelope()
	}
	return envs, nil
}

// LatestSeq returns the latest available sequence from the outbox.
func (r *OutboxReader) LatestSeq() (uint64, error) {
	client, err := r.getClient()
	if err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()

	var result hexutil.Uint64
	err = client.CallContext(ctx, &result, "ubt_latestSeq")
	if err != nil {
		r.resetClient(err)
		return 0, fmt.Errorf("ubt_latestSeq: %w", err)
	}
	return uint64(result), nil
}

// ReadAccountRange reads a page of accounts at a specific block using debug_accountRange.
func (r *OutboxReader) ReadAccountRange(
	blockNrOrHash rpc.BlockNumberOrHash,
	start hexutil.Bytes,
	maxResults int,
	nocode, nostorage, incompletes bool,
) (*rpcStateDump, error) {
	client, err := r.getClient()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()

	var result rpcStateDump
	err = client.CallContext(ctx, &result, "debug_accountRange", blockNrOrHash, start, maxResults, nocode, nostorage, incompletes)
	if err != nil {
		r.resetClient(err)
		return nil, fmt.Errorf("debug_accountRange: %w", err)
	}
	return &result, nil
}

// resetClient closes the current client to force reconnection on next call.
// This handles scenarios where the RPC server was restarted or the
// network connection dropped.
func (r *OutboxReader) resetClient(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.client != nil {
		r.client.Close()
		r.client = nil
		log.Warn("UBT outbox RPC connection reset due to error", "err", err)
	}
}

// Close closes the RPC connection.
func (r *OutboxReader) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	if r.client != nil {
		r.client.Close()
		r.client = nil
	}
}

// Reconnect closes the current connection and allows reconnecting to a new endpoint.
// Unlike Close(), this does not permanently shut down the reader.
func (r *OutboxReader) Reconnect(endpoint string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.client != nil {
		r.client.Close()
		r.client = nil
	}
	r.endpoint = endpoint
	r.closed = false
	r.lastReconnect = time.Time{} // allow immediate reconnection
}
