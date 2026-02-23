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

package main

import (
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/ubtemit"
	"github.com/ethereum/go-ethereum/core/ubtwal"
)

func writeTestWALEnvelope(t *testing.T, w *ubtwal.Writer, seq uint64, payload []byte) {
	t.Helper()
	env := &ubtemit.OutboxEnvelope{
		Seq:         seq,
		Version:     ubtemit.EnvelopeVersionV1,
		Kind:        ubtemit.KindDiff,
		BlockNumber: seq + 100,
		BlockHash:   common.BigToHash(common.Big1),
		ParentHash:  common.Hash{},
		Timestamp:   seq + 1,
		Payload:     payload,
	}
	blob, err := ubtemit.EncodeEnvelope(env)
	if err != nil {
		t.Fatalf("encode envelope: %v", err)
	}
	if err := w.Append(seq, blob); err != nil {
		t.Fatalf("append wal: %v", err)
	}
}

func TestOutboxReader_WALSourceReadEventAndRange(t *testing.T) {
	walDir := t.TempDir()
	w, err := ubtwal.OpenWriter(walDir, 1<<20)
	if err != nil {
		t.Fatalf("open wal writer: %v", err)
	}
	writeTestWALEnvelope(t, w, 7, []byte("a"))
	writeTestWALEnvelope(t, w, 8, []byte("bb"))
	writeTestWALEnvelope(t, w, 9, []byte("ccc"))
	if err := w.Close(); err != nil {
		t.Fatalf("close wal writer: %v", err)
	}

	reader := NewOutboxReader("http://localhost:8545")
	if err := reader.EnableWALSource(walDir, 0); err != nil {
		t.Fatalf("enable wal source: %v", err)
	}

	env, err := reader.ReadEvent(8)
	if err != nil {
		t.Fatalf("read event: %v", err)
	}
	if env == nil || env.Seq != 8 {
		t.Fatalf("unexpected envelope: %#v", env)
	}
	if string(env.Payload) != "bb" {
		t.Fatalf("unexpected payload: %q", env.Payload)
	}

	rangeEnvs, err := reader.ReadRange(7, 10)
	if err != nil {
		t.Fatalf("read range: %v", err)
	}
	if len(rangeEnvs) != 3 {
		t.Fatalf("unexpected range len: %d", len(rangeEnvs))
	}
}

func TestOutboxReader_WALSourceLatestLowest(t *testing.T) {
	walDir := t.TempDir()
	w, err := ubtwal.OpenWriter(walDir, 1<<20)
	if err != nil {
		t.Fatalf("open wal writer: %v", err)
	}
	writeTestWALEnvelope(t, w, 101, []byte("x"))
	writeTestWALEnvelope(t, w, 102, []byte("y"))
	if err := w.Close(); err != nil {
		t.Fatalf("close wal writer: %v", err)
	}

	reader := NewOutboxReader("http://localhost:8545")
	if err := reader.EnableWALSource(walDir, 0); err != nil {
		t.Fatalf("enable wal source: %v", err)
	}
	latest, err := reader.LatestSeq()
	if err != nil {
		t.Fatalf("latest seq: %v", err)
	}
	lowest, err := reader.LowestSeq()
	if err != nil {
		t.Fatalf("lowest seq: %v", err)
	}
	if latest != 102 || lowest != 101 {
		t.Fatalf("unexpected latest/lowest: latest=%d lowest=%d", latest, lowest)
	}
}

