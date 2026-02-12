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
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/ubtemit"
	"github.com/ethereum/go-ethereum/rpc"
)

// mockOutboxAPI implements the ubt_* namespace consumed by OutboxReader.
type mockOutboxAPI struct {
	mu        sync.Mutex
	events    map[uint64]*ubtemit.OutboxEnvelope
	latestSeq uint64
	dumpPages map[string]*rpcStateDump

	failGetEvent  bool
	failLatestSeq bool
	failCompact   bool
	pruneBelow    uint64
	responseDelay time.Duration

	compactCalls []uint64 // records belowSeq values from CompactOutboxBelow calls
}

func newMockOutboxAPI() *mockOutboxAPI {
	return &mockOutboxAPI{
		events:    make(map[uint64]*ubtemit.OutboxEnvelope),
		dumpPages: make(map[string]*rpcStateDump),
	}
}

func mockBlockHash(blockNumber uint64) common.Hash {
	var h common.Hash
	binary.BigEndian.PutUint64(h[24:], blockNumber)
	return h
}

func (m *mockOutboxAPI) addEnvelope(env *ubtemit.OutboxEnvelope) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cp := *env
	cp.Payload = append([]byte(nil), env.Payload...)
	m.events[cp.Seq] = &cp
	if cp.Seq > m.latestSeq {
		m.latestSeq = cp.Seq
	}
}

func (m *mockOutboxAPI) addDiff(t *testing.T, seq, blockNum uint64, addr common.Address, nonce uint64, balance *big.Int) {
	t.Helper()

	payload, err := ubtemit.EncodeDiff(makeDiff(addr, nonce, balance))
	if err != nil {
		t.Fatalf("EncodeDiff(seq=%d): %v", seq, err)
	}
	env := &ubtemit.OutboxEnvelope{
		Seq:         seq,
		Version:     ubtemit.EnvelopeVersionV1,
		Kind:        ubtemit.KindDiff,
		BlockNumber: blockNum,
		BlockHash:   mockBlockHash(blockNum),
		ParentHash:  mockBlockHash(blockNum - 1),
		Timestamp:   uint64(time.Now().Unix()),
		Payload:     payload,
	}
	m.addEnvelope(env)
}

func (m *mockOutboxAPI) addReorg(t *testing.T, seq uint64, fromBlock, toBlock, ancestor uint64) {
	t.Helper()

	marker := ubtemit.NewReorgMarker(
		fromBlock, mockBlockHash(fromBlock),
		toBlock, mockBlockHash(toBlock),
		ancestor, mockBlockHash(ancestor),
	)
	payload, err := ubtemit.EncodeReorgMarker(marker)
	if err != nil {
		t.Fatalf("EncodeReorgMarker(seq=%d): %v", seq, err)
	}
	env := &ubtemit.OutboxEnvelope{
		Seq:         seq,
		Version:     ubtemit.EnvelopeVersionV1,
		Kind:        ubtemit.KindReorg,
		BlockNumber: toBlock,
		BlockHash:   mockBlockHash(toBlock),
		ParentHash:  mockBlockHash(toBlock - 1),
		Timestamp:   uint64(time.Now().Unix()),
		Payload:     payload,
	}
	m.addEnvelope(env)
}

func (m *mockOutboxAPI) setPruneBelow(seq uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pruneBelow = seq
}

func (m *mockOutboxAPI) setFailGetEvent(v bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failGetEvent = v
}

func (m *mockOutboxAPI) setFailLatestSeq(v bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failLatestSeq = v
}

func (m *mockOutboxAPI) setResponseDelay(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.responseDelay = d
}

func (m *mockOutboxAPI) setDumpPage(start hexutil.Bytes, dump *rpcStateDump) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dumpPages[hexutil.Encode(start)] = dump
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (m *mockOutboxAPI) GetEvent(ctx context.Context, seq hexutil.Uint64) (*rpcOutboxEvent, error) {
	m.mu.Lock()
	failGet := m.failGetEvent
	pruneBelow := m.pruneBelow
	delay := m.responseDelay
	env := m.events[uint64(seq)]
	m.mu.Unlock()

	if err := sleepWithContext(ctx, delay); err != nil {
		return nil, err
	}
	if failGet {
		return nil, errors.New("mock fault: GetEvent failed")
	}
	if uint64(seq) < pruneBelow || env == nil {
		return nil, nil
	}
	return &rpcOutboxEvent{
		Seq:         hexutil.Uint64(env.Seq),
		Version:     hexutil.Uint(env.Version),
		Kind:        env.Kind,
		BlockNumber: hexutil.Uint64(env.BlockNumber),
		BlockHash:   env.BlockHash,
		ParentHash:  env.ParentHash,
		Timestamp:   hexutil.Uint64(env.Timestamp),
		Payload:     hexutil.Bytes(env.Payload),
	}, nil
}

