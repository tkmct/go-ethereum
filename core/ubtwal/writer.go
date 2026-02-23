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
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// Writer appends sequence-addressed records into rotated WAL segment files.
type Writer struct {
	dir         string
	segmentSize uint64

	mu         sync.Mutex
	active     *os.File
	activeSize uint64
	closed     bool
}

// OpenWriter creates a WAL writer rooted at dir.
func OpenWriter(dir string, segmentSize uint64) (*Writer, error) {
	if dir == "" {
		return nil, fmt.Errorf("wal dir is required")
	}
	if segmentSize == 0 {
		segmentSize = DefaultSegmentSize
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Writer{
		dir:         dir,
		segmentSize: segmentSize,
	}, nil
}

// Append appends one envelope payload at seq.
func (w *Writer) Append(seq uint64, payload []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return fmt.Errorf("wal writer closed")
	}
	h := header{
		Type:       recordTypeEnv,
		Seq:        seq,
		PayloadLen: uint32(len(payload)),
		Checksum:   checksum(payload),
	}
	headerBytes := encodeHeader(h)
	recordBytes := uint64(len(headerBytes) + len(payload))

	if err := w.ensureSegment(seq, recordBytes); err != nil {
		return err
	}
	if _, err := w.active.Write(headerBytes[:]); err != nil {
		return err
	}
	if _, err := w.active.Write(payload); err != nil {
		return err
	}
	w.activeSize += recordBytes
	// Durability policy: fsync each appended record. This keeps WAL recovery
	// deterministic across crashes at the cost of write throughput.
	if err := w.active.Sync(); err != nil {
		return err
	}
	return nil
}

func (w *Writer) ensureSegment(seq uint64, incomingBytes uint64) error {
	if w.active != nil && (w.activeSize+incomingBytes <= w.segmentSize || w.activeSize == 0) {
		return nil
	}
	if w.active != nil {
		if err := w.active.Sync(); err != nil {
			_ = w.active.Close()
			w.active = nil
			return err
		}
		if err := w.active.Close(); err != nil {
			w.active = nil
			return err
		}
		w.active = nil
	}
	path := filepath.Join(w.dir, segmentFileName(seq))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	w.active = f
	w.activeSize = 0
	return nil
}

// Close flushes and closes the active segment.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return nil
	}
	w.closed = true
	if w.active == nil {
		return nil
	}
	if err := w.active.Sync(); err != nil {
		_ = w.active.Close()
		w.active = nil
		return err
	}
	err := w.active.Close()
	w.active = nil
	return err
}

type segmentMeta struct {
	start uint64
	path  string
}

// PruneBelow removes segment files that only contain records below floorSeq.
// Returns the number of deleted segment files.
func (w *Writer) PruneBelow(floorSeq uint64) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if floorSeq == 0 {
		return 0, nil
	}
	entries, err := os.ReadDir(w.dir)
	if err != nil {
		return 0, err
	}
	segments := make([]segmentMeta, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		start, ok := parseSegmentStart(entry.Name())
		if !ok {
			continue
		}
		segments = append(segments, segmentMeta{
			start: start,
			path:  filepath.Join(w.dir, entry.Name()),
		})
	}
	sort.Slice(segments, func(i, j int) bool { return segments[i].start < segments[j].start })

	activePath := ""
	if w.active != nil {
		activePath = w.active.Name()
	}
	deleted := 0
	var firstErr error
	for i := 0; i+1 < len(segments); i++ {
		seg := segments[i]
		next := segments[i+1]
		// A segment is safe to delete only when the next segment starts at or
		// above floorSeq, which means all records in seg are < floorSeq.
		if next.start > floorSeq {
			continue
		}
		if seg.path == activePath {
			continue
		}
		if err := os.Remove(seg.path); err != nil && !os.IsNotExist(err) {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		deleted++
	}
	return deleted, firstErr
}
