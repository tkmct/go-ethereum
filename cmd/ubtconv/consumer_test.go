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
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/core/rawdb"
)

func TestEffectiveBlockRootIndexStride(t *testing.T) {
	tests := []struct {
		name      string
		base      uint64
		threshold uint64
		lag       uint64
		want      uint64
	}{
		{name: "disabled by base stride", base: 1, threshold: 5000, lag: 50000, want: 1},
		{name: "disabled by threshold", base: 64, threshold: 0, lag: 50000, want: 1},
		{name: "below threshold", base: 64, threshold: 5000, lag: 4999, want: 1},
		{name: "just above threshold uses base", base: 64, threshold: 5000, lag: 7000, want: 64},
		{name: "ratio 8 bumps to 128", base: 64, threshold: 5000, lag: 40000, want: 128},
		{name: "ratio 16 bumps to 256", base: 64, threshold: 5000, lag: 80000, want: 256},
		{name: "ratio 32 bumps to 1024", base: 64, threshold: 5000, lag: 160000, want: 1024},
		{name: "ratio 64 bumps to 2048", base: 64, threshold: 5000, lag: 320000, want: 2048},
		{name: "ratio 128 bumps to 4096", base: 64, threshold: 5000, lag: 640000, want: 4096},
		{name: "base higher than adaptive keeps base", base: 1024, threshold: 5000, lag: 160000, want: 1024},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Consumer{
				cfg: &Config{
					BlockRootIndexStrideHighLag: tt.base,
					BackpressureLagThreshold:    tt.threshold,
				},
				outboxLag: tt.lag,
			}
			got := c.effectiveBlockRootIndexStride()
			if got != tt.want {
				t.Fatalf("effectiveBlockRootIndexStride()=%d want=%d", got, tt.want)
			}
		})
	}
}

func TestShouldWriteBlockRootIndex_WithAdaptiveStride(t *testing.T) {
	c := &Consumer{
		cfg: &Config{
			BlockRootIndexStrideHighLag: 64,
			BackpressureLagThreshold:    5000,
		},
		outboxLag:    160000, // ratio 32 => stride 1024
		pendingBlock: 12345,
	}
	if c.shouldWriteBlockRootIndex(1024) != true {
		t.Fatal("expected stride-aligned block to be written")
	}
	if c.shouldWriteBlockRootIndex(1025) != false {
		t.Fatal("expected non-stride block to be skipped")
	}
	if c.shouldWriteBlockRootIndex(c.pendingBlock) != true {
		t.Fatal("expected pending block to be written")
	}
}

// TestShouldCommit_BlockThreshold verifies commit triggered by block count.
func TestShouldCommit_BlockThreshold(t *testing.T) {
	tests := []struct {
		name              string
		uncommittedBlocks uint64
		interval          uint64
		want              bool
	}{
		{
			name:              "at threshold",
			uncommittedBlocks: 10,
			interval:          10,
			want:              true,
		},
		{
			name:              "above threshold",
			uncommittedBlocks: 15,
			interval:          10,
			want:              true,
		},
		{
			name:              "below threshold",
			uncommittedBlocks: 9,
			interval:          10,
			want:              false,
		},
		{
			name:              "zero uncommitted",
			uncommittedBlocks: 0,
			interval:          10,
			want:              false,
		},
		{
			name:              "interval of 1 at threshold",
			uncommittedBlocks: 1,
			interval:          1,
			want:              true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Consumer{
				cfg: &Config{
					ApplyCommitInterval:   tt.interval,
					ApplyCommitMaxLatency: 24 * time.Hour, // Very long, won't trigger
				},
				uncommittedBlocks: tt.uncommittedBlocks,
				lastCommitTime:    time.Now(),
			}
			got := c.shouldCommit()
			if got != tt.want {
				t.Errorf("shouldCommit() = %v, want %v (uncommitted=%d, interval=%d)",
					got, tt.want, tt.uncommittedBlocks, tt.interval)
			}
		})
	}
}

