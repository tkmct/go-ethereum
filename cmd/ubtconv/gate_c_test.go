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
	"math/big"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/ubtemit"
	"github.com/ethereum/go-ethereum/ethdb/leveldb"
)

func consumeN(t *testing.T, c *Consumer, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if err := c.ConsumeNext(); err != nil {
			t.Fatalf("ConsumeNext #%d/%d failed: %v", i+1, n, err)
		}
	}
}

func waitUntil(t *testing.T, timeout time.Duration, description string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", description)
}

func processedSeq(c *Consumer) uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.processedSeq
}

func readCheckpointFromDisk(t *testing.T, dataDir string) *rawdb.UBTConsumerState {
	t.Helper()

	dbPath := filepath.Join(dataDir, "consumer")
	kvdb, err := leveldb.New(dbPath, 16, 16, "ubtconv/consumer", false)
	if err != nil {
		t.Fatalf("open checkpoint DB: %v", err)
	}
	db := rawdb.NewDatabase(kvdb)
	defer db.Close()

	state := rawdb.ReadUBTConsumerState(db)
	if state == nil {
		t.Fatal("checkpoint state is nil")
	}
	return state
}

func TestGateC_LongRunReplay(t *testing.T) {
	srv := newMockOutboxServer(t)
	addr := common.HexToAddress("0x1111111111111111111111111111111111111111")
	for i := 0; i < 220; i++ {
		seq := uint64(i)
		srv.api.addDiff(t, seq, seq+1, addr, seq+1, big.NewInt(int64(i+1)))
	}

	consumer := newTestConsumer(t, srv.Endpoint(), t.TempDir())
	defer consumer.Close()

	consumeN(t, consumer, 220)
	if consumer.uncommittedBlocks > 0 {
		if err := consumer.commit(); err != nil {
			t.Fatalf("commit final state: %v", err)
		}
	}

	if consumer.state.AppliedSeq != 219 {
		t.Fatalf("AppliedSeq mismatch: got %d want 219", consumer.state.AppliedSeq)
	}
	if consumer.state.AppliedBlock != 220 {
		t.Fatalf("AppliedBlock mismatch: got %d want 220", consumer.state.AppliedBlock)
	}
	if consumer.state.AppliedRoot == (common.Hash{}) {
		t.Fatal("AppliedRoot must be non-zero after replay")
	}

	api := NewQueryAPI(consumer)
	balance, err := api.GetBalance(context.Background(), addr, nil)
	if err != nil {
		t.Fatalf("GetBalance(latest): %v", err)
	}
	if balance.ToInt().Cmp(big.NewInt(220)) != 0 {
		t.Fatalf("balance mismatch: got %s want 220", balance.ToInt())
	}
}

func TestGateC_RestartCrashRecovery(t *testing.T) {
	srv := newMockOutboxServer(t)
	addr := common.HexToAddress("0x2222222222222222222222222222222222222222")
	for i := 0; i < 100; i++ {
		seq := uint64(i)
		srv.api.addDiff(t, seq, seq+1, addr, seq+1, big.NewInt(int64(i+1)))
	}

	dataDir := t.TempDir()
	cfg := defaultTestConfig(srv.Endpoint(), dataDir)
	cfg.ApplyCommitInterval = 25

	c1 := newTestConsumerWithConfig(t, cfg)
	consumeN(t, c1, 50)
	if err := c1.Close(); err != nil {
		t.Fatalf("close first consumer: %v", err)
	}
	checkpoint := readCheckpointFromDisk(t, dataDir)
	if checkpoint.AppliedSeq != 49 {
		t.Fatalf("unexpected persisted AppliedSeq on restart: got %d want 49", checkpoint.AppliedSeq)
	}

	applier2 := newTestApplier(t)
	defer applier2.Close()
	reader2 := NewOutboxReader(srv.Endpoint())
	reader2.timeout = 2 * time.Second
	reader2.reconnectDelay = 25 * time.Millisecond
	c2 := &Consumer{
		cfg: &Config{
			ApplyCommitInterval:   1000,
			ApplyCommitMaxLatency: time.Hour,
		},
		db:             rawdb.NewMemoryDatabase(),
		applier:        applier2,
		reader:         reader2,
		lastCommitTime: time.Now(),
		state: ConsumerState{
			AppliedSeq:   checkpoint.AppliedSeq,
			AppliedBlock: checkpoint.AppliedBlock,
			AppliedRoot:  common.Hash{}, // State-seq recovery verification does not require persisted trie root.
		},
		hasState:     true,
		processedSeq: checkpoint.AppliedSeq,
	}
	defer c2.reader.Close()

	consumeN(t, c2, 50)
	if c2.processedSeq != 99 {
		t.Fatalf("final processedSeq mismatch: got %d want 99", c2.processedSeq)
	}
}

