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

package eth

import (
	"bytes"
	"context"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/ethereum/go-ethereum/triedb"
	"github.com/holiman/uint256"
)

func TestGetUBTStateMinimal(t *testing.T) {
	db := rawdb.NewMemoryDatabase()
	genesis := &core.Genesis{
		Config: params.TestChainConfig,
		Alloc: types.GenesisAlloc{
			common.HexToAddress("0x0000000000000000000000000000000000000001"): {
				Balance: big.NewInt(1),
			},
		},
	}
	cfg := core.DefaultConfig().WithStateScheme(rawdb.PathScheme)
	cfg.ArchiveMode = true
	cfg.Preimages = true
	cfg.SnapshotLimit = 0
	cfg.SnapshotWait = false
	cfg.SnapshotNoBuild = true
	cfg.UBTSidecar = true

	chain, err := core.NewBlockChain(db, genesis, ethash.NewFaker(), cfg)
	if err != nil {
		t.Fatalf("failed to create chain: %v", err)
	}
	defer chain.Stop()

	eth := &Ethereum{blockchain: chain, chainDb: db}
	eth.APIBackend = &EthAPIBackend{eth: eth}
	api := NewDebugAPI(eth)

	sc := chain.UBTSidecar()
	if sc == nil {
		t.Fatalf("expected sidecar to be initialized")
	}

	addr := common.HexToAddress("0x00000000000000000000000000000000000000aa")
	sentinel := common.HexToAddress("0x00000000000000000000000000000000000000ab")
	slot := common.HexToHash("0x01")
	value := common.HexToHash("0xdeadbeef")

	tdb := triedb.NewDatabase(rawdb.NewMemoryDatabase(), nil)
	stateDB := state.NewDatabase(tdb, nil)
	sdb, err := state.New(types.EmptyRootHash, stateDB)
	if err != nil {
		t.Fatalf("state init failed: %v", err)
	}
	sdb.SetBalance(addr, uint256.NewInt(7), tracing.BalanceChangeUnspecified)
	sdb.SetNonce(addr, 2, tracing.NonceChangeUnspecified)
	sdb.SetState(addr, slot, value)
	sdb.SetBalance(sentinel, uint256.NewInt(1), tracing.BalanceChangeUnspecified)
	_, update, err := sdb.CommitWithUpdate(0, true, true)
	if err != nil {
		t.Fatalf("commit failed: %v", err)
	}
	block := chain.GetBlockByNumber(0)
	if block == nil {
		t.Fatalf("missing genesis block")
	}
	if err := sc.ApplyStateUpdate(block, update, db); err != nil {
		t.Fatalf("apply update failed: %v", err)
	}
	if err := sc.InitFromDB(); err != nil {
		t.Fatalf("sidecar init failed: %v", err)
	}
	if !sc.Ready() {
		t.Fatalf("expected sidecar to be ready after init")
	}

	res, err := api.GetUBTState(context.Background(), addr, []string{slot.Hex()}, rpc.BlockNumberOrHashWithNumber(0))
	if err != nil {
		t.Fatalf("GetUBTState failed: %v", err)
	}
	if res.Address != addr {
		t.Fatalf("unexpected address: %x", res.Address)
	}
	if res.Balance == nil || res.Balance.ToInt().Cmp(big.NewInt(7)) != 0 {
		t.Fatalf("unexpected balance: %v", res.Balance)
	}
	if uint64(res.Nonce) != 2 {
		t.Fatalf("unexpected nonce: %d", res.Nonce)
	}
	if got := res.Storage[slot]; !bytes.Equal(got, value.Bytes()) {
		t.Fatalf("unexpected storage value: %x", got)
	}
	if res.UbtRoot != sc.CurrentRoot() {
		t.Fatalf("unexpected UBT root: %x", res.UbtRoot)
	}
}
