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

package ubtemit

import (
	"bytes"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/rlp"
)

// TestEncodeDiff_NilBalance verifies handling of nil balance (should encode as zero).
func TestEncodeDiff_NilBalance(t *testing.T) {
	diff := &QueuedDiffV1{
		OriginRoot: common.HexToHash("0x1234"),
		Root:       common.HexToHash("0x5678"),
		Accounts: []AccountEntry{
			{
				Address:  common.HexToAddress("0xaaa"),
				Nonce:    1,
				Balance:  nil, // Nil balance
				CodeHash: common.HexToHash("0xcode1"),
				Alive:    true,
			},
		},
	}

	encoded, err := EncodeDiff(diff)
	if err != nil {
		t.Fatalf("EncodeDiff failed: %v", err)
	}

	decoded, err := DecodeDiff(encoded)
	if err != nil {
		t.Fatalf("DecodeDiff failed: %v", err)
	}

	// Nil balance should decode as nil (RLP will handle this)
	// The important thing is it doesn't crash
	if decoded.Accounts[0].Address != diff.Accounts[0].Address {
		t.Error("Address mismatch after encoding nil balance")
	}
}

// TestEncodeDiff_ZeroBalance verifies explicit zero balance encoding.
func TestEncodeDiff_ZeroBalance(t *testing.T) {
	diff := &QueuedDiffV1{
		OriginRoot: common.HexToHash("0x1234"),
		Root:       common.HexToHash("0x5678"),
		Accounts: []AccountEntry{
			{
				Address:  common.HexToAddress("0xaaa"),
				Nonce:    0,
				Balance:  big.NewInt(0),
				CodeHash: common.Hash{},
				Alive:    false,
			},
		},
	}

	encoded, err := EncodeDiff(diff)
	if err != nil {
		t.Fatalf("EncodeDiff failed: %v", err)
	}

	decoded, err := DecodeDiff(encoded)
	if err != nil {
		t.Fatalf("DecodeDiff failed: %v", err)
	}

	if decoded.Accounts[0].Balance.Sign() != 0 {
		t.Errorf("Zero balance should remain zero, got %v", decoded.Accounts[0].Balance)
	}
	if decoded.Accounts[0].Alive {
		t.Error("Dead account Alive flag should be false")
	}
}

// TestEncodeDiff_LargeCode verifies handling of very large contract code.
func TestEncodeDiff_LargeCode(t *testing.T) {
	// Create a large bytecode (24KB, close to EIP-170 limit of 24576 bytes)
	largeCode := make([]byte, 24*1024)
	for i := range largeCode {
		largeCode[i] = byte(i % 256)
	}
	codeHash := common.BytesToHash([]byte("large_code_hash"))

	diff := &QueuedDiffV1{
		OriginRoot: common.HexToHash("0x1234"),
		Root:       common.HexToHash("0x5678"),
		Codes: []CodeEntry{
			{
				Address:  common.HexToAddress("0xaaa"),
				CodeHash: codeHash,
				Code:     largeCode,
			},
		},
	}

	encoded, err := EncodeDiff(diff)
	if err != nil {
		t.Fatalf("EncodeDiff failed: %v", err)
	}

	decoded, err := DecodeDiff(encoded)
	if err != nil {
		t.Fatalf("DecodeDiff failed: %v", err)
	}

	if !bytes.Equal(decoded.Codes[0].Code, largeCode) {
		t.Error("Large code not preserved correctly")
	}
}