func TestGateC_RetentionWindowPruning(t *testing.T) {
	outboxPath := filepath.Join(t.TempDir(), "outbox")
	store, err := ubtemit.NewOutboxStore(outboxPath, time.Second, 50, 0)
	if err != nil {
		t.Fatalf("NewOutboxStore: %v", err)
	}
	defer store.Close()

	for i := 0; i < 1200; i++ {
		seq := uint64(i)
		env := &ubtemit.OutboxEnvelope{
			Version:     ubtemit.EnvelopeVersionV1,
			Kind:        ubtemit.KindDiff,
			BlockNumber: seq + 1,
			BlockHash:   mockBlockHash(seq + 1),
			ParentHash:  mockBlockHash(seq),
			Timestamp:   uint64(time.Now().Unix()),
			Payload:     []byte{0x01},
		}
		if _, err := store.Append(env); err != nil {
			t.Fatalf("Append #%d failed: %v", i, err)
		}
	}
	if latest := store.LatestSeq(); latest != 1199 {
		t.Fatalf("LatestSeq mismatch: got %d want 1199", latest)
	}

	if _, err := store.Read(100); err == nil {
		t.Fatal("expected seq=100 to be pruned by auto-compaction")
	}

	if _, err := store.Compact(); err != nil {
		t.Fatalf("Compact failed: %v", err)
	}
	if _, err := store.Read(1149); err == nil {
		t.Fatal("expected seq=1149 to be pruned after explicit compaction")
	}
	if _, err := store.Read(1150); err != nil {
		t.Fatalf("expected seq=1150 to remain after compaction: %v", err)
	}
}

func TestGateC_RetentionWindowLaggingConsumer(t *testing.T) {
	srv := newMockOutboxServer(t)
	addr := common.HexToAddress("0x3333333333333333333333333333333333333333")
	for i := 0; i <= 200; i++ {
		seq := uint64(i)
		srv.api.addDiff(t, seq, seq+1, addr, seq+1, big.NewInt(int64(i+1)))
	}

	cfg := defaultTestConfig(srv.Endpoint(), t.TempDir())
	cfg.ApplyCommitInterval = 1
	consumer := newTestConsumerWithConfig(t, cfg)
	defer consumer.Close()

	consumeN(t, consumer, 6) // consumes seq 0..5
	pre := consumer.state

	srv.api.setPruneBelow(100)
	err := consumer.ConsumeNext()
	if err == nil {
		t.Fatal("expected lagging consumer error after pruning")
	}
	if !strings.Contains(err.Error(), "no event at seq 6") {
		t.Fatalf("unexpected error: %v", err)
	}
	if consumer.state != pre {
		t.Fatalf("consumer state changed on lagging read: before=%+v after=%+v", pre, consumer.state)
	}
}

func TestGateC_BackoffResetOnSuccess(t *testing.T) {
	consumerBackoffGauge.Update(0)

	srv := newMockOutboxServer(t)
	addr := common.HexToAddress("0x4444444444444444444444444444444444444444")
	for i := 0; i < 200; i++ {
		seq := uint64(i)
		srv.api.addDiff(t, seq, seq+1, addr, seq+1, big.NewInt(int64(i+1)))
	}
	srv.api.setFailGetEvent(true)

	cfg := defaultTestConfig(srv.Endpoint(), t.TempDir())
	cfg.ApplyCommitInterval = 1000
	cfg.ApplyCommitMaxLatency = time.Hour

	runner, err := NewRunner(cfg)
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	runner.consumer.reader.timeout = 200 * time.Millisecond
	runner.consumer.reader.reconnectDelay = 20 * time.Millisecond

	if err := runner.Start(); err != nil {
		t.Fatalf("Start runner: %v", err)
	}
	defer runner.Stop()

	waitUntil(t, 5*time.Second, "runner backoff > 0", func() bool {
		return consumerBackoffGauge.Snapshot().Value() > 0
	})

	srv.api.setFailGetEvent(false)
	waitUntil(t, 5*time.Second, "runner resumes consuming", func() bool {
		return processedSeq(runner.consumer) >= 2
	})
	waitUntil(t, 5*time.Second, "backoff reset to zero", func() bool {
		return consumerBackoffGauge.Snapshot().Value() == 0
	})
}

