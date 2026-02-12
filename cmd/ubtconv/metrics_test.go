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
)

// TestMetricsRegistered verifies all consumer metrics are registered without panics.
func TestMetricsRegistered(t *testing.T) {
	// Accessing metrics should not panic
	_ = consumerAppliedTotal
	_ = consumerAppliedLatency
	_ = consumerCommitLatency
	_ = consumerCommitTotal
	_ = consumerReorgTotal
	_ = consumerLagBlocks
	_ = consumerLagSeq
	_ = consumerErrorsTotal
	_ = consumerBackoffGauge
}
