// Copyright 2018 The go-ethereum Authors
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

package rawdb

import (
	"bytes"
	"errors"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/ethdb/memorydb"
	"github.com/ethereum/go-ethereum/rlp"
)

// -- Error-injecting mocks for TDD -----------------------------------------

// errKeyValueReader always returns an error from Get and Has.
type errKeyValueReader struct {
	err error
}

func (e *errKeyValueReader) Has(key []byte) (bool, error)   { return false, e.err }
func (e *errKeyValueReader) Get(key []byte) ([]byte, error) { return nil, e.err }

// errIterator yields items then returns an error after exhaustion.
type errIterator struct {
	items []struct{ k, v []byte }
	pos   int
	err   error
}

func (it *errIterator) Next() bool {
	if it.pos < len(it.items) {
		it.pos++
		return true
	}
	return false
}
func (it *errIterator) Error() error  { return it.err }
func (it *errIterator) Key() []byte   { return it.items[it.pos-1].k }
func (it *errIterator) Value() []byte { return it.items[it.pos-1].v }
func (it *errIterator) Release()      {}

// errIteratee returns an errIterator that has data but ends with an error.
type errIteratee struct {
	items []struct{ k, v []byte }
	err   error
}

func (e *errIteratee) NewIterator(prefix []byte, start []byte) ethdb.Iterator {
	return &errIterator{items: e.items, err: e.err}
}

func TestUBTOutboxEventWriteRead(t *testing.T) {
	db := memorydb.New()

	// Test write and read single event
	seq := uint64(1)
	data := []byte("test event data")

	WriteUBTOutboxEvent(db, seq, data)

	// Read it back
	readData, err := ReadUBTOutboxEvent(db, seq)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !bytes.Equal(data, readData) {
		t.Fatalf("Expected %v, got %v", data, readData)
	}

	// Test HasUBTOutboxEvent
	has, err := HasUBTOutboxEvent(db, seq)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !has {
		t.Fatal("Event should exist")
	}

	// Test reading non-existent event (memorydb returns error for missing keys)
	_, err = ReadUBTOutboxEvent(db, 999)
	if err == nil {
		t.Fatal("Expected error for non-existent event")
	}

	has, err = HasUBTOutboxEvent(db, 999)
	if err != nil {
		t.Fatalf("Unexpected error from Has: %v", err)
	}
	if has {
		t.Fatal("Non-existent event should not exist")
	}
}

func TestUBTOutboxSeqCounter(t *testing.T) {
	db := memorydb.New()

	// Test reading non-existent counter (should return 0)
	counter := ReadUBTOutboxSeqCounter(db)
	if counter != 0 {
		t.Fatalf("Expected 0 for non-existent counter, got %d", counter)
	}

	// Write and read counter
	WriteUBTOutboxSeqCounter(db, 42)
	counter = ReadUBTOutboxSeqCounter(db)
	if counter != 42 {
		t.Fatalf("Expected 42, got %d", counter)
	}

	// Update counter
	WriteUBTOutboxSeqCounter(db, 100)
	counter = ReadUBTOutboxSeqCounter(db)
	if counter != 100 {
		t.Fatalf("Expected 100, got %d", counter)
	}
}

func TestUBTConsumerState(t *testing.T) {
	db := memorydb.New()

	// Test reading non-existent state (should return nil)
	state := ReadUBTConsumerState(db)
	if state != nil {
		t.Fatalf("Expected nil for non-existent state, got %v", state)
	}

	// Write and read state
	testState := &UBTConsumerState{
		PendingSeq:       5,
		PendingSeqActive: true,
		PendingStatus:    UBTConsumerPendingInFlight,
		PendingUpdatedAt: 1700000000,
		AppliedSeq:       4,
		AppliedRoot:      common.HexToHash("0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"),
		AppliedBlock:     1000,
	}

	WriteUBTConsumerState(db, testState)
	readState := ReadUBTConsumerState(db)

	if readState == nil {
		t.Fatal("Expected state, got nil")
	}
	if readState.PendingSeq != testState.PendingSeq {
		t.Fatalf("Expected PendingSeq %d, got %d", testState.PendingSeq, readState.PendingSeq)
	}
	if readState.PendingSeqActive != testState.PendingSeqActive {
		t.Fatalf("Expected PendingSeqActive %v, got %v", testState.PendingSeqActive, readState.PendingSeqActive)
	}
	if readState.PendingStatus != testState.PendingStatus {
		t.Fatalf("Expected PendingStatus %v, got %v", testState.PendingStatus, readState.PendingStatus)
	}
	if readState.PendingUpdatedAt != testState.PendingUpdatedAt {
		t.Fatalf("Expected PendingUpdatedAt %d, got %d", testState.PendingUpdatedAt, readState.PendingUpdatedAt)
	}
	if readState.AppliedSeq != testState.AppliedSeq {
		t.Fatalf("Expected AppliedSeq %d, got %d", testState.AppliedSeq, readState.AppliedSeq)
	}
	if readState.AppliedRoot != testState.AppliedRoot {
		t.Fatalf("Expected AppliedRoot %v, got %v", testState.AppliedRoot, readState.AppliedRoot)
	}
	if readState.AppliedBlock != testState.AppliedBlock {
		t.Fatalf("Expected AppliedBlock %d, got %d", testState.AppliedBlock, readState.AppliedBlock)
	}
}

