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
	reconnectMin   time.Duration
	reconnectMax   time.Duration
	reconnectFails uint32
	prefetchBatch  uint64
	prefetchCache  map[uint64]*ubtemit.OutboxEnvelope
}

// NewOutboxReader creates a new outbox reader for the given RPC endpoint.
func NewOutboxReader(endpoint string) *OutboxReader {
	return &OutboxReader{
		endpoint:       endpoint,
		timeout:        30 * time.Second,
		reconnectDelay: 250 * time.Millisecond,
		reconnectMin:   250 * time.Millisecond,
		reconnectMax:   5 * time.Second,
		prefetchBatch:  1,
		prefetchCache:  make(map[uint64]*ubtemit.OutboxEnvelope),
	}
}

// SetPrefetchBatch configures how many events ReadEvent should fetch in one RPC call.
// A value of 1 disables prefetch. The value is clamped to [1, 1000].
func (r *OutboxReader) SetPrefetchBatch(batch uint64) {
	if batch == 0 {
		batch = 1
	}
	if batch > 1000 {
		batch = 1000
	}
	r.mu.Lock()
	r.prefetchBatch = batch
	r.prefetchCache = make(map[uint64]*ubtemit.OutboxEnvelope)
	r.mu.Unlock()
}

func (r *OutboxReader) prefetchConfig() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.prefetchBatch == 0 {
		return 1
	}
	return r.prefetchBatch
}

func (r *OutboxReader) getPrefetched(seq uint64) (*ubtemit.OutboxEnvelope, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	env, ok := r.prefetchCache[seq]
	if ok {
		delete(r.prefetchCache, seq)
	}
	return env, ok
}

func (r *OutboxReader) fillPrefetch(envs []*ubtemit.OutboxEnvelope) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Reset cache each fill to keep memory bounded and sequence-local.
	r.prefetchCache = make(map[uint64]*ubtemit.OutboxEnvelope, len(envs))
	for _, env := range envs {
		r.prefetchCache[env.Seq] = env
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
		r.bumpReconnectDelayLocked()
		r.lastReconnect = time.Now()
		return fmt.Errorf("failed to connect to %s: %w", r.endpoint, err)
	}
	r.client = client
	r.reconnectFails = 0
	r.reconnectDelay = r.reconnectMin
	r.lastReconnect = time.Now()
	log.Info("Connected to geth outbox RPC", "endpoint", r.endpoint)
	return nil
}

func (r *OutboxReader) bumpReconnectDelayLocked() {
	r.reconnectFails++
	delay := r.reconnectMin
	for i := uint32(0); i < r.reconnectFails; i++ {
		delay *= 2
		if delay >= r.reconnectMax {
			delay = r.reconnectMax
			break
		}
	}
	if delay < r.reconnectMin {
		delay = r.reconnectMin
	}
	if delay > r.reconnectMax {
		delay = r.reconnectMax
	}
	r.reconnectDelay = delay
}

// getClient returns the current RPC client, connecting if necessary.
func (r *OutboxReader) getClient() (*rpc.Client, error) {
	return r.acquireClient()
}

// dialWithBackoff establishes a connection while honoring reconnectDelay.
func (r *OutboxReader) dialWithBackoff() error {
	for {
		var (
			waitTime time.Duration
			closed   bool
		)
		r.mu.Lock()
		if r.client != nil {
			r.mu.Unlock()
			return nil
		}
		closed = r.closed
		if !closed {
			timeSinceReconnect := time.Since(r.lastReconnect)
			if timeSinceReconnect < r.reconnectDelay {
				waitTime = r.reconnectDelay - timeSinceReconnect
			}
		}
		r.mu.Unlock()
		if closed {
			return fmt.Errorf("outbox reader is closed")
		}
		if waitTime > 0 {
			log.Debug("Throttling reconnection attempt", "endpoint", r.endpoint, "wait", waitTime)
			time.Sleep(waitTime)
			continue
		}

		r.mu.Lock()
		err := r.connectLocked()
		r.mu.Unlock()
		if err != nil {
			return err
		}
		return nil
	}
}

