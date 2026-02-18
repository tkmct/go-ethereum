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
	"encoding/binary"
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/rlp"
)

// UBTConsumerPendingStatus defines durable pending state transitions for crash recovery.
type UBTConsumerPendingStatus uint8

const (
	UBTConsumerPendingNone UBTConsumerPendingStatus = iota
	UBTConsumerPendingInFlight
)

func (s UBTConsumerPendingStatus) String() string {
	switch s {
	case UBTConsumerPendingInFlight:
		return "inflight"
	default:
		return "none"
	}
}

// UBTConsumerState holds the durable consumer checkpoint.
type UBTConsumerState struct {
	PendingSeq       uint64                   // Sequence currently being processed (valid only when PendingStatus=UBTConsumerPendingInFlight)
	PendingSeqActive bool                     // Deprecated compatibility flag for seq=0 support; mirrored from PendingStatus.
	AppliedSeq       uint64                   // Last fully applied sequence
	AppliedRoot      common.Hash              // UBT root after last applied sequence
	AppliedBlock     uint64                   // Last applied block number
	PendingStatus    UBTConsumerPendingStatus `rlp:"optional"` // Explicit pending status state machine.
	PendingUpdatedAt uint64                   `rlp:"optional"` // Unix timestamp of last pending status transition.
}

// UBTFailureCheckpoint stores the latest degraded emitter failure reason.
type UBTFailureCheckpoint struct {
	BlockNumber uint64
	Reason      string
}

// WriteUBTOutboxEvent writes an outbox event at the given sequence number.
// NOTE: This function uses log.Crit on failure, making it unsuitable for
// production hot paths. For production outbox writes from the emitter,
// use WriteUBTOutboxEventAtomic which returns errors for graceful degradation.
// This function is primarily used in tests and as a building block for atomic writes.
func WriteUBTOutboxEvent(db ethdb.KeyValueWriter, seq uint64, data []byte) {
	key := ubtOutboxEventKey(seq)
	if err := db.Put(key, data); err != nil {
		log.Crit("Failed to write UBT outbox event", "seq", seq, "err", err)
	}
}

// ReadUBTOutboxEvent reads an outbox event by sequence number.
// Returns the raw data and any error from the underlying database.
func ReadUBTOutboxEvent(db ethdb.KeyValueReader, seq uint64) ([]byte, error) {
	return db.Get(ubtOutboxEventKey(seq))
}

// HasUBTOutboxEvent checks if an outbox event exists at the given sequence.
func HasUBTOutboxEvent(db ethdb.KeyValueReader, seq uint64) (bool, error) {
	return db.Has(ubtOutboxEventKey(seq))
}

// DeleteUBTOutboxEvent deletes an outbox event by sequence number.
// NOTE: This function uses log.Crit on failure. For production event cleanup,
// prefer DeleteUBTOutboxEventRange which returns errors for graceful handling.
// This function is primarily used in tests and internal helpers.
func DeleteUBTOutboxEvent(db ethdb.KeyValueWriter, seq uint64) {
	key := ubtOutboxEventKey(seq)
	if err := db.Delete(key); err != nil {
		log.Crit("Failed to delete UBT outbox event", "seq", seq, "err", err)
	}
}

// ReadUBTOutboxEvents reads a range of outbox events [fromSeq, toSeq].
func ReadUBTOutboxEvents(db ethdb.Iteratee, fromSeq, toSeq uint64) ([][]byte, error) {
	if fromSeq > toSeq {
		return nil, nil
	}

	result := make([][]byte, 0, toSeq-fromSeq+1)
	startKey := ubtOutboxEventKey(fromSeq)

	it := db.NewIterator(ubtOutboxEventPrefix, startKey[len(ubtOutboxEventPrefix):])
	defer it.Release()

	for it.Next() {
		key := it.Key()
		if len(key) != len(ubtOutboxEventPrefix)+8 {
			continue
		}

		seq := binary.BigEndian.Uint64(key[len(ubtOutboxEventPrefix):])
		if seq > toSeq {
			break
		}

		// Make a copy of the value since iterator reuses the buffer
		value := make([]byte, len(it.Value()))
		copy(value, it.Value())
		result = append(result, value)
	}

	if err := it.Error(); err != nil {
		return nil, fmt.Errorf("iterate outbox events [%d, %d]: %w", fromSeq, toSeq, err)
	}
	return result, nil
}

