// Copyright 2024 The go-ethereum Authors
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
	"os"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// setupTestApplier creates a temporary applier for testing proof methods.
func setupTestApplier(t *testing.T) (*Applier, func()) {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "applier-proof-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	cfg := &Config{
		DataDir:            tmpDir,
		TrieDBStateHistory: 10,
		TrieDBScheme:       "path",
	}

	applier, err := NewApplier(cfg, common.Hash{})
	if err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("failed to create applier: %v", err)
	}

	cleanup := func() {
		applier.Close()
		os.RemoveAll(tmpDir)
	}

	return applier, cleanup
}

// TestRoot_Accessor verifies the Root() accessor works.
func TestRoot_Accessor(t *testing.T) {
	applier, cleanup := setupTestApplier(t)
	defer cleanup()

	// Root() should return the initial root
	root := applier.Root()
	if root != types.EmptyBinaryHash {
		t.Errorf("Root() = %s, want EmptyBinaryHash %s", root, types.EmptyBinaryHash)
	}
}

// TestValidateProofRequest_ValidKey verifies validation passes for valid 32-byte keys.
func TestValidateProofRequest_ValidKey(t *testing.T) {
	applier, cleanup := setupTestApplier(t)
	defer cleanup()

	// Manually set a non-zero root to pass validation
	applier.root = common.HexToHash("0x1234567890123456789012345678901234567890123456789012345678901234")

	validKey := common.HexToHash("0xabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcd")
	err := applier.ValidateProofRequest(validKey.Bytes())
	if err != nil {
		t.Errorf("ValidateProofRequest with valid key failed: %v", err)
	}
}

// TestValidateProofRequest_InvalidKeyLengths verifies validation fails for invalid key lengths.
func TestValidateProofRequest_InvalidKeyLengths(t *testing.T) {
	applier, cleanup := setupTestApplier(t)
	defer cleanup()

	// Set a non-zero root
	applier.root = common.HexToHash("0x1234567890123456789012345678901234567890123456789012345678901234")

	tests := []struct {
		name    string
		keyLen  int
		wantErr bool
	}{
		{"empty key", 0, true},
		{"key too short", 16, true},
		{"key too long", 64, true},
		{"key slightly short", 31, true},
		{"key slightly long", 33, true},
		{"valid key", 32, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := make([]byte, tt.keyLen)
			err := applier.ValidateProofRequest(key)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateProofRequest() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && err != nil {
				expectedMsg := "invalid key length"
				if len(err.Error()) < len(expectedMsg) || err.Error()[:len(expectedMsg)] != expectedMsg {
					t.Errorf("expected error to mention '%s', got: %v", expectedMsg, err)
				}
			}
		})
	}
}

// TestValidateProofRequest_EmptyRoot verifies validation fails with empty root.
func TestValidateProofRequest_EmptyRoot(t *testing.T) {
	applier, cleanup := setupTestApplier(t)
	defer cleanup()

	// Keep root as zero/EmptyBinaryHash
	validKey := common.HexToHash("0x1234567890123456789012345678901234567890123456789012345678901234")
	err := applier.ValidateProofRequest(validKey.Bytes())
	if err == nil {
		t.Error("ValidateProofRequest should fail with empty root")
	}

	expectedMsg := "UBT trie has no committed root"
	if err.Error() != expectedMsg {
		t.Errorf("expected error '%s', got: %v", expectedMsg, err)
	}
}

// TestGenerateProof_EmptyRoot verifies proof generation fails with empty root.
func TestGenerateProof_EmptyRoot(t *testing.T) {
	applier, cleanup := setupTestApplier(t)
	defer cleanup()

	// Root is empty by default
	validKey := common.HexToHash("0x1234567890123456789012345678901234567890123456789012345678901234")
	_, err := applier.GenerateProof(validKey.Bytes())
	if err == nil {
		t.Error("GenerateProof should fail with empty root")
	}

	// Should fail at validation
	expectedMsg := "UBT trie has no committed root"
	if err.Error() != expectedMsg {
		t.Errorf("expected error '%s', got: %v", expectedMsg, err)
	}
}

// TestGenerateProof_InvalidKey verifies proof generation fails for invalid keys.
func TestGenerateProof_InvalidKey(t *testing.T) {
	applier, cleanup := setupTestApplier(t)
	defer cleanup()

	// Set a non-zero root
	applier.root = common.HexToHash("0x1234567890123456789012345678901234567890123456789012345678901234")

	// Try invalid key lengths
	tests := []struct {
		name   string
		keyLen int
	}{
		{"empty key", 0},
		{"short key", 16},
		{"long key", 64},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			invalidKey := make([]byte, tt.keyLen)
			_, err := applier.GenerateProof(invalidKey)
			if err == nil {
				t.Error("GenerateProof should fail with invalid key length")
			}
			expectedMsg := "invalid key length"
			if len(err.Error()) < len(expectedMsg) || err.Error()[:len(expectedMsg)] != expectedMsg {
				t.Errorf("expected error to mention '%s', got: %v", expectedMsg, err)
			}
		})
	}
}

// TestValidateProofRequest_BoundaryConditions tests exact boundary conditions.
func TestValidateProofRequest_BoundaryConditions(t *testing.T) {
	applier, cleanup := setupTestApplier(t)
	defer cleanup()

	// Set a non-zero root
	applier.root = common.HexToHash("0xabcd1234abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234")

	tests := []struct {
		name    string
		keyLen  int
		wantErr bool
	}{
		{"31 bytes", 31, true},
		{"32 bytes", 32, false},
		{"33 bytes", 33, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := make([]byte, tt.keyLen)
			for i := range key {
				key[i] = byte(i) // Fill with non-zero values
			}
			err := applier.ValidateProofRequest(key)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateProofRequest(%d bytes) error = %v, wantErr %v", tt.keyLen, err, tt.wantErr)
			}
		})
	}
}

// TestRoot_InitialValue verifies initial root value.
func TestRoot_InitialValue(t *testing.T) {
	applier, cleanup := setupTestApplier(t)
	defer cleanup()

	root := applier.Root()

	// EmptyBinaryHash should be zero
	if types.EmptyBinaryHash != (common.Hash{}) {
		t.Errorf("EmptyBinaryHash should be zero hash, got %s", types.EmptyBinaryHash)
	}

	if root != (common.Hash{}) {
		t.Errorf("Initial root should be zero hash, got %s", root)
	}
}
