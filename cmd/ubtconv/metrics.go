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
	consumerAppliedTotal   = metrics.NewRegisteredCounter("ubt/consumer/applied/total", nil)
	consumerAppliedLatency = metrics.NewRegisteredTimer("ubt/consumer/applied/latency", nil)
	consumerCommitLatency  = metrics.NewRegisteredTimer("ubt/consumer/commit/latency", nil)
	consumerCommitTotal    = metrics.NewRegisteredCounter("ubt/consumer/commit/total", nil)
	consumerReorgTotal     = metrics.NewRegisteredCounter("ubt/consumer/reorg/total", nil)
	consumerLagBlocks      = metrics.NewRegisteredGauge("ubt/consumer/lag/blocks", nil)
	consumerLagSeq         = metrics.NewRegisteredGauge("ubt/consumer/lag/seq", nil)
	consumerErrorsTotal    = metrics.NewRegisteredCounter("ubt/consumer/errors/total", nil)
	consumerBackoffGauge   = metrics.NewRegisteredGauge("ubt/consumer/backoff/ms", nil)
	consumerQueueDepth     = metrics.NewRegisteredGauge("ubt/consumer/queue/depth", nil)
	validationChecksTotal  = metrics.NewRegisteredCounter("ubt/validation/checks/total", nil)
	validationMismatches   = metrics.NewRegisteredCounter("ubt/validation/mismatches", nil)

	// Daemon replay metrics
	daemonReplayBlocksPerSec = metrics.NewRegisteredMeter("ubt/daemon/replay/blocks", nil)

	// Snapshot restore metrics
	daemonSnapshotRestoreTotal   = metrics.NewRegisteredCounter("ubt/daemon/snapshot/restore/total", nil)
	daemonSnapshotRestoreLatency = metrics.NewRegisteredTimer("ubt/daemon/snapshot/restore/latency", nil)

	// Recovery metrics
	daemonRecoveryAttempts  = metrics.NewRegisteredCounter("ubt/daemon/recovery/attempts", nil)
	daemonRecoverySuccesses = metrics.NewRegisteredCounter("ubt/daemon/recovery/successes", nil)
	daemonRecoveryFailures  = metrics.NewRegisteredCounter("ubt/daemon/recovery/failures", nil)

	// Compaction metrics
	compactionAttemptsTotal = metrics.NewRegisteredCounter("ubt/compaction/attempts/total", nil)
	compactionSuccessTotal  = metrics.NewRegisteredCounter("ubt/compaction/success/total", nil)
	compactionErrorsTotal   = metrics.NewRegisteredCounter("ubt/compaction/errors/total", nil)
	compactionLastRunGauge  = metrics.NewRegisteredGauge("ubt/compaction/lastrun/timestamp", nil)
)