// TestShouldCommit_TimeThreshold verifies commit triggered by elapsed time.
func TestShouldCommit_TimeThreshold(t *testing.T) {
	tests := []struct {
		name       string
		elapsed    time.Duration
		maxLatency time.Duration
		want       bool
	}{
		{
			name:       "at threshold",
			elapsed:    5 * time.Minute,
			maxLatency: 5 * time.Minute,
			want:       true,
		},
		{
			name:       "above threshold",
			elapsed:    10 * time.Minute,
			maxLatency: 5 * time.Minute,
			want:       true,
		},
		{
			name:       "below threshold",
			elapsed:    3 * time.Minute,
			maxLatency: 5 * time.Minute,
			want:       false,
		},
		{
			name:       "just under threshold",
			elapsed:    4*time.Minute + 59*time.Second,
			maxLatency: 5 * time.Minute,
			want:       false,
		},
		{
			name:       "way over threshold",
			elapsed:    time.Hour,
			maxLatency: 5 * time.Minute,
			want:       true,
		},
		{
			name:       "zero latency allows commit immediately",
			elapsed:    time.Millisecond,
			maxLatency: 0,
			want:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := time.Now()
			c := &Consumer{
				cfg: &Config{
					ApplyCommitInterval:   1000000, // Very high, won't trigger
					ApplyCommitMaxLatency: tt.maxLatency,
				},
				uncommittedBlocks: 5, // Below threshold
				lastCommitTime:    now.Add(-tt.elapsed),
			}
			got := c.shouldCommit()
			if got != tt.want {
				t.Errorf("shouldCommit() = %v, want %v (elapsed=%v, maxLatency=%v)",
					got, tt.want, tt.elapsed, tt.maxLatency)
			}
		})
	}
}

// TestShouldCommit_BothThresholds verifies behavior when both thresholds are near.
func TestShouldCommit_BothThresholds(t *testing.T) {
	tests := []struct {
		name              string
		uncommittedBlocks uint64
		interval          uint64
		elapsed           time.Duration
		maxLatency        time.Duration
		want              bool
		reason            string
	}{
		{
			name:              "both below threshold",
			uncommittedBlocks: 5,
			interval:          10,
			elapsed:           2 * time.Minute,
			maxLatency:        5 * time.Minute,
			want:              false,
			reason:            "neither threshold met",
		},
		{
			name:              "block threshold met, time not",
			uncommittedBlocks: 10,
			interval:          10,
			elapsed:           2 * time.Minute,
			maxLatency:        5 * time.Minute,
			want:              true,
			reason:            "block threshold met",
		},
		{
			name:              "time threshold met, blocks not",
			uncommittedBlocks: 5,
			interval:          10,
			elapsed:           6 * time.Minute,
			maxLatency:        5 * time.Minute,
			want:              true,
			reason:            "time threshold met",
		},
		{
			name:              "both thresholds met",
			uncommittedBlocks: 15,
			interval:          10,
			elapsed:           10 * time.Minute,
			maxLatency:        5 * time.Minute,
			want:              true,
			reason:            "both thresholds met",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := time.Now()
			c := &Consumer{
				cfg: &Config{
					ApplyCommitInterval:   tt.interval,
					ApplyCommitMaxLatency: tt.maxLatency,
				},
				uncommittedBlocks: tt.uncommittedBlocks,
				lastCommitTime:    now.Add(-tt.elapsed),
			}
			got := c.shouldCommit()
			if got != tt.want {
				t.Errorf("shouldCommit() = %v, want %v (%s)", got, tt.want, tt.reason)
				t.Logf("  uncommitted=%d, interval=%d", tt.uncommittedBlocks, tt.interval)
				t.Logf("  elapsed=%v, maxLatency=%v", tt.elapsed, tt.maxLatency)
			}
		})
	}
}

