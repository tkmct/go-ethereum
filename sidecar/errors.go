// Copyright 2025 The go-ethereum Authors
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

import "errors"

var (
	// ErrSidecarNotReady is returned when an operation requires the sidecar to be
	// in Ready state but it is not.
	ErrSidecarNotReady = errors.New("ubt sidecar not ready")

	// ErrSidecarNotEnabled is returned when the UBT sidecar is not enabled.
	ErrSidecarNotEnabled = errors.New("ubt sidecar not enabled")

	// ErrPendingNotSupported is returned when a pending block is requested for UBT operations.
	ErrPendingNotSupported = errors.New("pending block not supported for UBT")
)
