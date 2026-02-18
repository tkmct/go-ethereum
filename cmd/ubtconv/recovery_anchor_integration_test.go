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
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/ethdb/leveldb"
)

func runUntilSeq(t *testing.T, c *Consumer, targetSeq uint64) {
	t.Helper()
	for {
		p := processedSeq(c)
		if p != ^uint64(0) && p >= targetSeq {
			return
		}
		if err := c.ConsumeNext(); err != nil {
			t.Fatalf("ConsumeNext while targeting seq=%d failed: %v", targetSeq, err)
		}
	}
}

var unexpectedTrieGotPattern = regexp.MustCompile(`!=([0-9a-f]{64})`)

func extractUnexpectedTrieGotRoot(err error) (common.Hash, bool) {
	if err == nil {
		return common.Hash{}, false
	}
	matches := unexpectedTrieGotPattern.FindStringSubmatch(err.Error())
	if len(matches) != 2 {
		return common.Hash{}, false
	}
	return common.HexToHash("0x" + matches[1]), true
}

func materializeRecoveryAnchorForTest(t *testing.T, cfg *Config, state ConsumerState, anchorID uint64) ConsumerState {
	t.Helper()
	anchorDir := filepath.Join(cfg.DataDir, "recovery", "anchors", fmt.Sprintf("%020d", anchorID))
	anchorTrieDir := filepath.Join(anchorDir, "triedb")
	if err := os.MkdirAll(filepath.Dir(anchorDir), 0o755); err != nil {
		t.Fatalf("create recovery anchor parent dir: %v", err)
	}
	if err := copyDirRecursive(filepath.Join(cfg.DataDir, "triedb"), anchorTrieDir); err != nil {
		t.Fatalf("copy triedb into recovery anchor: %v", err)
	}

	dbPath := filepath.Join(cfg.DataDir, "consumer")
	kvdb, err := leveldb.New(dbPath, 16, 16, "ubtconv/consumer", false)
	if err != nil {
		t.Fatalf("open consumer DB for anchor metadata: %v", err)
	}
	db := rawdb.NewDatabase(kvdb)
	defer db.Close()

	manifest := &rawdb.UBTRecoveryAnchorManifest{
		AnchorID:      anchorID,
		Seq:           state.AppliedSeq,
		BlockNumber:   state.AppliedBlock,
		BlockRoot:     state.AppliedRoot,
		CreatedAt:     uint64(time.Now().Unix()),
		FormatVersion: recoveryAnchorFormatVersion,
		State:         rawdb.UBTRecoveryAnchorReady,
	}
	batch := db.NewBatch()
	rawdb.WriteUBTRecoveryAnchorManifest(batch, anchorID, manifest)
	rawdb.WriteUBTRecoveryAnchorCount(batch, anchorID+1)
	rawdb.WriteUBTRecoveryAnchorLatestReady(batch, anchorID)
	if err := batch.Write(); err != nil {
		t.Fatalf("write recovery anchor metadata: %v", err)
	}
	return state
}

func overwriteConsumerStateRootForTest(t *testing.T, dataDir string, newRoot common.Hash) *rawdb.UBTConsumerState {
	t.Helper()
	dbPath := filepath.Join(dataDir, "consumer")
	kvdb, err := leveldb.New(dbPath, 16, 16, "ubtconv/consumer", false)
	if err != nil {
		t.Fatalf("open consumer DB for state overwrite: %v", err)
	}
	db := rawdb.NewDatabase(kvdb)
	defer db.Close()

	state := rawdb.ReadUBTConsumerState(db)
	if state == nil {
		t.Fatal("expected persisted consumer state before overwrite")
	}
	state.AppliedRoot = newRoot
	rawdb.WriteUBTConsumerState(db, state)
	return state
}