func TestUBTConsumerState_BackwardCompatibleDecode(t *testing.T) {
	db := memorydb.New()

	// Legacy payload without PendingStatus/PendingUpdatedAt fields.
	legacy := struct {
		PendingSeq       uint64
		PendingSeqActive bool
		AppliedSeq       uint64
		AppliedRoot      common.Hash
		AppliedBlock     uint64
	}{
		PendingSeq:       7,
		PendingSeqActive: true,
		AppliedSeq:       6,
		AppliedRoot:      common.HexToHash("0x01"),
		AppliedBlock:     42,
	}
	data, err := rlp.EncodeToBytes(&legacy)
	if err != nil {
		t.Fatalf("encode legacy state: %v", err)
	}
	if err := db.Put(ubtOutboxConsumerStateKey, data); err != nil {
		t.Fatalf("write legacy state: %v", err)
	}

	state := ReadUBTConsumerState(db)
	if state == nil {
		t.Fatal("expected decoded state")
	}
	if state.PendingSeq != legacy.PendingSeq {
		t.Fatalf("pending seq mismatch: got %d want %d", state.PendingSeq, legacy.PendingSeq)
	}
	if state.PendingStatus != UBTConsumerPendingNone {
		t.Fatalf("expected default pending status none, got %v", state.PendingStatus)
	}
	if state.PendingUpdatedAt != 0 {
		t.Fatalf("expected zero PendingUpdatedAt for legacy state, got %d", state.PendingUpdatedAt)
	}
}

func TestReadUBTOutboxEvents(t *testing.T) {
	db := memorydb.New()

	// Write multiple events
	events := make(map[uint64][]byte)
	for i := uint64(1); i <= 10; i++ {
		data := []byte{byte(i), byte(i * 2)}
		events[i] = data
		WriteUBTOutboxEvent(db, i, data)
	}

	// Test reading range
	results, err := ReadUBTOutboxEvents(db, 3, 7)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if len(results) != 5 {
		t.Fatalf("Expected 5 events, got %d", len(results))
	}

	// Verify order and content
	for i, data := range results {
		seq := uint64(3 + i)
		expected := events[seq]
		if !bytes.Equal(data, expected) {
			t.Fatalf("Event %d: expected %v, got %v", seq, expected, data)
		}
	}

	// Test reading beyond range
	results, err = ReadUBTOutboxEvents(db, 8, 20)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if len(results) != 3 { // Only events 8, 9, 10 exist
		t.Fatalf("Expected 3 events, got %d", len(results))
	}

	// Test invalid range
	results, err = ReadUBTOutboxEvents(db, 10, 5)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if results != nil {
		t.Fatalf("Expected nil for invalid range, got %v", results)
	}
}

func TestIterateUBTOutboxEvents(t *testing.T) {
	db := memorydb.New()

	// Write events
	for i := uint64(1); i <= 10; i++ {
		data := []byte{byte(i)}
		WriteUBTOutboxEvent(db, i, data)
	}

	// Test full iteration
	count := 0
	lastSeq := uint64(0)
	err := IterateUBTOutboxEvents(db, 1, func(seq uint64, data []byte) bool {
		count++
		if seq <= lastSeq {
			t.Fatalf("Events not in order: %d after %d", seq, lastSeq)
		}
		lastSeq = seq
		if len(data) != 1 || data[0] != byte(seq) {
			t.Fatalf("Unexpected data for seq %d: %v", seq, data)
		}
		return true
	})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if count != 10 {
		t.Fatalf("Expected 10 events, iterated %d", count)
	}

	// Test partial iteration (stop early)
	count = 0
	err = IterateUBTOutboxEvents(db, 1, func(seq uint64, data []byte) bool {
		count++
		return count < 5 // Stop after 5 events
	})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if count != 5 {
		t.Fatalf("Expected 5 events, iterated %d", count)
	}

	// Test iteration from middle
	count = 0
	firstSeq := uint64(0)
	err = IterateUBTOutboxEvents(db, 5, func(seq uint64, data []byte) bool {
		if firstSeq == 0 {
			firstSeq = seq
		}
		count++
		return true
	})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if firstSeq != 5 {
		t.Fatalf("Expected first seq to be 5, got %d", firstSeq)
	}
	if count != 6 { // Events 5-10
		t.Fatalf("Expected 6 events from seq 5, got %d", count)
	}
}

