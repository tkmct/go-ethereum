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

import "fmt"

// ValidateCompactBelowBounds verifies that safeSeq satisfies "below safeSeq" compaction bounds.
// The highest allowed value is latestSeq+1, which means "compact everything currently persisted".
func ValidateCompactBelowBounds(safeSeq, latestSeq uint64) error {
	if safeSeq == 0 {
		return nil
	}
	if safeSeq-1 > latestSeq {
		return fmt.Errorf("safeSeq %d exceeds latest+1 outbox boundary (latest=%d)", safeSeq, latestSeq)
	}
	return nil
}