// TestEncodeDiff_EmptyCode verifies encoding of empty/nil code.
func TestEncodeDiff_EmptyCode(t *testing.T) {
	diff := &QueuedDiffV1{
		OriginRoot: common.HexToHash("0x1234"),
		Root:       common.HexToHash("0x5678"),
		Codes: []CodeEntry{
			{
				Address:  common.HexToAddress("0xaaa"),
				CodeHash: common.Hash{},
				Code:     nil, // Empty code
			},
			{
				Address:  common.HexToAddress("0xbbb"),
				CodeHash: common.Hash{},
				Code:     []byte{}, // Empty slice
			},
		},
	}

	encoded, err := EncodeDiff(diff)
	if err != nil {
		t.Fatalf("EncodeDiff failed: %v", err)
	}

	decoded, err := DecodeDiff(encoded)
	if err != nil {
		t.Fatalf("DecodeDiff failed: %v", err)
	}

	if len(decoded.Codes) != 2 {
		t.Fatalf("Expected 2 code entries, got %d", len(decoded.Codes))
	}

	// Both should decode to empty slices (not nil)
	if decoded.Codes[0].Code == nil || len(decoded.Codes[0].Code) != 0 {
		t.Errorf("First empty code should be non-nil empty slice, got %v", decoded.Codes[0].Code)
	}
	if decoded.Codes[1].Code == nil || len(decoded.Codes[1].Code) != 0 {
		t.Errorf("Second empty code should be non-nil empty slice, got %v", decoded.Codes[1].Code)
	}
}

// TestEncodeDiff_MaxUint64Values verifies handling of maximum uint64 values.
func TestEncodeDiff_MaxUint64Values(t *testing.T) {
	diff := &QueuedDiffV1{
		OriginRoot: common.HexToHash("0x1234"),
		Root:       common.HexToHash("0x5678"),
		Accounts: []AccountEntry{
			{
				Address:  common.HexToAddress("0xaaa"),
				Nonce:    ^uint64(0), // Max uint64
				Balance:  big.NewInt(1000),
				CodeHash: common.HexToHash("0xcode1"),
				Alive:    true,
			},
		},
	}

	encoded, err := EncodeDiff(diff)
	if err != nil {
		t.Fatalf("EncodeDiff failed: %v", err)
	}

	decoded, err := DecodeDiff(encoded)
	if err != nil {
		t.Fatalf("DecodeDiff failed: %v", err)
	}

	if decoded.Accounts[0].Nonce != ^uint64(0) {
		t.Errorf("Max uint64 nonce not preserved: got %d, want %d", decoded.Accounts[0].Nonce, ^uint64(0))
	}
}

// TestEncodeDiff_ZeroHashes verifies handling of zero hashes.
func TestEncodeDiff_ZeroHashes(t *testing.T) {
	diff := &QueuedDiffV1{
		OriginRoot: common.Hash{}, // Zero hash
		Root:       common.Hash{}, // Zero hash
		Accounts: []AccountEntry{
			{
				Address:  common.Address{}, // Zero address
				Nonce:    0,
				Balance:  big.NewInt(0),
				CodeHash: common.Hash{}, // Zero hash
				Alive:    false,
			},
		},
		Storage: []StorageEntry{
			{
				Address:    common.Address{}, // Zero address
				SlotKeyRaw: common.Hash{},    // Zero hash
				Value:      common.Hash{},    // Zero hash
			},
		},
	}

	encoded, err := EncodeDiff(diff)
	if err != nil {
		t.Fatalf("EncodeDiff failed: %v", err)
	}

	decoded, err := DecodeDiff(encoded)
	if err != nil {
		t.Fatalf("DecodeDiff failed: %v", err)
	}

	if decoded.OriginRoot != (common.Hash{}) {
		t.Error("Zero origin root not preserved")
	}
	if decoded.Root != (common.Hash{}) {
		t.Error("Zero root not preserved")
	}
	if decoded.Accounts[0].Address != (common.Address{}) {
		t.Error("Zero address not preserved")
	}
	if decoded.Storage[0].SlotKeyRaw != (common.Hash{}) {
		t.Error("Zero slot key not preserved")
	}
}

