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
	"github.com/ethereum/go-ethereum/core/types"
)

func TestQueuedDiffV1_RoundTrip(t *testing.T) {
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
			{
				Address:  common.HexToAddress("0xbbb"),
				Nonce:    2,
				Balance:  big.NewInt(2000),
				CodeHash: common.HexToHash("0xcode2"),
				Alive:    false,
			},
		},
		Storage: []StorageEntry{
			{
				Address:    common.HexToAddress("0xaaa"),
				SlotKeyRaw: common.HexToHash("0xslot1"),
				Value:      common.HexToHash("0xval1"),
			},
			{
				Address:    common.HexToAddress("0xbbb"),
				SlotKeyRaw: common.HexToHash("0xslot2"),
				Value:      common.HexToHash("0xval2"),
			},
		},
		Codes: []CodeEntry{
			{
				Address:  common.HexToAddress("0xaaa"),
				CodeHash: common.HexToHash("0xcode1"),
				Code:     []byte{0x60, 0x60, 0x60},
			},
			{
				Address:  common.HexToAddress("0xbbb"),
				CodeHash: common.HexToHash("0xcode2"),
				Code:     []byte{0x61, 0x61, 0x61},
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

	// Verify all fields
	if decoded.OriginRoot != diff.OriginRoot {
		t.Errorf("OriginRoot mismatch: got %v, want %v", decoded.OriginRoot, diff.OriginRoot)
	}
	if decoded.Root != diff.Root {
		t.Errorf("Root mismatch: got %v, want %v", decoded.Root, diff.Root)
	}
	if len(decoded.Accounts) != len(diff.Accounts) {
		t.Fatalf("Accounts length mismatch: got %d, want %d", len(decoded.Accounts), len(diff.Accounts))
	}
	for i := range diff.Accounts {
		if decoded.Accounts[i].Address != diff.Accounts[i].Address {
			t.Errorf("Account[%d].Address mismatch", i)
		}
		if decoded.Accounts[i].Nonce != diff.Accounts[i].Nonce {
			t.Errorf("Account[%d].Nonce mismatch", i)
		}
		if decoded.Accounts[i].Balance.Cmp(diff.Accounts[i].Balance) != 0 {
			t.Errorf("Account[%d].Balance mismatch", i)
		}
		if decoded.Accounts[i].CodeHash != diff.Accounts[i].CodeHash {
			t.Errorf("Account[%d].CodeHash mismatch", i)
		}
		if decoded.Accounts[i].Alive != diff.Accounts[i].Alive {
			t.Errorf("Account[%d].Alive mismatch", i)
		}
	}
	if len(decoded.Storage) != len(diff.Storage) {
		t.Fatalf("Storage length mismatch: got %d, want %d", len(decoded.Storage), len(diff.Storage))
	}
	for i := range diff.Storage {
		if decoded.Storage[i].Address != diff.Storage[i].Address {
			t.Errorf("Storage[%d].Address mismatch", i)
		}
		if decoded.Storage[i].SlotKeyRaw != diff.Storage[i].SlotKeyRaw {
			t.Errorf("Storage[%d].SlotKeyRaw mismatch", i)
		}
		if decoded.Storage[i].Value != diff.Storage[i].Value {
			t.Errorf("Storage[%d].Value mismatch", i)
		}
	}
	if len(decoded.Codes) != len(diff.Codes) {
		t.Fatalf("Codes length mismatch: got %d, want %d", len(decoded.Codes), len(diff.Codes))
	}
	for i := range diff.Codes {
		if decoded.Codes[i].Address != diff.Codes[i].Address {
			t.Errorf("Code[%d].Address mismatch", i)
		}
		if decoded.Codes[i].CodeHash != diff.Codes[i].CodeHash {
			t.Errorf("Code[%d].CodeHash mismatch", i)
		}
		if !bytes.Equal(decoded.Codes[i].Code, diff.Codes[i].Code) {
			t.Errorf("Code[%d].Code mismatch", i)
		}
	}
}

func TestReorgMarkerV1_RoundTrip(t *testing.T) {
	marker := &ReorgMarkerV1{
		FromBlockNumber:      100,
		FromBlockHash:        common.HexToHash("0xfrom"),
		ToBlockNumber:        102,
		ToBlockHash:          common.HexToHash("0xto"),
		CommonAncestorNumber: 99,
		CommonAncestorHash:   common.HexToHash("0xancestor"),
	}

	encoded, err := EncodeReorgMarker(marker)
	if err != nil {
		t.Fatalf("EncodeReorgMarker failed: %v", err)
	}

	decoded, err := DecodeReorgMarker(encoded)
	if err != nil {
		t.Fatalf("DecodeReorgMarker failed: %v", err)
	}

	if decoded.FromBlockNumber != marker.FromBlockNumber {
		t.Errorf("FromBlockNumber mismatch: got %d, want %d", decoded.FromBlockNumber, marker.FromBlockNumber)
	}
	if decoded.FromBlockHash != marker.FromBlockHash {
		t.Errorf("FromBlockHash mismatch")
	}
	if decoded.ToBlockNumber != marker.ToBlockNumber {
		t.Errorf("ToBlockNumber mismatch: got %d, want %d", decoded.ToBlockNumber, marker.ToBlockNumber)
	}
	if decoded.ToBlockHash != marker.ToBlockHash {
		t.Errorf("ToBlockHash mismatch")
	}
	if decoded.CommonAncestorNumber != marker.CommonAncestorNumber {
		t.Errorf("CommonAncestorNumber mismatch: got %d, want %d", decoded.CommonAncestorNumber, marker.CommonAncestorNumber)
	}
	if decoded.CommonAncestorHash != marker.CommonAncestorHash {
		t.Errorf("CommonAncestorHash mismatch")
	}
}

func TestOutboxEnvelope_RoundTrip_Diff(t *testing.T) {
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

	payload, err := EncodeDiff(diff)
	if err != nil {
		t.Fatalf("EncodeDiff failed: %v", err)
	}

	env := &OutboxEnvelope{
		Seq:         42,
		Version:     EnvelopeVersionV1,
		Kind:        KindDiff,
		BlockNumber: 1000,
		BlockHash:   common.HexToHash("0xblock"),
		ParentHash:  common.HexToHash("0xparent"),
		Timestamp:   1234567890,
		Payload:     payload,
	}

	encoded, err := EncodeEnvelope(env)
	if err != nil {
		t.Fatalf("EncodeEnvelope failed: %v", err)
	}

	decoded, err := DecodeEnvelope(encoded)
	if err != nil {
		t.Fatalf("DecodeEnvelope failed: %v", err)
	}

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
		t.Errorf("BlockHash mismatch")
	}
	if decoded.ParentHash != env.ParentHash {
		t.Errorf("ParentHash mismatch")
	}
	if decoded.Timestamp != env.Timestamp {
		t.Errorf("Timestamp mismatch: got %d, want %d", decoded.Timestamp, env.Timestamp)
	}
	if !bytes.Equal(decoded.Payload, env.Payload) {
		t.Errorf("Payload mismatch")
	}

	// Decode payload back to diff
	decodedDiff, err := DecodeDiff(decoded.Payload)
	if err != nil {
		t.Fatalf("DecodeDiff of payload failed: %v", err)
	}
	if decodedDiff.OriginRoot != diff.OriginRoot {
		t.Errorf("Decoded diff OriginRoot mismatch")
	}
}

