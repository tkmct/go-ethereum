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

// ubtconv is the external UBT conversion daemon.
package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ethereum/go-ethereum/internal/flags"
	"github.com/ethereum/go-ethereum/log"
	"github.com/urfave/cli/v2"
)

var (
	app = flags.NewApp("UBT conversion daemon")

	// Daemon flags
	outboxRPCEndpointFlag = &cli.StringFlag{
		Name:  "outbox-rpc-endpoint",
		Usage: "Geth RPC endpoint for outbox consumption",
		Value: "http://localhost:8545",
	}
	outboxReadBatchFlag = &cli.Uint64Flag{
		Name:  "outbox-read-batch",
		Usage: "Number of outbox events to prefetch per read (1 = disable prefetch, max 1000)",
		Value: 1,
	}
	outboxReadAheadFlag = &cli.Uint64Flag{
		Name:  "outbox-read-ahead",
		Usage: "Consumer-side read-ahead window size (1 = disabled)",
		Value: 64,
	}
	dataDirectoryFlag = &cli.StringFlag{
		Name:  "datadir",
		Usage: "Data directory for UBT trie database",
		Value: "./ubtconv-data",
	}
	applyCommitIntervalFlag = &cli.Uint64Flag{
		Name:  "apply-commit-interval",
		Usage: "Number of blocks between UBT trie commits",
		Value: 128,
	}
	applyCommitMaxLatencyFlag = &cli.DurationFlag{
		Name:  "apply-commit-max-latency",
		Usage: "Maximum time between UBT trie commits",
		Value: 10 * time.Second,
	}
	pendingStatePersistIntervalFlag = &cli.DurationFlag{
		Name:  "pending-state-persist-interval",
		Usage: "Debounce interval for pending-seq state writes (0 = persist every transition)",
		Value: 200 * time.Millisecond,
	}
	treatNoEventAsIdleFlag = &cli.BoolFlag{
		Name:  "treat-no-event-as-idle",
		Usage: "Treat missing next outbox event as idle (avoid exponential backoff)",
		Value: true,
	}
	maxRecoverableReorgDepthFlag = &cli.Uint64Flag{
		Name:  "max-recoverable-reorg-depth",
		Usage: "Maximum reorg depth for fast-path recovery",
		Value: 128,
	}
	trieDBSchemeFlag = &cli.StringFlag{
		Name:  "triedb-scheme",
		Usage: "Trie database scheme (path)",
		Value: "path",
	}
	trieDBStateHistoryFlag = &cli.Uint64Flag{
		Name:  "triedb-state-history",
		Usage: "Number of blocks of state history to retain",
		Value: 90000,
	}
	requireArchiveReplayFlag = &cli.BoolFlag{
		Name:  "require-archive-replay",
		Usage: "Require archive node for deep replay",
		Value: true,
	}
	queryRPCEnabledFlag = &cli.BoolFlag{
		Name:  "query-rpc-enabled",
		Usage: "Enable UBT query RPC server",
		Value: true,
	}
	queryRPCListenAddrFlag = &cli.StringFlag{
		Name:  "query-rpc-listen-addr",
		Usage: "Listen address for UBT query RPC server",
		Value: "localhost:8560",
	}
	pprofEnabledFlag = &cli.BoolFlag{
		Name:  "pprof-enabled",
		Usage: "Enable pprof HTTP server for CPU/heap profiling",
		Value: false,
	}
	pprofListenAddrFlag = &cli.StringFlag{
		Name:  "pprof-listen-addr",
		Usage: "Listen address for pprof HTTP server",
		Value: "127.0.0.1:6061",
	}
	queryRPCMaxBatchFlag = &cli.Uint64Flag{
		Name:  "query-rpc-max-batch",
		Usage: "Maximum batch size for list-style UBT RPC methods",
		Value: 100,
	}
	anchorSnapshotIntervalFlag = &cli.Uint64Flag{
		Name:  "anchor-snapshot-interval",
		Usage: "Create anchor snapshot every N commits (0 = disabled)",
		Value: 0,
	}
	anchorSnapshotRetentionFlag = &cli.Uint64Flag{
		Name:  "anchor-snapshot-retention",
		Usage: "Keep last N anchor snapshots (0 = keep all)",
		Value: 0,
	}
	recoveryAnchorIntervalFlag = &cli.Uint64Flag{
		Name:  "recovery-anchor-interval",
		Usage: "Create materialized recovery anchor every N commits (0 = disabled)",
		Value: 0,
	}
	recoveryAnchorRetentionFlag = &cli.Uint64Flag{
		Name:  "recovery-anchor-retention",
		Usage: "Keep last N materialized recovery anchors (0 = keep all)",
		Value: 0,
	}
	recoveryStrictFlag = &cli.BoolFlag{
		Name:  "recovery-strict",
		Usage: "Fail startup if expected root is unavailable and no usable materialized recovery anchor exists",
		Value: false,
	}
	recoveryAllowGenesisFallbackFlag = &cli.BoolFlag{
		Name:  "recovery-allow-genesis-fallback",
		Usage: "Allow fallback to genesis when strict recovery cannot restore a materialized anchor",
		Value: true,
	}
	validationEnabledFlag = &cli.BoolFlag{
		Name:  "validation-enabled",
		Usage: "Enable validation checkpoint logging",
		Value: false,
	}
	validationSampleRateFlag = &cli.Uint64Flag{
		Name:  "validation-sample-rate",
		Usage: "Log validation checkpoint every Nth block (0 = disabled)",
		Value: 0,
	}
	chainIDFlag = &cli.Uint64Flag{
		Name:  "chain-id",
		Usage: "Chain ID for EVM execution context (default: 1 = mainnet)",
		Value: 1,
	}
	rpcGasCapFlag = &cli.Uint64Flag{
		Name:  "rpc-gas-cap",
		Usage: "Gas cap for ubt_callUBT RPC (default: 50000000, same as geth)",
		Value: 50_000_000,
	}
	backpressureLagThresholdFlag = &cli.Uint64Flag{
		Name:  "backpressure-lag-threshold",
		Usage: "Outbox seq lag that triggers faster commits (0 = disabled)",
		Value: 5000,
	}
	outboxDiskBudgetBytesFlag = &cli.Uint64Flag{
		Name:  "outbox-disk-budget-bytes",
		Usage: "Outbox disk budget in bytes (0 = unlimited)",
		Value: 0,
	}
	outboxAlertThresholdPctFlag = &cli.Uint64Flag{
		Name:  "outbox-alert-threshold-pct",
		Usage: "Trigger compaction when outbox disk usage exceeds this percentage",
		Value: 80,
	}
	slotIndexDiskBudgetFlag = &cli.Uint64Flag{
		Name:  "slot-index-disk-budget",
		Usage: "Slot index disk budget in bytes (0 = unlimited)",
		Value: 0,
	}
	cancunBlockFlag = &cli.Uint64Flag{
		Name:  "cancun-block",
		Usage: "Explicit Cancun fork block number for slot index boundary (0 = estimate from chain config timestamp)",
		Value: 0,
	}
	slotIndexEnabledFlag = &cli.BoolFlag{
		Name:  "slot-index-enabled",
		Usage: "Enable pre-Cancun slot index tracking",
		Value: true,
	}
	validationStrictFlag = &cli.BoolFlag{
		Name:  "validation-strict",
		Usage: "Enable strict validation of all accounts/storage in diff against MPT (plan §13: default on)",
		Value: true,
	}
	validationHaltOnMismatchFlag = &cli.BoolFlag{
		Name:  "validation-halt-on-mismatch",
		Usage: "Halt daemon on strict validation mismatch",
		Value: false,
	}
	validationStrictCatchupSampleRateFlag = &cli.Uint64Flag{
		Name:  "validation-strict-catchup-sample-rate",
		Usage: "Strict validation sampling rate while backlog is high (0 = disable strict validation during catch-up)",
		Value: 0,
	}
	validationStrictAsyncFlag = &cli.BoolFlag{
		Name:  "validation-strict-async",
		Usage: "Run strict validation asynchronously when halt-on-mismatch is disabled",
		Value: true,
	}
	validationQueueCapacityFlag = &cli.Uint64Flag{
		Name:  "validation-queue-capacity",
		Usage: "Async strict validation queue capacity",
		Value: 2048,
	}
	blockRootIndexStrideHighLagFlag = &cli.Uint64Flag{
		Name:  "block-root-index-stride-high-lag",
		Usage: "Write block-root index every N blocks while lag is high (1 = disabled)",
		Value: 16,
	}
	executionClassRPCEnabledFlag = &cli.BoolFlag{
		Name:  "execution-class-rpc-enabled",
		Usage: "Enable execution-class RPC methods (ubt_callUBT, ubt_executionWitnessUBT)",
		Value: false,
	}
)

