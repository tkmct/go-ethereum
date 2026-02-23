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
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/ubtemit"
	"github.com/ethereum/go-ethereum/core/ubtwal"
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
	mu             sync.RWMutex
	closed         bool
	timeout        time.Duration
	lastReconnect  time.Time
	reconnectDelay time.Duration // minimum delay between reconnection attempts
	reconnectMin   time.Duration
	reconnectMax   time.Duration
	reconnectFails uint32

	// Source selection. "rpc" is the default and "wal" uses a shared file WAL.
	source             string
	walReader          *ubtwal.Reader
	walRefreshInterval time.Duration
	lastWALRefresh     time.Time
}

// NewOutboxReader creates a new outbox reader for the given RPC endpoint.
func NewOutboxReader(endpoint string) *OutboxReader {
	return &OutboxReader{
		endpoint:           endpoint,
		timeout:            30 * time.Second,
		reconnectDelay:     250 * time.Millisecond,
		reconnectMin:       250 * time.Millisecond,
		reconnectMax:       5 * time.Second,
		source:             "rpc",
		walRefreshInterval: 250 * time.Millisecond,
	}
}

// EnableWALSource switches event reads to WAL mode.
// RPC remains available for validation/replay/compaction calls.
func (r *OutboxReader) EnableWALSource(dir string, refreshInterval time.Duration) error {
	reader, err := ubtwal.OpenReader(dir)
	if err != nil {
		return err
	}
	if refreshInterval <= 0 {
		refreshInterval = 250 * time.Millisecond
	}
	r.mu.Lock()
	r.source = "wal"
	r.walReader = reader
	r.walRefreshInterval = refreshInterval
	r.lastWALRefresh = time.Time{}
	r.mu.Unlock()
	return nil
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
	if r.walEnabled() {
		return r.readEventWAL(seq)
	}
	return r.readEventRPC(seq)
}

// ReadRange reads outbox events in [fromSeq, toSeq].
func (r *OutboxReader) ReadRange(fromSeq, toSeq uint64) ([]*ubtemit.OutboxEnvelope, error) {
	if r.walEnabled() {
		return r.readRangeWAL(fromSeq, toSeq)
	}
	return r.readRangeRPC(fromSeq, toSeq)
}

func (r *OutboxReader) readRangeRPC(fromSeq, toSeq uint64) ([]*ubtemit.OutboxEnvelope, error) {
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
	if r.walEnabled() {
		if err := r.refreshWAL(true); err != nil {
			log.Warn("WAL refresh failed for latest seq, falling back to RPC", "err", err)
			return r.latestSeqRPC()
		}
		r.mu.Lock()
		walReader := r.walReader
		r.mu.Unlock()
		if walReader == nil {
			return r.latestSeqRPC()
		}
		seq, ok := walReader.LatestSeq()
		if !ok {
			return r.latestSeqRPC()
		}
		return seq, nil
	}
	return r.latestSeqRPC()
}

func (r *OutboxReader) latestSeqRPC() (uint64, error) {
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
	if r.walEnabled() {
		if err := r.refreshWAL(true); err != nil {
			log.Warn("WAL refresh failed for lowest seq, falling back to RPC", "err", err)
			return r.lowestSeqRPC()
		}
		r.mu.Lock()
		walReader := r.walReader
		r.mu.Unlock()
		if walReader == nil {
			return r.lowestSeqRPC()
		}
		seq, ok := walReader.LowestSeq()
		if !ok {
			return r.lowestSeqRPC()
		}
		return seq, nil
	}
	return r.lowestSeqRPC()
}

func (r *OutboxReader) lowestSeqRPC() (uint64, error) {
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

func (r *OutboxReader) readEventRPC(seq uint64) (*ubtemit.OutboxEnvelope, error) {
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

func (r *OutboxReader) walEnabled() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.source == "wal" && r.walReader != nil
}

func (r *OutboxReader) refreshWAL(force bool) error {
	r.mu.Lock()
	if r.source != "wal" || r.walReader == nil {
		r.mu.Unlock()
		return nil
	}
	reader := r.walReader
	interval := r.walRefreshInterval
	last := r.lastWALRefresh
	r.mu.Unlock()

	if !force && time.Since(last) < interval {
		return nil
	}
	if err := reader.Refresh(); err != nil {
		return err
	}
	r.mu.Lock()
	if r.source == "wal" && r.walReader == reader {
		r.lastWALRefresh = time.Now()
	}
	r.mu.Unlock()
	return nil
}

func (r *OutboxReader) readEventWAL(seq uint64) (*ubtemit.OutboxEnvelope, error) {
	if err := r.refreshWAL(false); err != nil {
		log.Warn("WAL refresh failed for event read, falling back to RPC", "seq", seq, "err", err)
		return r.readEventRPC(seq)
	}
	r.mu.Lock()
	reader := r.walReader
	r.mu.Unlock()
	if reader == nil {
		return r.readEventRPC(seq)
	}
	payload, err := reader.Read(seq)
	if errors.Is(err, ubtwal.ErrRecordNotFound) {
		// Force one refresh before giving up to reduce stale-index misses.
		if err := r.refreshWAL(true); err != nil {
			return nil, err
		}
		payload, err = reader.Read(seq)
	}
	if errors.Is(err, ubtwal.ErrRecordNotFound) {
		// WAL can lag or miss records transiently; verify against RPC before
		// returning "not found" to the consumer.
		return r.readEventRPC(seq)
	}
	if err != nil {
		log.Warn("WAL read failed for event, falling back to RPC", "seq", seq, "err", err)
		return r.readEventRPC(seq)
	}
	env, err := ubtemit.DecodeEnvelope(payload)
	if err != nil {
		return nil, fmt.Errorf("decode wal envelope seq=%d: %w", seq, err)
	}
	return env, nil
}

func (r *OutboxReader) readRangeWAL(fromSeq, toSeq uint64) ([]*ubtemit.OutboxEnvelope, error) {
	if err := r.refreshWAL(false); err != nil {
		log.Warn("WAL refresh failed for range read, falling back to RPC", "from", fromSeq, "to", toSeq, "err", err)
		return r.readRangeRPC(fromSeq, toSeq)
	}
	r.mu.Lock()
	reader := r.walReader
	r.mu.Unlock()
	if reader == nil {
		return r.readRangeRPC(fromSeq, toSeq)
	}
	payloads, err := reader.ReadRange(fromSeq, toSeq)
	if err != nil {
		log.Warn("WAL read failed for range, falling back to RPC", "from", fromSeq, "to", toSeq, "err", err)
		return r.readRangeRPC(fromSeq, toSeq)
	}
	envs := make([]*ubtemit.OutboxEnvelope, 0, len(payloads))
	for i, payload := range payloads {
		env, err := ubtemit.DecodeEnvelope(payload)
		if err != nil {
			seq := fromSeq + uint64(i)
			return nil, fmt.Errorf("decode wal envelope seq=%d: %w", seq, err)
		}
		envs = append(envs, env)
	}
	// WAL ReadRange stops at first gap; complete tail via RPC so callers get the
	// same semantics as RPC source mode when RPC indicates more events exist.
	if fromSeq <= toSeq && uint64(len(envs)) < (toSeq-fromSeq+1) {
		nextSeq := fromSeq + uint64(len(envs))
		latestSeq, err := r.latestSeqRPC()
		if err != nil || latestSeq < nextSeq {
			return envs, nil
		}
		fallbackEnvs, err := r.readRangeRPC(nextSeq, toSeq)
		if err != nil {
			return nil, err
		}
		envs = append(envs, fallbackEnvs...)
	}
	return envs, nil
}