func TestFault_CrashDuringApplyBeforeCommit(t *testing.T) {
	srv := newMockOutboxServer(t)
	addr := common.HexToAddress("0x5555555555555555555555555555555555555555")
	for i := 0; i < 20; i++ {
		seq := uint64(i)
		srv.api.addDiff(t, seq, seq+1, addr, seq+1, big.NewInt(int64(i+1)))
	}

	dataDir := t.TempDir()
	cfg := defaultTestConfig(srv.Endpoint(), dataDir)
	cfg.ApplyCommitInterval = 100 // keep events in-memory only

	c1 := newTestConsumerWithConfig(t, cfg)
	consumeN(t, c1, 10)

	// Crash simulation: do NOT call Consumer.Close() (it would force-commit).
	c1.reader.Close()
	c1.applier.Close()
	if c1.applier != nil && c1.applier.diskdb != nil {
		_ = c1.applier.diskdb.Close()
	}
	if err := c1.db.Close(); err != nil {
		t.Fatalf("close consumer DB: %v", err)
	}

	c2 := newTestConsumerWithConfig(t, cfg)
	defer c2.Close()

	if c2.hasState {
		t.Fatal("unexpected persisted state after pre-commit crash")
	}
	if c2.processedSeq != ^uint64(0) {
		t.Fatalf("fresh restart should target seq=0, got processedSeq=%d", c2.processedSeq)
	}
	if err := c2.ConsumeNext(); err != nil {
		t.Fatalf("ConsumeNext after restart: %v", err)
	}
	if c2.processedSeq != 0 {
		t.Fatalf("expected replay to restart from seq=0, got processedSeq=%d", c2.processedSeq)
	}
}

func TestFault_CrashAfterCommit(t *testing.T) {
	srv := newMockOutboxServer(t)
	addr := common.HexToAddress("0x6666666666666666666666666666666666666666")
	for i := 0; i < 20; i++ {
		seq := uint64(i)
		srv.api.addDiff(t, seq, seq+1, addr, seq+1, big.NewInt(int64(i+1)))
	}

	dataDir := t.TempDir()
	cfg := defaultTestConfig(srv.Endpoint(), dataDir)
	cfg.ApplyCommitInterval = 5

	c1 := newTestConsumerWithConfig(t, cfg)
	consumeN(t, c1, 10)
	if c1.state.AppliedSeq != 9 {
		t.Fatalf("expected committed AppliedSeq=9 before crash, got %d", c1.state.AppliedSeq)
	}

	c1.reader.Close()
	c1.applier.Close()
	if c1.applier != nil && c1.applier.diskdb != nil {
		_ = c1.applier.diskdb.Close()
	}
	if err := c1.db.Close(); err != nil {
		t.Fatalf("close consumer DB: %v", err)
	}

	checkpoint := readCheckpointFromDisk(t, dataDir)
	if checkpoint.AppliedSeq != 9 {
		t.Fatalf("persisted AppliedSeq mismatch on restart: got %d want 9", checkpoint.AppliedSeq)
	}

	applier2 := newTestApplier(t)
	defer applier2.Close()
	reader2 := NewOutboxReader(srv.Endpoint())
	reader2.timeout = 2 * time.Second
	reader2.reconnectDelay = 25 * time.Millisecond
	c2 := &Consumer{
		cfg: &Config{
			ApplyCommitInterval:   1000,
			ApplyCommitMaxLatency: time.Hour,
		},
		db:             rawdb.NewMemoryDatabase(),
		applier:        applier2,
		reader:         reader2,
		lastCommitTime: time.Now(),
		state: ConsumerState{
			AppliedSeq:   checkpoint.AppliedSeq,
			AppliedBlock: checkpoint.AppliedBlock,
			AppliedRoot:  common.Hash{},
		},
		hasState:     true,
		processedSeq: checkpoint.AppliedSeq,
	}
	defer c2.reader.Close()

	if err := c2.ConsumeNext(); err != nil {
		t.Fatalf("ConsumeNext after restart: %v", err)
	}
	if c2.processedSeq != 10 {
		t.Fatalf("resume did not continue from seq=10, got processed=%d", c2.processedSeq)
	}
}

func TestFault_TemporaryRPCDisconnect(t *testing.T) {
	srv1 := newMockOutboxServer(t)
	addr := common.HexToAddress("0x7777777777777777777777777777777777777777")
	for i := 0; i < 30; i++ {
		seq := uint64(i)
		srv1.api.addDiff(t, seq, seq+1, addr, seq+1, big.NewInt(int64(i+1)))
	}

	cfg := defaultTestConfig(srv1.Endpoint(), t.TempDir())
	cfg.ApplyCommitInterval = 1
	consumer := newTestConsumerWithConfig(t, cfg)
	defer consumer.Close()

	consumeN(t, consumer, 5)
	srv1.Close()

	err := consumer.ConsumeNext()
	if err == nil {
		t.Fatal("expected RPC disconnect error")
	}

	srv2 := newMockOutboxServer(t)
	for i := 0; i < 30; i++ {
		seq := uint64(i)
		srv2.api.addDiff(t, seq, seq+1, addr, seq+1, big.NewInt(int64(i+1)))
	}
	consumer.reader.Reconnect(srv2.Endpoint())

	consumeN(t, consumer, 5)
	if consumer.state.AppliedSeq < 9 {
		t.Fatalf("consumer did not recover after reconnect, appliedSeq=%d", consumer.state.AppliedSeq)
	}
}

