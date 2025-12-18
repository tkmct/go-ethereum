// Copyright 2025 The go-ethereum Authors
// This file is part of the go-ethereum library.

package rawdb

import (
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

func TestUBTConversionProgressSerialize(t *testing.T) {
	db := NewMemoryDatabase()

	progress := &UBTConversionProgress{
		Version:         1,
		Stage:           UBTStageRunning,
		StateRoot:       common.HexToHash("0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"),
		UbtRoot:         common.HexToHash("0x5678901234abcdef5678901234abcdef5678901234abcdef5678901234abcdef"),
		NextAccountHash: common.HexToHash("0xaaaa1111222233334444555566667777888899990000aaaabbbbccccddddeeeef"),
		CurrentAccount:  common.HexToHash("0xbbbb2222333344445555666677778888999900001111aaaabbbbccccddddeeeef"),
		NextStorageHash: common.HexToHash("0xcccc3333444455556666777788889999000011112222aaaabbbbccccddddeeeef"),
		AccountsDone:    1000,
		SlotsDone:       50000,
		LastError:       "test error",
		UpdatedAt:       uint64(time.Now().Unix()),
	}

	WriteUBTConversionStatus(db, progress)

	got := ReadUBTConversionStatus(db)
	if got == nil {
		t.Fatal("ReadUBTConversionStatus returned nil")
	}

	if got.Version != progress.Version {
		t.Errorf("Version mismatch: got %d, want %d", got.Version, progress.Version)
	}
	if got.Stage != progress.Stage {
		t.Errorf("Stage mismatch: got %d, want %d", got.Stage, progress.Stage)
	}
	if got.StateRoot != progress.StateRoot {
		t.Errorf("StateRoot mismatch: got %s, want %s", got.StateRoot.Hex(), progress.StateRoot.Hex())
	}
	if got.UbtRoot != progress.UbtRoot {
		t.Errorf("UbtRoot mismatch: got %s, want %s", got.UbtRoot.Hex(), progress.UbtRoot.Hex())
	}
	if got.NextAccountHash != progress.NextAccountHash {
		t.Errorf("NextAccountHash mismatch: got %s, want %s", got.NextAccountHash.Hex(), progress.NextAccountHash.Hex())
	}
	if got.CurrentAccount != progress.CurrentAccount {
		t.Errorf("CurrentAccount mismatch: got %s, want %s", got.CurrentAccount.Hex(), progress.CurrentAccount.Hex())
	}
	if got.NextStorageHash != progress.NextStorageHash {
		t.Errorf("NextStorageHash mismatch: got %s, want %s", got.NextStorageHash.Hex(), progress.NextStorageHash.Hex())
	}
	if got.AccountsDone != progress.AccountsDone {
		t.Errorf("AccountsDone mismatch: got %d, want %d", got.AccountsDone, progress.AccountsDone)
	}
	if got.SlotsDone != progress.SlotsDone {
		t.Errorf("SlotsDone mismatch: got %d, want %d", got.SlotsDone, progress.SlotsDone)
	}
	if got.LastError != progress.LastError {
		t.Errorf("LastError mismatch: got %s, want %s", got.LastError, progress.LastError)
	}
	if got.UpdatedAt != progress.UpdatedAt {
		t.Errorf("UpdatedAt mismatch: got %d, want %d", got.UpdatedAt, progress.UpdatedAt)
	}
}

func TestUBTConversionProgressEmpty(t *testing.T) {
	db := NewMemoryDatabase()

	got := ReadUBTConversionStatus(db)
	if got != nil {
		t.Errorf("Expected nil for empty database, got %+v", got)
	}
}

func TestUBTConversionProgressDelete(t *testing.T) {
	db := NewMemoryDatabase()

	progress := &UBTConversionProgress{
		Version:      1,
		Stage:        UBTStageDone,
		StateRoot:    common.HexToHash("0x1234"),
		AccountsDone: 100,
		SlotsDone:    1000,
		UpdatedAt:    uint64(time.Now().Unix()),
	}

	WriteUBTConversionStatus(db, progress)

	got := ReadUBTConversionStatus(db)
	if got == nil {
		t.Fatal("ReadUBTConversionStatus returned nil after write")
	}

	DeleteUBTConversionStatus(db)

	got = ReadUBTConversionStatus(db)
	if got != nil {
		t.Errorf("Expected nil after delete, got %+v", got)
	}
}

func TestUBTConversionProgressStages(t *testing.T) {
	if UBTStageIdle != 0 {
		t.Errorf("UBTStageIdle: got %d, want 0", UBTStageIdle)
	}
	if UBTStageRunning != 1 {
		t.Errorf("UBTStageRunning: got %d, want 1", UBTStageRunning)
	}
	if UBTStageFailed != 2 {
		t.Errorf("UBTStageFailed: got %d, want 2", UBTStageFailed)
	}
	if UBTStageDone != 3 {
		t.Errorf("UBTStageDone: got %d, want 3", UBTStageDone)
	}
}