// TestEncodeDiff_ManyAccounts verifies handling of many accounts.
func TestEncodeDiff_ManyAccounts(t *testing.T) {
	const numAccounts = 1000

	diff := &QueuedDiffV1{
		OriginRoot: common.HexToHash("0x1234"),
		Root:       common.HexToHash("0x5678"),
		Accounts:   make([]AccountEntry, numAccounts),
	}

	// Create many accounts
	for i := 0; i < numAccounts; i++ {
		addr := common.Address{}
		addr[19] = byte(i) // Set last byte
		addr[18] = byte(i >> 8)

		diff.Accounts[i] = AccountEntry{
			Address:  addr,
			Nonce:    uint64(i),
			Balance:  big.NewInt(int64(i * 100)),
			CodeHash: common.BytesToHash([]byte{byte(i)}),
			Alive:    i%2 == 0,
		}
	}

	encoded, err := EncodeDiff(diff)
	if err != nil {
		t.Fatalf("EncodeDiff failed: %v", err)
	}

	decoded, err := DecodeDiff(encoded)
	if err != nil {
		t.Fatalf("DecodeDiff failed: %v", err)
	}

	if len(decoded.Accounts) != numAccounts {
		t.Fatalf("Account count mismatch: got %d, want %d", len(decoded.Accounts), numAccounts)
	}

	// Verify a few random accounts
	checkIndices := []int{0, numAccounts / 2, numAccounts - 1}
	for _, i := range checkIndices {
		if decoded.Accounts[i].Nonce != uint64(i) {
			t.Errorf("Account[%d] nonce mismatch", i)
		}
		if decoded.Accounts[i].Balance.Cmp(big.NewInt(int64(i*100))) != 0 {
			t.Errorf("Account[%d] balance mismatch", i)
		}
	}
}

// TestEncodeDiff_ManyStorageSlots verifies handling of many storage slots.
func TestEncodeDiff_ManyStorageSlots(t *testing.T) {
	const numSlots = 1000

	diff := &QueuedDiffV1{
		OriginRoot: common.HexToHash("0x1234"),
		Root:       common.HexToHash("0x5678"),
		Storage:    make([]StorageEntry, numSlots),
	}

	addr := common.HexToAddress("0xaaaa")
	for i := 0; i < numSlots; i++ {
		key := common.Hash{}
		key[31] = byte(i)
		key[30] = byte(i >> 8)

		value := common.Hash{}
		value[31] = byte(i * 2)

		diff.Storage[i] = StorageEntry{
			Address:    addr,
			SlotKeyRaw: key,
			Value:      value,
		}
	}

	encoded, err := EncodeDiff(diff)
	if err != nil {
		t.Fatalf("EncodeDiff failed: %v", err)
	}

	decoded, err := DecodeDiff(encoded)
	if err != nil {
		t.Fatalf("DecodeDiff failed: %v", err)
	}

	if len(decoded.Storage) != numSlots {
		t.Fatalf("Storage count mismatch: got %d, want %d", len(decoded.Storage), numSlots)
	}

	// Verify a few random slots
	checkIndices := []int{0, numSlots / 2, numSlots - 1}
	for _, i := range checkIndices {
		if decoded.Storage[i].Value[31] != byte(i*2) {
			t.Errorf("Storage[%d] value mismatch", i)
		}
	}
}

// TestDecodeEnvelope_Corrupted verifies proper error handling for corrupted data.
func TestDecodeEnvelope_Corrupted(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{
			name: "empty data",
			data: []byte{},
		},
		{
			name: "random garbage",
			data: []byte{0xff, 0xff, 0xff, 0xff},
		},
		{
			name: "partial RLP",
			data: []byte{0xc0}, // Empty list
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DecodeEnvelope(tt.data)
			if err == nil {
				t.Errorf("DecodeEnvelope should fail for %s", tt.name)
			}
		})
	}
}

// TestDecodeDiff_Corrupted verifies proper error handling for corrupted diff data.
func TestDecodeDiff_Corrupted(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{
			name: "empty data",
			data: []byte{},
		},
		{
			name: "random garbage",
			data: []byte{0xff, 0xff, 0xff, 0xff},
		},
		{
			name: "incomplete account entry",
			data: []byte{0xc0}, // Empty list
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DecodeDiff(tt.data)
			if err == nil {
				t.Errorf("DecodeDiff should fail for %s", tt.name)
			}
		})
	}
}

// TestDecodeReorgMarker_Corrupted verifies proper error handling for corrupted marker data.
func TestDecodeReorgMarker_Corrupted(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{
			name: "empty data",
			data: []byte{},
		},
		{
			name: "random garbage",
			data: []byte{0xff, 0xff, 0xff, 0xff},
		},
		{
			name: "partial structure",
			data: []byte{0xc0}, // Empty list
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DecodeReorgMarker(tt.data)
			if err == nil {
				t.Errorf("DecodeReorgMarker should fail for %s", tt.name)
			}
		})
	}
}

