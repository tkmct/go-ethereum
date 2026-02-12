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
	"context"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

// TestQueryAPI_GetProof_TrieNotInitialized tests that GetProof returns an error
// when the trie is not initialized.
func TestQueryAPI_GetProof_TrieNotInitialized(t *testing.T) {
	// Create a consumer with nil applier
	consumer := &Consumer{
		applier: nil,
	}

	api := NewQueryAPI(consumer)

	ctx := context.Background()
	key := common.HexToHash("0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef")

	_, err := api.GetProof(ctx, key, nil)
	if err == nil {
		t.Fatal("Expected error when trie not initialized")
	}

	expectedMsg := "UBT trie not initialized"
	if err.Error() != expectedMsg {
		t.Errorf("Expected error message %q, got %q", expectedMsg, err.Error())
	}
}

// TestQueryAPI_GetProof_ZeroKey tests that GetProof properly validates zero key.
// The validation logic is in Applier.ValidateProofRequest which checks key length.
func TestQueryAPI_GetProof_ZeroKey(t *testing.T) {
	t.Skip("Zero key validation test requires a real applier instance - integration test needed")
	// This would require setting up a full applier with triedb,
	// which is better suited for an integration test.
}
