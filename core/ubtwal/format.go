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
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"strconv"
	"strings"
)

const (
	// Record and file constants.
	recordMagic   uint32 = 0x55425457 // "UBTW"
	recordVersion uint8  = 1
	recordTypeEnv uint8  = 1

	headerSize = 24
	fileExt    = ".wal"

	// DefaultSegmentSize controls file rollover in the writer.
	DefaultSegmentSize uint64 = 64 * 1024 * 1024
)

var (
	ErrCorruptRecord  = errors.New("ubt wal corrupt record")
	ErrRecordNotFound = errors.New("ubt wal record not found")
)

type header struct {
	Type       uint8
	Seq        uint64
	PayloadLen uint32
	Checksum   uint32
}

func checksum(payload []byte) uint32 {
	return crc32.ChecksumIEEE(payload)
}

func encodeHeader(h header) [headerSize]byte {
	var b [headerSize]byte
	binary.BigEndian.PutUint32(b[0:4], recordMagic)
	b[4] = recordVersion
	b[5] = h.Type
	// b[6:8] reserved
	binary.BigEndian.PutUint64(b[8:16], h.Seq)
	binary.BigEndian.PutUint32(b[16:20], h.PayloadLen)
	binary.BigEndian.PutUint32(b[20:24], h.Checksum)
	return b
}

func decodeHeader(b []byte) (header, error) {
	if len(b) != headerSize {
		return header{}, fmt.Errorf("%w: invalid header size %d", ErrCorruptRecord, len(b))
	}
	if binary.BigEndian.Uint32(b[0:4]) != recordMagic {
		return header{}, fmt.Errorf("%w: magic mismatch", ErrCorruptRecord)
	}
	if b[4] != recordVersion {
		return header{}, fmt.Errorf("%w: unsupported version %d", ErrCorruptRecord, b[4])
	}
	h := header{
		Type:       b[5],
		Seq:        binary.BigEndian.Uint64(b[8:16]),
		PayloadLen: binary.BigEndian.Uint32(b[16:20]),
		Checksum:   binary.BigEndian.Uint32(b[20:24]),
	}
	if h.Type != recordTypeEnv {
		return header{}, fmt.Errorf("%w: unsupported type %d", ErrCorruptRecord, h.Type)
	}
	return h, nil
}

func segmentFileName(startSeq uint64) string {
	return fmt.Sprintf("%020d%s", startSeq, fileExt)
}

func parseSegmentStart(name string) (uint64, bool) {
	if !strings.HasSuffix(name, fileExt) {
		return 0, false
	}
	base := strings.TrimSuffix(name, fileExt)
	if len(base) != 20 {
		return 0, false
	}
	seq, err := strconv.ParseUint(base, 10, 64)
	if err != nil {
		return 0, false
	}
	return seq, true
}
