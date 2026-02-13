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

import "testing"

func TestValidateCompactBelowBounds(t *testing.T) {
	tests := []struct {
		name    string
		safeSeq uint64
		latest  uint64
		wantErr bool
	}{
		{name: "zero safe seq", safeSeq: 0, latest: 0, wantErr: false},
		{name: "equal latest", safeSeq: 7, latest: 7, wantErr: false},
		{name: "latest plus one", safeSeq: 8, latest: 7, wantErr: false},
		{name: "beyond latest plus one", safeSeq: 9, latest: 7, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCompactBelowBounds(tt.safeSeq, tt.latest)
			if tt.wantErr && err == nil {
				t.Fatal("expected error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