// TestEncodeEnvelope_AllFields verifies all envelope fields are properly encoded.
func TestEncodeEnvelope_AllFields(t *testing.T) {
	env := &OutboxEnvelope{
		Seq:         ^uint64(0), // Max uint64
		Version:     EnvelopeVersionV1,
		Kind:        KindDiff,
		BlockNumber: ^uint64(0), // Max uint64
		BlockHash:   common.HexToHash("0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"),
		ParentHash:  common.HexToHash("0xeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"),
		Timestamp:   ^uint64(0), // Max uint64
		Payload:     []byte{0x01, 0x02, 0x03},
	}

	encoded, err := EncodeEnvelope(env)
	if err != nil {
		t.Fatalf("EncodeEnvelope failed: %v", err)
	}

	decoded, err := DecodeEnvelope(encoded)
	if err != nil {
		t.Fatalf("DecodeEnvelope failed: %v", err)
	}

	// Verify all fields
	if decoded.Seq != env.Seq {
		t.Errorf("Seq mismatch: got %d, want %d", decoded.Seq, env.Seq)
	}
	if decoded.Version != env.Version {
		t.Errorf("Version mismatch: got %d, want %d", decoded.Version, env.Version)
	}
	if decoded.Kind != env.Kind {
		t.Errorf("Kind mismatch: got %s, want %s", decoded.Kind, env.Kind)
	}
	if decoded.BlockNumber != env.BlockNumber {
		t.Errorf("BlockNumber mismatch: got %d, want %d", decoded.BlockNumber, env.BlockNumber)
	}
	if decoded.BlockHash != env.BlockHash {
		t.Errorf("BlockHash mismatch: got %v, want %v", decoded.BlockHash, env.BlockHash)
	}
	if decoded.ParentHash != env.ParentHash {
		t.Errorf("ParentHash mismatch: got %v, want %v", decoded.ParentHash, env.ParentHash)
	}
	if decoded.Timestamp != env.Timestamp {
		t.Errorf("Timestamp mismatch: got %d, want %d", decoded.Timestamp, env.Timestamp)
	}
	if !bytes.Equal(decoded.Payload, env.Payload) {
		t.Errorf("Payload mismatch")
	}
}

// TestNegativeBalance verifies that encoding negative balances fails.
func TestNegativeBalance(t *testing.T) {
	// Negative balances shouldn't occur in practice,
	// and RLP cannot encode them - verify we get an error
	diff := &QueuedDiffV1{
		OriginRoot: common.HexToHash("0x1234"),
		Root:       common.HexToHash("0x5678"),
		Accounts: []AccountEntry{
			{
				Address:  common.HexToAddress("0xaaa"),
				Nonce:    1,
				Balance:  big.NewInt(-1000), // Negative balance
				CodeHash: common.HexToHash("0xcode1"),
				Alive:    true,
			},
		},
	}

	_, err := EncodeDiff(diff)
	if err == nil {
		t.Fatal("EncodeDiff should fail for negative balance")
	}
	// Verify it's the expected RLP error
	if err.Error() != "rlp: cannot encode negative big.Int" {
		t.Errorf("Unexpected error message: %v", err)
	}
}

// TestEncodeDiff_NilSlices verifies handling of nil slices (should encode as empty).
func TestEncodeDiff_NilSlices(t *testing.T) {
	diff := &QueuedDiffV1{
		OriginRoot: common.HexToHash("0x1234"),
		Root:       common.HexToHash("0x5678"),
		Accounts:   nil, // Nil slice
		Storage:    nil, // Nil slice
		Codes:      nil, // Nil slice
	}

	encoded, err := EncodeDiff(diff)
	if err != nil {
		t.Fatalf("EncodeDiff failed: %v", err)
	}

	decoded, err := DecodeDiff(encoded)
	if err != nil {
		t.Fatalf("DecodeDiff failed: %v", err)
	}

	// Nil slices should decode as non-nil empty slices
	if decoded.Accounts == nil {
		t.Error("Accounts should be non-nil empty slice")
	}
	if len(decoded.Accounts) != 0 {
		t.Error("Accounts should be empty")
	}
	if decoded.Storage == nil {
		t.Error("Storage should be non-nil empty slice")
	}
	if len(decoded.Storage) != 0 {
		t.Error("Storage should be empty")
	}
	if decoded.Codes == nil {
		t.Error("Codes should be non-nil empty slice")
	}
	if len(decoded.Codes) != 0 {
		t.Error("Codes should be empty")
	}
}

