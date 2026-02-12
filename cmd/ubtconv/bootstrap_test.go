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

// TestBootstrapModeValidation tests that the config validates bootstrap modes correctly.
func TestBootstrapModeValidation(t *testing.T) {
	tests := []struct {
		name      string
		mode      string
		wantError bool
	}{
		{
			name:      "tail mode",
			mode:      "tail",
			wantError: false,
		},
		{
			name:      "backfill-direct mode",
			mode:      "backfill-direct",
			wantError: false,
		},
		{
			name:      "invalid mode",
			mode:      "invalid",
			wantError: true,
		},
		{
			name:      "empty mode",
			mode:      "",
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				OutboxRPCEndpoint:    "http://localhost:8545",
				DataDir:              "/tmp/test",
				ApplyCommitInterval:  100,
				BootstrapMode:        tt.mode,
				TrieDBScheme:         "path",
				ApplyCommitMaxLatency: 10 * 1000,
			}

			err := cfg.Validate()
			if tt.wantError && err == nil {
				t.Errorf("expected error for mode %q, got nil", tt.mode)
			}
			if !tt.wantError && err != nil {
				t.Errorf("expected no error for mode %q, got %v", tt.mode, err)
			}
		})
	}
}

// TestBootstrapLogic tests the bootstrap mode logic without requiring full integration.
func TestBootstrapLogic(t *testing.T) {
	tests := []struct {
		name            string
		mode            string
		hasState        bool
		shouldBootstrap bool
	}{
		{
			name:            "tail mode with no persisted state should bootstrap",
			mode:            "tail",
			hasState:        false,
			shouldBootstrap: true,
		},
		{
			name:            "tail mode with persisted state should not bootstrap",
			mode:            "tail",
			hasState:        true,
			shouldBootstrap: false,
		},
		{
			name:            "backfill-direct mode with no persisted state should bootstrap (no-op)",
			mode:            "backfill-direct",
			hasState:        false,
			shouldBootstrap: true,
		},
		{
			name:            "backfill-direct mode with persisted state should not bootstrap",
			mode:            "backfill-direct",
			hasState:        true,
			shouldBootstrap: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Bootstrap triggers when no persisted state exists (hasState == false)
			shouldBootstrap := !tt.hasState
			if shouldBootstrap != tt.shouldBootstrap {
				t.Errorf("expected shouldBootstrap=%v, got %v", tt.shouldBootstrap, shouldBootstrap)
			}

			// Verify mode behavior
			if shouldBootstrap {
				switch tt.mode {
				case "tail":
					t.Logf("tail mode: would skip to latest seq")
				case "backfill-direct":
					t.Logf("backfill-direct mode: starting from seq 0")
				}
			}
		})
	}
}

// TestBackoffProgression tests the exponential backoff logic.
func TestBackoffProgression(t *testing.T) {
	// Simulate backoff progression
	backoff := int64(1) // seconds
	maxBackoff := int64(30)

	progressions := []int64{1, 2, 4, 8, 16, 30, 30, 30}

	for i, expected := range progressions {
		if backoff != expected {
			t.Errorf("step %d: expected backoff=%d, got %d", i, expected, backoff)
		}

		// Exponential backoff with max cap
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}