func TestOutboxReader_WALSourceFallsBackToRPCOnMissingSeq(t *testing.T) {
	server := newMockOutboxServer(t)
	server.api.addEnvelope(&ubtemit.OutboxEnvelope{
		Seq:         8,
		Version:     ubtemit.EnvelopeVersionV1,
		Kind:        ubtemit.KindDiff,
		BlockNumber: 108,
		BlockHash:   common.BigToHash(common.Big1),
		ParentHash:  common.Hash{},
		Timestamp:   1,
		Payload:     []byte("rpc"),
	})

	walDir := t.TempDir()
	w, err := ubtwal.OpenWriter(walDir, 1<<20)
	if err != nil {
		t.Fatalf("open wal writer: %v", err)
	}
	writeTestWALEnvelope(t, w, 7, []byte("wal"))
	if err := w.Close(); err != nil {
		t.Fatalf("close wal writer: %v", err)
	}

	reader := NewOutboxReader(server.Endpoint())
	reader.timeout = 2 * time.Second
	if err := reader.EnableWALSource(walDir, 0); err != nil {
		t.Fatalf("enable wal source: %v", err)
	}

	walEnv, err := reader.ReadEvent(7)
	if err != nil {
		t.Fatalf("read wal event: %v", err)
	}
	if walEnv == nil || string(walEnv.Payload) != "wal" {
		t.Fatalf("unexpected wal envelope: %#v", walEnv)
	}

	rpcEnv, err := reader.ReadEvent(8)
	if err != nil {
		t.Fatalf("read rpc fallback event: %v", err)
	}
	if rpcEnv == nil || rpcEnv.Seq != 8 || string(rpcEnv.Payload) != "rpc" {
		t.Fatalf("unexpected rpc fallback envelope: %#v", rpcEnv)
	}
}

func TestOutboxReader_WALSourceRangeTailFallbackToRPC(t *testing.T) {
	server := newMockOutboxServer(t)
	server.api.addEnvelope(&ubtemit.OutboxEnvelope{
		Seq:         3,
		Version:     ubtemit.EnvelopeVersionV1,
		Kind:        ubtemit.KindDiff,
		BlockNumber: 103,
		BlockHash:   common.BigToHash(common.Big1),
		ParentHash:  common.Hash{},
		Timestamp:   1,
		Payload:     []byte("rpc-tail"),
	})

	walDir := t.TempDir()
	w, err := ubtwal.OpenWriter(walDir, 1<<20)
	if err != nil {
		t.Fatalf("open wal writer: %v", err)
	}
	writeTestWALEnvelope(t, w, 1, []byte("a"))
	writeTestWALEnvelope(t, w, 2, []byte("b"))
	if err := w.Close(); err != nil {
		t.Fatalf("close wal writer: %v", err)
	}

	reader := NewOutboxReader(server.Endpoint())
	reader.timeout = 2 * time.Second
	if err := reader.EnableWALSource(walDir, 0); err != nil {
		t.Fatalf("enable wal source: %v", err)
	}
	envs, err := reader.ReadRange(1, 3)
	if err != nil {
		t.Fatalf("read range with fallback: %v", err)
	}
	if len(envs) != 3 {
		t.Fatalf("unexpected range len: %d", len(envs))
	}
	if string(envs[2].Payload) != "rpc-tail" {
		t.Fatalf("expected rpc-tail payload at seq=3, got %q", envs[2].Payload)
	}
}

func TestOutboxReader_WALSourceLatestLowestFallbackToRPCWhenEmpty(t *testing.T) {
	server := newMockOutboxServer(t)
	server.api.setPruneBelow(100)
	server.api.addEnvelope(&ubtemit.OutboxEnvelope{
		Seq:         102,
		Version:     ubtemit.EnvelopeVersionV1,
		Kind:        ubtemit.KindDiff,
		BlockNumber: 202,
		BlockHash:   common.BigToHash(common.Big1),
		ParentHash:  common.Hash{},
		Timestamp:   1,
		Payload:     []byte("x"),
	})

	walDir := t.TempDir()
	reader := NewOutboxReader(server.Endpoint())
	reader.timeout = 2 * time.Second
	if err := reader.EnableWALSource(walDir, 0); err != nil {
		t.Fatalf("enable wal source: %v", err)
	}
	latest, err := reader.LatestSeq()
	if err != nil {
		t.Fatalf("latest seq fallback: %v", err)
	}
	lowest, err := reader.LowestSeq()
	if err != nil {
		t.Fatalf("lowest seq fallback: %v", err)
	}
	if latest != 102 || lowest != 100 {
		t.Fatalf("unexpected latest/lowest from fallback: latest=%d lowest=%d", latest, lowest)
	}
}
