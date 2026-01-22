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

package stateless

import "testing"

func TestWitnessAddStateDoesNotPopulatePaths(t *testing.T) {
	w := &Witness{
		State: make(map[string]struct{}),
	}
	nodes := map[string][]byte{
		"":  {0x01},
		"p": {0x02},
	}
	w.AddState(nodes)
	if len(w.StatePaths) != 0 {
		t.Fatalf("unexpected StatePaths entries: %d", len(w.StatePaths))
	}
	if len(w.State) != len(nodes) {
		t.Fatalf("unexpected State size: %d", len(w.State))
	}
}

func TestWitnessAddStatePathsPopulatesPaths(t *testing.T) {
	w := &Witness{
		State: make(map[string]struct{}),
	}
	nodes := map[string][]byte{
		"a": {0x01},
		"b": {0x02},
	}
	w.AddStatePaths(nodes)
	if len(w.StatePaths) != len(nodes) {
		t.Fatalf("unexpected StatePaths size: %d", len(w.StatePaths))
	}
	if len(w.State) != len(nodes) {
		t.Fatalf("unexpected State size: %d", len(w.State))
	}
	for path, blob := range nodes {
		got, ok := w.StatePaths[path]
		if !ok {
			t.Fatalf("missing StatePaths entry for %q", path)
		}
		if string(got) != string(blob) {
			t.Fatalf("unexpected StatePaths value for %q", path)
		}
		if _, ok := w.State[string(blob)]; !ok {
			t.Fatalf("missing State entry for %q", path)
		}
	}
}
