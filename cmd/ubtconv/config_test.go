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
	"strings"
	"testing"
	"time"
)

func TestConfigValidate_MissingRPCEndpoint(t *testing.T) {
	cfg := &Config{
		OutboxRPCEndpoint:     "", // Missing
		DataDir:               "/tmp/test",
		ApplyCommitInterval:   10,
		ApplyCommitMaxLatency: time.Minute,
		BootstrapMode:         "tail",
		TrieDBScheme:          "path",
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for missing RPC endpoint")
	}
	if !strings.Contains(err.Error(), "outbox-rpc-endpoint is required") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestConfigValidate_MissingDataDir(t *testing.T) {
	cfg := &Config{
		OutboxRPCEndpoint:     "http://localhost:8545",
		DataDir:               "", // Missing
		ApplyCommitInterval:   10,
		ApplyCommitMaxLatency: time.Minute,
		BootstrapMode:         "tail",
		TrieDBScheme:          "path",
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for missing data dir")
	}
	if !strings.Contains(err.Error(), "datadir is required") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestConfigValidate_ZeroCommitInterval(t *testing.T) {
	cfg := &Config{
		OutboxRPCEndpoint:     "http://localhost:8545",
		DataDir:               "/tmp/test",
		ApplyCommitInterval:   0, // Zero not allowed
		ApplyCommitMaxLatency: time.Minute,
		BootstrapMode:         "tail",
		TrieDBScheme:          "path",
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for zero commit interval")
	}
	if !strings.Contains(err.Error(), "apply-commit-interval must be > 0") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestConfigValidate_InvalidBootstrapMode(t *testing.T) {
	tests := []struct {
		name string
		mode string
	}{
		{"empty", ""},
		{"invalid", "invalid-mode"},
		{"typo", "tails"},
		{"backfill", "backfill"}, // Missing "-direct"
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				OutboxRPCEndpoint:     "http://localhost:8545",
				DataDir:               "/tmp/test",
				ApplyCommitInterval:   10,
				ApplyCommitMaxLatency: time.Minute,
				BootstrapMode:         tt.mode,
				TrieDBScheme:          "path",
			}
			err := cfg.Validate()
			if err == nil {
				t.Fatalf("expected validation error for bootstrap mode %q", tt.mode)
			}
			if !strings.Contains(err.Error(), "bootstrap-mode must be") {
				t.Errorf("unexpected error message: %v", err)
			}
		})
	}
}

func TestConfigValidate_InvalidTrieDBScheme(t *testing.T) {
	tests := []struct {
		name   string
		scheme string
	}{
		{"empty", ""},
		{"hash", "hash"},
		{"verkle", "verkle"},
		{"invalid", "invalid"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				OutboxRPCEndpoint:     "http://localhost:8545",
				DataDir:               "/tmp/test",
				ApplyCommitInterval:   10,
				ApplyCommitMaxLatency: time.Minute,
				BootstrapMode:         "tail",
				TrieDBScheme:          tt.scheme,
			}
			err := cfg.Validate()
			if err == nil {
				t.Fatalf("expected validation error for trie scheme %q", tt.scheme)
			}
			if !strings.Contains(err.Error(), "triedb-scheme must be 'path'") {
				t.Errorf("unexpected error message: %v", err)
			}
		})
	}
}