// WriteUBTOutboxSeqCounter persists the current sequence counter.
// NOTE: This function uses log.Crit on failure. For production sequence counter
// updates from the emitter, use WriteUBTOutboxEventAtomic which atomically writes
// both the event and counter with error handling.
// This function is primarily used in tests and internal helpers.
func WriteUBTOutboxSeqCounter(db ethdb.KeyValueWriter, seq uint64) {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, seq)
	if err := db.Put(ubtOutboxSeqCounterKey, buf); err != nil {
		log.Crit("Failed to write UBT outbox sequence counter", "seq", seq, "err", err)
	}
}

// ReadUBTOutboxSeqCounter reads the current sequence counter.
func ReadUBTOutboxSeqCounter(db ethdb.KeyValueReader) uint64 {
	data, err := db.Get(ubtOutboxSeqCounterKey)
	if err != nil || len(data) != 8 {
		return 0
	}
	return binary.BigEndian.Uint64(data)
}

// WriteUBTOutboxLowestSeq persists the lowest non-deleted sequence number.
func WriteUBTOutboxLowestSeq(db ethdb.KeyValueWriter, seq uint64) {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, seq)
	if err := db.Put(ubtOutboxLowestSeqKey, buf); err != nil {
		log.Crit("Failed to write UBT outbox lowest seq", "seq", seq, "err", err)
	}
}

// ReadUBTOutboxLowestSeq reads the lowest non-deleted sequence number.
// Returns 0 if not found.
func ReadUBTOutboxLowestSeq(db ethdb.KeyValueReader) uint64 {
	data, err := db.Get(ubtOutboxLowestSeqKey)
	if err != nil || len(data) != 8 {
		return 0
	}
	return binary.BigEndian.Uint64(data)
}

// WriteUBTOutboxDiskUsage persists the cumulative disk usage bytes.
func WriteUBTOutboxDiskUsage(db ethdb.KeyValueWriter, bytes uint64) {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, bytes)
	if err := db.Put(ubtOutboxDiskUsageKey, buf); err != nil {
		log.Crit("Failed to write UBT outbox disk usage", "bytes", bytes, "err", err)
	}
}

// ReadUBTOutboxDiskUsage reads the cumulative disk usage bytes.
// Returns 0 if not found.
func ReadUBTOutboxDiskUsage(db ethdb.KeyValueReader) uint64 {
	data, err := db.Get(ubtOutboxDiskUsageKey)
	if err != nil || len(data) != 8 {
		return 0
	}
	return binary.BigEndian.Uint64(data)
}

// WriteUBTFailureCheckpoint writes the last emitter failure checkpoint.
func WriteUBTFailureCheckpoint(db ethdb.KeyValueWriter, blockNumber uint64, reason string) {
	data := make([]byte, 8+len(reason))
	binary.BigEndian.PutUint64(data[:8], blockNumber)
	copy(data[8:], []byte(reason))
	if err := db.Put(ubtOutboxFailureCkptKey, data); err != nil {
		log.Crit("Failed to write UBT failure checkpoint", "block", blockNumber, "err", err)
	}
}

// ReadUBTFailureCheckpoint reads the latest emitter failure checkpoint.
func ReadUBTFailureCheckpoint(db ethdb.KeyValueReader) *UBTFailureCheckpoint {
	data, err := db.Get(ubtOutboxFailureCkptKey)
	if err != nil || len(data) < 8 {
		return nil
	}
	return &UBTFailureCheckpoint{
		BlockNumber: binary.BigEndian.Uint64(data[:8]),
		Reason:      string(data[8:]),
	}
}

// DeleteUBTFailureCheckpoint clears the latest emitter failure checkpoint.
func DeleteUBTFailureCheckpoint(db ethdb.KeyValueWriter) {
	if err := db.Delete(ubtOutboxFailureCkptKey); err != nil {
		log.Crit("Failed to delete UBT failure checkpoint", "err", err)
	}
}

