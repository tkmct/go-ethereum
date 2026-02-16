// Copyright 2026 The go-ethereum Authors
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
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

func TestUBTStorageSlotPreimageReadWrite(t *testing.T) {
	db := NewMemoryDatabase()

	addr := common.HexToAddress("0x1234")
	raw := common.HexToHash("0x5678")
	slotHash := crypto.Keccak256Hash(raw.Bytes())

	WriteUBTStorageSlotPreimage(db, addr, slotHash, raw)

	got, ok := ReadUBTStorageSlotPreimage(db, addr, slotHash)
	if !ok {
		t.Fatal("expected preimage mapping")
	}
	if got != raw {
		t.Fatalf("preimage mismatch: got %s want %s", got, raw)
	}
}

func TestUBTStorageSlotPreimageReadRejectsInvalidMapping(t *testing.T) {
	db := NewMemoryDatabase()

	addr := common.HexToAddress("0xaaaa")
	raw := common.HexToHash("0xbbbb")
	wrongHash := common.HexToHash("0x01")

	// Intentionally store a mismatched mapping directly.
	if err := db.Put(ubtSlotPreimageKey(addr, wrongHash), raw.Bytes()); err != nil {
		t.Fatalf("failed to write mismatched preimage: %v", err)
	}

	_, ok := ReadUBTStorageSlotPreimage(db, addr, wrongHash)
	if ok {
		t.Fatal("expected invalid preimage mapping to be rejected")
	}
}

func TestWriteUBTStorageSlotPreimages(t *testing.T) {
	db := NewMemoryDatabase()

	addr1 := common.HexToAddress("0x1111")
	addr2 := common.HexToAddress("0x2222")
	raw1 := common.HexToHash("0x01")
	raw2 := common.HexToHash("0x02")

	preimages := map[common.Address]map[common.Hash]common.Hash{
		addr1: {
			crypto.Keccak256Hash(raw1.Bytes()): raw1,
		},
		addr2: {
			crypto.Keccak256Hash(raw2.Bytes()): raw2,
		},
	}
	if got := WriteUBTStorageSlotPreimages(db, preimages); got != 2 {
		t.Fatalf("written count mismatch: got %d want 2", got)
	}
}