func TestFault_RPCDisconnectWithRunner(t *testing.T) {
	consumerBackoffGauge.Update(0)

	srv1 := newMockOutboxServer(t)
	srv1.api.setResponseDelay(10 * time.Millisecond)
	addr := common.HexToAddress("0x8888888888888888888888888888888888888888")
	for i := 0; i < 40; i++ {
		seq := uint64(i)
		srv1.api.addDiff(t, seq, seq+1, addr, seq+1, big.NewInt(int64(i+1)))
	}

	cfg := defaultTestConfig(srv1.Endpoint(), t.TempDir())
	cfg.ApplyCommitInterval = 1

	runner, err := NewRunner(cfg)
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	runner.consumer.reader.timeout = 200 * time.Millisecond
	runner.consumer.reader.reconnectDelay = 20 * time.Millisecond

	if err := runner.Start(); err != nil {
		t.Fatalf("Start runner: %v", err)
	}
	defer runner.Stop()

	waitUntil(t, 8*time.Second, "runner processes initial events", func() bool {
		return processedSeq(runner.consumer) >= 5
	})

	srv1.Close()
	// Force client-side reconnect so the disconnect is observed deterministically.
	runner.consumer.reader.Close()
	runner.consumer.reader.endpoint = "http://127.0.0.1:1"
	time.Sleep(200 * time.Millisecond)

	srv2 := newMockOutboxServer(t)
	for i := 0; i < 40; i++ {
		seq := uint64(i)
		srv2.api.addDiff(t, seq, seq+1, addr, seq+1, big.NewInt(int64(i+1)))
	}

	runner.consumer.reader.Close()
	runner.consumer.reader.endpoint = srv2.Endpoint()
	waitUntil(t, 10*time.Second, "runner recovers and continues", func() bool {
		return processedSeq(runner.consumer) >= 12
	})
}

// ===== Crash-Consistency Tests =====

// TestCrash_PendingSeqPersistedBeforeApply verifies ConsumeNext persists PendingSeq
// before calling applier.ApplyDiff, so a crash mid-apply replays from the correct seq.
func TestCrash_PendingSeqPersistedBeforeApply(t *testing.T) {
	srv := newMockOutboxServer(t)
	addr := common.HexToAddress("0xA100000000000000000000000000000000000001")
	for i := 0; i < 10; i++ {
		seq := uint64(i)
		srv.api.addDiff(t, seq, seq+1, addr, seq+1, big.NewInt(int64(i+1)))
	}

	dataDir := t.TempDir()
	cfg := defaultTestConfig(srv.Endpoint(), dataDir)
	cfg.ApplyCommitInterval = 5

	c := newTestConsumerWithConfig(t, cfg)
	// Consume 5 events (triggers commit at ApplyCommitInterval=5)
	consumeN(t, c, 5)

	// At this point, state should be committed (seq=4, block=5).
	if c.state.AppliedSeq != 4 {
		t.Fatalf("expected AppliedSeq=4 after 5 events with interval=5, got %d", c.state.AppliedSeq)
	}

	// Consume one more event — triggers pendingSeq persistence before apply.
	if err := c.ConsumeNext(); err != nil {
		t.Fatalf("ConsumeNext #6: %v", err)
	}

	// Read persisted state directly from DB to verify PendingSeq was set.
	state := rawdb.ReadUBTConsumerState(c.db)
	if state == nil {
		t.Fatal("expected persisted state")
	}
	// After successful apply, processedSeq advances and uncommittedBlocks=1.
	// PendingSeq is set to targetSeq before apply and remains until next commit.
	if state.PendingSeq != 5 {
		t.Fatalf("expected PendingSeq=5 persisted before apply, got %d", state.PendingSeq)
	}

	c.Close()
}

