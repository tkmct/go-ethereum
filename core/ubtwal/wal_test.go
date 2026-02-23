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

package ubtwal

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestWriterReaderRoundTrip(t *testing.T) {
	dir := t.TempDir()
	w, err := OpenWriter(dir, 1024)
	if err != nil {
		t.Fatalf("open writer: %v", err)
	}
	defer w.Close()

	payloads := map[uint64][]byte{
		10: []byte("a"),
		11: []byte("bb"),
		12: []byte("ccc"),
	}
	for seq, payload := range payloads {
		if err := w.Append(seq, payload); err != nil {
			t.Fatalf("append seq=%d: %v", seq, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	r, err := OpenReader(dir)
	if err != nil {
		t.Fatalf("open reader: %v", err)
	}
	latest, ok := r.LatestSeq()
	if !ok || latest != 12 {
		t.Fatalf("latest mismatch: ok=%v latest=%d", ok, latest)
	}
	lowest, ok := r.LowestSeq()
	if !ok || lowest != 10 {
		t.Fatalf("lowest mismatch: ok=%v lowest=%d", ok, lowest)
	}
	got, err := r.ReadRange(10, 12)
	if err != nil {
		t.Fatalf("read range: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("range len mismatch: got %d", len(got))
	}
	if string(got[0]) != "a" || string(got[1]) != "bb" || string(got[2]) != "ccc" {
		t.Fatalf("unexpected range payloads")
	}
}

func TestSegmentRotation(t *testing.T) {
	dir := t.TempDir()
	w, err := OpenWriter(dir, 32) // tiny segment size
	if err != nil {
		t.Fatalf("open writer: %v", err)
	}
	defer w.Close()

	for i := 0; i < 10; i++ {
		if err := w.Append(uint64(i), []byte("1234567890")); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	count := 0
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == fileExt {
			count++
		}
	}
	if count < 2 {
		t.Fatalf("expected rotation into multiple segments, got %d", count)
	}
}

func TestTruncatedTailIgnored(t *testing.T) {
	dir := t.TempDir()
	w, err := OpenWriter(dir, 1024)
	if err != nil {
		t.Fatalf("open writer: %v", err)
	}
	if err := w.Append(1, []byte("ok")); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	segment := filepath.Join(dir, "00000000000000000001"+fileExt)
	f, err := os.OpenFile(segment, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatalf("open segment: %v", err)
	}
	if _, err := f.Write([]byte{0x01, 0x02, 0x03}); err != nil {
		t.Fatalf("append junk: %v", err)
	}
	_ = f.Close()

	r, err := OpenReader(dir)
	if err != nil {
		t.Fatalf("open reader: %v", err)
	}
	payload, err := r.Read(1)
	if err != nil {
		t.Fatalf("read seq=1: %v", err)
	}
	if string(payload) != "ok" {
		t.Fatalf("unexpected payload: %q", payload)
	}
	_, err = r.Read(2)
	if !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("expected ErrRecordNotFound, got %v", err)
	}
}

func TestPruneBelow(t *testing.T) {
	dir := t.TempDir()
	w, err := OpenWriter(dir, 25) // one record per segment for this payload size
	if err != nil {
		t.Fatalf("open writer: %v", err)
	}
	for seq := uint64(0); seq < 6; seq++ {
		if err := w.Append(seq, []byte("x")); err != nil {
			t.Fatalf("append %d: %v", seq, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	pruned, err := w.PruneBelow(3)
	if err != nil {
		t.Fatalf("prune below: %v", err)
	}
	if pruned != 3 {
		t.Fatalf("expected 3 pruned segments, got %d", pruned)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	starts := make([]uint64, 0)
	for _, entry := range entries {
		start, ok := parseSegmentStart(entry.Name())
		if ok {
			starts = append(starts, start)
		}
	}
	sort.Slice(starts, func(i, j int) bool { return starts[i] < starts[j] })
	if len(starts) != 3 || starts[0] != 3 || starts[1] != 4 || starts[2] != 5 {
		t.Fatalf("unexpected remaining segments: %v", starts)
	}
}

func TestReaderRefreshAfterPrune(t *testing.T) {
	dir := t.TempDir()
	w, err := OpenWriter(dir, 25)
	if err != nil {
		t.Fatalf("open writer: %v", err)
	}
	for seq := uint64(0); seq < 5; seq++ {
		if err := w.Append(seq, []byte("x")); err != nil {
			t.Fatalf("append %d: %v", seq, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	r, err := OpenReader(dir)
	if err != nil {
		t.Fatalf("open reader: %v", err)
	}
	if _, err := r.Read(0); err != nil {
		t.Fatalf("expected seq 0 before prune: %v", err)
	}
	if _, err := w.PruneBelow(2); err != nil {
		t.Fatalf("prune below: %v", err)
	}
	if err := r.Refresh(); err != nil {
		t.Fatalf("refresh after prune: %v", err)
	}
	if _, err := r.Read(0); !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("expected seq 0 pruned, got err=%v", err)
	}
	payload, err := r.Read(2)
	if err != nil {
		t.Fatalf("expected seq 2 after prune: %v", err)
	}
	if string(payload) != "x" {
		t.Fatalf("unexpected seq 2 payload: %q", payload)
	}
}