// WriteUBTConsumerState writes the consumer checkpoint state.
func WriteUBTConsumerState(db ethdb.KeyValueWriter, state *UBTConsumerState) {
	data, err := rlp.EncodeToBytes(state)
	if err != nil {
		log.Crit("Failed to RLP encode UBT consumer state", "err", err)
	}
	if err := db.Put(ubtOutboxConsumerStateKey, data); err != nil {
		log.Crit("Failed to write UBT consumer state", "err", err)
	}
}

// ReadUBTConsumerState reads the consumer checkpoint state.
func ReadUBTConsumerState(db ethdb.KeyValueReader) *UBTConsumerState {
	data, err := db.Get(ubtOutboxConsumerStateKey)
	if err != nil {
		return nil
	}

	var state UBTConsumerState
	if err := rlp.DecodeBytes(data, &state); err != nil {
		log.Error("Failed to decode UBT consumer state", "err", err)
		return nil
	}
	return &state
}

// WriteUBTOutboxEventAtomic atomically writes an outbox event and updates the sequence counter.
// This ensures that both operations succeed or fail together, preventing sequence counter
// desynchronization in case of crashes.
func WriteUBTOutboxEventAtomic(db ethdb.Batcher, seq uint64, data []byte, nextSeq uint64) error {
	batch := db.NewBatch()

	// Write the event
	eventKey := ubtOutboxEventKey(seq)
	if err := batch.Put(eventKey, data); err != nil {
		return fmt.Errorf("failed to add event to batch: %w", err)
	}

	// Write the updated sequence counter
	seqBuf := make([]byte, 8)
	binary.BigEndian.PutUint64(seqBuf, nextSeq)
	if err := batch.Put(ubtOutboxSeqCounterKey, seqBuf); err != nil {
		return fmt.Errorf("failed to add sequence counter to batch: %w", err)
	}

	// Atomically commit both writes
	if err := batch.Write(); err != nil {
		return fmt.Errorf("failed to write outbox batch: %w", err)
	}

	return nil
}

// DeleteUBTOutboxEventRange deletes outbox events in range [fromSeq, toSeq] for retention/compaction.
func DeleteUBTOutboxEventRange(db ethdb.KeyValueStore, fromSeq, toSeq uint64) (int, error) {
	if fromSeq > toSeq {
		return 0, nil
	}

	batch := db.NewBatch()
	count := 0

	for seq := fromSeq; seq <= toSeq; seq++ {
		key := ubtOutboxEventKey(seq)
		if err := batch.Delete(key); err != nil {
			return count, fmt.Errorf("failed to delete seq %d: %w", seq, err)
		}
		count++

		// Commit batch every 1000 deletions to avoid memory pressure
		if count%1000 == 0 {
			if err := batch.Write(); err != nil {
				return count, fmt.Errorf("failed to write batch: %w", err)
			}
			batch.Reset()
		}
	}

	// Write remaining deletions
	if batch.ValueSize() > 0 {
		if err := batch.Write(); err != nil {
			return count, fmt.Errorf("failed to write final batch: %w", err)
		}
	}

	return count, nil
}

// IterateUBTOutboxEvents iterates outbox events from startSeq, calling fn for each.
// If fn returns false, iteration stops early (no error).
func IterateUBTOutboxEvents(db ethdb.Iteratee, startSeq uint64, fn func(seq uint64, data []byte) bool) error {
	startKey := ubtOutboxEventKey(startSeq)
	it := db.NewIterator(ubtOutboxEventPrefix, startKey[len(ubtOutboxEventPrefix):])
	defer it.Release()

	for it.Next() {
		key := it.Key()
		if len(key) != len(ubtOutboxEventPrefix)+8 {
			continue
		}

		seq := binary.BigEndian.Uint64(key[len(ubtOutboxEventPrefix):])

		// Make a copy of the value since iterator reuses the buffer
		value := make([]byte, len(it.Value()))
		copy(value, it.Value())

		if !fn(seq, value) {
			return nil
		}
	}

	if err := it.Error(); err != nil {
		return fmt.Errorf("iterate outbox events from seq %d: %w", startSeq, err)
	}
	return nil
}

