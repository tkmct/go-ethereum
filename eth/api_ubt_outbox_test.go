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

package eth

import (
	"context"
	"math/big"
	"os"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/ubtemit"
)

// newTestOutboxStore creates a temporary outbox store for testing.
func newTestOutboxStore(t *testing.T) (*ubtemit.OutboxStore, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "ubt-outbox-api-test-*")
	if err != nil {
		t.Fatal(err)
	}
	store, err := ubtemit.NewOutboxStore(dir, 5*time.Second, 0, 0) // No retention for tests
	if err != nil {
		os.RemoveAll(dir)
		t.Fatal(err)
	}
	return store, dir
}

// TestToRPCEvent tests conversion of OutboxEnvelope to RPCOutboxEvent.
func TestToRPCEvent(t *testing.T) {
	blockHash := common.HexToHash("0xdeadbeef")
	parentHash := common.HexToHash("0xcafebabe")
	payload := []byte{1, 2, 3, 4, 5}

	env := &ubtemit.OutboxEnvelope{
		Seq:         42,
		Version:     ubtemit.EnvelopeVersionV1,
		Kind:        ubtemit.KindDiff,
		BlockNumber: 12345,
		BlockHash:   blockHash,
		ParentHash:  parentHash,
		Timestamp:   1234567890,
		Payload:     payload,
	}

	rpc := toRPCEvent(env)

	// Verify all fields are correctly converted
	if uint64(rpc.Seq) != 42 {
		t.Errorf("Seq mismatch: got %d, want 42", rpc.Seq)
	}
	if uint(rpc.Version) != uint(ubtemit.EnvelopeVersionV1) {
		t.Errorf("Version mismatch: got %d, want %d", rpc.Version, ubtemit.EnvelopeVersionV1)
	}
	if rpc.Kind != ubtemit.KindDiff {
		t.Errorf("Kind mismatch: got %s, want %s", rpc.Kind, ubtemit.KindDiff)
	}
	if uint64(rpc.BlockNumber) != 12345 {
		t.Errorf("BlockNumber mismatch: got %d, want 12345", rpc.BlockNumber)
	}
	if rpc.BlockHash != blockHash {
		t.Errorf("BlockHash mismatch: got %s, want %s", rpc.BlockHash.Hex(), blockHash.Hex())
	}
	if rpc.ParentHash != parentHash {
		t.Errorf("ParentHash mismatch: got %s, want %s", rpc.ParentHash.Hex(), parentHash.Hex())
	}
	if uint64(rpc.Timestamp) != 1234567890 {
		t.Errorf("Timestamp mismatch: got %d, want 1234567890", rpc.Timestamp)
	}
	if len(rpc.Payload) != len(payload) {
		t.Errorf("Payload length mismatch: got %d, want %d", len(rpc.Payload), len(payload))
	}
	for i, b := range payload {
		if rpc.Payload[i] != b {
			t.Errorf("Payload[%d] mismatch: got %d, want %d", i, rpc.Payload[i], b)
		}
	}
}

// TestToRPCEvent_ZeroValues tests conversion with zero/empty fields.
func TestToRPCEvent_ZeroValues(t *testing.T) {
	env := &ubtemit.OutboxEnvelope{
		Seq:         0,
		Version:     0,
		Kind:        "",
		BlockNumber: 0,
		BlockHash:   common.Hash{},
		ParentHash:  common.Hash{},
		Timestamp:   0,
		Payload:     nil,
	}

	rpc := toRPCEvent(env)

	// Verify zero values are correctly converted
	if uint64(rpc.Seq) != 0 {
		t.Errorf("Seq should be 0, got %d", rpc.Seq)
	}
	if uint(rpc.Version) != 0 {
		t.Errorf("Version should be 0, got %d", rpc.Version)
	}
	if rpc.Kind != "" {
		t.Errorf("Kind should be empty, got %s", rpc.Kind)
	}
	if uint64(rpc.BlockNumber) != 0 {
		t.Errorf("BlockNumber should be 0, got %d", rpc.BlockNumber)
	}
	if rpc.BlockHash != (common.Hash{}) {
		t.Errorf("BlockHash should be empty, got %s", rpc.BlockHash.Hex())
	}
	if rpc.ParentHash != (common.Hash{}) {
		t.Errorf("ParentHash should be empty, got %s", rpc.ParentHash.Hex())
	}
	if uint64(rpc.Timestamp) != 0 {
		t.Errorf("Timestamp should be 0, got %d", rpc.Timestamp)
	}
	if rpc.Payload != nil {
		t.Errorf("Payload should be nil, got %v", rpc.Payload)
	}
}