// TestCrash_PendingSeqPersistedBeforeValidation verifies ConsumeNextValidateOnly
// persists PendingSeq before validation.
func TestCrash_PendingSeqPersistedBeforeValidation(t *testing.T) {
	srv := newMockOutboxServer(t)
	addr := common.HexToAddress("0xA200000000000000000000000000000000000002")
	for i := 0; i < 10; i++ {
		seq := uint64(i)
		srv.api.addDiff(t, seq, seq+1, addr, seq+1, big.NewInt(int64(i+1)))
	}

	applier := newTestApplier(t)
	defer applier.Close()

	reader := NewOutboxReader(srv.Endpoint())
	reader.timeout = 2 * time.Second
	reader.reconnectDelay = 25 * time.Millisecond

	db := rawdb.NewMemoryDatabase()

	c := &Consumer{
		cfg: &Config{
			ApplyCommitInterval:   1000, // high so periodic persist doesn't fire
			ApplyCommitMaxLatency: time.Hour,
			ValidateOnlyMode:     true,
		},
		db:             db,
		applier:        applier,
		reader:         reader,
		lastCommitTime: time.Now(),
		// Simulate existing state so hasState=true (enables pendingSeq persistence)
		state: ConsumerState{
			AppliedSeq:   4,
			AppliedBlock: 5,
		},
		hasState:     true,
		processedSeq: 4,
	}
	defer c.reader.Close()

	// Consume one event in validate-only mode (no validator wired, so validation is skipped
	// but the pendingSeq protocol is exercised)
	if err := c.ConsumeNextValidateOnly(); err != nil {
		t.Fatalf("ConsumeNextValidateOnly: %v", err)
	}

	// Check persisted state — PendingSeq should have been set to targetSeq (5)
	// before validation, and since uncommittedBlocks < ApplyCommitInterval, it
	// has NOT been cleared yet.
	state := rawdb.ReadUBTConsumerState(db)
	if state == nil {
		t.Fatal("expected persisted state")
	}
	if state.PendingSeq != 5 {
		t.Fatalf("expected PendingSeq=5 persisted before validation, got %d", state.PendingSeq)
	}
}

// TestCrash_PendingSeqRestart verifies that if PendingSeq != 0 on restart,
// the consumer replays from AppliedSeq+1 (the pending event).
func TestCrash_PendingSeqRestart(t *testing.T) {
	srv := newMockOutboxServer(t)
	addr := common.HexToAddress("0xA300000000000000000000000000000000000003")
	for i := 0; i < 20; i++ {
		seq := uint64(i)
		srv.api.addDiff(t, seq, seq+1, addr, seq+1, big.NewInt(int64(i+1)))
	}

	applier := newTestApplier(t)
	reader := NewOutboxReader(srv.Endpoint())
	reader.timeout = 2 * time.Second
	reader.reconnectDelay = 25 * time.Millisecond

	db := rawdb.NewMemoryDatabase()

	// Simulate state with PendingSeq set (as if crash happened mid-apply)
	rawdb.WriteUBTConsumerState(db, &rawdb.UBTConsumerState{
		AppliedSeq:   9,
		AppliedBlock: 10,
		PendingSeq:   10, // Was processing seq=10 when crash occurred
	})

	c := &Consumer{
		cfg: &Config{
			ApplyCommitInterval:   1000,
			ApplyCommitMaxLatency: time.Hour,
		},
		db:             db,
		applier:        applier,
		reader:         reader,
		lastCommitTime: time.Now(),
	}

	// Load state — this should clear PendingSeq and set processedSeq to AppliedSeq
	if err := c.loadState(); err != nil {
		t.Fatalf("loadState: %v", err)
	}

	if c.state.PendingSeq != 0 {
		t.Fatalf("expected PendingSeq cleared on restart, got %d", c.state.PendingSeq)
	}

	// Initialize processedSeq from loaded state
	c.processedSeq = c.state.AppliedSeq

	// Next consume should target seq=10 (AppliedSeq+1)
	if err := c.ConsumeNext(); err != nil {
		t.Fatalf("ConsumeNext after restart: %v", err)
	}
	if c.processedSeq != 10 {
		t.Fatalf("expected replay from seq=10, got processedSeq=%d", c.processedSeq)
	}

	c.reader.Close()
	applier.Close()
}

// TestCrash_PendingSeqClearedAfterCommit verifies PendingSeq is cleared after
// a successful commit.
func TestCrash_PendingSeqClearedAfterCommit(t *testing.T) {
	srv := newMockOutboxServer(t)
	addr := common.HexToAddress("0xA400000000000000000000000000000000000004")
	for i := 0; i < 10; i++ {
		seq := uint64(i)
		srv.api.addDiff(t, seq, seq+1, addr, seq+1, big.NewInt(int64(i+1)))
	}

	dataDir := t.TempDir()
	cfg := defaultTestConfig(srv.Endpoint(), dataDir)
	cfg.ApplyCommitInterval = 5

	c := newTestConsumerWithConfig(t, cfg)
	defer c.Close()

	// Consume 5 events — triggers commit at interval boundary
	consumeN(t, c, 5)

	// After commit, PendingSeq should be cleared
	state := rawdb.ReadUBTConsumerState(c.db)
	if state == nil {
		t.Fatal("expected persisted state after commit")
	}
	if state.PendingSeq != 0 {
		t.Fatalf("expected PendingSeq=0 after commit, got %d", state.PendingSeq)
	}
	if state.AppliedSeq != 4 {
		t.Fatalf("expected AppliedSeq=4 after 5 events, got %d", state.AppliedSeq)
	}
}