func TestDeleteUBTOutboxEvent(t *testing.T) {
	db := memorydb.New()

	// Write and delete single event
	seq := uint64(5)
	data := []byte("test")
	WriteUBTOutboxEvent(db, seq, data)

	has, err := HasUBTOutboxEvent(db, seq)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !has {
		t.Fatal("Event should exist before deletion")
	}

	DeleteUBTOutboxEvent(db, seq)

	has, err = HasUBTOutboxEvent(db, seq)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if has {
		t.Fatal("Event should not exist after deletion")
	}
}

func TestDeleteUBTOutboxEventRange(t *testing.T) {
	db := memorydb.New()

	// Write multiple events
	for i := uint64(1); i <= 100; i++ {
		data := []byte{byte(i)}
		WriteUBTOutboxEvent(db, i, data)
	}

	// Delete range
	deleted, err := DeleteUBTOutboxEventRange(db, 10, 50)
	if err != nil {
		t.Fatalf("Failed to delete range: %v", err)
	}

	if deleted != 41 { // 10-50 inclusive
		t.Fatalf("Expected 41 deletions, got %d", deleted)
	}

	// Verify deleted events don't exist
	for i := uint64(10); i <= 50; i++ {
		has, err := HasUBTOutboxEvent(db, i)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if has {
			t.Fatalf("Event %d should be deleted", i)
		}
	}

	// Verify other events still exist
	for i := uint64(1); i < 10; i++ {
		has, err := HasUBTOutboxEvent(db, i)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if !has {
			t.Fatalf("Event %d should still exist", i)
		}
	}
	for i := uint64(51); i <= 100; i++ {
		has, err := HasUBTOutboxEvent(db, i)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if !has {
			t.Fatalf("Event %d should still exist", i)
		}
	}

	// Test invalid range
	deleted, err = DeleteUBTOutboxEventRange(db, 100, 50)
	if err != nil {
		t.Fatalf("Expected no error for invalid range, got: %v", err)
	}
	if deleted != 0 {
		t.Fatalf("Expected 0 deletions for invalid range, got %d", deleted)
	}
}

func TestSequenceOrdering(t *testing.T) {
	db := memorydb.New()

	// Write events out of order
	sequences := []uint64{5, 2, 8, 1, 10, 3, 7, 4, 6, 9}
	for _, seq := range sequences {
		data := []byte{byte(seq)}
		WriteUBTOutboxEvent(db, seq, data)
	}

	// Read range and verify ordering
	results, err := ReadUBTOutboxEvents(db, 1, 10)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if len(results) != 10 {
		t.Fatalf("Expected 10 events, got %d", len(results))
	}

	// Verify they are in sequential order
	for i, data := range results {
		expectedSeq := uint64(i + 1)
		if len(data) != 1 || data[0] != byte(expectedSeq) {
			t.Fatalf("Event at position %d should be seq %d, got %v", i, expectedSeq, data)
		}
	}

	// Verify iteration order
	collectedSeqs := []uint64{}
	err = IterateUBTOutboxEvents(db, 1, func(seq uint64, data []byte) bool {
		collectedSeqs = append(collectedSeqs, seq)
		return true
	})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	for i, seq := range collectedSeqs {
		expectedSeq := uint64(i + 1)
		if seq != expectedSeq {
			t.Fatalf("Iteration position %d: expected seq %d, got %d", i, expectedSeq, seq)
		}
	}
}