// TestToRPCEvent_ReorgKind tests conversion with reorg event kind.
func TestToRPCEvent_ReorgKind(t *testing.T) {
	env := &ubtemit.OutboxEnvelope{
		Seq:         1,
		Version:     ubtemit.EnvelopeVersionV1,
		Kind:        ubtemit.KindReorg,
		BlockNumber: 100,
		Payload:     []byte("reorg-payload"),
	}

	rpc := toRPCEvent(env)

	if rpc.Kind != ubtemit.KindReorg {
		t.Errorf("Kind mismatch: got %s, want %s", rpc.Kind, ubtemit.KindReorg)
	}
}

// TestGetEvent_NotFound tests GetEvent with non-existent sequence number.
func TestGetEvent_NotFound(t *testing.T) {
	store, dir := newTestOutboxStore(t)
	defer os.RemoveAll(dir)
	defer store.Close()

	eth := &Ethereum{outboxStore: store}
	api := NewUBTOutboxAPI(eth)

	// Query non-existent event should return nil (not error)
	result, err := api.GetEvent(context.Background(), hexutil.Uint64(999))
	if err != nil {
		t.Errorf("GetEvent should not return error for not found, got: %v", err)
	}
	if result != nil {
		t.Errorf("GetEvent should return nil for not found, got: %v", result)
	}
}

// TestGetEvent_Success tests GetEvent with valid sequence number.
func TestGetEvent_Success(t *testing.T) {
	store, dir := newTestOutboxStore(t)
	defer os.RemoveAll(dir)
	defer store.Close()

	// Append an event
	env := &ubtemit.OutboxEnvelope{
		Version:     ubtemit.EnvelopeVersionV1,
		Kind:        ubtemit.KindDiff,
		BlockNumber: 42,
		BlockHash:   common.HexToHash("0xabcd"),
		Payload:     []byte{1, 2, 3},
	}
	seq, err := store.Append(env)
	if err != nil {
		t.Fatal(err)
	}

	eth := &Ethereum{outboxStore: store}
	api := NewUBTOutboxAPI(eth)

	// Query the event
	result, err := api.GetEvent(context.Background(), hexutil.Uint64(seq))
	if err != nil {
		t.Fatalf("GetEvent failed: %v", err)
	}
	if result == nil {
		t.Fatal("GetEvent returned nil")
	}
	if uint64(result.BlockNumber) != 42 {
		t.Errorf("BlockNumber mismatch: got %d, want 42", result.BlockNumber)
	}
	if result.Kind != ubtemit.KindDiff {
		t.Errorf("Kind mismatch: got %s, want %s", result.Kind, ubtemit.KindDiff)
	}
}

// TestGetEvent_NoOutbox tests GetEvent when outbox is not enabled.
func TestGetEvent_NoOutbox(t *testing.T) {
	eth := &Ethereum{outboxStore: nil}
	api := NewUBTOutboxAPI(eth)

	_, err := api.GetEvent(context.Background(), hexutil.Uint64(0))
	if err == nil {
		t.Error("GetEvent should return error when outbox not enabled")
	}
	if err.Error() != "UBT outbox not enabled" {
		t.Errorf("Wrong error message: got %v, want 'UBT outbox not enabled'", err)
	}
}

// TestGetEvents_RangeValidation tests that fromSeq > toSeq returns error.
func TestGetEvents_RangeValidation(t *testing.T) {
	store, dir := newTestOutboxStore(t)
	defer os.RemoveAll(dir)
	defer store.Close()

	eth := &Ethereum{outboxStore: store}
	api := NewUBTOutboxAPI(eth)

	// fromSeq > toSeq should error
	_, err := api.GetEvents(context.Background(), hexutil.Uint64(10), hexutil.Uint64(5))
	if err == nil {
		t.Error("GetEvents should return error when fromSeq > toSeq")
	}
	if err.Error() != "fromSeq must be <= toSeq" {
		t.Errorf("Wrong error message: got %v", err)
	}
}