func TestOutboxEnvelope_RoundTrip_Reorg(t *testing.T) {
	marker := &ReorgMarkerV1{
		FromBlockNumber:      100,
		FromBlockHash:        common.HexToHash("0xfrom"),
		ToBlockNumber:        102,
		ToBlockHash:          common.HexToHash("0xto"),
		CommonAncestorNumber: 99,
		CommonAncestorHash:   common.HexToHash("0xancestor"),
	}

	payload, err := EncodeReorgMarker(marker)
	if err != nil {
		t.Fatalf("EncodeReorgMarker failed: %v", err)
	}

	env := &OutboxEnvelope{
		Seq:         43,
		Version:     EnvelopeVersionV1,
		Kind:        KindReorg,
		BlockNumber: 102,
		BlockHash:   common.HexToHash("0xto"),
		ParentHash:  common.HexToHash("0xparent"),
		Timestamp:   1234567890,
		Payload:     payload,
	}

	encoded, err := EncodeEnvelope(env)
	if err != nil {
		t.Fatalf("EncodeEnvelope failed: %v", err)
	}

	decoded, err := DecodeEnvelope(encoded)
	if err != nil {
		t.Fatalf("DecodeEnvelope failed: %v", err)
	}

	if decoded.Kind != KindReorg {
		t.Errorf("Kind mismatch: got %s, want %s", decoded.Kind, KindReorg)
	}

	// Decode payload back to marker
	decodedMarker, err := DecodeReorgMarker(decoded.Payload)
	if err != nil {
		t.Fatalf("DecodeReorgMarker of payload failed: %v", err)
	}
	if decodedMarker.FromBlockNumber != marker.FromBlockNumber {
		t.Errorf("Decoded marker FromBlockNumber mismatch")
	}
}