func TestUBTBlockRoot(t *testing.T) {
	db := memorydb.New()

	// Test reading non-existent block root (should return zero hash)
	root := ReadUBTBlockRoot(db, 1000)
	if root != (common.Hash{}) {
		t.Fatalf("Expected zero hash for non-existent block root, got %v", root)
	}

	// Write and read block root
	blockNumber := uint64(1000)
	expectedRoot := common.HexToHash("0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef")

	WriteUBTBlockRoot(db, blockNumber, expectedRoot)
	readRoot := ReadUBTBlockRoot(db, blockNumber)

	if readRoot != expectedRoot {
		t.Fatalf("Expected root %v, got %v", expectedRoot, readRoot)
	}

	// Test writing multiple block roots
	for i := uint64(1); i <= 10; i++ {
		root := common.BytesToHash([]byte{byte(i)})
		WriteUBTBlockRoot(db, i, root)
	}

	// Verify all roots can be read back
	for i := uint64(1); i <= 10; i++ {
		expectedRoot := common.BytesToHash([]byte{byte(i)})
		readRoot := ReadUBTBlockRoot(db, i)
		if readRoot != expectedRoot {
			t.Fatalf("Block %d: expected root %v, got %v", i, expectedRoot, readRoot)
		}
	}

	// Test overwriting a block root
	newRoot := common.HexToHash("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890")
	WriteUBTBlockRoot(db, 1, newRoot)
	readRoot = ReadUBTBlockRoot(db, 1)
	if readRoot != newRoot {
		t.Fatalf("Expected updated root %v, got %v", newRoot, readRoot)
	}
}

func TestUBTAnchorSnapshot_WriteRead(t *testing.T) {
	db := memorydb.New()

	// Test reading non-existent snapshot (should return nil)
	snap := ReadUBTAnchorSnapshot(db, 0)
	if snap != nil {
		t.Fatalf("Expected nil for non-existent snapshot, got %v", snap)
	}

	// Write and read anchor snapshot
	testSnap := &UBTAnchorSnapshot{
		BlockNumber: 1000,
		BlockRoot:   common.HexToHash("0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"),
		Seq:         42,
		Timestamp:   1234567890,
	}

	WriteUBTAnchorSnapshot(db, 0, testSnap)
	readSnap := ReadUBTAnchorSnapshot(db, 0)

	if readSnap == nil {
		t.Fatal("Expected snapshot, got nil")
	}
	if readSnap.BlockNumber != testSnap.BlockNumber {
		t.Fatalf("Expected BlockNumber %d, got %d", testSnap.BlockNumber, readSnap.BlockNumber)
	}
	if readSnap.BlockRoot != testSnap.BlockRoot {
		t.Fatalf("Expected BlockRoot %v, got %v", testSnap.BlockRoot, readSnap.BlockRoot)
	}
	if readSnap.Seq != testSnap.Seq {
		t.Fatalf("Expected Seq %d, got %d", testSnap.Seq, readSnap.Seq)
	}
	if readSnap.Timestamp != testSnap.Timestamp {
		t.Fatalf("Expected Timestamp %d, got %d", testSnap.Timestamp, readSnap.Timestamp)
	}

	// Write multiple snapshots
	for i := uint64(1); i <= 5; i++ {
		snap := &UBTAnchorSnapshot{
			BlockNumber: 1000 * i,
			BlockRoot:   common.BytesToHash([]byte{byte(i)}),
			Seq:         i * 10,
			Timestamp:   1234567890 + i,
		}
		WriteUBTAnchorSnapshot(db, i, snap)
	}

	// Verify all snapshots can be read back
	for i := uint64(1); i <= 5; i++ {
		readSnap := ReadUBTAnchorSnapshot(db, i)
		if readSnap == nil {
			t.Fatalf("Snapshot %d should exist", i)
		}
		if readSnap.BlockNumber != 1000*i {
			t.Fatalf("Snapshot %d: expected BlockNumber %d, got %d", i, 1000*i, readSnap.BlockNumber)
		}
		if readSnap.Seq != i*10 {
			t.Fatalf("Snapshot %d: expected Seq %d, got %d", i, i*10, readSnap.Seq)
		}
	}
}

func TestUBTAnchorSnapshot_Count(t *testing.T) {
	db := memorydb.New()

	// Test reading non-existent count (should return 0)
	count := ReadUBTAnchorSnapshotCount(db)
	if count != 0 {
		t.Fatalf("Expected 0 for non-existent count, got %d", count)
	}

	// Write and read count
	WriteUBTAnchorSnapshotCount(db, 5)
	count = ReadUBTAnchorSnapshotCount(db)
	if count != 5 {
		t.Fatalf("Expected 5, got %d", count)
	}

	// Update count
	WriteUBTAnchorSnapshotCount(db, 10)
	count = ReadUBTAnchorSnapshotCount(db)
	if count != 10 {
		t.Fatalf("Expected 10, got %d", count)
	}

	// Test incrementing count
	for i := uint64(0); i < 5; i++ {
		currentCount := ReadUBTAnchorSnapshotCount(db)
		WriteUBTAnchorSnapshotCount(db, currentCount+1)
	}
	count = ReadUBTAnchorSnapshotCount(db)
	if count != 15 {
		t.Fatalf("Expected 15 after increments, got %d", count)
	}
}