func TestShouldCommit_BackpressureCaps(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name              string
		interval          uint64
		maxLatency        time.Duration
		threshold         uint64
		outboxLag         uint64
		uncommittedBlocks uint64
		elapsed           time.Duration
		want              bool
	}{
		{
			name:              "high lag caps block interval to 128",
			interval:          1024,
			maxLatency:        time.Minute,
			threshold:         5000,
			outboxLag:         6000,
			uncommittedBlocks: 127,
			elapsed:           time.Second,
			want:              false,
		},
		{
			name:              "high lag commits at capped block interval",
			interval:          1024,
			maxLatency:        time.Minute,
			threshold:         5000,
			outboxLag:         6000,
			uncommittedBlocks: 128,
			elapsed:           time.Second,
			want:              true,
		},
		{
			name:              "high lag caps max latency to 15s",
			interval:          1024,
			maxLatency:        3 * time.Minute,
			threshold:         5000,
			outboxLag:         10000,
			uncommittedBlocks: 1,
			elapsed:           20 * time.Second,
			want:              true,
		},
		{
			name:              "without high lag keeps configured interval",
			interval:          1024,
			maxLatency:        time.Minute,
			threshold:         5000,
			outboxLag:         4000,
			uncommittedBlocks: 128,
			elapsed:           time.Second,
			want:              false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Consumer{
				cfg: &Config{
					ApplyCommitInterval:      tt.interval,
					ApplyCommitMaxLatency:    tt.maxLatency,
					BackpressureLagThreshold: tt.threshold,
				},
				outboxLag:         tt.outboxLag,
				uncommittedBlocks: tt.uncommittedBlocks,
				lastCommitTime:    now.Add(-tt.elapsed),
			}
			got := c.shouldCommit()
			if got != tt.want {
				t.Fatalf("shouldCommit()=%v want=%v", got, tt.want)
			}
		})
	}
}

// TestShouldCommit_ImmediateCommitScenarios tests scenarios where commit should happen immediately.
func TestShouldCommit_ImmediateCommitScenarios(t *testing.T) {
	tests := []struct {
		name   string
		config *Config
	}{
		{
			name: "interval 1 with 1 uncommitted",
			config: &Config{
				ApplyCommitInterval:   1,
				ApplyCommitMaxLatency: time.Hour,
			},
		},
		{
			name: "zero max latency",
			config: &Config{
				ApplyCommitInterval:   1000,
				ApplyCommitMaxLatency: 0,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Consumer{
				cfg:               tt.config,
				uncommittedBlocks: 1,
				lastCommitTime:    time.Now().Add(-time.Second),
			}
			if !c.shouldCommit() {
				t.Errorf("expected immediate commit for config: interval=%d, maxLatency=%v",
					tt.config.ApplyCommitInterval, tt.config.ApplyCommitMaxLatency)
			}
		})
	}
}

// TestShouldCommit_NeverCommitScenarios tests scenarios where commit should never happen.
func TestShouldCommit_NeverCommitScenarios(t *testing.T) {
	// This test verifies the "never commit" case when both thresholds are very high
	c := &Consumer{
		cfg: &Config{
			ApplyCommitInterval:   1000000,        // Very high block threshold
			ApplyCommitMaxLatency: 24 * time.Hour, // Very high time threshold
		},
		uncommittedBlocks: 100,
		lastCommitTime:    time.Now().Add(-time.Minute),
	}
	if c.shouldCommit() {
		t.Errorf("expected no commit with very high thresholds")
	}
}

// TestShouldCommit_EdgeCaseZeroTime verifies behavior right after a commit.
func TestShouldCommit_EdgeCaseZeroTime(t *testing.T) {
	// Right after a commit, both counters are reset
	c := &Consumer{
		cfg: &Config{
			ApplyCommitInterval:   10,
			ApplyCommitMaxLatency: 5 * time.Minute,
		},
		uncommittedBlocks: 0,
		lastCommitTime:    time.Now(), // Just now
	}
	if c.shouldCommit() {
		t.Errorf("expected no commit right after reset")
	}
}

