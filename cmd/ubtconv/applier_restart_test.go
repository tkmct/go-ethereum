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
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

// TestApplier_RestartReopenCommittedRoot ensures the triedb mode used by the
// applier can reopen and read a committed binary-trie root after restart.
func TestApplier_RestartReopenCommittedRoot(t *testing.T) {
	cfg := &Config{
		DataDir:            t.TempDir(),
		TrieDBScheme:       "path",
		TrieDBStateHistory: 128,
	}

	addr := common.HexToAddress("0x7400000000000000000000000000000000000001")
	diff := makeDiff(addr, 1, big.NewInt(42))

	a1, err := NewApplier(cfg, common.Hash{})
	if err != nil {
		t.Fatalf("NewApplier first open: %v", err)
	}
	if !a1.TrieDB().IsVerkle() {
		t.Fatal("applier triedb must be opened in binary/verkle-compatible mode")
	}
	if _, err := a1.ApplyDiff(diff, 1); err != nil {
		t.Fatalf("ApplyDiff: %v", err)
	}
	if err := a1.CommitAt(1); err != nil {
		t.Fatalf("CommitAt: %v", err)
	}
	root := a1.Root()
	a1.Close()

	a2, err := NewApplier(cfg, root)
	if err != nil {
		t.Fatalf("NewApplier reopen with expected root: %v", err)
	}
	defer a2.Close()

	acct, err := a2.Trie().GetAccount(addr)
	if err != nil {
		t.Fatalf("GetAccount after reopen: %v", err)
	}
	if acct == nil {
		t.Fatal("expected account to be present after reopen")
	}
	if got := acct.Balance.ToBig(); got.Cmp(big.NewInt(42)) != 0 {
		t.Fatalf("balance mismatch after reopen: got %s want 42", got)
	}
}