func TestDeterministicEncoding(t *testing.T) {
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

	encoded1, err := EncodeDiff(diff)
	if err != nil {
		t.Fatalf("EncodeDiff first call failed: %v", err)
	}

	encoded2, err := EncodeDiff(diff)
	if err != nil {
		t.Fatalf("EncodeDiff second call failed: %v", err)
	}

	if !bytes.Equal(encoded1, encoded2) {
		t.Errorf("Deterministic encoding failed: two encodings of same diff differ")
	}
}

func TestVersionRejection(t *testing.T) {
	env := &OutboxEnvelope{
		Seq:         42,
		Version:     99, // Invalid version
		Kind:        KindDiff,
		BlockNumber: 1000,
		BlockHash:   common.HexToHash("0xblock"),
		ParentHash:  common.HexToHash("0xparent"),
		Timestamp:   1234567890,
		Payload:     []byte{},
	}

	encoded, err := EncodeEnvelope(env)
	if err != nil {
		t.Fatalf("EncodeEnvelope failed: %v", err)
	}

	_, err = DecodeEnvelope(encoded)
	if err == nil {
		t.Errorf("DecodeEnvelope should reject version 99, but succeeded")
	}
}

func TestInvalidKindRejection(t *testing.T) {
	env := &OutboxEnvelope{
		Seq:         42,
		Version:     EnvelopeVersionV1,
		Kind:        "invalid_kind",
		BlockNumber: 1000,
		BlockHash:   common.HexToHash("0xblock"),
		ParentHash:  common.HexToHash("0xparent"),
		Timestamp:   1234567890,
		Payload:     []byte{},
	}

	encoded, err := EncodeEnvelope(env)
	if err != nil {
		t.Fatalf("EncodeEnvelope failed: %v", err)
	}

	_, err = DecodeEnvelope(encoded)
	if err == nil {
		t.Errorf("DecodeEnvelope should reject invalid kind, but succeeded")
	}
}

func TestEmptyDiff_RoundTrip(t *testing.T) {
	diff := &QueuedDiffV1{
		OriginRoot: common.HexToHash("0x1234"),
		Root:       common.HexToHash("0x5678"),
		Accounts:   []AccountEntry{},
		Storage:    []StorageEntry{},
		Codes:      []CodeEntry{},
	}

	encoded, err := EncodeDiff(diff)
	if err != nil {
		t.Fatalf("EncodeDiff failed: %v", err)
	}

	decoded, err := DecodeDiff(encoded)
	if err != nil {
		t.Fatalf("DecodeDiff failed: %v", err)
	}

	if decoded.OriginRoot != diff.OriginRoot {
		t.Errorf("OriginRoot mismatch")
	}
	if decoded.Root != diff.Root {
		t.Errorf("Root mismatch")
	}
	if len(decoded.Accounts) != 0 {
		t.Errorf("Accounts should be empty")
	}
	if len(decoded.Storage) != 0 {
		t.Errorf("Storage should be empty")
	}
	if len(decoded.Codes) != 0 {
		t.Errorf("Codes should be empty")
	}
}

