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
	"time"

	"github.com/ethereum/go-ethereum/log"
)

// DaemonPhase represents the daemon's operational phase.
type DaemonPhase string

const (
	PhaseInitializing DaemonPhase = "initializing"
	PhaseCatchingUp   DaemonPhase = "catching-up"
	PhaseSynced       DaemonPhase = "synced"
	PhaseDiverged     DaemonPhase = "diverged"
	PhaseValidateOnly DaemonPhase = "validate-only"
)

// PhaseTracker manages the daemon's operational phase transitions.
type PhaseTracker struct {
	current            DaemonPhase
	syncedLagThreshold uint64
	productionReadyMin time.Duration
	syncedSince        time.Time
	validationPassed   uint64
}

// NewPhaseTracker creates a new PhaseTracker.
func NewPhaseTracker(syncedLagThreshold uint64, productionReadyMin time.Duration, validateOnly bool) *PhaseTracker {
	phase := PhaseInitializing
	if validateOnly {
		phase = PhaseValidateOnly
	}
	return &PhaseTracker{
		current:            phase,
		syncedLagThreshold: syncedLagThreshold,
		productionReadyMin: productionReadyMin,
	}
}

// UpdatePhase updates the daemon phase based on current conditions.
func (pt *PhaseTracker) UpdatePhase(lag uint64, validationOK bool, hasError bool) {
	if pt.current == PhaseValidateOnly {
		return // validate-only is a sticky mode
	}
	if hasError {
		if pt.current == PhaseSynced {
			log.Warn("UBT daemon phase transition", "from", pt.current, "to", PhaseDiverged)
		}
		pt.current = PhaseDiverged
		pt.syncedSince = time.Time{}
		pt.validationPassed = 0
		return
	}

	synced := lag <= pt.syncedLagThreshold
	prev := pt.current

	if synced {
		if pt.current != PhaseSynced {
			pt.current = PhaseSynced
			pt.syncedSince = time.Now()
		}
		if validationOK {
			pt.validationPassed++
		}
	} else {
		pt.current = PhaseCatchingUp
		pt.syncedSince = time.Time{}
		pt.validationPassed = 0
	}

	if prev != pt.current {
		log.Info("UBT daemon phase transition", "from", prev, "to", pt.current)
	}
}

// IsProductionReady returns true if the daemon has been synced for the required
// duration and has passed a minimum number of consecutive validations.
func (pt *PhaseTracker) IsProductionReady() bool {
	if pt.current != PhaseSynced {
		return false
	}
	if pt.syncedSince.IsZero() {
		return false
	}
	if time.Since(pt.syncedSince) < pt.productionReadyMin {
		return false
	}
	return pt.validationPassed >= 100
}

// Current returns the current daemon phase.
func (pt *PhaseTracker) Current() DaemonPhase {
	return pt.current
}

// SyncedSince returns when the daemon entered synced phase.
func (pt *PhaseTracker) SyncedSince() time.Time {
	return pt.syncedSince
}

// ValidationPassed returns the count of consecutive passed validations.
func (pt *PhaseTracker) ValidationPassed() uint64 {
	return pt.validationPassed
}