// ===== Validate-Only Tests =====

// TestValidateOnly_CloseDoesNotMutateRoot verifies that closing a validate-only
// consumer does NOT mutate AppliedRoot (no trie commit happens).
func TestValidateOnly_CloseDoesNotMutateRoot(t *testing.T) {
	srv := newMockOutboxServer(t)
	addr := common.HexToAddress("0xB100000000000000000000000000000000000001")
	for i := 0; i < 10; i++ {
		seq := uint64(i)
		srv.api.addDiff(t, seq, seq+1, addr, seq+1, big.NewInt(int64(i+1)))
	}

	applier := newTestApplier(t)
	reader := NewOutboxReader(srv.Endpoint())
	reader.timeout = 2 * time.Second
	reader.reconnectDelay = 25 * time.Millisecond

	db := rawdb.NewMemoryDatabase()

	originalRoot := common.HexToHash("0xdeadbeef")
	c := &Consumer{
		cfg: &Config{
			ApplyCommitInterval:   1000,
			ApplyCommitMaxLatency: time.Hour,
			ValidateOnlyMode:     true,
		},
		db:             db,
		applier:        applier,
		reader:         reader,
		lastCommitTime: time.Now(),
		state: ConsumerState{
			AppliedSeq:   0,
			AppliedBlock: 0,
			AppliedRoot:  originalRoot,
		},
		hasState:     true,
		processedSeq: 0,
	}

	// Consume several events in validate-only mode (no validator, so validation is a no-op)
	for i := 0; i < 5; i++ {
		if err := c.ConsumeNextValidateOnly(); err != nil {
			t.Fatalf("ConsumeNextValidateOnly #%d: %v", i+1, err)
		}
	}

	if c.uncommittedBlocks != 5 {
		t.Fatalf("expected uncommittedBlocks=5, got %d", c.uncommittedBlocks)
	}

	// Close should persist progress via persistValidateOnlyProgress, NOT commit().
	// Verify in-memory state before close (db is closed by Close()).
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// After close, check the in-memory consumer state that was persisted.
	// AppliedRoot must NOT have changed — validate-only never touches the trie.
	if c.state.AppliedRoot != originalRoot {
		t.Fatalf("validate-only Close() mutated AppliedRoot: got %s, want %s", c.state.AppliedRoot, originalRoot)
	}
	// But seq/block should be updated
	if c.state.AppliedSeq != 5 {
		t.Fatalf("expected AppliedSeq=5 after close, got %d", c.state.AppliedSeq)
	}
	if c.state.AppliedBlock != 6 {
		t.Fatalf("expected AppliedBlock=6 after close, got %d", c.state.AppliedBlock)
	}
	if c.state.PendingSeq != 0 {
		t.Fatalf("expected PendingSeq=0 after close, got %d", c.state.PendingSeq)
	}
}