func TestConfigValidate_ValidConfigs(t *testing.T) {
	tests := []struct {
		name   string
		config *Config
	}{
		{
			name: "tail mode minimal",
			config: &Config{
				OutboxRPCEndpoint:     "http://localhost:8545",
				DataDir:               "/tmp/ubtconv",
				ApplyCommitInterval:   10,
				ApplyCommitMaxLatency: time.Minute,
				BootstrapMode:         "tail",
				TrieDBScheme:          "path",
			},
		},
		{
			name: "backfill-direct mode",
			config: &Config{
				OutboxRPCEndpoint:     "http://localhost:8545",
				DataDir:               "/tmp/ubtconv",
				ApplyCommitInterval:   100,
				ApplyCommitMaxLatency: 5 * time.Minute,
				BootstrapMode:         "backfill-direct",
				TrieDBScheme:          "path",
				TrieDBStateHistory:    90000,
			},
		},
		{
			name: "with archive replay",
			config: &Config{
				OutboxRPCEndpoint:        "http://localhost:8545",
				DataDir:                  "/var/lib/ubtconv",
				ApplyCommitInterval:      1000,
				ApplyCommitMaxLatency:    30 * time.Minute,
				BootstrapMode:            "tail",
				MaxRecoverableReorgDepth: 128,
				TrieDBScheme:             "path",
				TrieDBStateHistory:       90000,
				RequireArchiveReplay:     true,
			},
		},
		{
			name: "https endpoint",
			config: &Config{
				OutboxRPCEndpoint:     "https://mainnet.example.com:8545",
				DataDir:               "/data/ubtconv",
				ApplyCommitInterval:   50,
				ApplyCommitMaxLatency: 10 * time.Minute,
				BootstrapMode:         "tail",
				TrieDBScheme:          "path",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if err != nil {
				t.Fatalf("expected valid config, got error: %v", err)
			}
		})
	}
}

func TestConfigValidate_EdgeCases(t *testing.T) {
	t.Run("very small commit interval", func(t *testing.T) {
		cfg := &Config{
			OutboxRPCEndpoint:     "http://localhost:8545",
			DataDir:               "/tmp/test",
			ApplyCommitInterval:   1, // Very small but valid
			ApplyCommitMaxLatency: time.Second,
			BootstrapMode:         "tail",
			TrieDBScheme:          "path",
		}
		err := cfg.Validate()
		if err != nil {
			t.Errorf("expected valid config with interval=1, got error: %v", err)
		}
	})

	t.Run("very large commit interval", func(t *testing.T) {
		cfg := &Config{
			OutboxRPCEndpoint:     "http://localhost:8545",
			DataDir:               "/tmp/test",
			ApplyCommitInterval:   1000000, // Very large but valid
			ApplyCommitMaxLatency: 24 * time.Hour,
			BootstrapMode:         "tail",
			TrieDBScheme:          "path",
		}
		err := cfg.Validate()
		if err != nil {
			t.Errorf("expected valid config with large interval, got error: %v", err)
		}
	})

	t.Run("zero max latency is valid", func(t *testing.T) {
		cfg := &Config{
			OutboxRPCEndpoint:     "http://localhost:8545",
			DataDir:               "/tmp/test",
			ApplyCommitInterval:   10,
			ApplyCommitMaxLatency: 0, // Zero latency effectively disables time-based commits
			BootstrapMode:         "tail",
			TrieDBScheme:          "path",
		}
		err := cfg.Validate()
		if err != nil {
			t.Errorf("expected valid config with zero max latency, got error: %v", err)
		}
	})

	t.Run("zero state history is valid", func(t *testing.T) {
		cfg := &Config{
			OutboxRPCEndpoint:     "http://localhost:8545",
			DataDir:               "/tmp/test",
			ApplyCommitInterval:   10,
			ApplyCommitMaxLatency: time.Minute,
			BootstrapMode:         "tail",
			TrieDBScheme:          "path",
			TrieDBStateHistory:    0, // No history retained
		}
		err := cfg.Validate()
		if err != nil {
			t.Errorf("expected valid config with zero state history, got error: %v", err)
		}
	})
}

func TestConfig_ExecutionClassRPCEnabled_DefaultFalse(t *testing.T) {
	cfg := &Config{}
	if cfg.ExecutionClassRPCEnabled {
		t.Fatalf("expected ExecutionClassRPCEnabled default false")
	}
}

func TestConfig_ExecutionClassRPCEnabled_ExplicitTrue(t *testing.T) {
	cfg := &Config{ExecutionClassRPCEnabled: true}
	if !cfg.ExecutionClassRPCEnabled {
		t.Fatalf("expected ExecutionClassRPCEnabled true when explicitly set")
	}
}
