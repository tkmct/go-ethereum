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
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/params"
)

// Config holds the ubtconv daemon configuration.
type Config struct {
	OutboxRPCEndpoint        string
	DataDir                  string
	ApplyCommitInterval      uint64
	ApplyCommitMaxLatency    time.Duration
	BootstrapMode            string // "tail" or "backfill-direct"
	MaxRecoverableReorgDepth uint64
	TrieDBScheme             string // "path"
	TrieDBStateHistory       uint64
	RequireArchiveReplay     bool
	AnchorSnapshotInterval   uint64 // Create anchor every N commits (0 = disabled)
	AnchorSnapshotRetention  uint64 // Keep last N anchors (0 = keep all)
	ValidationEnabled        bool   // Enable validation checkpoint logging
	// ValidationSampleRate specifies validation frequency as every Nth block (0 = disabled).
	// Note: plan §16.2 specifies float64 (random probability), but uint64 was chosen for
	// deterministic, reproducible behavior — every Nth block is easier to reason about
	// in production and provides consistent validation coverage.
	ValidationSampleRate     uint64
	QueryRPCEnabled          bool   // Enable query RPC server
	QueryRPCListenAddr       string // Listen address for query RPC (default ":8546")
	ChainID                  uint64 // Chain ID for EVM execution (default: 1 = mainnet)
	RPCGasCap                uint64 // Gas cap for CallUBT RPC (default: 50_000_000, same as geth)
	BackpressureLagThreshold uint64 // Seq lag threshold to trigger faster commits (0 = disabled)

	// Outbox disk budget (Chunk 2)
	OutboxDiskBudgetBytes   uint64 // 0 = unlimited
	OutboxAlertThresholdPct uint64 // Trigger compaction when usage exceeds this % (default: 80)

	// Slot index (Chunk 4)
	SlotIndexMode       string // "auto", "on", "off" (default: "auto")
	SlotIndexDiskBudget uint64 // 0 = unlimited
	CancunBlock         uint64 // Explicit Cancun fork block number (0 = auto-detect from chain config)

	// Query RPC limits
	QueryRPCMaxBatch uint64 // Max batch size for list-style RPC methods (default: 100)

	// Strict validation (Chunk 5)
	ValidationStrictMode    bool // Validate ALL accounts/storage in diff against MPT
	ValidationHaltOnMismatch bool // Halt daemon on strict validation mismatch

	// Migration workflow (Chunk 7)
	ValidateOnlyMode       bool          // Shadow verification without trie modification
	SyncedLagThreshold     uint64        // Blocks behind head to consider synced (default: 10)
	ProductionReadinessMin time.Duration // Duration synced before production-ready (default: 10m)
}

// Validate checks if the configuration is valid.
func (c *Config) Validate() error {
	if c.OutboxRPCEndpoint == "" {
		return fmt.Errorf("outbox-rpc-endpoint is required")
	}
	if c.DataDir == "" {
		return fmt.Errorf("datadir is required")
	}
	if c.BootstrapMode != "tail" && c.BootstrapMode != "backfill-direct" {
		return fmt.Errorf("bootstrap-mode must be 'tail' or 'backfill-direct', got %q", c.BootstrapMode)
	}
	if c.TrieDBScheme != "path" {
		return fmt.Errorf("triedb-scheme must be 'path', got %q", c.TrieDBScheme)
	}
	if c.ApplyCommitInterval == 0 {
		return fmt.Errorf("apply-commit-interval must be > 0")
	}
	// §11 R12: Retention invariant — state history must accommodate reorg recovery + safety margin
	const retentionSafetyMargin = 64
	if c.MaxRecoverableReorgDepth > 0 && c.TrieDBStateHistory > 0 {
		required := c.MaxRecoverableReorgDepth + retentionSafetyMargin
		if required > c.TrieDBStateHistory {
			return fmt.Errorf(
				"retention invariant violated: MaxRecoverableReorgDepth (%d) + safety margin (%d) = %d exceeds TrieDBStateHistory (%d); increase --triedb-state-history or decrease --max-recoverable-reorg-depth",
				c.MaxRecoverableReorgDepth, retentionSafetyMargin, required, c.TrieDBStateHistory)
		}
	}
	return nil
}

// resolveChainConfig returns the appropriate chain config for the configured chain ID.
func (c *Config) resolveChainConfig() *params.ChainConfig {
	switch c.ChainID {
	case 0, 1:
		return params.MainnetChainConfig
	case 11155111:
		return params.SepoliaChainConfig
	case 17000:
		return params.HoleskyChainConfig
	default:
		cfg := *params.AllEthashProtocolChanges
		cfg.ChainID = new(big.Int).SetUint64(c.ChainID)
		return &cfg
	}
}

// effectiveRPCGasCap returns the configured RPC gas cap, or the default (50M) if unset.
func (c *Config) effectiveRPCGasCap() uint64 {
	if c.RPCGasCap == 0 {
		return 50_000_000
	}
	return c.RPCGasCap
}