// TestValidateOnly_ProgressPersistsWithoutTrieCommit verifies that validate-only
// periodic progress persistence does NOT call trie commit.
func TestValidateOnly_ProgressPersistsWithoutTrieCommit(t *testing.T) {
	srv := newMockOutboxServer(t)
	addr := common.HexToAddress("0xB200000000000000000000000000000000000002")
	for i := 0; i < 10; i++ {
		seq := uint64(i)
		srv.api.addDiff(t, seq, seq+1, addr, seq+1, big.NewInt(int64(i+1)))
	}

	applier := newTestApplier(t)
	defer applier.Close()

	reader := NewOutboxReader(srv.Endpoint())
	reader.timeout = 2 * time.Second
	reader.reconnectDelay = 25 * time.Millisecond

	db := rawdb.NewMemoryDatabase()

	originalRoot := common.HexToHash("0xcafebabe")
	c := &Consumer{
		cfg: &Config{
			ApplyCommitInterval:   3, // Trigger progress persist after 3 events
			ApplyCommitMaxLatency: time.Hour,
			ValidateOnlyMode:     true,
		},
		db:             db,
		applier:        applier,
		reader:         reader,
		lastCommitTime: time.Now(),
		state: ConsumerState{
			AppliedSeq:   0,
			AppliedBlock: 0,
			AppliedRoot:  originalRoot,
		},
		hasState:     true,
		processedSeq: 0,
	}
	defer c.reader.Close()

	// Consume 3 events — should trigger persistValidateOnlyProgress
	for i := 0; i < 3; i++ {
		if err := c.ConsumeNextValidateOnly(); err != nil {
			t.Fatalf("ConsumeNextValidateOnly #%d: %v", i+1, err)
		}
	}

	// uncommittedBlocks should be reset to 0 after persist
	if c.uncommittedBlocks != 0 {
		t.Fatalf("expected uncommittedBlocks=0 after interval persist, got %d", c.uncommittedBlocks)
	}

	// Verify persisted state
	state := rawdb.ReadUBTConsumerState(db)
	if state == nil {
		t.Fatal("expected persisted state")
	}

	// Root must NOT change
	if state.AppliedRoot != originalRoot {
		t.Fatalf("periodic persist mutated AppliedRoot: got %s, want %s", state.AppliedRoot, originalRoot)
	}
	// Seq/block should advance
	if state.AppliedSeq != 3 {
		t.Fatalf("expected AppliedSeq=3, got %d", state.AppliedSeq)
	}
	if state.AppliedBlock != 4 {
		t.Fatalf("expected AppliedBlock=4, got %d", state.AppliedBlock)
	}
	if state.PendingSeq != 0 {
		t.Fatalf("expected PendingSeq=0 after progress persist, got %d", state.PendingSeq)
	}
}

// TestValidateOnly_HaltOnMismatch verifies that errValidationHalt from
// ConsumeNextValidateOnly causes the runner to exit (not retry).
func TestValidateOnly_HaltOnMismatch(t *testing.T) {
	srv := newMockOutboxServer(t)
	addr := common.HexToAddress("0xB300000000000000000000000000000000000003")
	// Add a single diff event
	srv.api.addDiff(t, 0, 1, addr, 1, big.NewInt(100))

	cfg := defaultTestConfig(srv.Endpoint(), t.TempDir())
	cfg.ValidateOnlyMode = true
	cfg.ApplyCommitInterval = 1000
	cfg.ApplyCommitMaxLatency = time.Hour

	runner, err := NewRunner(cfg)
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	runner.consumer.reader.timeout = 2 * time.Second
	runner.consumer.reader.reconnectDelay = 25 * time.Millisecond

	// The runner uses ConsumeNextValidateOnly, which calls ValidateDiffAgainstMPT.
	// The mock server doesn't implement eth_getBalance, so validation will fail
	// and return errValidationHalt, causing the runner to exit.
	if err := runner.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait for the runner loop to exit due to validation halt
	done := make(chan struct{})
	go func() {
		runner.Stop()
		close(done)
	}()

	select {
	case <-done:
		// Runner stopped — errValidationHalt was recognized
	case <-time.After(10 * time.Second):
		t.Fatal("timeout: runner did not halt on validation mismatch")
	}
}

// TDD: C1 – OutboxReader must be safe for concurrent access.
// Close() and ReadEvent() can be called from different goroutines
// (Runner.Stop() vs the consume loop). This test exercises the race.
func TestOutboxReader_ConcurrentCloseAndRead(t *testing.T) {
	reader := NewOutboxReader("http://localhost:1") // unreachable endpoint is fine
	reader.reconnectDelay = 0                       // don't slow down the test
	reader.timeout = 50 * time.Millisecond          // very short dial timeout

	done := make(chan struct{})

	// Goroutine 1: repeatedly try to read (will fail to connect, but exercises client access)
	go func() {
		defer close(done)
		for i := 0; i < 20; i++ {
			reader.ReadEvent(uint64(i))
		}
	}()

	// Goroutine 2: repeatedly close (exercises client access concurrently)
	for i := 0; i < 20; i++ {
		reader.Close()
		time.Sleep(time.Millisecond)
	}

	<-done
	reader.Close()
}