func TestRecoveryAnchor_IntegrationRestoreAfterTrieLoss(t *testing.T) {
	srv := newMockOutboxServer(t)
	addr := common.HexToAddress("0x7100000000000000000000000000000000000001")
	const latestSeq = uint64(24)
	for i := uint64(0); i <= latestSeq; i++ {
		srv.api.addDiff(t, i, i+1, addr, i+1, big.NewInt(int64(i+1)))
	}

	dataDir := t.TempDir()
	cfg := defaultTestConfig(srv.Endpoint(), dataDir)
	cfg.ApplyCommitInterval = 1
	cfg.ApplyCommitMaxLatency = time.Hour
	cfg.RecoveryAnchorInterval = 0 // creation path is tested separately; here we test restore path end-to-end.
	cfg.RecoveryAnchorRetention = 8
	cfg.RecoveryStrict = true
	cfg.RecoveryAllowGenesisFallback = false

	// Build baseline expected root by replaying from genesis in separate datadir.
	baselineCfg := defaultTestConfig(srv.Endpoint(), t.TempDir())
	baselineCfg.ApplyCommitInterval = 1
	baselineCfg.ApplyCommitMaxLatency = time.Hour
	baseline := newTestConsumerWithConfig(t, baselineCfg)
	runUntilSeq(t, baseline, latestSeq)
	expectedRoot := baseline.state.AppliedRoot
	expectedBlock := baseline.state.AppliedBlock
	if err := baseline.Close(); err != nil {
		t.Fatalf("close baseline consumer: %v", err)
	}

	// First run: consume part of the stream, then materialize one recovery anchor offline.
	c1 := newTestConsumerWithConfig(t, cfg)
	runUntilSeq(t, c1, 1)
	preClose := c1.state
	if preClose.AppliedSeq == 0 || preClose.AppliedRoot == (common.Hash{}) {
		t.Fatalf("expected non-zero checkpoint before materializing recovery anchor, got seq=%d root=%s", preClose.AppliedSeq, preClose.AppliedRoot)
	}
	if liveRoot := c1.applier.Root(); liveRoot != preClose.AppliedRoot {
		t.Fatalf("pre-close root mismatch: applier root=%s state root=%s", liveRoot, preClose.AppliedRoot)
	}
	// Use the live trie hash for materialized recovery anchor so restart-open validation
	// uses a root that can be reopened from on-disk triedb.
	checkpointRoot := c1.applier.Trie().Hash()
	if err := c1.Close(); err != nil {
		t.Fatalf("close first consumer: %v", err)
	}
	diskCheckpoint := readCheckpointFromDisk(t, dataDir)
	if diskCheckpoint.AppliedRoot != preClose.AppliedRoot {
		t.Fatalf("disk checkpoint root mismatch after close: disk=%s preClose=%s", diskCheckpoint.AppliedRoot, preClose.AppliedRoot)
	}
	checkpoint := ConsumerState{
		AppliedSeq:   diskCheckpoint.AppliedSeq,
		AppliedBlock: diskCheckpoint.AppliedBlock,
		AppliedRoot:  checkpointRoot,
	}
	checkpoint = materializeRecoveryAnchorForTest(t, cfg, checkpoint, 0)

	// Simulate live triedb loss/corruption.
	trieDir := filepath.Join(dataDir, "triedb")
	if err := os.RemoveAll(trieDir); err != nil {
		t.Fatalf("remove triedb: %v", err)
	}
	if err := os.MkdirAll(trieDir, 0o755); err != nil {
		t.Fatalf("recreate empty triedb: %v", err)
	}

	// Restart: startup should restore from materialized recovery anchor.
	c2, err := NewConsumer(cfg)
	if err != nil {
		t.Fatalf("NewConsumer after triedb loss should recover from recovery anchor: %v", err)
	}
	c2.reader.timeout = 2 * time.Second
	c2.reader.reconnectDelay = 25 * time.Millisecond
	defer c2.Close()

	if c2.recoveryMode != "anchor-restore" {
		t.Fatalf("expected recoveryMode=anchor-restore, got %q", c2.recoveryMode)
	}
	if c2.state.AppliedSeq != checkpoint.AppliedSeq || c2.state.AppliedBlock != checkpoint.AppliedBlock || c2.state.AppliedRoot != checkpoint.AppliedRoot {
		t.Fatalf("unexpected restored checkpoint: got seq=%d block=%d root=%s want seq=%d block=%d root=%s",
			c2.state.AppliedSeq, c2.state.AppliedBlock, c2.state.AppliedRoot,
			checkpoint.AppliedSeq, checkpoint.AppliedBlock, checkpoint.AppliedRoot)
	}
	if !c2.hasRecoveryAnchor || c2.latestRecoveryAnchorSeq != checkpoint.AppliedSeq {
		t.Fatalf("expected recovery anchor metadata after restore, got hasRecoveryAnchor=%v latestSeq=%d", c2.hasRecoveryAnchor, c2.latestRecoveryAnchorSeq)
	}

	// Continue replay from restored anchor and verify deterministic final state.
	runUntilSeq(t, c2, latestSeq)
	if c2.state.AppliedSeq != latestSeq {
		t.Fatalf("applied seq mismatch after recovery replay: got %d want %d", c2.state.AppliedSeq, latestSeq)
	}
	if c2.state.AppliedBlock != expectedBlock {
		t.Fatalf("applied block mismatch after recovery replay: got %d want %d", c2.state.AppliedBlock, expectedBlock)
	}
	if c2.state.AppliedRoot != expectedRoot {
		t.Fatalf("applied root mismatch after recovery replay: got %s want %s", c2.state.AppliedRoot, expectedRoot)
	}
	api := NewQueryAPI(c2)
	bal, err := api.GetBalance(context.Background(), addr, nil)
	if err != nil {
		t.Fatalf("GetBalance after recovery replay: %v", err)
	}
	if bal.ToInt().Uint64() != latestSeq+1 {
		t.Fatalf("balance mismatch after recovery replay: got %s want %d", bal.ToInt(), latestSeq+1)
	}
}