// TestGetEvents_MaxRangeCap tests that ranges > 1000 get capped.
func TestGetEvents_MaxRangeCap(t *testing.T) {
	store, dir := newTestOutboxStore(t)
	defer os.RemoveAll(dir)
	defer store.Close()

	// Append 1500 events
	for i := uint64(0); i < 1500; i++ {
		env := &ubtemit.OutboxEnvelope{
			Version:     ubtemit.EnvelopeVersionV1,
			Kind:        ubtemit.KindDiff,
			BlockNumber: i,
			Payload:     []byte{byte(i % 256)},
		}
		if _, err := store.Append(env); err != nil {
			t.Fatal(err)
		}
	}

	eth := &Ethereum{outboxStore: store}
	api := NewUBTOutboxAPI(eth)

	// Request range > 1000 (0 to 1499 = 1500 events)
	result, err := api.GetEvents(context.Background(), hexutil.Uint64(0), hexutil.Uint64(1499))
	if err != nil {
		t.Fatalf("GetEvents failed: %v", err)
	}

	// Should be capped to 1000
	if len(result) != 1000 {
		t.Errorf("Result length mismatch: got %d, want 1000", len(result))
	}

	// Verify sequence: should be 0..999
	if uint64(result[0].Seq) != 0 {
		t.Errorf("First seq mismatch: got %d, want 0", result[0].Seq)
	}
	if uint64(result[999].Seq) != 999 {
		t.Errorf("Last seq mismatch: got %d, want 999", result[999].Seq)
	}
}

// TestGetEvents_ExactlyMaxRange tests requesting exactly 1000 events.
func TestGetEvents_ExactlyMaxRange(t *testing.T) {
	store, dir := newTestOutboxStore(t)
	defer os.RemoveAll(dir)
	defer store.Close()

	// Append exactly 1000 events
	for i := uint64(0); i < 1000; i++ {
		env := &ubtemit.OutboxEnvelope{
			Version:     ubtemit.EnvelopeVersionV1,
			Kind:        ubtemit.KindDiff,
			BlockNumber: i,
			Payload:     []byte{byte(i % 256)},
		}
		if _, err := store.Append(env); err != nil {
			t.Fatal(err)
		}
	}

	eth := &Ethereum{outboxStore: store}
	api := NewUBTOutboxAPI(eth)

	// Request exactly 1000 events
	result, err := api.GetEvents(context.Background(), hexutil.Uint64(0), hexutil.Uint64(999))
	if err != nil {
		t.Fatalf("GetEvents failed: %v", err)
	}

	if len(result) != 1000 {
		t.Errorf("Result length mismatch: got %d, want 1000", len(result))
	}
}

// TestGetEvents_SmallRange tests requesting a small range of events.
func TestGetEvents_SmallRange(t *testing.T) {
	store, dir := newTestOutboxStore(t)
	defer os.RemoveAll(dir)
	defer store.Close()

	// Append 10 events
	for i := uint64(0); i < 10; i++ {
		env := &ubtemit.OutboxEnvelope{
			Version:     ubtemit.EnvelopeVersionV1,
			Kind:        ubtemit.KindDiff,
			BlockNumber: i * 100,
			Payload:     []byte{byte(i)},
		}
		if _, err := store.Append(env); err != nil {
			t.Fatal(err)
		}
	}

	eth := &Ethereum{outboxStore: store}
	api := NewUBTOutboxAPI(eth)

	// Request events 3..7
	result, err := api.GetEvents(context.Background(), hexutil.Uint64(3), hexutil.Uint64(7))
	if err != nil {
		t.Fatalf("GetEvents failed: %v", err)
	}

	if len(result) != 5 {
		t.Errorf("Result length mismatch: got %d, want 5", len(result))
	}

	// Verify sequence and block numbers
	for i, evt := range result {
		expectedSeq := uint64(3 + i)
		if uint64(evt.Seq) != expectedSeq {
			t.Errorf("Seq[%d] mismatch: got %d, want %d", i, evt.Seq, expectedSeq)
		}
		expectedBlock := expectedSeq * 100
		if uint64(evt.BlockNumber) != expectedBlock {
			t.Errorf("BlockNumber[%d] mismatch: got %d, want %d", i, evt.BlockNumber, expectedBlock)
		}
	}
}

// TestGetEvents_NoOutbox tests GetEvents when outbox is not enabled.
func TestGetEvents_NoOutbox(t *testing.T) {
	eth := &Ethereum{outboxStore: nil}
	api := NewUBTOutboxAPI(eth)

	_, err := api.GetEvents(context.Background(), hexutil.Uint64(0), hexutil.Uint64(10))
	if err == nil {
		t.Error("GetEvents should return error when outbox not enabled")
	}
	if err.Error() != "UBT outbox not enabled" {
		t.Errorf("Wrong error message: got %v", err)
	}
}