func TestUBTAnchorSnapshot_Latest(t *testing.T) {
	db := memorydb.New()

	// Test reading latest when no snapshots exist
	latest := ReadLatestUBTAnchorSnapshot(db)
	if latest != nil {
		t.Fatalf("Expected nil for non-existent latest snapshot, got %v", latest)
	}

	// Write snapshots
	for i := uint64(0); i < 5; i++ {
		snap := &UBTAnchorSnapshot{
			BlockNumber: 1000 * (i + 1),
			BlockRoot:   common.BytesToHash([]byte{byte(i + 1)}),
			Seq:         (i + 1) * 10,
			Timestamp:   1234567890 + i,
		}
		WriteUBTAnchorSnapshot(db, i, snap)
		WriteUBTAnchorSnapshotCount(db, i+1)
	}

	// Read latest snapshot
	latest = ReadLatestUBTAnchorSnapshot(db)
	if latest == nil {
		t.Fatal("Expected latest snapshot, got nil")
	}

	// Should be the last one (index 4)
	if latest.BlockNumber != 5000 {
		t.Fatalf("Expected BlockNumber 5000, got %d", latest.BlockNumber)
	}
	if latest.Seq != 50 {
		t.Fatalf("Expected Seq 50, got %d", latest.Seq)
	}

	// Write one more snapshot and verify latest updates
	newSnap := &UBTAnchorSnapshot{
		BlockNumber: 6000,
		BlockRoot:   common.BytesToHash([]byte{6}),
		Seq:         60,
		Timestamp:   1234567895,
	}
	WriteUBTAnchorSnapshot(db, 5, newSnap)
	WriteUBTAnchorSnapshotCount(db, 6)

	latest = ReadLatestUBTAnchorSnapshot(db)
	if latest.BlockNumber != 6000 {
		t.Fatalf("Expected BlockNumber 6000, got %d", latest.BlockNumber)
	}
}

func TestUBTAnchorSnapshot_Delete(t *testing.T) {
	db := memorydb.New()

	// Write snapshots
	for i := uint64(0); i < 5; i++ {
		snap := &UBTAnchorSnapshot{
			BlockNumber: 1000 * (i + 1),
			BlockRoot:   common.BytesToHash([]byte{byte(i + 1)}),
			Seq:         (i + 1) * 10,
			Timestamp:   1234567890 + i,
		}
		WriteUBTAnchorSnapshot(db, i, snap)
	}
	WriteUBTAnchorSnapshotCount(db, 5)

	// Delete first two snapshots
	DeleteUBTAnchorSnapshot(db, 0)
	DeleteUBTAnchorSnapshot(db, 1)

	// Verify deleted snapshots don't exist
	if ReadUBTAnchorSnapshot(db, 0) != nil {
		t.Fatal("Snapshot 0 should be deleted")
	}
	if ReadUBTAnchorSnapshot(db, 1) != nil {
		t.Fatal("Snapshot 1 should be deleted")
	}

	// Verify other snapshots still exist
	for i := uint64(2); i < 5; i++ {
		snap := ReadUBTAnchorSnapshot(db, i)
		if snap == nil {
			t.Fatalf("Snapshot %d should still exist", i)
		}
		if snap.BlockNumber != 1000*(i+1) {
			t.Fatalf("Snapshot %d: expected BlockNumber %d, got %d", i, 1000*(i+1), snap.BlockNumber)
		}
	}
}

