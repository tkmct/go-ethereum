// Copyright 2024 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package ubtemit

import "github.com/ethereum/go-ethereum/metrics"

var (
	outboxAppendLatency    = metrics.NewRegisteredTimer("ubt/emitter/outbox/append/latency", nil)
	emitterAppendLatency   = metrics.NewRegisteredTimer("ubt/emitter/append/latency", nil)
	emitterDegradedTotal   = metrics.NewRegisteredCounter("ubt/emitter/degraded/total", nil)
	emitterDegradedGauge   = metrics.NewRegisteredGauge("ubt/emitter/degraded", nil) // 0=healthy, 1=degraded
	emitterAppendErrors    = metrics.NewRegisteredCounter("ubt/emitter/append/errors", nil)
	emitterRawKeyFailures  = metrics.NewRegisteredCounter("ubt/emitter/rawkey/failures", nil)
	emitterDeletedAccounts = metrics.NewRegisteredCounter("ubt/emitter/deleted/accounts", nil)
	outboxDiskUsage        = metrics.NewRegisteredGauge("ubt/outbox/disk/usage/bytes", nil)
	outboxCompactedTotal   = metrics.NewRegisteredCounter("ubt/outbox/compacted/total", nil)
)