func init() {
	app.Action = runDaemon
	app.Flags = []cli.Flag{
		outboxRPCEndpointFlag,
		outboxReadBatchFlag,
		outboxReadAheadFlag,
		dataDirectoryFlag,
		applyCommitIntervalFlag,
		applyCommitMaxLatencyFlag,
		pendingStatePersistIntervalFlag,
		treatNoEventAsIdleFlag,
		maxRecoverableReorgDepthFlag,
		trieDBSchemeFlag,
		trieDBStateHistoryFlag,
		requireArchiveReplayFlag,
		queryRPCEnabledFlag,
		queryRPCListenAddrFlag,
		pprofEnabledFlag,
		pprofListenAddrFlag,
		queryRPCMaxBatchFlag,
		anchorSnapshotIntervalFlag,
		anchorSnapshotRetentionFlag,
		recoveryAnchorIntervalFlag,
		recoveryAnchorRetentionFlag,
		recoveryStrictFlag,
		recoveryAllowGenesisFallbackFlag,
		validationEnabledFlag,
		validationSampleRateFlag,
		chainIDFlag,
		rpcGasCapFlag,
		backpressureLagThresholdFlag,
		outboxDiskBudgetBytesFlag,
		outboxAlertThresholdPctFlag,
		slotIndexDiskBudgetFlag,
		cancunBlockFlag,
		slotIndexEnabledFlag,
		validationStrictFlag,
		validationHaltOnMismatchFlag,
		validationStrictCatchupSampleRateFlag,
		validationStrictAsyncFlag,
		validationQueueCapacityFlag,
		blockRootIndexStrideHighLagFlag,
		executionClassRPCEnabledFlag,
	}
}