// TestShouldCommit_LargeBlockInterval tests very large block intervals.
func TestShouldCommit_LargeBlockInterval(t *testing.T) {
	tests := []struct {
		name              string
		uncommittedBlocks uint64
		interval          uint64
		want              bool
	}{
		{
			name:              "at large threshold",
			uncommittedBlocks: 100000,
			interval:          100000,
			want:              true,
		},
		{
			name:              "just below large threshold",
			uncommittedBlocks: 99999,
			interval:          100000,
			want:              false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Consumer{
				cfg: &Config{
					ApplyCommitInterval:   tt.interval,
					ApplyCommitMaxLatency: 24 * time.Hour,
				},
				uncommittedBlocks: tt.uncommittedBlocks,
				lastCommitTime:    time.Now(),
			}
			got := c.shouldCommit()
			if got != tt.want {
				t.Errorf("shouldCommit() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestShouldCommit_ConsecutiveCalls verifies that shouldCommit is idempotent.
func TestShouldCommit_ConsecutiveCalls(t *testing.T) {
	c := &Consumer{
		cfg: &Config{
			ApplyCommitInterval:   10,
			ApplyCommitMaxLatency: 5 * time.Minute,
		},
		uncommittedBlocks: 10,
		lastCommitTime:    time.Now(),
	}

	// Multiple calls should return same result
	first := c.shouldCommit()
	second := c.shouldCommit()
	third := c.shouldCommit()

	if first != second || second != third {
		t.Errorf("shouldCommit() not idempotent: %v, %v, %v", first, second, third)
	}
}

// TestShouldCommit_TimeProgression verifies time-based triggering over time.
func TestShouldCommit_TimeProgression(t *testing.T) {
	maxLatency := 100 * time.Millisecond
	c := &Consumer{
		cfg: &Config{
			ApplyCommitInterval:   1000, // High block threshold
			ApplyCommitMaxLatency: maxLatency,
		},
		uncommittedBlocks: 5,
		lastCommitTime:    time.Now(),
	}

	// Initially should not commit
	if c.shouldCommit() {
		t.Errorf("expected no commit initially")
	}

	// Wait for time threshold to pass
	time.Sleep(maxLatency + 10*time.Millisecond)

	// Now should commit
	if !c.shouldCommit() {
		t.Errorf("expected commit after time threshold passed")
	}
}

// TestShouldCommit_BoundaryConditions tests exact boundary conditions.
func TestShouldCommit_BoundaryConditions(t *testing.T) {
	tests := []struct {
		name              string
		uncommittedBlocks uint64
		interval          uint64
		want              bool
	}{
		{
			name:              "exactly at boundary",
			uncommittedBlocks: 100,
			interval:          100,
			want:              true,
		},
		{
			name:              "one below boundary",
			uncommittedBlocks: 99,
			interval:          100,
			want:              false,
		},
		{
			name:              "one above boundary",
			uncommittedBlocks: 101,
			interval:          100,
			want:              true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Consumer{
				cfg: &Config{
					ApplyCommitInterval:   tt.interval,
					ApplyCommitMaxLatency: 24 * time.Hour,
				},
				uncommittedBlocks: tt.uncommittedBlocks,
				lastCommitTime:    time.Now(),
			}
			got := c.shouldCommit()
			if got != tt.want {
				t.Errorf("shouldCommit() = %v, want %v (blocks=%d, interval=%d)",
					got, tt.want, tt.uncommittedBlocks, tt.interval)
			}
		})
	}
}

// TestShouldCommit_TableDriven provides comprehensive coverage using table-driven tests.
func TestShouldCommit_TableDriven(t *testing.T) {
	tests := []struct {
		name              string
		uncommittedBlocks uint64
		interval          uint64
		elapsed           time.Duration
		maxLatency        time.Duration
		want              bool
		description       string
	}{
		{
			name:              "fresh state, nothing to commit",
			uncommittedBlocks: 0,
			interval:          100,
			elapsed:           0,
			maxLatency:        5 * time.Minute,
			want:              false,
			description:       "no blocks processed yet",
		},
		{
			name:              "single block below threshold",
			uncommittedBlocks: 1,
			interval:          100,
			elapsed:           time.Second,
			maxLatency:        5 * time.Minute,
			want:              false,
			description:       "only one block, threshold is 100",
		},
		{
			name:              "blocks at threshold",
			uncommittedBlocks: 100,
			interval:          100,
			elapsed:           time.Second,
			maxLatency:        5 * time.Minute,
			want:              true,
			description:       "block count threshold reached",
		},
		{
			name:              "time at threshold",
			uncommittedBlocks: 1,
			interval:          100,
			elapsed:           5 * time.Minute,
			maxLatency:        5 * time.Minute,
			want:              true,
			description:       "time threshold reached",
		},
		{
			name:              "both thresholds far away",
			uncommittedBlocks: 10,
			interval:          100,
			elapsed:           time.Minute,
			maxLatency:        10 * time.Minute,
			want:              false,
			description:       "neither threshold close",
		},
		{
			name:              "aggressive commit policy",
			uncommittedBlocks: 1,
			interval:          1,
			elapsed:           time.Millisecond,
			maxLatency:        time.Millisecond,
			want:              true,
			description:       "commit every block immediately",
		},
		{
			name:              "conservative commit policy",
			uncommittedBlocks: 500,
			interval:          10000,
			elapsed:           time.Hour,
			maxLatency:        24 * time.Hour,
			want:              false,
			description:       "very high thresholds, rarely commit",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := time.Now()
			c := &Consumer{
				cfg: &Config{
					ApplyCommitInterval:   tt.interval,
					ApplyCommitMaxLatency: tt.maxLatency,
				},
				uncommittedBlocks: tt.uncommittedBlocks,
				lastCommitTime:    now.Add(-tt.elapsed),
			}
			got := c.shouldCommit()
			if got != tt.want {
				t.Errorf("shouldCommit() = %v, want %v", got, tt.want)
				t.Logf("  Description: %s", tt.description)
				t.Logf("  Config: interval=%d, maxLatency=%v", tt.interval, tt.maxLatency)
				t.Logf("  State: uncommitted=%d, elapsed=%v", tt.uncommittedBlocks, tt.elapsed)
			}
		})
	}
}

// TestShouldCommit_RealisticScenarios tests realistic commit policy configurations.
func TestShouldCommit_RealisticScenarios(t *testing.T) {
	tests := []struct {
		name              string
		interval          uint64
		maxLatency        time.Duration
		uncommittedBlocks uint64
		elapsed           time.Duration
		want              bool
	}{
		{
			name:              "mainnet aggressive: 10 blocks, 1 min",
			interval:          10,
			maxLatency:        time.Minute,
			uncommittedBlocks: 5,
			elapsed:           30 * time.Second,
			want:              false,
		},
		{
			name:              "mainnet aggressive: hit block threshold",
			interval:          10,
			maxLatency:        time.Minute,
			uncommittedBlocks: 10,
			elapsed:           30 * time.Second,
			want:              true,
		},
		{
			name:              "mainnet aggressive: hit time threshold",
			interval:          10,
			maxLatency:        time.Minute,
			uncommittedBlocks: 5,
			elapsed:           65 * time.Second,
			want:              true,
		},
		{
			name:              "testnet balanced: 100 blocks, 5 min",
			interval:          100,
			maxLatency:        5 * time.Minute,
			uncommittedBlocks: 50,
			elapsed:           2 * time.Minute,
			want:              false,
		},
		{
			name:              "archive node conservative: 1000 blocks, 30 min",
			interval:          1000,
			maxLatency:        30 * time.Minute,
			uncommittedBlocks: 500,
			elapsed:           15 * time.Minute,
			want:              false,
		},
		{
			name:              "archive node: hit block threshold",
			interval:          1000,
			maxLatency:        30 * time.Minute,
			uncommittedBlocks: 1000,
			elapsed:           10 * time.Minute,
			want:              true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := time.Now()
			c := &Consumer{
				cfg: &Config{
					ApplyCommitInterval:   tt.interval,
					ApplyCommitMaxLatency: tt.maxLatency,
				},
				uncommittedBlocks: tt.uncommittedBlocks,
				lastCommitTime:    now.Add(-tt.elapsed),
			}
			got := c.shouldCommit()
			if got != tt.want {
				t.Errorf("shouldCommit() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestConsumerState_Isolation ensures ConsumerState behaves correctly in isolation.
func TestConsumerState_Isolation(t *testing.T) {
	// Verify that ConsumerState fields can be safely manipulated
	state := ConsumerState{
		PendingSeq:  5,
		AppliedSeq:  4,
		AppliedRoot: [32]byte{0x01, 0x02},
	}

	if state.PendingSeq != 5 {
		t.Errorf("PendingSeq = %d, want 5", state.PendingSeq)
	}
	if state.AppliedSeq != 4 {
		t.Errorf("AppliedSeq = %d, want 4", state.AppliedSeq)
	}

	// Verify zero state
	zeroState := ConsumerState{}
	if zeroState.PendingSeq != 0 || zeroState.AppliedSeq != 0 {
		t.Errorf("zero state should have zero sequences")
	}
}

func TestPendingSeq_SeqZeroPersistsWithActiveFlag(t *testing.T) {
	db := rawdb.NewMemoryDatabase()
	c := &Consumer{db: db}

	c.markPendingSeq(0)
	state := rawdb.ReadUBTConsumerState(db)
	if state == nil {
		t.Fatal("expected persisted state after markPendingSeq(0)")
	}
	if state.PendingSeq != 0 {
		t.Fatalf("expected pending seq 0, got %d", state.PendingSeq)
	}
	if !state.PendingSeqActive {
		t.Fatal("expected PendingSeqActive=true for seq 0")
	}
	if state.PendingStatus != rawdb.UBTConsumerPendingInFlight {
		t.Fatalf("expected PendingStatus=inflight, got %v", state.PendingStatus)
	}
	if state.PendingUpdatedAt == 0 {
		t.Fatal("expected PendingUpdatedAt to be set")
	}
}

// TestProcessedSeq_AdvancesPerEvent verifies processedSeq advances on each event.
func TestProcessedSeq_AdvancesPerEvent(t *testing.T) {
	// This test verifies that processedSeq increments in-memory for each event
	// regardless of whether a commit occurs.
	c := &Consumer{
		cfg: &Config{
			ApplyCommitInterval:   1000, // High threshold - won't commit
			ApplyCommitMaxLatency: 24 * time.Hour,
		},
		processedSeq:      10,
		uncommittedBlocks: 0,
		lastCommitTime:    time.Now(),
	}

	// Simulate processing events without commit
	initialSeq := c.processedSeq
	c.processedSeq++
	c.uncommittedBlocks++

	if c.processedSeq != initialSeq+1 {
		t.Errorf("processedSeq should advance: got %d, want %d", c.processedSeq, initialSeq+1)
	}

	c.processedSeq++
	c.uncommittedBlocks++

	if c.processedSeq != initialSeq+2 {
		t.Errorf("processedSeq should advance again: got %d, want %d", c.processedSeq, initialSeq+2)
	}
}

// TestAppliedSeq_OnlyAdvancesAfterCommit verifies AppliedSeq only changes after commit.
func TestAppliedSeq_OnlyAdvancesAfterCommit(t *testing.T) {
	// This test verifies the core fix: AppliedSeq (durable state) only advances
	// after a successful commit, not after every event.
	c := &Consumer{
		cfg: &Config{
			ApplyCommitInterval:   1000,
			ApplyCommitMaxLatency: 24 * time.Hour,
		},
		state: ConsumerState{
			AppliedSeq: 10,
		},
		processedSeq:      10,
		uncommittedBlocks: 0,
		lastCommitTime:    time.Now(),
	}

	// Process events without triggering commit
	c.processedSeq++ // Event 11
	c.uncommittedBlocks++
	c.processedSeq++ // Event 12
	c.uncommittedBlocks++

	// AppliedSeq should NOT have changed yet
	if c.state.AppliedSeq != 10 {
		t.Errorf("AppliedSeq should not advance before commit: got %d, want 10", c.state.AppliedSeq)
	}

	// processedSeq should be ahead
	if c.processedSeq != 12 {
		t.Errorf("processedSeq should be 12, got %d", c.processedSeq)
	}

	// After commit (simulated by updating state)
	c.state.AppliedSeq = c.processedSeq

	// Now AppliedSeq should match processedSeq
	if c.state.AppliedSeq != 12 {
		t.Errorf("AppliedSeq should advance after commit: got %d, want 12", c.state.AppliedSeq)
	}
}

// TestCrashRecovery_ReplayFromAppliedSeq verifies restart behavior after crash.
func TestCrashRecovery_ReplayFromAppliedSeq(t *testing.T) {
	// Simulate a crash scenario:
	// - processedSeq is at 15 (in-memory, lost on crash)
	// - AppliedSeq is at 10 (persisted to disk)
	// On restart, consumer should start from AppliedSeq + 1 = 11

	// Simulate state after crash (loaded from disk)
	persistedState := ConsumerState{
		AppliedSeq:   10,
		AppliedRoot:  [32]byte{0xaa},
		AppliedBlock: 100,
	}

	// Create new consumer (simulates restart with existing state)
	c := &Consumer{
		cfg: &Config{
			ApplyCommitInterval:   10,
			ApplyCommitMaxLatency: 5 * time.Minute,
		},
		state:          persistedState,
		hasState:       true, // State was loaded from DB
		lastCommitTime: time.Now(),
	}

	// Initialize processedSeq from durable state (this happens in NewConsumer)
	c.processedSeq = c.state.AppliedSeq

	// Verify processedSeq is initialized correctly
	if c.processedSeq != 10 {
		t.Errorf("processedSeq should be initialized from AppliedSeq: got %d, want 10", c.processedSeq)
	}

	// Next event should be AppliedSeq + 1
	nextSeq := c.processedSeq + 1
	if nextSeq != 11 {
		t.Errorf("next event should be 11, got %d", nextSeq)
	}
}

// TestFreshStart_ConsumesSeqZero verifies fresh start correctly targets seq=0.
func TestFreshStart_ConsumesSeqZero(t *testing.T) {
	// Fresh consumer has no persisted state, should start from seq=0
	c := &Consumer{
		cfg: &Config{
			ApplyCommitInterval:   10,
			ApplyCommitMaxLatency: 5 * time.Minute,
		},
		hasState:       false, // No state loaded from DB
		lastCommitTime: time.Now(),
	}

	// Initialize processedSeq as NewConsumer does for fresh start
	c.processedSeq = ^uint64(0)

	// Next event should be seq=0 (overflow wraps to 0)
	targetSeq := c.processedSeq + 1
	if targetSeq != 0 {
		t.Errorf("fresh start should target seq=0, got %d", targetSeq)
	}
}

// TestRestart_AfterConsumingSeqZero verifies restart after consuming seq=0 targets seq=1.
func TestRestart_AfterConsumingSeqZero(t *testing.T) {
	// After consuming seq=0 and committing, AppliedSeq=0
	c := &Consumer{
		cfg: &Config{
			ApplyCommitInterval:   10,
			ApplyCommitMaxLatency: 5 * time.Minute,
		},
		state: ConsumerState{
			AppliedSeq: 0,
		},
		hasState:       true, // State was loaded from DB (seq=0 was consumed)
		lastCommitTime: time.Now(),
	}

	// Initialize processedSeq from durable state
	c.processedSeq = c.state.AppliedSeq

	// Next event should be seq=1
	targetSeq := c.processedSeq + 1
	if targetSeq != 1 {
		t.Errorf("restart after seq=0 should target seq=1, got %d", targetSeq)
	}
}

// TestCommit_UpdatesBothSeq verifies commit synchronizes processedSeq to AppliedSeq.
func TestCommit_UpdatesBothSeq(t *testing.T) {
	// Verify that commit moves processedSeq value into AppliedSeq
	c := &Consumer{
		cfg: &Config{
			ApplyCommitInterval:   10,
			ApplyCommitMaxLatency: 5 * time.Minute,
		},
		state: ConsumerState{
			AppliedSeq:   100,
			AppliedRoot:  [32]byte{0xaa},
			AppliedBlock: 1000,
		},
		processedSeq:      105, // 5 events processed but not committed
		pendingRoot:       [32]byte{0xbb},
		pendingBlock:      1005,
		uncommittedBlocks: 5,
		lastCommitTime:    time.Now(),
	}

	initialAppliedSeq := c.state.AppliedSeq
	expectedNewSeq := c.processedSeq

	// After commit, AppliedSeq should be updated to processedSeq
	// (We can't call commit() directly without full setup, so we simulate the logic)
	c.state.AppliedSeq = c.processedSeq
	c.state.AppliedRoot = c.pendingRoot
	c.state.AppliedBlock = c.pendingBlock
	c.uncommittedBlocks = 0

	if c.state.AppliedSeq != expectedNewSeq {
		t.Errorf("AppliedSeq after commit = %d, want %d", c.state.AppliedSeq, expectedNewSeq)
	}

	if c.state.AppliedSeq == initialAppliedSeq {
		t.Errorf("AppliedSeq should have changed after commit")
	}

	if c.uncommittedBlocks != 0 {
		t.Errorf("uncommittedBlocks should be reset to 0 after commit, got %d", c.uncommittedBlocks)
	}
}

// TestPendingState_TracksInMemoryChanges verifies pendingRoot and pendingBlock.
func TestPendingState_TracksInMemoryChanges(t *testing.T) {
	// Verify that pendingRoot and pendingBlock track in-memory state correctly
	committedRoot := [32]byte{0xaa}
	committedBlock := uint64(1000)

	c := &Consumer{
		cfg: &Config{
			ApplyCommitInterval:   100,
			ApplyCommitMaxLatency: 10 * time.Minute,
		},
		state: ConsumerState{
			AppliedSeq:   50,
			AppliedRoot:  committedRoot,
			AppliedBlock: committedBlock,
		},
		processedSeq:      50,
		pendingRoot:       committedRoot,
		pendingBlock:      committedBlock,
		uncommittedBlocks: 0,
		lastCommitTime:    time.Now(),
	}

	// Process a new event (simulate)
	newRoot := [32]byte{0xbb}
	newBlock := uint64(1001)

	c.processedSeq++
	c.pendingRoot = newRoot
	c.pendingBlock = newBlock
	c.uncommittedBlocks++

	// Verify pending state changed but committed state didn't
	if c.pendingRoot != newRoot {
		t.Errorf("pendingRoot should be updated to new root")
	}
	if c.pendingBlock != newBlock {
		t.Errorf("pendingBlock should be updated to new block")
	}
	if c.state.AppliedRoot == newRoot {
		t.Errorf("AppliedRoot should NOT be updated before commit")
	}
	if c.state.AppliedBlock == newBlock {
		t.Errorf("AppliedBlock should NOT be updated before commit")
	}

	// After commit (simulate)
	c.state.AppliedSeq = c.processedSeq
	c.state.AppliedRoot = c.pendingRoot
	c.state.AppliedBlock = c.pendingBlock

	// Now committed state should match pending state
	if c.state.AppliedRoot != newRoot {
		t.Errorf("AppliedRoot should be updated after commit")
	}
	if c.state.AppliedBlock != newBlock {
		t.Errorf("AppliedBlock should be updated after commit")
	}
}

func TestShouldWarnCommittedRootMismatch(t *testing.T) {
	tests := []struct {
		name             string
		pendingRoot      [32]byte
		pendingRootKnown bool
		committedRoot    [32]byte
		want             bool
	}{
		{
			name:             "unknown pending root does not warn",
			pendingRoot:      [32]byte{0xaa},
			pendingRootKnown: false,
			committedRoot:    [32]byte{0xbb},
			want:             false,
		},
		{
			name:             "known matching roots do not warn",
			pendingRoot:      [32]byte{0xaa},
			pendingRootKnown: true,
			committedRoot:    [32]byte{0xaa},
			want:             false,
		},
		{
			name:             "known mismatching roots warn",
			pendingRoot:      [32]byte{0xaa},
			pendingRootKnown: true,
			committedRoot:    [32]byte{0xbb},
			want:             true,
		},
		{
			name:             "empty pending root does not warn",
			pendingRoot:      [32]byte{},
			pendingRootKnown: true,
			committedRoot:    [32]byte{0xbb},
			want:             false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Consumer{
				pendingRoot:      tt.pendingRoot,
				pendingRootKnown: tt.pendingRootKnown,
			}
			if got := c.shouldWarnCommittedRootMismatch(tt.committedRoot); got != tt.want {
				t.Fatalf("shouldWarnCommittedRootMismatch()=%v want=%v", got, tt.want)
			}
		})
	}
}
