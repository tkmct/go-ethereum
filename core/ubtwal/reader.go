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
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

type location struct {
	path          string
	payloadOffset int64
	payloadLen    uint32
}

type segmentState struct {
	scannedOffset int64
}

type segmentFile struct {
	path string
	size int64
}

// Reader indexes WAL segments for sequence-addressed lookups.
type Reader struct {
	dir string

	refreshMu sync.Mutex
	mu        sync.RWMutex
	index     map[uint64]location
	lowest    uint64
	latest    uint64
	hasData   bool

	segmentOrder  []string
	segmentStates map[string]segmentState
}

// OpenReader creates a WAL reader rooted at dir.
func OpenReader(dir string) (*Reader, error) {
	if dir == "" {
		return nil, fmt.Errorf("wal dir is required")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	r := &Reader{
		dir:           dir,
		index:         make(map[uint64]location),
		segmentStates: make(map[string]segmentState),
	}
	if err := r.Refresh(); err != nil {
		return nil, err
	}
	return r, nil
}

// Refresh rebuilds the in-memory sequence index from segments.
func (r *Reader) Refresh() error {
	r.refreshMu.Lock()
	defer r.refreshMu.Unlock()

	segments, err := collectSegments(r.dir)
	if err != nil {
		return err
	}
	if len(segments) == 0 {
		r.mu.Lock()
		r.index = make(map[uint64]location)
		r.lowest = 0
		r.latest = 0
		r.hasData = false
		r.segmentOrder = nil
		r.segmentStates = make(map[string]segmentState)
		r.mu.Unlock()
		return nil
	}

	r.mu.RLock()
	prevOrder := append([]string(nil), r.segmentOrder...)
	prevStates := make(map[string]segmentState, len(r.segmentStates))
	for path, st := range r.segmentStates {
		prevStates[path] = st
	}
	lowest := r.lowest
	latest := r.latest
	hasData := r.hasData
	r.mu.RUnlock()

	if !canIncrementalRefresh(prevOrder, prevStates, segments) {
		return r.rebuildFromSegments(segments)
	}

	additions := make(map[uint64]location)
	nextOrder := make([]string, 0, len(segments))
	nextStates := make(map[string]segmentState, len(segments))
	for i, segment := range segments {
		nextOrder = append(nextOrder, segment.path)
		startOffset := int64(0)
		if i < len(prevOrder) && prevOrder[i] == segment.path {
			startOffset = prevStates[segment.path].scannedOffset
			if segment.size == startOffset {
				nextStates[segment.path] = prevStates[segment.path]
				continue
			}
		}
		offset, err := scanSegmentFrom(segment.path, startOffset, additions, func(seq uint64) {
			if !hasData || seq < lowest {
				lowest = seq
			}
			if !hasData || seq > latest {
				latest = seq
			}
			hasData = true
		})
		if err != nil {
			return err
		}
		nextStates[segment.path] = segmentState{scannedOffset: offset}
	}

	r.mu.Lock()
	if r.index == nil {
		r.index = make(map[uint64]location, len(additions))
	}
	for seq, loc := range additions {
		r.index[seq] = loc
	}
	r.lowest = lowest
	r.latest = latest
	r.hasData = hasData
	r.segmentOrder = nextOrder
	r.segmentStates = nextStates
	r.mu.Unlock()
	return nil
}

func (r *Reader) rebuildFromSegments(segments []segmentFile) error {
	newIndex := make(map[uint64]location)
	nextOrder := make([]string, 0, len(segments))
	nextStates := make(map[string]segmentState, len(segments))
	var (
		lowest  uint64
		latest  uint64
		hasData bool
	)
	for _, segment := range segments {
		nextOrder = append(nextOrder, segment.path)
		offset, err := scanSegmentFrom(segment.path, 0, newIndex, func(seq uint64) {
			if !hasData || seq < lowest {
				lowest = seq
			}
			if !hasData || seq > latest {
				latest = seq
			}
			hasData = true
		})
		if err != nil {
			return err
		}
		nextStates[segment.path] = segmentState{scannedOffset: offset}
	}

	r.mu.Lock()
	r.index = newIndex
	r.lowest = lowest
	r.latest = latest
	r.hasData = hasData
	r.segmentOrder = nextOrder
	r.segmentStates = nextStates
	r.mu.Unlock()
	return nil
}

func collectSegments(dir string) ([]segmentFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	segments := make([]segmentFile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if _, ok := parseSegmentStart(entry.Name()); !ok {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		segments = append(segments, segmentFile{
			path: filepath.Join(dir, entry.Name()),
			size: info.Size(),
		})
	}
	sort.Slice(segments, func(i, j int) bool { return segments[i].path < segments[j].path })
	return segments, nil
}

func canIncrementalRefresh(previousOrder []string, previousStates map[string]segmentState, current []segmentFile) bool {
	if len(previousOrder) == 0 {
		return false
	}
	if len(current) < len(previousOrder) {
		return false
	}
	for i, prevPath := range previousOrder {
		if current[i].path != prevPath {
			return false
		}
		state, ok := previousStates[prevPath]
		if !ok {
			return false
		}
		// Non-tail segments are immutable once rotated.
		if i < len(previousOrder)-1 {
			if current[i].size != state.scannedOffset {
				return false
			}
			continue
		}
		// Tail segment may have grown, but never shrinks.
		if current[i].size < state.scannedOffset {
			return false
		}
	}
	return true
}

func scanSegmentFrom(path string, startOffset int64, index map[uint64]location, onRecord func(uint64)) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return startOffset, err
	}
	defer f.Close()

	if startOffset > 0 {
		if _, err := f.Seek(startOffset, io.SeekStart); err != nil {
			return startOffset, err
		}
	}
	offset := startOffset
	headerBuf := make([]byte, headerSize)
	for {
		if _, err := io.ReadFull(f, headerBuf); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				// Allow truncated tail on active segment.
				return offset, nil
			}
			return offset, err
		}
		h, err := decodeHeader(headerBuf)
		if err != nil {
			return offset, fmt.Errorf("%w at %s:%d", err, path, offset)
		}
		payload := make([]byte, h.PayloadLen)
		if _, err := io.ReadFull(f, payload); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				// Allow truncated tail on active segment.
				return offset, nil
			}
			return offset, err
		}
		if checksum(payload) != h.Checksum {
			return offset, fmt.Errorf("%w: checksum mismatch at %s:%d", ErrCorruptRecord, path, offset)
		}
		index[h.Seq] = location{
			path:          path,
			payloadOffset: offset + headerSize,
			payloadLen:    h.PayloadLen,
		}
		if onRecord != nil {
			onRecord(h.Seq)
		}
		offset += int64(headerSize) + int64(h.PayloadLen)
	}
}