// TestGetEvents_SingleEvent tests requesting a single event.
func TestGetEvents_SingleEvent(t *testing.T) {
	store, dir := newTestOutboxStore(t)
	defer os.RemoveAll(dir)
	defer store.Close()

	// Append 5 events
	for i := uint64(0); i < 5; i++ {
		env := &ubtemit.OutboxEnvelope{
			Version:     ubtemit.EnvelopeVersionV1,
			Kind:        ubtemit.KindDiff,
			BlockNumber: i,
			Payload:     []byte{byte(i)},
		}
		if _, err := store.Append(env); err != nil {
			t.Fatal(err)
		}
	}

	eth := &Ethereum{outboxStore: store}
	api := NewUBTOutboxAPI(eth)

	// Request single event (fromSeq == toSeq)
	result, err := api.GetEvents(context.Background(), hexutil.Uint64(2), hexutil.Uint64(2))
	if err != nil {
		t.Fatalf("GetEvents failed: %v", err)
	}

	if len(result) != 1 {
		t.Errorf("Result length mismatch: got %d, want 1", len(result))
	}
	if uint64(result[0].Seq) != 2 {
		t.Errorf("Seq mismatch: got %d, want 2", result[0].Seq)
	}
	if uint64(result[0].BlockNumber) != 2 {
		t.Errorf("BlockNumber mismatch: got %d, want 2", result[0].BlockNumber)
	}
}

// TestLatestSeq_EmptyOutbox tests LatestSeq with empty outbox.
func TestLatestSeq_EmptyOutbox(t *testing.T) {
	store, dir := newTestOutboxStore(t)
	defer os.RemoveAll(dir)
	defer store.Close()

	eth := &Ethereum{outboxStore: store}
	api := NewUBTOutboxAPI(eth)

	seq, err := api.LatestSeq(context.Background())
	if err != nil {
		t.Fatalf("LatestSeq failed: %v", err)
	}

	// Empty outbox should return 0
	if uint64(seq) != 0 {
		t.Errorf("Empty outbox LatestSeq should be 0, got %d", seq)
	}
}

// TestLatestSeq_WithEvents tests LatestSeq with events.
func TestLatestSeq_WithEvents(t *testing.T) {
	store, dir := newTestOutboxStore(t)
	defer os.RemoveAll(dir)
	defer store.Close()

	// Append 3 events (seq 0, 1, 2)
	for i := uint64(0); i < 3; i++ {
		env := &ubtemit.OutboxEnvelope{
			Version:     ubtemit.EnvelopeVersionV1,
			Kind:        ubtemit.KindDiff,
			BlockNumber: i,
			Payload:     []byte{byte(i)},
		}
		if _, err := store.Append(env); err != nil {
			t.Fatal(err)
		}
	}

	eth := &Ethereum{outboxStore: store}
	api := NewUBTOutboxAPI(eth)

	seq, err := api.LatestSeq(context.Background())
	if err != nil {
		t.Fatalf("LatestSeq failed: %v", err)
	}

	// Should return 2 (last seq)
	if uint64(seq) != 2 {
		t.Errorf("LatestSeq mismatch: got %d, want 2", seq)
	}
}

// TestLatestSeq_NoOutbox tests LatestSeq when outbox is not enabled.
func TestLatestSeq_NoOutbox(t *testing.T) {
	eth := &Ethereum{outboxStore: nil}
	api := NewUBTOutboxAPI(eth)

	_, err := api.LatestSeq(context.Background())
	if err == nil {
		t.Error("LatestSeq should return error when outbox not enabled")
	}
	if err.Error() != "UBT outbox not enabled" {
		t.Errorf("Wrong error message: got %v", err)
	}
}