// WriteUBTBlockRoot persists the UBT root for a block number.
func WriteUBTBlockRoot(db ethdb.KeyValueWriter, blockNumber uint64, root common.Hash) {
	key := ubtBlockRootKey(blockNumber)
	if err := db.Put(key, root.Bytes()); err != nil {
		log.Crit("Failed to write UBT block root", "block", blockNumber, "root", root, "err", err)
	}
}

// DeleteUBTBlockRoot deletes a UBT root for a block number.
func DeleteUBTBlockRoot(db ethdb.KeyValueWriter, blockNumber uint64) {
	if err := db.Delete(ubtBlockRootKey(blockNumber)); err != nil {
		log.Crit("Failed to delete UBT block root", "block", blockNumber, "err", err)
	}
}

// ReadUBTBlockRoot reads the UBT root for a block number.
// Returns zero hash if the root is not found.
func ReadUBTBlockRoot(db ethdb.KeyValueReader, blockNumber uint64) common.Hash {
	data, err := db.Get(ubtBlockRootKey(blockNumber))
	if err != nil || len(data) != common.HashLength {
		return common.Hash{}
	}
	return common.BytesToHash(data)
}

// WriteUBTCanonicalBlock writes canonical hash/parent metadata and reverse hash->number lookup.
func WriteUBTCanonicalBlock(db ethdb.KeyValueWriter, blockNumber uint64, blockHash common.Hash, parentHash common.Hash) {
	if blockHash == (common.Hash{}) {
		return
	}
	num := make([]byte, 8)
	binary.BigEndian.PutUint64(num, blockNumber)

	if err := db.Put(ubtBlockHashKey(blockNumber), blockHash.Bytes()); err != nil {
		log.Crit("Failed to write UBT canonical block hash", "block", blockNumber, "hash", blockHash, "err", err)
	}
	if err := db.Put(ubtBlockParentHashKey(blockNumber), parentHash.Bytes()); err != nil {
		log.Crit("Failed to write UBT canonical parent hash", "block", blockNumber, "parent", parentHash, "err", err)
	}
	if err := db.Put(ubtBlockNumberByHashKey(blockHash), num); err != nil {
		log.Crit("Failed to write UBT canonical hash->number", "block", blockNumber, "hash", blockHash, "err", err)
	}
}

// DeleteUBTCanonicalBlock deletes canonical block metadata for a block number.
func DeleteUBTCanonicalBlock(db ethdb.KeyValueStore, blockNumber uint64) {
	hash := ReadUBTCanonicalBlockHash(db, blockNumber)
	if hash != (common.Hash{}) {
		if err := db.Delete(ubtBlockNumberByHashKey(hash)); err != nil {
			log.Crit("Failed to delete UBT canonical hash->number", "block", blockNumber, "hash", hash, "err", err)
		}
	}
	if err := db.Delete(ubtBlockHashKey(blockNumber)); err != nil {
		log.Crit("Failed to delete UBT canonical block hash", "block", blockNumber, "err", err)
	}
	if err := db.Delete(ubtBlockParentHashKey(blockNumber)); err != nil {
		log.Crit("Failed to delete UBT canonical parent hash", "block", blockNumber, "err", err)
	}
}

// ReadUBTCanonicalBlockHash reads canonical block hash for block number.
func ReadUBTCanonicalBlockHash(db ethdb.KeyValueReader, blockNumber uint64) common.Hash {
	data, err := db.Get(ubtBlockHashKey(blockNumber))
	if err != nil || len(data) != common.HashLength {
		return common.Hash{}
	}
	return common.BytesToHash(data)
}

// ReadUBTCanonicalParentHash reads canonical parent hash for block number.
func ReadUBTCanonicalParentHash(db ethdb.KeyValueReader, blockNumber uint64) common.Hash {
	data, err := db.Get(ubtBlockParentHashKey(blockNumber))
	if err != nil || len(data) != common.HashLength {
		return common.Hash{}
	}
	return common.BytesToHash(data)
}

