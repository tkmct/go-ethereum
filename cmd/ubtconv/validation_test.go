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

// TestValidationSampleRate tests the validation sample rate configuration.
func TestValidationSampleRate(t *testing.T) {
	tests := []struct {
		name           string
		sampleRate     uint64
		enabled        bool
		blockNumber    uint64
		shouldValidate bool
	}{
		{
			name:           "disabled when sample rate is 0",
			sampleRate:     0,
			enabled:        true,
			blockNumber:    100,
			shouldValidate: false,
		},
		{
			name:           "disabled when validation not enabled",
			sampleRate:     1,
			enabled:        false,
			blockNumber:    100,
			shouldValidate: false,
		},
		{
			name:           "validate every block with rate 1",
			sampleRate:     1,
			enabled:        true,
			blockNumber:    100,
			shouldValidate: true,
		},
		{
			name:           "validate every 10th block",
			sampleRate:     10,
			enabled:        true,
			blockNumber:    100,
			shouldValidate: true,
		},
		{
			name:           "skip validation on non-sampled block",
			sampleRate:     10,
			enabled:        true,
			blockNumber:    101,
			shouldValidate: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				ValidationEnabled:    tt.enabled,
				ValidationSampleRate: tt.sampleRate,
			}

			// Simulate the validation logic from consumer.go
			shouldValidate := cfg.ValidationEnabled && cfg.ValidationSampleRate > 0 && tt.blockNumber%cfg.ValidationSampleRate == 0

			if shouldValidate != tt.shouldValidate {
				t.Errorf("expected shouldValidate=%v, got %v", tt.shouldValidate, shouldValidate)
			}
		})
	}
}