func TestRecoveryAnchor_IntegrationExpectedRootReopenMismatchRecovers(t *testing.T) {
	srv := newMockOutboxServer(t)
	addr := common.HexToAddress("0x7300000000000000000000000000000000000001")
	const latestSeq = uint64(20)
	for i := uint64(0); i <= latestSeq; i++ {
		srv.api.addDiff(t, i, i+1, addr, i+1, big.NewInt(int64(i+1)))
	}

	dataDir := t.TempDir()
	cfg := defaultTestConfig(srv.Endpoint(), dataDir)
	cfg.ApplyCommitInterval = 1
	cfg.ApplyCommitMaxLatency = time.Hour
	cfg.RecoveryAnchorInterval = 0
	cfg.RecoveryAnchorRetention = 8
	cfg.RecoveryStrict = true
	cfg.RecoveryAllowGenesisFallback = false

	// Build baseline expected root by replaying from genesis in separate datadir.
	baselineCfg := defaultTestConfig(srv.Endpoint(), t.TempDir())
	baselineCfg.ApplyCommitInterval = 1
	baselineCfg.ApplyCommitMaxLatency = time.Hour
	baseline := newTestConsumerWithConfig(t, baselineCfg)
	runUntilSeq(t, baseline, latestSeq)
	expectedRoot := baseline.state.AppliedRoot
	expectedBlock := baseline.state.AppliedBlock
	if err := baseline.Close(); err != nil {
		t.Fatalf("close baseline consumer: %v", err)
	}

	// First run: consume and materialize one recovery anchor.
	c1 := newTestConsumerWithConfig(t, cfg)
	runUntilSeq(t, c1, 2)
	if c1.state.AppliedSeq == 0 || c1.state.AppliedRoot == (common.Hash{}) {
		t.Fatalf("expected non-zero checkpoint before recovery anchor materialization, got seq=%d root=%s", c1.state.AppliedSeq, c1.state.AppliedRoot)
	}
	checkpointRoot := c1.applier.Trie().Hash()
	if err := c1.Close(); err != nil {
		t.Fatalf("close first consumer: %v", err)
	}
	diskCheckpoint := readCheckpointFromDisk(t, dataDir)
	checkpoint := ConsumerState{
		AppliedSeq:   diskCheckpoint.AppliedSeq,
		AppliedBlock: diskCheckpoint.AppliedBlock,
		AppliedRoot:  checkpointRoot,
	}
	checkpoint = materializeRecoveryAnchorForTest(t, cfg, checkpoint, 0)

	// Simulate expected-root reopen inconsistency by persisting a bogus root.
	bogusRoot := common.HexToHash("0xdeadbeef00000000000000000000000000000000000000000000000000000000")
	overwritten := overwriteConsumerStateRootForTest(t, dataDir, bogusRoot)
	if overwritten.AppliedRoot != bogusRoot {
		t.Fatalf("consumer state root overwrite failed: got %s want %s", overwritten.AppliedRoot, bogusRoot)
	}

	// Verify direct reopen by the bogus root fails before recovery kicks in.
	a, err := NewApplier(cfg, bogusRoot)
	if err == nil {
		a.Close()
		t.Fatalf("expected NewApplier to fail for bogus expected root %s", bogusRoot)
	}
	if !strings.Contains(err.Error(), "failed to open trie with expected root") {
		t.Fatalf("unexpected NewApplier error for bogus root: %v", err)
	}

	// Restart: startup should recover from materialized recovery anchor.
	c2, err := NewConsumer(cfg)
	if err != nil {
		t.Fatalf("NewConsumer after expected-root reopen mismatch should recover: %v", err)
	}
	c2.reader.timeout = 2 * time.Second
	c2.reader.reconnectDelay = 25 * time.Millisecond
	defer c2.Close()

	if c2.recoveryMode != "anchor-restore" {
		t.Fatalf("expected recoveryMode=anchor-restore, got %q", c2.recoveryMode)
	}
	if c2.state.AppliedSeq != checkpoint.AppliedSeq || c2.state.AppliedBlock != checkpoint.AppliedBlock || c2.state.AppliedRoot != checkpoint.AppliedRoot {
		t.Fatalf("unexpected restored checkpoint: got seq=%d block=%d root=%s want seq=%d block=%d root=%s",
			c2.state.AppliedSeq, c2.state.AppliedBlock, c2.state.AppliedRoot,
			checkpoint.AppliedSeq, checkpoint.AppliedBlock, checkpoint.AppliedRoot)
	}
	if c2.state.AppliedRoot == bogusRoot {
		t.Fatalf("recovery did not replace bogus root, still at %s", bogusRoot)
	}

	runUntilSeq(t, c2, latestSeq)
	if c2.state.AppliedSeq != latestSeq {
		t.Fatalf("applied seq mismatch after recovery replay: got %d want %d", c2.state.AppliedSeq, latestSeq)
	}
	if c2.state.AppliedBlock != expectedBlock {
		t.Fatalf("applied block mismatch after recovery replay: got %d want %d", c2.state.AppliedBlock, expectedBlock)
	}
	if c2.state.AppliedRoot != expectedRoot {
		t.Fatalf("applied root mismatch after recovery replay: got %s want %s", c2.state.AppliedRoot, expectedRoot)
	}
}