// ReadUBTCanonicalBlockNumber reads canonical block number by block hash.
func ReadUBTCanonicalBlockNumber(db ethdb.KeyValueReader, blockHash common.Hash) (uint64, bool) {
	data, err := db.Get(ubtBlockNumberByHashKey(blockHash))
	if err != nil || len(data) != 8 {
		return 0, false
	}
	return binary.BigEndian.Uint64(data), true
}

// UBTAnchorSnapshot holds a point-in-time UBT state checkpoint.
type UBTAnchorSnapshot struct {
	BlockNumber uint64      // Block number at this anchor
	BlockRoot   common.Hash // UBT root at this block
	Seq         uint64      // Outbox sequence at this block
	Timestamp   uint64      // Unix timestamp when anchor was created
}

// UBTRecoveryAnchorState describes lifecycle state of a materialized recovery anchor.
type UBTRecoveryAnchorState uint8

const (
	UBTRecoveryAnchorCreating UBTRecoveryAnchorState = iota
	UBTRecoveryAnchorReady
	UBTRecoveryAnchorBroken
)

func (s UBTRecoveryAnchorState) String() string {
	switch s {
	case UBTRecoveryAnchorCreating:
		return "creating"
	case UBTRecoveryAnchorReady:
		return "ready"
	case UBTRecoveryAnchorBroken:
		return "broken"
	default:
		return "unknown"
	}
}

// UBTRecoveryAnchorManifest stores metadata for a materialized recovery anchor.
type UBTRecoveryAnchorManifest struct {
	AnchorID      uint64
	Seq           uint64
	BlockNumber   uint64
	BlockRoot     common.Hash
	CreatedAt     uint64
	FormatVersion uint16
	State         UBTRecoveryAnchorState
	FailureReason string `rlp:"optional"`
}

// WriteUBTAnchorSnapshot writes an anchor snapshot for the given index.
func WriteUBTAnchorSnapshot(db ethdb.KeyValueWriter, index uint64, snap *UBTAnchorSnapshot) {
	data, err := rlp.EncodeToBytes(snap)
	if err != nil {
		log.Crit("Failed to RLP encode UBT anchor snapshot", "err", err)
	}
	key := ubtAnchorSnapshotKey(index)
	if err := db.Put(key, data); err != nil {
		log.Crit("Failed to write UBT anchor snapshot", "index", index, "err", err)
	}
}

// ReadUBTAnchorSnapshot reads an anchor snapshot by index.
func ReadUBTAnchorSnapshot(db ethdb.KeyValueReader, index uint64) *UBTAnchorSnapshot {
	key := ubtAnchorSnapshotKey(index)
	data, err := db.Get(key)
	if err != nil {
		return nil
	}
	var snap UBTAnchorSnapshot
	if err := rlp.DecodeBytes(data, &snap); err != nil {
		log.Error("Failed to decode UBT anchor snapshot", "index", index, "err", err)
		return nil
	}
	return &snap
}

// WriteUBTAnchorSnapshotCount writes the total number of anchor snapshots.
func WriteUBTAnchorSnapshotCount(db ethdb.KeyValueWriter, count uint64) {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, count)
	if err := db.Put(ubtAnchorSnapshotCountKey, buf); err != nil {
		log.Crit("Failed to write UBT anchor snapshot count", "count", count, "err", err)
	}
}

// ReadUBTAnchorSnapshotCount reads the total number of anchor snapshots.
func ReadUBTAnchorSnapshotCount(db ethdb.KeyValueReader) uint64 {
	data, err := db.Get(ubtAnchorSnapshotCountKey)
	if err != nil || len(data) != 8 {
		return 0
	}
	return binary.BigEndian.Uint64(data)
}

// ReadLatestUBTAnchorSnapshot reads the most recent anchor snapshot.
func ReadLatestUBTAnchorSnapshot(db ethdb.KeyValueReader) *UBTAnchorSnapshot {
	count := ReadUBTAnchorSnapshotCount(db)
	if count == 0 {
		return nil
	}
	return ReadUBTAnchorSnapshot(db, count-1)
}

