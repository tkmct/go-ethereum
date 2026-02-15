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
)

// BenchmarkShouldCommit_BlockThreshold benchmarks the block threshold check.
func BenchmarkShouldCommit_BlockThreshold(b *testing.B) {
	c := &Consumer{
		cfg: &Config{
			ApplyCommitInterval:   100,
			ApplyCommitMaxLatency: 24 * time.Hour,
		},
		uncommittedBlocks: 50,
		lastCommitTime:    time.Now(),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.shouldCommit()
	}
}

// BenchmarkShouldCommit_TimeThreshold benchmarks the time threshold check.
func BenchmarkShouldCommit_TimeThreshold(b *testing.B) {
	c := &Consumer{
		cfg: &Config{
			ApplyCommitInterval:   1000000,
			ApplyCommitMaxLatency: 5 * time.Minute,
		},
		uncommittedBlocks: 50,
		lastCommitTime:    time.Now().Add(-2 * time.Minute),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.shouldCommit()
	}
}

// BenchmarkShouldCommit_BothChecks benchmarks when both checks are evaluated.
func BenchmarkShouldCommit_BothChecks(b *testing.B) {
	c := &Consumer{
		cfg: &Config{
			ApplyCommitInterval:   100,
			ApplyCommitMaxLatency: 5 * time.Minute,
		},
		uncommittedBlocks: 50,
		lastCommitTime:    time.Now().Add(-2 * time.Minute),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.shouldCommit()
	}
}

// BenchmarkConfigValidate benchmarks configuration validation.
func BenchmarkConfigValidate(b *testing.B) {
	cfg := &Config{
		OutboxRPCEndpoint:     "http://localhost:8545",
		DataDir:               "/tmp/ubtconv",
		ApplyCommitInterval:   100,
		ApplyCommitMaxLatency: 5 * time.Minute,
		TrieDBScheme:          "path",
		TrieDBStateHistory:    90000,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = cfg.Validate()
	}
}

// BenchmarkConfigValidate_Parallel benchmarks validation with parallel execution.
func BenchmarkConfigValidate_Parallel(b *testing.B) {
	cfg := &Config{
		OutboxRPCEndpoint:     "http://localhost:8545",
		DataDir:               "/tmp/ubtconv",
		ApplyCommitInterval:   100,
		ApplyCommitMaxLatency: 5 * time.Minute,
		TrieDBScheme:          "path",
	}

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = cfg.Validate()
		}
	})
}

// BenchmarkShouldCommit_Parallel benchmarks shouldCommit with parallel execution.
func BenchmarkShouldCommit_Parallel(b *testing.B) {
	c := &Consumer{
		cfg: &Config{
			ApplyCommitInterval:   100,
			ApplyCommitMaxLatency: 5 * time.Minute,
		},
		uncommittedBlocks: 50,
		lastCommitTime:    time.Now().Add(-2 * time.Minute),
	}

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			c.shouldCommit()
		}
	})
}
