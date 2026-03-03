// Copyright 2026 The go-ethereum Authors
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

package utils

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestReadListIgnoresEmptyLines(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "checksums.txt")
	content := "0xaaa\n0xbbb\n\n   \n0xccc\n"

	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}
	got, err := readList(file)
	if err != nil {
		t.Fatalf("readList returned error: %v", err)
	}
	want := []string{"0xaaa", "0xbbb", "0xccc"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected list: got %v, want %v", got, want)
	}
}