func TestLargeBalance_RoundTrip(t *testing.T) {
	// Create a very large balance
	largeBalance := new(big.Int)
	largeBalance.SetString("123456789012345678901234567890123456789012345678901234567890", 10)

	diff := &QueuedDiffV1{
		OriginRoot: common.HexToHash("0x1234"),
		Root:       common.HexToHash("0x5678"),
		Accounts: []AccountEntry{
			{
				Address:  common.HexToAddress("0xaaa"),
				Nonce:    1,
				Balance:  largeBalance,
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

	if decoded.Accounts[0].Balance.Cmp(largeBalance) != 0 {
		t.Errorf("Large balance mismatch: got %s, want %s", decoded.Accounts[0].Balance.String(), largeBalance.String())
	}
}

func TestSortedSliceInvariant(t *testing.T) {
	// Create diff with pre-sorted accounts
	diff := &QueuedDiffV1{
		OriginRoot: common.HexToHash("0x1234"),
		Root:       common.HexToHash("0x5678"),
		Accounts: []AccountEntry{
			{Address: common.HexToAddress("0x1111"), Nonce: 1, Balance: big.NewInt(100), Alive: true},
			{Address: common.HexToAddress("0x2222"), Nonce: 2, Balance: big.NewInt(200), Alive: true},
			{Address: common.HexToAddress("0x3333"), Nonce: 3, Balance: big.NewInt(300), Alive: true},
		},
		Storage: []StorageEntry{
			{Address: common.HexToAddress("0x1111"), SlotKeyRaw: common.HexToHash("0x01"), Value: common.HexToHash("0xa1")},
			{Address: common.HexToAddress("0x1111"), SlotKeyRaw: common.HexToHash("0x02"), Value: common.HexToHash("0xa2")},
			{Address: common.HexToAddress("0x2222"), SlotKeyRaw: common.HexToHash("0x01"), Value: common.HexToHash("0xb1")},
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

	// Verify order is preserved
	for i := range diff.Accounts {
		if decoded.Accounts[i].Address != diff.Accounts[i].Address {
			t.Errorf("Account order not preserved at index %d", i)
		}
	}
	for i := range diff.Storage {
		if decoded.Storage[i].Address != diff.Storage[i].Address || decoded.Storage[i].SlotKeyRaw != diff.Storage[i].SlotKeyRaw {
			t.Errorf("Storage order not preserved at index %d", i)
		}
	}
}

func TestAccountEntry_IsZeroAccount(t *testing.T) {
	tests := []struct {
		name     string
		account  AccountEntry
		wantZero bool
	}{
		{
			name: "alive account with balance",
			account: AccountEntry{
				Address:  common.HexToAddress("0xaaa"),
				Nonce:    1,
				Balance:  big.NewInt(1000),
				CodeHash: common.HexToHash("0xcode1"),
				Alive:    true,
			},
			wantZero: false,
		},
		{
			name: "deleted account marked not alive",
			account: AccountEntry{
				Address:  common.HexToAddress("0xbbb"),
				Nonce:    0,
				Balance:  big.NewInt(0),
				CodeHash: common.Hash{},
				Alive:    false,
			},
			wantZero: true,
		},
		{
			name: "deleted account with EmptyCodeHash",
			account: AccountEntry{
				Address:  common.HexToAddress("0xccc"),
				Nonce:    0,
				Balance:  big.NewInt(0),
				CodeHash: types.EmptyCodeHash,
				Alive:    false,
			},
			wantZero: true,
		},
		{
			name: "zero balance alive account with empty code",
			account: AccountEntry{
				Address:  common.HexToAddress("0xddd"),
				Nonce:    0,
				Balance:  new(big.Int),
				CodeHash: types.EmptyCodeHash,
				Alive:    true,
			},
			wantZero: true,
		},
		{
			name: "alive account with nonce but no balance",
			account: AccountEntry{
				Address:  common.HexToAddress("0xeee"),
				Nonce:    5,
				Balance:  big.NewInt(0),
				CodeHash: types.EmptyCodeHash,
				Alive:    true,
			},
			wantZero: false,
		},
		{
			name: "nil balance",
			account: AccountEntry{
				Address:  common.HexToAddress("0xfff"),
				Nonce:    0,
				Balance:  nil,
				CodeHash: types.EmptyCodeHash,
				Alive:    false,
			},
			wantZero: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.account.IsZeroAccount()
			if got != tt.wantZero {
				t.Errorf("IsZeroAccount() = %v, want %v", got, tt.wantZero)
			}
		})
	}
}

func TestNewDeletedAccountEntry(t *testing.T) {
	addr := common.HexToAddress("0xdeadbeef")
	entry := NewDeletedAccountEntry(addr)

	if entry.Address != addr {
		t.Errorf("Address mismatch: got %v, want %v", entry.Address, addr)
	}
	if entry.Nonce != 0 {
		t.Errorf("Nonce should be 0, got %d", entry.Nonce)
	}
	if entry.Balance == nil || entry.Balance.Sign() != 0 {
		t.Errorf("Balance should be zero, got %v", entry.Balance)
	}
	if entry.CodeHash != types.EmptyCodeHash {
		t.Errorf("CodeHash should be EmptyCodeHash, got %v", entry.CodeHash)
	}
	if entry.Alive {
		t.Errorf("Alive should be false")
	}
	if !entry.IsZeroAccount() {
		t.Errorf("NewDeletedAccountEntry should create a zero account")
	}
}

func TestQueuedDiffV1_DeletedAccountCount(t *testing.T) {
	tests := []struct {
		name          string
		diff          *QueuedDiffV1
		wantDeleted   int
	}{
		{
			name: "no accounts",
			diff: &QueuedDiffV1{
				Accounts: []AccountEntry{},
			},
			wantDeleted: 0,
		},
		{
			name: "all alive accounts",
			diff: &QueuedDiffV1{
				Accounts: []AccountEntry{
					{Address: common.HexToAddress("0xa1"), Alive: true},
					{Address: common.HexToAddress("0xa2"), Alive: true},
					{Address: common.HexToAddress("0xa3"), Alive: true},
				},
			},
			wantDeleted: 0,
		},
		{
			name: "all deleted accounts",
			diff: &QueuedDiffV1{
				Accounts: []AccountEntry{
					{Address: common.HexToAddress("0xb1"), Alive: false},
					{Address: common.HexToAddress("0xb2"), Alive: false},
				},
			},
			wantDeleted: 2,
		},
		{
			name: "mixed accounts",
			diff: &QueuedDiffV1{
				Accounts: []AccountEntry{
					{Address: common.HexToAddress("0xc1"), Alive: true},
					{Address: common.HexToAddress("0xc2"), Alive: false},
					{Address: common.HexToAddress("0xc3"), Alive: true},
					{Address: common.HexToAddress("0xc4"), Alive: false},
					{Address: common.HexToAddress("0xc5"), Alive: false},
				},
			},
			wantDeleted: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.diff.DeletedAccountCount()
			if got != tt.wantDeleted {
				t.Errorf("DeletedAccountCount() = %d, want %d", got, tt.wantDeleted)
			}
		})
	}
}

func TestQueuedDiffV1_ActiveAccountCount(t *testing.T) {
	tests := []struct {
		name       string
		diff       *QueuedDiffV1
		wantActive int
	}{
		{
			name: "no accounts",
			diff: &QueuedDiffV1{
				Accounts: []AccountEntry{},
			},
			wantActive: 0,
		},
		{
			name: "all alive accounts",
			diff: &QueuedDiffV1{
				Accounts: []AccountEntry{
					{Address: common.HexToAddress("0xa1"), Alive: true},
					{Address: common.HexToAddress("0xa2"), Alive: true},
					{Address: common.HexToAddress("0xa3"), Alive: true},
				},
			},
			wantActive: 3,
		},
		{
			name: "all deleted accounts",
			diff: &QueuedDiffV1{
				Accounts: []AccountEntry{
					{Address: common.HexToAddress("0xb1"), Alive: false},
					{Address: common.HexToAddress("0xb2"), Alive: false},
				},
			},
			wantActive: 0,
		},
		{
			name: "mixed accounts",
			diff: &QueuedDiffV1{
				Accounts: []AccountEntry{
					{Address: common.HexToAddress("0xc1"), Alive: true},
					{Address: common.HexToAddress("0xc2"), Alive: false},
					{Address: common.HexToAddress("0xc3"), Alive: true},
					{Address: common.HexToAddress("0xc4"), Alive: false},
					{Address: common.HexToAddress("0xc5"), Alive: false},
				},
			},
			wantActive: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.diff.ActiveAccountCount()
			if got != tt.wantActive {
				t.Errorf("ActiveAccountCount() = %d, want %d", got, tt.wantActive)
			}
		})
	}
}

func TestAccountEntry_IsZeroAccount_EdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		account  AccountEntry
		wantZero bool
	}{
		{
			name: "alive with positive balance but zero nonce",
			account: AccountEntry{
				Address:  common.HexToAddress("0x111"),
				Nonce:    0,
				Balance:  big.NewInt(100),
				CodeHash: types.EmptyCodeHash,
				Alive:    true,
			},
			wantZero: false,
		},
		{
			name: "alive with code but zero balance",
			account: AccountEntry{
				Address:  common.HexToAddress("0x222"),
				Nonce:    0,
				Balance:  big.NewInt(0),
				CodeHash: common.HexToHash("0xdeadbeef"),
				Alive:    true,
			},
			wantZero: false,
		},
		{
			name: "not alive overrides everything",
			account: AccountEntry{
				Address:  common.HexToAddress("0x333"),
				Nonce:    999,
				Balance:  big.NewInt(999999),
				CodeHash: common.HexToHash("0xdeadbeef"),
				Alive:    false,
			},
			wantZero: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.account.IsZeroAccount()
			if got != tt.wantZero {
				t.Errorf("IsZeroAccount() = %v, want %v", got, tt.wantZero)
			}
		})
	}
}