func (m *mockOutboxAPI) GetEvents(ctx context.Context, fromSeq, toSeq hexutil.Uint64) ([]rpcOutboxEvent, error) {
	from := uint64(fromSeq)
	to := uint64(toSeq)
	if from > to {
		return nil, fmt.Errorf("invalid range: from %d > to %d", from, to)
	}

	m.mu.Lock()
	failGet := m.failGetEvent
	pruneBelow := m.pruneBelow
	delay := m.responseDelay
	m.mu.Unlock()

	if err := sleepWithContext(ctx, delay); err != nil {
		return nil, err
	}
	if failGet {
		return nil, errors.New("mock fault: GetEvents failed")
	}

	events := make([]rpcOutboxEvent, 0, to-from+1)
	for seq := from; seq <= to; seq++ {
		if seq < pruneBelow {
			continue
		}
		m.mu.Lock()
		env := m.events[seq]
		m.mu.Unlock()
		if env == nil {
			continue
		}
		events = append(events, rpcOutboxEvent{
			Seq:         hexutil.Uint64(env.Seq),
			Version:     hexutil.Uint(env.Version),
			Kind:        env.Kind,
			BlockNumber: hexutil.Uint64(env.BlockNumber),
			BlockHash:   env.BlockHash,
			ParentHash:  env.ParentHash,
			Timestamp:   hexutil.Uint64(env.Timestamp),
			Payload:     hexutil.Bytes(env.Payload),
		})
	}
	return events, nil
}

func (m *mockOutboxAPI) LatestSeq(ctx context.Context) (hexutil.Uint64, error) {
	m.mu.Lock()
	failLatest := m.failLatestSeq
	delay := m.responseDelay
	latest := m.latestSeq
	m.mu.Unlock()

	if err := sleepWithContext(ctx, delay); err != nil {
		return 0, err
	}
	if failLatest {
		return 0, errors.New("mock fault: LatestSeq failed")
	}
	return hexutil.Uint64(latest), nil
}

func (m *mockOutboxAPI) Status(ctx context.Context) (map[string]any, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return map[string]any{
		"enabled":    true,
		"latestSeq":  m.latestSeq,
		"pruneBelow": m.pruneBelow,
	}, nil
}

func (m *mockOutboxAPI) CompactOutboxBelow(ctx context.Context, safeSeq hexutil.Uint64) (map[string]any, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.failCompact {
		return nil, errors.New("mock fault: CompactOutboxBelow failed")
	}
	m.compactCalls = append(m.compactCalls, uint64(safeSeq))
	return map[string]any{
		"deleted": uint64(safeSeq),
		"safeSeq": uint64(safeSeq),
	}, nil
}

func (m *mockOutboxAPI) setFailCompact(v bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failCompact = v
}

func (m *mockOutboxAPI) getCompactCalls() []uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]uint64, len(m.compactCalls))
	copy(out, m.compactCalls)
	return out
}

func (m *mockOutboxAPI) AccountRange(ctx context.Context, blockNrOrHash rpc.BlockNumberOrHash, start hexutil.Bytes, maxResults int, nocode, nostorage, incompletes bool) (*rpcStateDump, error) {
	m.mu.Lock()
	page := m.dumpPages[hexutil.Encode(start)]
	delay := m.responseDelay
	m.mu.Unlock()

	if err := sleepWithContext(ctx, delay); err != nil {
		return nil, err
	}
	if page != nil {
		return page, nil
	}
	return &rpcStateDump{Accounts: map[string]rpcDumpAccount{}}, nil
}

type mockOutboxServer struct {
	api       *mockOutboxAPI
	server    *rpc.Server
	listener  net.Listener
	httpSrv   *http.Server
	closeOnce sync.Once
}

func newMockOutboxServer(t *testing.T) *mockOutboxServer {
	t.Helper()

	server := rpc.NewServer()
	api := newMockOutboxAPI()
	if err := server.RegisterName("ubt", api); err != nil {
		t.Fatalf("register mock outbox API: %v", err)
	}
	if err := server.RegisterName("debug", api); err != nil {
		t.Fatalf("register mock debug API: %v", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen mock outbox RPC: %v", err)
	}
	httpSrv := &http.Server{Handler: server}
	go func() {
		_ = httpSrv.Serve(listener)
	}()

	s := &mockOutboxServer{
		api:      api,
		server:   server,
		listener: listener,
		httpSrv:  httpSrv,
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func (s *mockOutboxServer) Endpoint() string {
	return "http://" + s.listener.Addr().String()
}

func (s *mockOutboxServer) Close() {
	s.closeOnce.Do(func() {
		if s.httpSrv != nil {
			_ = s.httpSrv.Close()
		}
		if s.listener != nil {
			_ = s.listener.Close()
		}
	})
}

func defaultTestConfig(endpoint, dataDir string) *Config {
	return &Config{
		OutboxRPCEndpoint:        endpoint,
		DataDir:                  dataDir,
		ApplyCommitInterval:      100,
		ApplyCommitMaxLatency:    time.Hour,
		BootstrapMode:            "backfill-direct",
		MaxRecoverableReorgDepth: 128,
		TrieDBScheme:             "path",
		TrieDBStateHistory:       128,
	}
}

func newTestConsumerWithConfig(t *testing.T, cfg *Config) *Consumer {
	t.Helper()

	c, err := NewConsumer(cfg)
	if err != nil {
		t.Fatalf("NewConsumer: %v", err)
	}
	c.reader.timeout = 2 * time.Second
	c.reader.reconnectDelay = 25 * time.Millisecond
	return c
}

func newTestConsumer(t *testing.T, endpoint, dataDir string) *Consumer {
	t.Helper()
	return newTestConsumerWithConfig(t, defaultTestConfig(endpoint, dataDir))
}