func TestRecoveryAnchor_IntegrationStrictFailsWithoutAnchor(t *testing.T) {
	srv := newMockOutboxServer(t)
	addr := common.HexToAddress("0x7200000000000000000000000000000000000001")
	for i := uint64(0); i < 8; i++ {
		srv.api.addDiff(t, i, i+1, addr, i+1, big.NewInt(int64(i+1)))
	}

	dataDir := t.TempDir()
	cfg := defaultTestConfig(srv.Endpoint(), dataDir)
	cfg.ApplyCommitInterval = 1
	cfg.ApplyCommitMaxLatency = time.Hour
	cfg.RecoveryAnchorInterval = 0 // disabled on purpose
	cfg.RecoveryStrict = true
	cfg.RecoveryAllowGenesisFallback = false

	c1 := newTestConsumerWithConfig(t, cfg)
	runUntilSeq(t, c1, 4)
	if err := c1.Close(); err != nil {
		t.Fatalf("close first consumer: %v", err)
	}

	trieDir := filepath.Join(dataDir, "triedb")
	if err := os.RemoveAll(trieDir); err != nil {
		t.Fatalf("remove triedb: %v", err)
	}
	if err := os.MkdirAll(trieDir, 0o755); err != nil {
		t.Fatalf("recreate empty triedb: %v", err)
	}

	_, err := NewConsumer(cfg)
	if err == nil {
		t.Fatal("expected strict startup recovery to fail without materialized anchors")
	}
	if !strings.Contains(err.Error(), "strict recovery is enabled") {
		t.Fatalf("unexpected startup failure message: %v", err)
	}
}