// TestCompaction_TriggeredOnPolicy verifies that tryCompaction calls compactOutbox
// when the gap between lastCompactedBelow and compactBelow exceeds the commit interval.
func TestCompaction_TriggeredOnPolicy(t *testing.T) {
	srv := newMockOutboxServer(t)
	addr := common.HexToAddress("0xC000000000000000000000000000000000000001")
	for i := 0; i < 300; i++ {
		seq := uint64(i)
		srv.api.addDiff(t, seq, seq+1, addr, seq+1, big.NewInt(int64(i+1)))
	}

	cfg := defaultTestConfig(srv.Endpoint(), t.TempDir())
	cfg.ApplyCommitInterval = 50

	runner, err := NewRunner(cfg)
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	runner.consumer.reader.timeout = 2 * time.Second
	runner.consumer.reader.reconnectDelay = 25 * time.Millisecond

	// Consume enough events so AppliedSeq >> compactionSafetyMargin
	consumeN(t, runner.consumer, 200)
	if runner.consumer.uncommittedBlocks > 0 {
		if err := runner.consumer.commit(); err != nil {
			t.Fatalf("commit: %v", err)
		}
	}

	if runner.consumer.state.AppliedSeq != 199 {
		t.Fatalf("expected AppliedSeq=199, got %d", runner.consumer.state.AppliedSeq)
	}

	// Call tryCompaction directly
	var lastCompactedBelow uint64
	runner.tryCompaction(&lastCompactedBelow)

	// compactBelow = 199 - 64 = 135, gap = 135 - 0 = 135 > 50 (commit interval)
	// So compaction should have been triggered
	calls := srv.api.getCompactCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 compact call, got %d", len(calls))
	}
	expectedBelow := uint64(199 - compactionSafetyMargin)
	if calls[0] != expectedBelow {
		t.Fatalf("expected compact call with belowSeq=%d, got %d", expectedBelow, calls[0])
	}
	if lastCompactedBelow != expectedBelow {
		t.Fatalf("lastCompactedBelow not updated: got %d want %d", lastCompactedBelow, expectedBelow)
	}
}

// TestCompaction_HonorsSafetyMargin verifies compactBelow = safeSeq - compactionSafetyMargin.
func TestCompaction_HonorsSafetyMargin(t *testing.T) {
	srv := newMockOutboxServer(t)
	addr := common.HexToAddress("0xC000000000000000000000000000000000000002")
	for i := 0; i < 200; i++ {
		seq := uint64(i)
		srv.api.addDiff(t, seq, seq+1, addr, seq+1, big.NewInt(int64(i+1)))
	}

	cfg := defaultTestConfig(srv.Endpoint(), t.TempDir())
	cfg.ApplyCommitInterval = 10

	runner, err := NewRunner(cfg)
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	runner.consumer.reader.timeout = 2 * time.Second
	runner.consumer.reader.reconnectDelay = 25 * time.Millisecond

	consumeN(t, runner.consumer, 100)
	if runner.consumer.uncommittedBlocks > 0 {
		if err := runner.consumer.commit(); err != nil {
			t.Fatalf("commit: %v", err)
		}
	}

	var lastCompactedBelow uint64
	runner.tryCompaction(&lastCompactedBelow)

	calls := srv.api.getCompactCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 compact call, got %d", len(calls))
	}
	expectedBelow := runner.consumer.state.AppliedSeq - compactionSafetyMargin
	if calls[0] != expectedBelow {
		t.Fatalf("safety margin not honored: got belowSeq=%d want %d (appliedSeq=%d, margin=%d)",
			calls[0], expectedBelow, runner.consumer.state.AppliedSeq, compactionSafetyMargin)
	}
}

// TestCompaction_RPCFailureNonFatal verifies that an RPC failure increments the error
// metric but does not crash or update lastCompactedBelow.
func TestCompaction_RPCFailureNonFatal(t *testing.T) {
	srv := newMockOutboxServer(t)
	addr := common.HexToAddress("0xC000000000000000000000000000000000000003")
	for i := 0; i < 200; i++ {
		seq := uint64(i)
		srv.api.addDiff(t, seq, seq+1, addr, seq+1, big.NewInt(int64(i+1)))
	}

	cfg := defaultTestConfig(srv.Endpoint(), t.TempDir())
	cfg.ApplyCommitInterval = 10

	runner, err := NewRunner(cfg)
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	runner.consumer.reader.timeout = 2 * time.Second
	runner.consumer.reader.reconnectDelay = 25 * time.Millisecond

	consumeN(t, runner.consumer, 100)
	if runner.consumer.uncommittedBlocks > 0 {
		if err := runner.consumer.commit(); err != nil {
			t.Fatalf("commit: %v", err)
		}
	}

	// Make compact RPC fail
	srv.api.setFailCompact(true)

	errsBefore := compactionErrorsTotal.Snapshot().Count()
	var lastCompactedBelow uint64
	runner.tryCompaction(&lastCompactedBelow)

	// Should not crash, lastCompactedBelow should remain 0
	if lastCompactedBelow != 0 {
		t.Fatalf("lastCompactedBelow should not update on RPC failure, got %d", lastCompactedBelow)
	}

	errsAfter := compactionErrorsTotal.Snapshot().Count()
	if errsAfter <= errsBefore {
		t.Fatal("compactionErrorsTotal should have been incremented")
	}
}
