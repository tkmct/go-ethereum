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
	"bytes"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/ubtemit"
)

func TestCoalesceAccountEntries_LastWriteWins(t *testing.T) {
	a := common.HexToAddress("0x1000")
	b := common.HexToAddress("0x2000")
	c := common.HexToAddress("0x3000")
	entries := []ubtemit.AccountEntry{
		{Address: a, Nonce: 1, Balance: big.NewInt(1), CodeHash: types.EmptyCodeHash, Alive: true},
		{Address: b, Nonce: 2, Balance: big.NewInt(2), CodeHash: types.EmptyCodeHash, Alive: true},
		{Address: a, Nonce: 3, Balance: big.NewInt(3), CodeHash: types.EmptyCodeHash, Alive: true},
		{Address: c, Nonce: 4, Balance: big.NewInt(4), CodeHash: types.EmptyCodeHash, Alive: true},
		{Address: b, Nonce: 5, Balance: big.NewInt(5), CodeHash: types.EmptyCodeHash, Alive: false},
	}
	got := coalesceAccountEntries(entries)
	if len(got) != 3 {
		t.Fatalf("unexpected coalesced len: %d", len(got))
	}
	if got[0].Address != a || got[0].Nonce != 3 {
		t.Fatalf("account[0] mismatch: addr=%s nonce=%d", got[0].Address, got[0].Nonce)
	}
	if got[1].Address != c || got[1].Nonce != 4 {
		t.Fatalf("account[1] mismatch: addr=%s nonce=%d", got[1].Address, got[1].Nonce)
	}
	if got[2].Address != b || got[2].Nonce != 5 || got[2].Alive {
		t.Fatalf("account[2] mismatch: addr=%s nonce=%d alive=%v", got[2].Address, got[2].Nonce, got[2].Alive)
	}
}

func TestCoalesceStorageEntries_LastWriteWins(t *testing.T) {
	a := common.HexToAddress("0x1000")
	b := common.HexToAddress("0x2000")
	slot0 := common.HexToHash("0x1")
	slot1 := common.HexToHash("0x2")
	entries := []ubtemit.StorageEntry{
		{Address: a, SlotKeyRaw: slot0, Value: common.HexToHash("0x10")},
		{Address: a, SlotKeyRaw: slot1, Value: common.HexToHash("0x20")},
		{Address: b, SlotKeyRaw: slot0, Value: common.HexToHash("0x30")},
		{Address: a, SlotKeyRaw: slot0, Value: common.HexToHash("0x40")},
	}
	got := coalesceStorageEntries(entries)
	if len(got) != 3 {
		t.Fatalf("unexpected coalesced len: %d", len(got))
	}
	if got[0].Address != a || got[0].SlotKeyRaw != slot1 || got[0].Value != common.HexToHash("0x20") {
		t.Fatalf("storage[0] mismatch: %+v", got[0])
	}
	if got[1].Address != b || got[1].SlotKeyRaw != slot0 || got[1].Value != common.HexToHash("0x30") {
		t.Fatalf("storage[1] mismatch: %+v", got[1])
	}
	if got[2].Address != a || got[2].SlotKeyRaw != slot0 || got[2].Value != common.HexToHash("0x40") {
		t.Fatalf("storage[2] mismatch: %+v", got[2])
	}
}

func TestCoalesceCodeEntries_LastWriteWins(t *testing.T) {
	a := common.HexToAddress("0x1000")
	b := common.HexToAddress("0x2000")
	entries := []ubtemit.CodeEntry{
		{Address: a, CodeHash: common.HexToHash("0x11"), Code: []byte{0x01}},
		{Address: b, CodeHash: common.HexToHash("0x22"), Code: []byte{0x02}},
		{Address: a, CodeHash: common.HexToHash("0x33"), Code: []byte{0x03, 0x03}},
	}
	got := coalesceCodeEntries(entries)
	if len(got) != 2 {
		t.Fatalf("unexpected coalesced len: %d", len(got))
	}
	if got[0].Address != b || got[0].CodeHash != common.HexToHash("0x22") {
		t.Fatalf("code[0] mismatch: %+v", got[0])
	}
	if got[1].Address != a || got[1].CodeHash != common.HexToHash("0x33") {
		t.Fatalf("code[1] mismatch: %+v", got[1])
	}
}

func TestPreprocessDiffForApply_BuildsCodeByAddressFromCoalescedCodes(t *testing.T) {
	a := common.HexToAddress("0x1000")
	diff := &ubtemit.QueuedDiffV1{
		Codes: []ubtemit.CodeEntry{
			{Address: a, CodeHash: common.HexToHash("0x11"), Code: []byte{0x01}},
			{Address: a, CodeHash: common.HexToHash("0x22"), Code: []byte{0x02, 0x03}},
		},
	}
	_, _, codes, codeByAddr := preprocessDiffForApply(diff)
	if len(codes) != 1 {
		t.Fatalf("unexpected coalesced code len: %d", len(codes))
	}
	if !bytes.Equal(codeByAddr[a], []byte{0x02, 0x03}) {
		t.Fatalf("unexpected latest code bytes: %x", codeByAddr[a])
	}
}

func TestApplyDiff_DuplicateEntriesEquivalentToDedupedDiff(t *testing.T) {
	addr := common.HexToAddress("0x1234")
	slot := common.HexToHash("0x01")

	withDup := &ubtemit.QueuedDiffV1{
		Accounts: []ubtemit.AccountEntry{
			{Address: addr, Nonce: 1, Balance: big.NewInt(100), CodeHash: types.EmptyCodeHash, Alive: true},
			{Address: addr, Nonce: 2, Balance: big.NewInt(200), CodeHash: types.EmptyCodeHash, Alive: true},
		},
		Storage: []ubtemit.StorageEntry{
			{Address: addr, SlotKeyRaw: slot, Value: common.HexToHash("0x10")},
			{Address: addr, SlotKeyRaw: slot, Value: common.HexToHash("0x20")},
		},
		Codes: []ubtemit.CodeEntry{
			{Address: addr, CodeHash: common.HexToHash("0x11"), Code: []byte{0x01}},
			{Address: addr, CodeHash: common.HexToHash("0x22"), Code: []byte{0x02, 0x03}},
		},
	}
	deduped := &ubtemit.QueuedDiffV1{
		Accounts: []ubtemit.AccountEntry{
			{Address: addr, Nonce: 2, Balance: big.NewInt(200), CodeHash: types.EmptyCodeHash, Alive: true},
		},
		Storage: []ubtemit.StorageEntry{
			{Address: addr, SlotKeyRaw: slot, Value: common.HexToHash("0x20")},
		},
		Codes: []ubtemit.CodeEntry{
			{Address: addr, CodeHash: common.HexToHash("0x22"), Code: []byte{0x02, 0x03}},
		},
	}

	a1 := newTestApplier(t)
	defer a1.Close()
	rootWithDup, err := a1.ApplyDiff(withDup, 1)
	if err != nil {
		t.Fatalf("ApplyDiff(withDup): %v", err)
	}

	a2 := newTestApplier(t)
	defer a2.Close()
	rootDeduped, err := a2.ApplyDiff(deduped, 1)
	if err != nil {
		t.Fatalf("ApplyDiff(deduped): %v", err)
	}

	if rootWithDup != rootDeduped {
		t.Fatalf("root mismatch: withDup=%s deduped=%s", rootWithDup, rootDeduped)
	}
}
