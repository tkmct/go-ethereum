// Copyright 2026 The go-ethereum Authors
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

package sidecar

import (
	"errors"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/triedb/pathdb"
)

func TestIsRetryableIterError(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		retryable bool
	}{
		{name: "nil", err: nil, retryable: false},
		{name: "snapshot stale", err: pathdb.ErrSnapshotStale, retryable: true},
		{name: "not constructed", err: errors.New(retryableErrNotConstructed), retryable: true},
		{name: "waiting for sync", err: errors.New(retryableErrWaitingForSync), retryable: true},
		{name: "unknown layer", err: errors.New(retryableUnknownLayerPrefix + "0x1234"), retryable: true},
		{name: "other", err: errors.New("boom"), retryable: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isRetryableIterError(tc.err); got != tc.retryable {
				t.Fatalf("isRetryableIterError(%v) = %t, want %t", tc.err, got, tc.retryable)
			}
		})
	}
}

func TestIncrementHash(t *testing.T) {
	t.Run("increments", func(t *testing.T) {
		in := common.HexToHash("0x01")
		got := incrementHash(in)
		want := common.HexToHash("0x02")
		if got != want {
			t.Fatalf("incrementHash(%x) = %x, want %x", in, got, want)
		}
	})
	t.Run("carry", func(t *testing.T) {
		in := common.HexToHash("0x00ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff")
		got := incrementHash(in)
		want := common.HexToHash("0x0100000000000000000000000000000000000000000000000000000000000000")
		if got != want {
			t.Fatalf("incrementHash(%x) = %x, want %x", in, got, want)
		}
	})
	t.Run("overflow", func(t *testing.T) {
		var in common.Hash
		for i := range in {
			in[i] = 0xff
		}
		got := incrementHash(in)
		if got != (common.Hash{}) {
			t.Fatalf("incrementHash(max) = %x, want zero hash", got)
		}
	})
}
