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

package era

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math/big"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/internal/era/e2store"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/golang/snappy"
)

var (
	TypeVersion                uint16 = 0x3265
	TypeCompressedHeader       uint16 = 0x03
	TypeCompressedBody         uint16 = 0x04
	TypeCompressedReceipts     uint16 = 0x05
	TypeCompressedSlimReceipts uint16 = 0x0a
	TypeTotalDifficulty        uint16 = 0x06
	TypeAccumulator            uint16 = 0x07
	TypeBlockIndex             uint16 = 0x3266
	TypeBlockIndexEra          uint16 = 0x3267

	MaxEra1Size = 8192
)

// Filename returns a recognizable Era1-formatted file name for the specified
// epoch and network.
func Filename(network string, epoch int, root common.Hash) string {
	return fmt.Sprintf("%s-%05d-%s.era1", network, epoch, root.Hex()[2:10])
}

// ReadDir reads all the era files in a directory for a given network and extension.
// Format: <network>-<epoch>-<hexroot><ext>
// The ext parameter should include the leading dot, e.g. ".era1" or ".erae".
func ReadDir(dir, network, ext string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("error reading directory %s: %w", dir, err)
	}
	var (
		next = uint64(0)
		eras []string
	)
	for _, entry := range entries {
		if path.Ext(entry.Name()) != ext {
			continue
		}
		parts := strings.Split(entry.Name(), "-")
		if len(parts) != 3 || parts[0] != network {
			continue
		}
		epoch, err := strconv.ParseUint(parts[1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("malformed era filename: %s", entry.Name())
		}
		if epoch != next {
			return nil, fmt.Errorf("missing epoch %d", next)
		}
		next += 1
		eras = append(eras, entry.Name())
	}
	return eras, nil
}

type ReadAtSeekCloser interface {
	io.ReaderAt
	io.Seeker
	io.Closer
}

// Era reads and Era1 file.
type Era struct {
	f   ReadAtSeekCloser // backing era1 file
	s   *e2store.Reader  // e2store reader over f
	m   metadata         // start, count, length info
	mu  *sync.Mutex      // lock for buf
	buf [8]byte          // buffer reading entry offsets
}

// From returns an Era backed by f.
func From(f ReadAtSeekCloser) (*Era, error) {
	m, err := readMetadata(f)
	if err != nil {
		return nil, err
	}
	return &Era{
		f:  f,
		s:  e2store.NewReader(f),
		m:  m,
		mu: new(sync.Mutex),
	}, nil
}

// Open returns an Era backed by the given filename.
func Open(filename string) (*Era, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	return From(f)
}

func (e *Era) Close() error {
	return e.f.Close()
}

// GetBlockByNumber returns the block for the given block number.
func (e *Era) GetBlockByNumber(num uint64) (*types.Block, error) {
	headerOff, err := e.readComponentOffset(num, 0, TypeCompressedHeader)
	if err != nil {
		return nil, err
	}
	r, _, err := newSnappyReader(e.s, TypeCompressedHeader, headerOff)
	if err != nil {
		return nil, err
	}
	var header types.Header
	if err := rlp.Decode(r, &header); err != nil {
		return nil, err
	}
	bodyOff, err := e.readComponentOffset(num, 1, TypeCompressedBody)
	if err != nil {
		return nil, err
	}
	r, _, err = newSnappyReader(e.s, TypeCompressedBody, bodyOff)
	if err != nil {
		return nil, err
	}
	var body types.Body
	if err := rlp.Decode(r, &body); err != nil {
		return nil, err
	}
	return types.NewBlockWithHeader(&header).WithBody(body), nil
}

// GetRawBodyByNumber returns the RLP-encoded body for the given block number.
func (e *Era) GetRawBodyByNumber(num uint64) ([]byte, error) {
	off, err := e.readComponentOffset(num, 1, TypeCompressedBody)
	if err != nil {
		return nil, err
	}
	r, _, err := newSnappyReader(e.s, TypeCompressedBody, off)
	if err != nil {
		return nil, err
	}
	return io.ReadAll(r)
}

// GetRawReceiptsByNumber returns the RLP-encoded receipts for the given block number.
func (e *Era) GetRawReceiptsByNumber(num uint64) ([]byte, error) {
	off, err := e.readComponentOffset(num, 2, TypeCompressedReceipts, TypeCompressedSlimReceipts)
	if err != nil {
		return nil, err
	}
	typ, _, err := e.s.ReadMetadataAt(off)
	if err != nil {
		return nil, err
	}
	switch typ {
	case TypeCompressedReceipts:
		r, _, err := newSnappyReader(e.s, TypeCompressedReceipts, off)
		if err != nil {
			return nil, err
		}
		return io.ReadAll(r)
	case TypeCompressedSlimReceipts:
		r, _, err := newSnappyReader(e.s, TypeCompressedSlimReceipts, off)
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(r)
		if err != nil {
			return nil, err
		}
		receipts, err := decodeSlimReceipts(data)
		if err != nil {
			return nil, err
		}
		return rlp.EncodeToBytes(receipts)
	default:
		return nil, fmt.Errorf("unsupported receipts type %d", typ)
	}
}