// DeleteUBTAnchorSnapshot deletes an anchor snapshot by index.
func DeleteUBTAnchorSnapshot(db ethdb.KeyValueWriter, index uint64) {
	key := ubtAnchorSnapshotKey(index)
	if err := db.Delete(key); err != nil {
		log.Crit("Failed to delete UBT anchor snapshot", "index", index, "err", err)
	}
}

// WriteUBTRecoveryAnchorManifest writes a recovery anchor manifest at index.
func WriteUBTRecoveryAnchorManifest(db ethdb.KeyValueWriter, index uint64, manifest *UBTRecoveryAnchorManifest) {
	data, err := rlp.EncodeToBytes(manifest)
	if err != nil {
		log.Crit("Failed to RLP encode UBT recovery anchor manifest", "index", index, "err", err)
	}
	if err := db.Put(ubtRecoveryAnchorKey(index), data); err != nil {
		log.Crit("Failed to write UBT recovery anchor manifest", "index", index, "err", err)
	}
}

// ReadUBTRecoveryAnchorManifest reads a recovery anchor manifest by index.
func ReadUBTRecoveryAnchorManifest(db ethdb.KeyValueReader, index uint64) *UBTRecoveryAnchorManifest {
	data, err := db.Get(ubtRecoveryAnchorKey(index))
	if err != nil {
		return nil
	}
	var manifest UBTRecoveryAnchorManifest
	if err := rlp.DecodeBytes(data, &manifest); err != nil {
		log.Error("Failed to decode UBT recovery anchor manifest", "index", index, "err", err)
		return nil
	}
	return &manifest
}

// DeleteUBTRecoveryAnchorManifest deletes a recovery anchor manifest by index.
func DeleteUBTRecoveryAnchorManifest(db ethdb.KeyValueWriter, index uint64) {
	if err := db.Delete(ubtRecoveryAnchorKey(index)); err != nil {
		log.Crit("Failed to delete UBT recovery anchor manifest", "index", index, "err", err)
	}
}

// WriteUBTRecoveryAnchorCount writes the total number of created recovery anchors.
func WriteUBTRecoveryAnchorCount(db ethdb.KeyValueWriter, count uint64) {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, count)
	if err := db.Put(ubtRecoveryAnchorCountKey, buf); err != nil {
		log.Crit("Failed to write UBT recovery anchor count", "count", count, "err", err)
	}
}

// ReadUBTRecoveryAnchorCount reads the total number of created recovery anchors.
func ReadUBTRecoveryAnchorCount(db ethdb.KeyValueReader) uint64 {
	data, err := db.Get(ubtRecoveryAnchorCountKey)
	if err != nil || len(data) != 8 {
		return 0
	}
	return binary.BigEndian.Uint64(data)
}

// WriteUBTRecoveryAnchorLatestReady writes the latest ready recovery anchor index.
func WriteUBTRecoveryAnchorLatestReady(db ethdb.KeyValueWriter, index uint64) {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, index)
	if err := db.Put(ubtRecoveryAnchorLatestReadyKey, buf); err != nil {
		log.Crit("Failed to write latest ready recovery anchor index", "index", index, "err", err)
	}
}

// ReadUBTRecoveryAnchorLatestReady reads the latest ready recovery anchor index.
func ReadUBTRecoveryAnchorLatestReady(db ethdb.KeyValueReader) (uint64, bool) {
	data, err := db.Get(ubtRecoveryAnchorLatestReadyKey)
	if err != nil || len(data) != 8 {
		return 0, false
	}
	return binary.BigEndian.Uint64(data), true
}

// DeleteUBTRecoveryAnchorLatestReady deletes the latest ready recovery anchor marker.
func DeleteUBTRecoveryAnchorLatestReady(db ethdb.KeyValueWriter) {
	if err := db.Delete(ubtRecoveryAnchorLatestReadyKey); err != nil {
		log.Crit("Failed to delete latest ready recovery anchor marker", "err", err)
	}
}
