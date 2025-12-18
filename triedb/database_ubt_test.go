// Copyright 2025 The go-ethereum Authors
// This file is part of the go-ethereum library.

package triedb

import (
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/triedb/hashdb"
	"github.com/ethereum/go-ethereum/triedb/pathdb"
)

func TestUBTConversionStatusNotPathDB(t *testing.T) {
	diskdb := rawdb.NewMemoryDatabase()

	// Create hash-based database
	db := NewDatabase(diskdb, &Config{
		HashDB: hashdb.Defaults,
	})
	defer db.Close()

	// StartUBTConversion should fail for hashdb
	err := db.StartUBTConversion(common.Hash{}, 1000)
	if err == nil {
		t.Error("expected error for hashdb, got nil")
	}

	// Status should be nil
	if status := db.UBTConversionStatus(); status != nil {
		t.Errorf("expected nil status for hashdb, got %+v", status)
	}

	// Done should be false
	if db.UBTConversionDone() {
		t.Error("expected UBTConversionDone to be false for hashdb")
	}
}

func TestUBTConversionAPIsPathDB(t *testing.T) {
	diskdb := rawdb.NewMemoryDatabase()

	// Create path-based database with verkle mode
	db := NewDatabase(diskdb, &Config{
		IsVerkle: true,
		PathDB:   pathdb.Defaults,
	})
	defer db.Close()

	// Initially, status should be nil (no conversion started)
	if status := db.UBTConversionStatus(); status != nil {
		t.Errorf("expected nil initial status, got %+v", status)
	}

	// Done should be false initially
	if db.UBTConversionDone() {
		t.Error("expected UBTConversionDone to be false initially")
	}

	// StopUBTConversion should be a no-op when not running
	db.StopUBTConversion() // Should not panic or hang
}