func (e *Era) totalDifficultyOffset(num uint64) (int64, error) {
	// Try explicit indexed components first (eraE).
	for component := uint64(3); component < e.m.componentCount; component++ {
		off, err := e.readComponentOffset(num, component)
		if err != nil {
			return 0, err
		}
		typ, _, err := e.s.ReadMetadataAt(off)
		if err != nil {
			continue
		}
		if typ == TypeTotalDifficulty {
			return off, nil
		}
	}
	// For era1 total difficulty is placed right after receipts.
	off, err := e.readComponentOffset(num, 2, TypeCompressedReceipts)
	if err != nil {
		return 0, err
	}
	off, err = e.s.SkipN(off, 1)
	if err != nil {
		return 0, err
	}
	typ, _, err := e.s.ReadMetadataAt(off)
	if err != nil {
		return 0, err
	}
	if typ != TypeTotalDifficulty {
		return 0, fmt.Errorf("total difficulty not available for block %d", num)
	}
	return off, nil
}

// Accumulator reads the accumulator entry in the Era1 file.
func (e *Era) Accumulator() (common.Hash, error) {
	entry, err := e.s.Find(TypeAccumulator)
	if err != nil {
		return common.Hash{}, err
	}
	return common.BytesToHash(entry.Value), nil
}

// InitialTD returns initial total difficulty before the difficulty of the
// first block of the Era1 is applied.
func (e *Era) InitialTD() (*big.Int, error) {
	var (
		r      io.Reader
		header types.Header
		rawTd  []byte
		err    error
	)

	// Read first header.
	headerOff, err := e.readComponentOffset(e.m.start, 0, TypeCompressedHeader)
	if err != nil {
		return nil, err
	}
	if r, _, err = newSnappyReader(e.s, TypeCompressedHeader, headerOff); err != nil {
		return nil, err
	}
	if err := rlp.Decode(r, &header); err != nil {
		return nil, err
	}
	tdOff, err := e.totalDifficultyOffset(e.m.start)
	if err != nil {
		return nil, err
	}
	// Read total difficulty after first block.
	if r, _, err = e.s.ReaderAt(TypeTotalDifficulty, tdOff); err != nil {
		return nil, err
	}
	rawTd, err = io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	td := new(big.Int).SetBytes(reverseOrder(rawTd))
	return td.Sub(td, header.Difficulty), nil
}

// Start returns the listed start block.
func (e *Era) Start() uint64 {
	return e.m.start
}

// Count returns the total number of blocks in the Era1.
func (e *Era) Count() uint64 {
	return e.m.count
}

// newSnappyReader returns a snappy.Reader for the e2store entry value at off.
func newSnappyReader(e *e2store.Reader, expectedType uint16, off int64) (io.Reader, int64, error) {
	r, n, err := e.ReaderAt(expectedType, off)
	if err != nil {
		return nil, 0, err
	}
	return snappy.NewReader(r), int64(n), err
}

// metadata wraps the metadata in the block index.
type metadata struct {
	start          uint64
	count          uint64
	length         int64
	indexOffset    int64
	componentCount uint64
}

// readMetadata reads the metadata stored in an Era1 file's block index.
// The block index is always the last e2store entry. Its layout:
//
//	era1: start(8) | count * offset(8) | count(8)
//	eraE: start(8) | count * componentCount * offset(8) | componentCount(8) | count(8)
//
// We locate the entry by reading count (and componentCount for eraE) from
// the end of the file, computing the expected entry offset, and verifying
// the e2store type header.
func readMetadata(f ReadAtSeekCloser) (m metadata, err error) {
	if m.length, err = f.Seek(0, io.SeekEnd); err != nil {
		return
	}
	r := e2store.NewReader(f)
	b := make([]byte, 16)

	// Last 16 bytes contain either:
	//   era1: [last_offset(8), count(8)]
	//   eraE: [componentCount(8), count(8)]
	if _, err = f.ReadAt(b, m.length-16); err != nil {
		return
	}
	count := binary.LittleEndian.Uint64(b[8:])
	penultimate := binary.LittleEndian.Uint64(b[:8])

	// Try era1: value size = 16 + count*8
	era1ValueSize := int64(16 + count*8)
	if off := m.length - 8 - era1ValueSize; off >= 0 { // 8 = e2store header
		if typ, _, e := r.ReadMetadataAt(off); e == nil && typ == TypeBlockIndex {
			if _, err = f.ReadAt(b[:8], off+8); err != nil {
				return
			}
			m.indexOffset = off
			m.start = binary.LittleEndian.Uint64(b[:8])
			m.count = count
			m.componentCount = 1
			return
		}
	}
	// Try eraE: penultimate is componentCount, value size = 24 + count*componentCount*8
	componentCount := penultimate
	if componentCount == 0 {
		return m, fmt.Errorf("no era block index found")
	}
	eraEValueSize := int64(24 + count*componentCount*8)
	if off := m.length - 8 - eraEValueSize; off >= 0 {
		if typ, _, e := r.ReadMetadataAt(off); e == nil && typ == TypeBlockIndexEra {
			if _, err = f.ReadAt(b[:8], off+8); err != nil {
				return
			}
			m.indexOffset = off
			m.start = binary.LittleEndian.Uint64(b[:8])
			m.count = count
			m.componentCount = componentCount
			return
		}
	}
	return m, fmt.Errorf("no era block index found")
}