// TestGetEvents_MixedEventTypes tests retrieving both diff and reorg events.
func TestGetEvents_MixedEventTypes(t *testing.T) {
	store, dir := newTestOutboxStore(t)
	defer os.RemoveAll(dir)
	defer store.Close()

	// Append diff event
	diffPayload, _ := ubtemit.EncodeDiff(&ubtemit.QueuedDiffV1{
		OriginRoot: common.HexToHash("0x01"),
		Root:       common.HexToHash("0x02"),
		Accounts: []ubtemit.AccountEntry{
			{
				Address: common.HexToAddress("0x123"),
				Nonce:   1,
				Balance: big.NewInt(1000),
				Alive:   true,
			},
		},
	})
	store.Append(&ubtemit.OutboxEnvelope{
		Version:     ubtemit.EnvelopeVersionV1,
		Kind:        ubtemit.KindDiff,
		BlockNumber: 100,
		Payload:     diffPayload,
	})

	// Append reorg event
	reorgPayload, _ := ubtemit.EncodeReorgMarker(&ubtemit.ReorgMarkerV1{
		FromBlockNumber: 10,
		ToBlockNumber:   8,
	})
	store.Append(&ubtemit.OutboxEnvelope{
		Version:     ubtemit.EnvelopeVersionV1,
		Kind:        ubtemit.KindReorg,
		BlockNumber: 101,
		Payload:     reorgPayload,
	})

	eth := &Ethereum{outboxStore: store}
	api := NewUBTOutboxAPI(eth)

	result, err := api.GetEvents(context.Background(), hexutil.Uint64(0), hexutil.Uint64(1))
	if err != nil {
		t.Fatalf("GetEvents failed: %v", err)
	}

	if len(result) != 2 {
		t.Errorf("Result length mismatch: got %d, want 2", len(result))
	}

	if result[0].Kind != ubtemit.KindDiff {
		t.Errorf("First event kind mismatch: got %s, want %s", result[0].Kind, ubtemit.KindDiff)
	}
	if result[1].Kind != ubtemit.KindReorg {
		t.Errorf("Second event kind mismatch: got %s, want %s", result[1].Kind, ubtemit.KindReorg)
	}
}

// TestRPCEvent_HexutilEncoding tests that hexutil types encode correctly.
func TestRPCEvent_HexutilEncoding(t *testing.T) {
	env := &ubtemit.OutboxEnvelope{
		Seq:         0xFFFF,
		Version:     1,
		Kind:        ubtemit.KindDiff,
		BlockNumber: 0x123456,
		BlockHash:   common.HexToHash("0xabcdef"),
		ParentHash:  common.HexToHash("0x123456"),
		Timestamp:   0xDEADBEEF,
		Payload:     []byte{0xCA, 0xFE, 0xBA, 0xBE},
	}

	rpc := toRPCEvent(env)

	// Verify hexutil types maintain values correctly
	if uint64(rpc.Seq) != 0xFFFF {
		t.Errorf("Seq encoding issue: got %d, want %d", rpc.Seq, 0xFFFF)
	}
	if uint(rpc.Version) != 1 {
		t.Errorf("Version encoding issue: got %d, want 1", rpc.Version)
	}
	if uint64(rpc.BlockNumber) != 0x123456 {
		t.Errorf("BlockNumber encoding issue: got %d, want %d", rpc.BlockNumber, 0x123456)
	}
	if uint64(rpc.Timestamp) != 0xDEADBEEF {
		t.Errorf("Timestamp encoding issue: got %d, want %d", rpc.Timestamp, 0xDEADBEEF)
	}
}

// TestStatus_NoOutbox tests Status when outbox is not enabled.
func TestStatus_NoOutbox(t *testing.T) {
	eth := &Ethereum{outboxStore: nil}
	api := NewUBTOutboxAPI(eth)

	_, err := api.Status(context.Background())
	if err == nil {
		t.Error("Status should return error when outbox not enabled")
	}
	if err.Error() != "UBT outbox not enabled" {
		t.Errorf("Wrong error message: got %v, want 'UBT outbox not enabled'", err)
	}
}