// Read returns payload bytes for seq.
func (r *Reader) Read(seq uint64) ([]byte, error) {
	r.mu.RLock()
	loc, ok := r.index[seq]
	r.mu.RUnlock()
	if !ok {
		return nil, ErrRecordNotFound
	}
	f, err := os.Open(loc.path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	if _, err := f.Seek(loc.payloadOffset, io.SeekStart); err != nil {
		return nil, err
	}
	payload := make([]byte, loc.payloadLen)
	if _, err := io.ReadFull(f, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

// ReadRange reads a contiguous sequence window, stopping at the first gap.
func (r *Reader) ReadRange(fromSeq, toSeq uint64) ([][]byte, error) {
	if fromSeq > toSeq {
		return nil, fmt.Errorf("invalid range: from=%d to=%d", fromSeq, toSeq)
	}
	result := make([][]byte, 0, toSeq-fromSeq+1)
	for seq := fromSeq; seq <= toSeq; seq++ {
		payload, err := r.Read(seq)
		if err != nil {
			if errors.Is(err, ErrRecordNotFound) {
				break
			}
			return nil, err
		}
		result = append(result, payload)
	}
	return result, nil
}

// LatestSeq returns the latest indexed sequence.
func (r *Reader) LatestSeq() (uint64, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.latest, r.hasData
}

// LowestSeq returns the lowest indexed sequence.
func (r *Reader) LowestSeq() (uint64, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.lowest, r.hasData
}