func main() {
	if err := app.Run(os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runDaemon(ctx *cli.Context) error {
	// Set up logging
	log.SetDefault(log.NewLogger(log.NewTerminalHandlerWithLevel(os.Stderr, log.LevelInfo, true)))

	cfg := buildConfigFromCLI(ctx)

	// Validate config
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	// Create and start runner
	runner, err := NewRunner(cfg)
	if err != nil {
		return fmt.Errorf("failed to create runner: %w", err)
	}

	// Handle signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Start
	if err := runner.Start(); err != nil {
		return fmt.Errorf("failed to start: %w", err)
	}

	log.Info("UBT conversion daemon started", "endpoint", cfg.OutboxRPCEndpoint, "datadir", cfg.DataDir)

	// Wait for signal
	sig := <-sigCh
	log.Info("Received signal, shutting down", "signal", sig)

	return runner.Stop()
}

func buildConfigFromCLI(ctx *cli.Context) *Config {
	return &Config{
		OutboxRPCEndpoint:                 ctx.String(outboxRPCEndpointFlag.Name),
		OutboxReadBatch:                   ctx.Uint64(outboxReadBatchFlag.Name),
		OutboxReadAhead:                   ctx.Uint64(outboxReadAheadFlag.Name),
		DataDir:                           ctx.String(dataDirectoryFlag.Name),
		ApplyCommitInterval:               ctx.Uint64(applyCommitIntervalFlag.Name),
		ApplyCommitMaxLatency:             ctx.Duration(applyCommitMaxLatencyFlag.Name),
		PendingStatePersistInterval:       ctx.Duration(pendingStatePersistIntervalFlag.Name),
		TreatNoEventAsIdle:                ctx.Bool(treatNoEventAsIdleFlag.Name),
		MaxRecoverableReorgDepth:          ctx.Uint64(maxRecoverableReorgDepthFlag.Name),
		TrieDBScheme:                      ctx.String(trieDBSchemeFlag.Name),
		TrieDBStateHistory:                ctx.Uint64(trieDBStateHistoryFlag.Name),
		RequireArchiveReplay:              ctx.Bool(requireArchiveReplayFlag.Name),
		QueryRPCEnabled:                   ctx.Bool(queryRPCEnabledFlag.Name),
		QueryRPCListenAddr:                ctx.String(queryRPCListenAddrFlag.Name),
		PprofEnabled:                      ctx.Bool(pprofEnabledFlag.Name),
		PprofListenAddr:                   ctx.String(pprofListenAddrFlag.Name),
		QueryRPCMaxBatch:                  ctx.Uint64(queryRPCMaxBatchFlag.Name),
		AnchorSnapshotInterval:            ctx.Uint64(anchorSnapshotIntervalFlag.Name),
		AnchorSnapshotRetention:           ctx.Uint64(anchorSnapshotRetentionFlag.Name),
		RecoveryAnchorInterval:            ctx.Uint64(recoveryAnchorIntervalFlag.Name),
		RecoveryAnchorRetention:           ctx.Uint64(recoveryAnchorRetentionFlag.Name),
		RecoveryStrict:                    ctx.Bool(recoveryStrictFlag.Name),
		RecoveryAllowGenesisFallback:      ctx.Bool(recoveryAllowGenesisFallbackFlag.Name),
		ValidationEnabled:                 ctx.Bool(validationEnabledFlag.Name),
		ValidationSampleRate:              ctx.Uint64(validationSampleRateFlag.Name),
		ChainID:                           ctx.Uint64(chainIDFlag.Name),
		RPCGasCap:                         ctx.Uint64(rpcGasCapFlag.Name),
		BackpressureLagThreshold:          ctx.Uint64(backpressureLagThresholdFlag.Name),
		OutboxDiskBudgetBytes:             ctx.Uint64(outboxDiskBudgetBytesFlag.Name),
		OutboxAlertThresholdPct:           ctx.Uint64(outboxAlertThresholdPctFlag.Name),
		SlotIndexDiskBudget:               ctx.Uint64(slotIndexDiskBudgetFlag.Name),
		CancunBlock:                       ctx.Uint64(cancunBlockFlag.Name),
		SlotIndexEnabled:                  ctx.Bool(slotIndexEnabledFlag.Name),
		ValidationStrictMode:              ctx.Bool(validationStrictFlag.Name),
		ValidationHaltOnMismatch:          ctx.Bool(validationHaltOnMismatchFlag.Name),
		ValidationStrictCatchupSampleRate: ctx.Uint64(validationStrictCatchupSampleRateFlag.Name),
		ValidationStrictAsync:             ctx.Bool(validationStrictAsyncFlag.Name),
		ValidationQueueCapacity:           ctx.Uint64(validationQueueCapacityFlag.Name),
		ExecutionClassRPCEnabled:          ctx.Bool(executionClassRPCEnabledFlag.Name),
		BlockRootIndexStrideHighLag:       ctx.Uint64(blockRootIndexStrideHighLagFlag.Name),
	}
}