// TestStatus_Enabled tests Status with outbox enabled and no emitter service.
func TestStatus_Enabled(t *testing.T) {
	store, dir := newTestOutboxStore(t)
	defer os.RemoveAll(dir)
	defer store.Close()

	// Append some events
	for i := uint64(0); i < 5; i++ {
		env := &ubtemit.OutboxEnvelope{
			Version:     ubtemit.EnvelopeVersionV1,
			Kind:        ubtemit.KindDiff,
			BlockNumber: i,
			Payload:     []byte{byte(i)},
		}
		if _, err := store.Append(env); err != nil {
			t.Fatal(err)
		}
	}

	eth := &Ethereum{outboxStore: store}
	api := NewUBTOutboxAPI(eth)

	result, err := api.Status(context.Background())
	if err != nil {
		t.Fatalf("Status failed: %v", err)
	}

	// Check enabled status
	enabled, ok := result["enabled"].(bool)
	if !ok || !enabled {
		t.Errorf("Expected enabled=true, got %v", result["enabled"])
	}

	// Check latest seq
	latestSeq, ok := result["latestSeq"].(hexutil.Uint64)
	if !ok {
		t.Errorf("latestSeq not found or wrong type: %v", result["latestSeq"])
	}
	if uint64(latestSeq) != 4 {
		t.Errorf("latestSeq mismatch: got %d, want 4", latestSeq)
	}

	// degraded should not be present when no emitter service
	if _, exists := result["degraded"]; exists {
		t.Errorf("degraded should not be present without emitter service")
	}
}

// TestStatus_WithEmitterService tests Status with emitter service.
func TestStatus_WithEmitterService(t *testing.T) {
	store, dir := newTestOutboxStore(t)
	defer os.RemoveAll(dir)
	defer store.Close()

	// Create emitter service
	service := ubtemit.NewService(store)

	// Append some events
	for i := uint64(0); i < 3; i++ {
		env := &ubtemit.OutboxEnvelope{
			Version:     ubtemit.EnvelopeVersionV1,
			Kind:        ubtemit.KindDiff,
			BlockNumber: i,
			Payload:     []byte{byte(i)},
		}
		if _, err := store.Append(env); err != nil {
			t.Fatal(err)
		}
	}

	eth := &Ethereum{
		outboxStore:    store,
		emitterService: service,
	}
	api := NewUBTOutboxAPI(eth)

	result, err := api.Status(context.Background())
	if err != nil {
		t.Fatalf("Status failed: %v", err)
	}

	// Check enabled status
	enabled, ok := result["enabled"].(bool)
	if !ok || !enabled {
		t.Errorf("Expected enabled=true, got %v", result["enabled"])
	}

	// Check latest seq
	latestSeq, ok := result["latestSeq"].(hexutil.Uint64)
	if !ok {
		t.Errorf("latestSeq not found or wrong type: %v", result["latestSeq"])
	}
	if uint64(latestSeq) != 2 {
		t.Errorf("latestSeq mismatch: got %d, want 2", latestSeq)
	}

	// Check degraded status
	degraded, ok := result["degraded"].(bool)
	if !ok {
		t.Errorf("degraded not found or wrong type: %v", result["degraded"])
	}
	// Should be false initially
	if degraded {
		t.Errorf("degraded should be false initially, got true")
	}
}

func TestCompactOutboxBelow_AllowsLatestPlusOne(t *testing.T) {
	store, dir := newTestOutboxStore(t)
	defer os.RemoveAll(dir)
	defer store.Close()

	for i := 0; i < 3; i++ {
		if _, err := store.Append(&ubtemit.OutboxEnvelope{
			Version: ubtemit.EnvelopeVersionV1,
			Kind:    ubtemit.KindDiff,
			Payload: []byte{byte(i)},
		}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	api := NewUBTOutboxAPI(&Ethereum{outboxStore: store})
	// latest=2, safeSeq=3 should be accepted.
	result, err := api.CompactOutboxBelow(context.Background(), hexutil.Uint64(3))
	if err != nil {
		t.Fatalf("CompactOutboxBelow latest+1 should succeed: %v", err)
	}
	if deleted, ok := result["deleted"].(int); !ok || deleted != 3 {
		t.Fatalf("expected deleted=3, got %v (%T)", result["deleted"], result["deleted"])
	}
}

func TestCompactOutboxBelow_RejectsBeyondLatestPlusOne(t *testing.T) {
	store, dir := newTestOutboxStore(t)
	defer os.RemoveAll(dir)
	defer store.Close()

	for i := 0; i < 2; i++ {
		if _, err := store.Append(&ubtemit.OutboxEnvelope{
			Version: ubtemit.EnvelopeVersionV1,
			Kind:    ubtemit.KindDiff,
			Payload: []byte{byte(i)},
		}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	api := NewUBTOutboxAPI(&Ethereum{outboxStore: store})
	// latest=1, safeSeq=4 should fail.
	if _, err := api.CompactOutboxBelow(context.Background(), hexutil.Uint64(4)); err == nil {
		t.Fatal("expected CompactOutboxBelow error beyond latest+1")
	}
}