func (e *Era) readComponentOffset(n uint64, component uint64, expectedTypes ...uint16) (int64, error) {
	if n < e.m.start || n >= e.m.start+e.m.count {
		return 0, fmt.Errorf("out-of-bounds: %d not in [%d, %d)", n, e.m.start, e.m.start+e.m.count)
	}
	// Era1 block index stores only the header offset. Remaining components are
	// laid out sequentially in the file.
	if e.m.componentCount == 1 && component > 0 {
		base, err := e.readComponentOffset(n, 0)
		if err != nil {
			return 0, err
		}
		off, err := e.s.SkipN(base, component)
		if err != nil {
			return 0, err
		}
		if len(expectedTypes) == 0 {
			return off, nil
		}
		typ, _, err := e.s.ReadMetadataAt(off)
		if err != nil {
			return 0, err
		}
		for _, want := range expectedTypes {
			if typ == want {
				return off, nil
			}
		}
		return 0, fmt.Errorf("unexpected component type %d at block %d component %d", typ, n, component)
	}
	if component >= e.m.componentCount {
		return 0, fmt.Errorf("component %d out of range [0,%d)", component, e.m.componentCount)
	}
	var (
		firstIndex = e.m.indexOffset + 16
		blockBase  = int64(n-e.m.start) * int64(e.m.componentCount*8)
		offOffset  = firstIndex + blockBase + int64(component*8)
	)
	e.mu.Lock()
	defer e.mu.Unlock()
	clear(e.buf[:])
	if _, err := e.f.ReadAt(e.buf[:], offOffset); err != nil {
		return 0, err
	}
	rel := int64(binary.LittleEndian.Uint64(e.buf[:]))

	// Different era producers have used different offset bases. Try both:
	// 1) relative to start of block-index record
	// 2) relative to the index-field location itself
	candidates := []int64{
		e.m.indexOffset + rel,
		offOffset + rel,
	}
	seen := make(map[int64]struct{}, len(candidates))
	for _, candidate := range candidates {
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		if len(expectedTypes) == 0 {
			return candidate, nil
		}
		typ, _, err := e.s.ReadMetadataAt(candidate)
		if err != nil {
			continue
		}
		for _, want := range expectedTypes {
			if typ == want {
				return candidate, nil
			}
		}
	}
	if len(expectedTypes) > 0 {
		return 0, fmt.Errorf("unable to resolve component offset for block %d component %d", n, component)
	}
	return candidates[0], nil
}

func decodePostStateOrStatus(r *types.Receipt, status []byte) error {
	switch {
	case bytes.Equal(status, []byte{0x01}):
		r.Status = types.ReceiptStatusSuccessful
	case len(status) == 0:
		r.Status = types.ReceiptStatusFailed
	case len(status) == len(common.Hash{}):
		r.PostState = common.CopyBytes(status)
	default:
		return fmt.Errorf("invalid receipt status %x", status)
	}
	return nil
}

type slimReceipt struct {
	Type              uint64
	PostStateOrStatus []byte
	CumulativeGasUsed uint64
	Logs              []*types.Log
}

func decodeSlimReceipts(input []byte) (types.Receipts, error) {
	var slim []slimReceipt
	if err := rlp.DecodeBytes(input, &slim); err != nil {
		return nil, err
	}
	receipts := make(types.Receipts, len(slim))
	for i := range slim {
		r := &types.Receipt{
			Type:              uint8(slim[i].Type),
			CumulativeGasUsed: slim[i].CumulativeGasUsed,
			Logs:              slim[i].Logs,
		}
		if err := decodePostStateOrStatus(r, slim[i].PostStateOrStatus); err != nil {
			return nil, err
		}
		r.Bloom = types.CreateBloom(r)
		receipts[i] = r
	}
	return receipts, nil
}