// acquireClient returns an active client, reconnecting if needed.
func (r *OutboxReader) acquireClient() (*rpc.Client, error) {
	r.mu.Lock()
	if r.client != nil {
		c := r.client
		r.mu.Unlock()
		return c, nil
	}
	r.mu.Unlock()

	if err := r.dialWithBackoff(); err != nil {
		return nil, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.client == nil {
		return nil, fmt.Errorf("outbox reader has no active RPC client")
	}
	return r.client, nil
}

// ReadEvent reads an outbox event by sequence number.
// Returns nil if no event exists at the given sequence.
func (r *OutboxReader) ReadEvent(seq uint64) (*ubtemit.OutboxEnvelope, error) {
	if env, ok := r.getPrefetched(seq); ok {
		return env, nil
	}

	batch := r.prefetchConfig()
	if batch > 1 {
		to := seq + batch - 1
		if to < seq { // overflow guard
			to = ^uint64(0)
		}
		envs, err := r.ReadRange(seq, to)
		if err != nil {
			return nil, err
		}
		if len(envs) == 0 {
			return nil, nil
		}
		// If the first returned seq is not the target, there is a gap at target seq.
		// Preserve old behavior by reporting target as missing.
		if envs[0].Seq != seq {
			return nil, nil
		}
		r.fillPrefetch(envs)
		if env, ok := r.getPrefetched(seq); ok {
			return env, nil
		}
		return nil, nil
	}

	client, err := r.getClient()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()

	var result *rpcOutboxEvent
	err = client.CallContext(ctx, &result, "ubt_getEvent", hexutil.Uint64(seq))
	if err != nil {
		r.resetConnection(err, "ubt_getEvent")
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
		r.resetConnection(err, "ubt_getEvents")
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
		r.resetConnection(err, "ubt_latestSeq")
		return 0, fmt.Errorf("ubt_latestSeq: %w", err)
	}
	return uint64(result), nil
}

func parseRPCUint64(value any) (uint64, bool) {
	switch v := value.(type) {
	case string:
		if len(v) > 2 && (v[:2] == "0x" || v[:2] == "0X") {
			n, err := hexutil.DecodeUint64(v)
			if err != nil {
				return 0, false
			}
			return n, true
		}
		var n uint64
		_, err := fmt.Sscanf(v, "%d", &n)
		if err != nil {
			return 0, false
		}
		return n, true
	case float64:
		if v < 0 {
			return 0, false
		}
		return uint64(v), true
	case int:
		if v < 0 {
			return 0, false
		}
		return uint64(v), true
	case int64:
		if v < 0 {
			return 0, false
		}
		return uint64(v), true
	case uint64:
		return v, true
	case hexutil.Uint64:
		return uint64(v), true
	default:
		return 0, false
	}
}

// LowestSeq returns the lowest retained sequence from outbox status.
// Returns 0 when the field is unavailable.
func (r *OutboxReader) LowestSeq() (uint64, error) {
	client, err := r.getClient()
	if err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()

	var status map[string]any
	err = client.CallContext(ctx, &status, "ubt_status")
	if err != nil {
		r.resetConnection(err, "ubt_status")
		return 0, fmt.Errorf("ubt_status: %w", err)
	}
	raw, ok := status["lowestSeq"]
	if !ok {
		return 0, nil
	}
	if seq, ok := parseRPCUint64(raw); ok {
		return seq, nil
	}
	return 0, nil
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
		r.resetConnection(err, "debug_accountRange")
		return nil, fmt.Errorf("debug_accountRange: %w", err)
	}
	return &result, nil
}

// resetConnection closes the current client to force reconnection on next call.
// This handles scenarios where the RPC server was restarted or the network dropped.
func (r *OutboxReader) resetConnection(err error, reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.client != nil {
		r.client.Close()
		r.client = nil
		r.bumpReconnectDelayLocked()
		r.lastReconnect = time.Now()
		log.Warn("UBT outbox RPC connection reset", "endpoint", r.endpoint, "reason", reason, "err", err, "ts", r.lastReconnect, "nextReconnectDelay", r.reconnectDelay)
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
	r.reconnectFails = 0
	r.reconnectDelay = r.reconnectMin
	r.lastReconnect = time.Time{} // allow immediate reconnection
}
