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

import "github.com/ethereum/go-ethereum/common"

// NewReorgMarker creates a ReorgMarkerV1 from reorg parameters.
func NewReorgMarker(fromNumber uint64, fromHash common.Hash, toNumber uint64, toHash common.Hash, ancestorNumber uint64, ancestorHash common.Hash) *ReorgMarkerV1 {
	return &ReorgMarkerV1{
		FromBlockNumber:      fromNumber,
		FromBlockHash:        fromHash,
		ToBlockNumber:        toNumber,
		ToBlockHash:          toHash,
		CommonAncestorNumber: ancestorNumber,
		CommonAncestorHash:   ancestorHash,
	}
}