func TestUBTCanonicalBlockMetadata(t *testing.T) {
	db := memorydb.New()

	blockNumber := uint64(123)
	blockHash := common.HexToHash("0x111122223333444455556666777788889999aaaabbbbccccddddeeeeffff0000")
	parentHash := common.HexToHash("0x0000ffffeeeeddddccccbbbbaaaa999988887777666655554444333322221111")

	// Initially empty
	if got := ReadUBTCanonicalBlockHash(db, blockNumber); got != (common.Hash{}) {
		t.Fatalf("expected empty canonical hash, got %v", got)
	}
	if got := ReadUBTCanonicalParentHash(db, blockNumber); got != (common.Hash{}) {
		t.Fatalf("expected empty canonical parent hash, got %v", got)
	}
	if _, ok := ReadUBTCanonicalBlockNumber(db, blockHash); ok {
		t.Fatal("expected no hash->number mapping")
	}

	WriteUBTCanonicalBlock(db, blockNumber, blockHash, parentHash)

	if got := ReadUBTCanonicalBlockHash(db, blockNumber); got != blockHash {
		t.Fatalf("expected canonical hash %v, got %v", blockHash, got)
	}
	if got := ReadUBTCanonicalParentHash(db, blockNumber); got != parentHash {
		t.Fatalf("expected canonical parent hash %v, got %v", parentHash, got)
	}
	gotNum, ok := ReadUBTCanonicalBlockNumber(db, blockHash)
	if !ok {
		t.Fatal("expected hash->number mapping")
	}
	if gotNum != blockNumber {
		t.Fatalf("expected block number %d, got %d", blockNumber, gotNum)
	}

	DeleteUBTCanonicalBlock(db, blockNumber)

	if got := ReadUBTCanonicalBlockHash(db, blockNumber); got != (common.Hash{}) {
		t.Fatalf("expected empty canonical hash after delete, got %v", got)
	}
	if got := ReadUBTCanonicalParentHash(db, blockNumber); got != (common.Hash{}) {
		t.Fatalf("expected empty canonical parent hash after delete, got %v", got)
	}
	if _, ok := ReadUBTCanonicalBlockNumber(db, blockHash); ok {
		t.Fatal("expected hash->number mapping deleted")
	}
}

// --------------------------------------------------------------------------
// TDD: C3 – ReadUBTOutboxEvent / HasUBTOutboxEvent must propagate DB errors
// --------------------------------------------------------------------------

func TestReadUBTOutboxEvent_DBError(t *testing.T) {
	injected := errors.New("disk I/O error")
	db := &errKeyValueReader{err: injected}

	_, err := ReadUBTOutboxEvent(db, 1)
	if err == nil {
		t.Fatal("ReadUBTOutboxEvent should return error on DB failure")
	}
	if !errors.Is(err, injected) {
		t.Fatalf("expected wrapped injected error, got: %v", err)
	}
}

func TestHasUBTOutboxEvent_DBError(t *testing.T) {
	injected := errors.New("disk I/O error")
	db := &errKeyValueReader{err: injected}

	_, err := HasUBTOutboxEvent(db, 1)
	if err == nil {
		t.Fatal("HasUBTOutboxEvent should return error on DB failure")
	}
	if !errors.Is(err, injected) {
		t.Fatalf("expected wrapped injected error, got: %v", err)
	}
}

// --------------------------------------------------------------------------
// TDD: C4 – ReadUBTOutboxEvents / IterateUBTOutboxEvents must check it.Error()
// --------------------------------------------------------------------------

func TestReadUBTOutboxEvents_IteratorError(t *testing.T) {
	injected := errors.New("corrupt iterator")

	// Build two valid items with the correct key format: prefix + 8-byte seq
	items := make([]struct{ k, v []byte }, 2)
	for i := 0; i < 2; i++ {
		items[i].k = ubtOutboxEventKey(uint64(i + 1))
		items[i].v = []byte{byte(i + 1)}
	}

	db := &errIteratee{items: items, err: injected}

	_, err := ReadUBTOutboxEvents(db, 1, 10)
	if err == nil {
		t.Fatal("ReadUBTOutboxEvents should return error when iterator fails")
	}
	if !errors.Is(err, injected) {
		t.Fatalf("expected wrapped injected error, got: %v", err)
	}
}

func TestIterateUBTOutboxEvents_IteratorError(t *testing.T) {
	injected := errors.New("corrupt iterator")

	items := make([]struct{ k, v []byte }, 2)
	for i := 0; i < 2; i++ {
		items[i].k = ubtOutboxEventKey(uint64(i + 1))
		items[i].v = []byte{byte(i + 1)}
	}

	db := &errIteratee{items: items, err: injected}

	called := 0
	err := IterateUBTOutboxEvents(db, 1, func(seq uint64, data []byte) bool {
		called++
		return true
	})
	if err == nil {
		t.Fatal("IterateUBTOutboxEvents should return error when iterator fails")
	}
	if !errors.Is(err, injected) {
		t.Fatalf("expected wrapped injected error, got: %v", err)
	}
	if called != 2 {
		t.Fatalf("expected callback called 2 times (items were yielded), got %d", called)
	}
}