// TestRLPStreamEquivalence verifies that our encoding matches direct RLP stream encoding.
func TestRLPStreamEquivalence(t *testing.T) {
	diff := &QueuedDiffV1{
		OriginRoot: common.HexToHash("0x1234"),
		Root:       common.HexToHash("0x5678"),
		Accounts: []AccountEntry{
			{
				Address:  common.HexToAddress("0xaaa"),
				Nonce:    1,
				Balance:  big.NewInt(1000),
				CodeHash: common.HexToHash("0xcode1"),
				Alive:    true,
			},
		},
	}

	// Encode using our function
	encoded1, err := EncodeDiff(diff)
	if err != nil {
		t.Fatalf("EncodeDiff failed: %v", err)
	}

	// Create the RLP structure manually and encode
	rlpDiff := rlpQueuedDiffV1{
		OriginRoot: diff.OriginRoot,
		Root:       diff.Root,
		Accounts:   make([]rlpAccountEntry, 1),
		Storage:    make([]rlpStorageEntry, 0),
		Codes:      make([]rlpCodeEntry, 0),
	}
	rlpDiff.Accounts[0] = rlpAccountEntry{
		Address:  diff.Accounts[0].Address,
		Nonce:    diff.Accounts[0].Nonce,
		Balance:  diff.Accounts[0].Balance,
		CodeHash: diff.Accounts[0].CodeHash,
		Alive:    diff.Accounts[0].Alive,
	}

	encoded2, err := rlp.EncodeToBytes(&rlpDiff)
	if err != nil {
		t.Fatalf("Direct RLP encoding failed: %v", err)
	}

	if !bytes.Equal(encoded1, encoded2) {
		t.Error("EncodeDiff output differs from direct RLP encoding")
	}
}

// TestDecodeEnvelope_EmptyPayload verifies handling of empty payloads.
func TestDecodeEnvelope_EmptyPayload(t *testing.T) {
	env := &OutboxEnvelope{
		Seq:         1,
		Version:     EnvelopeVersionV1,
		Kind:        KindDiff,
		BlockNumber: 100,
		BlockHash:   common.HexToHash("0xblock"),
		ParentHash:  common.HexToHash("0xparent"),
		Timestamp:   1234567890,
		Payload:     []byte{}, // Empty payload
	}

	encoded, err := EncodeEnvelope(env)
	if err != nil {
		t.Fatalf("EncodeEnvelope failed: %v", err)
	}

	decoded, err := DecodeEnvelope(encoded)
	if err != nil {
		t.Fatalf("DecodeEnvelope failed: %v", err)
	}

	if len(decoded.Payload) != 0 {
		t.Errorf("Empty payload should remain empty, got length %d", len(decoded.Payload))
	}
}

// TestReorgMarker_ZeroAncestor verifies reorg marker with zero ancestor.
func TestReorgMarker_ZeroAncestor(t *testing.T) {
	marker := &ReorgMarkerV1{
		FromBlockNumber:      1,
		FromBlockHash:        common.HexToHash("0xfrom"),
		ToBlockNumber:        2,
		ToBlockHash:          common.HexToHash("0xto"),
		CommonAncestorNumber: 0, // Genesis block
		CommonAncestorHash:   common.Hash{},
	}

	encoded, err := EncodeReorgMarker(marker)
	if err != nil {
		t.Fatalf("EncodeReorgMarker failed: %v", err)
	}

	decoded, err := DecodeReorgMarker(encoded)
	if err != nil {
		t.Fatalf("DecodeReorgMarker failed: %v", err)
	}

	if decoded.CommonAncestorNumber != 0 {
		t.Errorf("CommonAncestorNumber should be 0, got %d", decoded.CommonAncestorNumber)
	}
	if decoded.CommonAncestorHash != (common.Hash{}) {
		t.Errorf("CommonAncestorHash should be zero, got %v", decoded.CommonAncestorHash)
	}
}
