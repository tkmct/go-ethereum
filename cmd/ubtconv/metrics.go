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

import "github.com/ethereum/go-ethereum/metrics"

var (
	consumerAppliedTotal            = metrics.NewRegisteredCounter("ubt/consumer/applied/total", nil)
	consumerAppliedLatency          = metrics.NewRegisteredTimer("ubt/consumer/applied/latency", nil)
	consumerCommitLatency           = metrics.NewRegisteredTimer("ubt/consumer/commit/latency", nil)
	consumerCommitTotal             = metrics.NewRegisteredCounter("ubt/consumer/commit/total", nil)
	consumerCommitTrieLatency       = metrics.NewRegisteredTimer("ubt/consumer/commit/trie/latency", nil)
	consumerCommitBatchWriteLatency = metrics.NewRegisteredTimer("ubt/consumer/commit/batchwrite/latency", nil)
	consumerReorgTotal              = metrics.NewRegisteredCounter("ubt/consumer/reorg/total", nil)
	consumerLagBlocks               = metrics.NewRegisteredGauge("ubt/consumer/lag/blocks", nil)
	consumerLagSeq                  = metrics.NewRegisteredGauge("ubt/consumer/lag/seq", nil)
	consumerErrorsTotal             = metrics.NewRegisteredCounter("ubt/consumer/errors/total", nil)
	consumerBackoffGauge            = metrics.NewRegisteredGauge("ubt/consumer/backoff/ms", nil)
	consumerQueueDepth              = metrics.NewRegisteredGauge("ubt/consumer/queue/depth", nil)
	consumerReadEventLatency        = metrics.NewRegisteredTimer("ubt/consumer/read/event/latency", nil)
	consumerReadRangeLatency        = metrics.NewRegisteredTimer("ubt/consumer/read/range/latency", nil)
	consumerReadRPCEventTotal       = metrics.NewRegisteredCounter("ubt/consumer/read/event/rpc/total", nil)
	consumerReadRPCRangeTotal       = metrics.NewRegisteredCounter("ubt/consumer/read/range/rpc/total", nil)
	consumerReadQueueHitTotal       = metrics.NewRegisteredCounter("ubt/consumer/read/queue/hit/total", nil)
	consumerDecodeDiffLatency       = metrics.NewRegisteredTimer("ubt/consumer/decode/diff/latency", nil)
	consumerDecodeReorgLatency      = metrics.NewRegisteredTimer("ubt/consumer/decode/reorg/latency", nil)
	consumerApplyDiffLatency        = metrics.NewRegisteredTimer("ubt/consumer/apply/diff/latency", nil)
	consumerRootHashComputeTotal    = metrics.NewRegisteredCounter("ubt/consumer/root/hash/compute/total", nil)
	consumerRootHashSkipTotal       = metrics.NewRegisteredCounter("ubt/consumer/root/hash/skip/total", nil)
	applierApplyAccountsLatency     = metrics.NewRegisteredTimer("ubt/applier/apply/accounts/latency", nil)
	applierApplyStorageLatency      = metrics.NewRegisteredTimer("ubt/applier/apply/storage/latency", nil)
	applierApplyCodeLatency         = metrics.NewRegisteredTimer("ubt/applier/apply/code/latency", nil)
	applierApplyAccountsTotal       = metrics.NewRegisteredCounter("ubt/applier/apply/accounts/entries/total", nil)
	applierApplyStorageTotal        = metrics.NewRegisteredCounter("ubt/applier/apply/storage/entries/total", nil)
	applierApplyCodeTotal           = metrics.NewRegisteredCounter("ubt/applier/apply/code/entries/total", nil)
	validationChecksTotal           = metrics.NewRegisteredCounter("ubt/validation/checks/total", nil)
	validationMismatches            = metrics.NewRegisteredCounter("ubt/validation/mismatches", nil)

	// Daemon replay metrics
	daemonReplayBlocksPerSec = metrics.NewRegisteredMeter("ubt/daemon/replay/blocks", nil)

	// Snapshot restore metrics
	daemonSnapshotRestoreTotal   = metrics.NewRegisteredCounter("ubt/daemon/snapshot/restore/total", nil)
	daemonSnapshotRestoreLatency = metrics.NewRegisteredTimer("ubt/daemon/snapshot/restore/latency", nil)

	// Recovery metrics
	daemonRecoveryAttempts         = metrics.NewRegisteredCounter("ubt/daemon/recovery/attempts", nil)
	daemonRecoverySuccesses        = metrics.NewRegisteredCounter("ubt/daemon/recovery/successes", nil)
	daemonRecoveryFailures         = metrics.NewRegisteredCounter("ubt/daemon/recovery/failures", nil)
	recoveryAnchorCreateAttempts   = metrics.NewRegisteredCounter("ubt/recovery_anchor/create/attempts", nil)
	recoveryAnchorCreateSuccesses  = metrics.NewRegisteredCounter("ubt/recovery_anchor/create/successes", nil)
	recoveryAnchorCreateFailures   = metrics.NewRegisteredCounter("ubt/recovery_anchor/create/failures", nil)
	recoveryAnchorRestoreAttempts  = metrics.NewRegisteredCounter("ubt/recovery_anchor/restore/attempts", nil)
	recoveryAnchorRestoreSuccesses = metrics.NewRegisteredCounter("ubt/recovery_anchor/restore/successes", nil)
	recoveryAnchorRestoreFailures  = metrics.NewRegisteredCounter("ubt/recovery_anchor/restore/failures", nil)
	recoveryAnchorLatestSeqGauge   = metrics.NewRegisteredGauge("ubt/recovery_anchor/latest/seq", nil)
	recoveryAnchorLatestBlockGauge = metrics.NewRegisteredGauge("ubt/recovery_anchor/latest/block", nil)

	// Compaction metrics
	compactionAttemptsTotal = metrics.NewRegisteredCounter("ubt/compaction/attempts/total", nil)
	compactionSuccessTotal  = metrics.NewRegisteredCounter("ubt/compaction/success/total", nil)
	compactionErrorsTotal   = metrics.NewRegisteredCounter("ubt/compaction/errors/total", nil)
	compactionLastRunGauge  = metrics.NewRegisteredGauge("ubt/compaction/lastrun/timestamp", nil)
	compactionLatency       = metrics.NewRegisteredTimer("ubt/compaction/latency", nil)
	compactionRPCLatency    = metrics.NewRegisteredTimer("ubt/compaction/rpc/latency", nil)
	pruneSkippedTotal       = metrics.NewRegisteredCounter("ubt/prune/skipped/total", nil)
	pruneDeletedTotal       = metrics.NewRegisteredCounter("ubt/prune/deleted/total", nil)
)
