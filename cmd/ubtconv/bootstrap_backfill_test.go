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
	"math/big"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rpc"
)

type mockDebugAPI struct {
	pages map[string]*rpcStateDump
}

func (m *mockDebugAPI) AccountRange(_ context.Context, blockNrOrHash rpc.BlockNumberOrHash, start hexutil.Bytes, maxResults int, _ bool, _ bool, _ bool) (*rpcStateDump, error) {
	if maxResults != accountRangePageSize {
		return nil, errInvalidMaxResults{have: maxResults, want: accountRangePageSize}
	}
	number, ok := blockNrOrHash.Number()
	if !ok || number != rpc.BlockNumber(0) {
		return nil, errInvalidBlockSelector{}
	}
	key := hexutil.Encode(start)
	if page, ok := m.pages[key]; ok {
		return page, nil
	}
	return &rpcStateDump{Accounts: map[string]rpcDumpAccount{}}, nil
}

type errInvalidMaxResults struct {
	have int
	want int
}

func (e errInvalidMaxResults) Error() string {
	return "invalid maxResults in AccountRange"
}

type errInvalidBlockSelector struct{}

func (e errInvalidBlockSelector) Error() string {
	return "invalid block selector in AccountRange"
}

type bootstrapRPCServer struct {
	outbox   *mockOutboxAPI
	debugAPI *mockDebugAPI
	server   *rpc.Server
	listener net.Listener
	httpSrv  *http.Server
}

func newBootstrapRPCServer(t *testing.T, debugPages map[string]*rpcStateDump) *bootstrapRPCServer {
	t.Helper()

	s := &bootstrapRPCServer{
		outbox:   newMockOutboxAPI(),
		debugAPI: &mockDebugAPI{pages: debugPages},
		server:   rpc.NewServer(),
	}
	if err := s.server.RegisterName("ubt", s.outbox); err != nil {
		t.Fatalf("register ubt namespace: %v", err)
	}
	if err := s.server.RegisterName("debug", s.debugAPI); err != nil {
		t.Fatalf("register debug namespace: %v", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s.listener = ln
	s.httpSrv = &http.Server{Handler: s.server}
	go func() {
		_ = s.httpSrv.Serve(s.listener)
	}()
	t.Cleanup(func() {
		_ = s.httpSrv.Close()
		_ = s.listener.Close()
	})
	return s
}

func (s *bootstrapRPCServer) endpoint() string {
	return "http://" + s.listener.Addr().String()
}

func testBackfillConfig(endpoint, dataDir string) *Config {
	return &Config{
		OutboxRPCEndpoint:        endpoint,
		DataDir:                  dataDir,
		ApplyCommitInterval:      100,
		ApplyCommitMaxLatency:    time.Hour,
		BootstrapMode:            "backfill-direct",
		MaxRecoverableReorgDepth: 128,
		TrieDBScheme:             "path",
		TrieDBStateHistory:       128,
	}
}

func TestBootstrapBackfillDirect_ImportsGenesisAccount(t *testing.T) {
	addr := common.HexToAddress("0x71562b71999873DB5b286dF957af199Ec94617F7")
	want := big.NewInt(987654321012345678)

	pages := map[string]*rpcStateDump{
		"0x": {
			Accounts: map[string]rpcDumpAccount{
				addr.Hex(): {
					Balance:  want.String(),
					Nonce:    0,
					CodeHash: types.EmptyCodeHash[:],
				},
			},
		},
	}
	server := newBootstrapRPCServer(t, pages)
	cfg := testBackfillConfig(server.endpoint(), t.TempDir())

	runner, err := NewRunner(cfg)
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	defer runner.consumer.Close()

	if err := runner.bootstrapBackfillDirect(); err != nil {
		t.Fatalf("bootstrapBackfillDirect: %v", err)
	}

	api := NewQueryAPI(runner.consumer)
	balance, err := api.GetBalance(context.Background(), addr, nil)
	if err != nil {
		t.Fatalf("GetBalance: %v", err)
	}
	if balance.ToInt().Cmp(want) != 0 {
		t.Fatalf("balance mismatch: have=%s want=%s", balance.ToInt(), want)
	}
	if runner.consumer.processedSeq != ^uint64(0) {
		t.Fatalf("expected fresh processedSeq sentinel, have=%d", runner.consumer.processedSeq)
	}
}

func TestBootstrapBackfillDirect_ConsumesSeqZeroAfterGenesisImport(t *testing.T) {
	addr := common.HexToAddress("0x1000000000000000000000000000000000000001")
	genesisBal := big.NewInt(100)
	afterSeq0Bal := big.NewInt(200)

	pages := map[string]*rpcStateDump{
		"0x": {
			Accounts: map[string]rpcDumpAccount{
				addr.Hex(): {
					Balance:  genesisBal.String(),
					Nonce:    0,
					CodeHash: types.EmptyCodeHash[:],
				},
			},
		},
	}
	server := newBootstrapRPCServer(t, pages)
	server.outbox.addDiff(t, 0, 1, addr, 1, afterSeq0Bal)

	cfg := testBackfillConfig(server.endpoint(), t.TempDir())
	cfg.ApplyCommitInterval = 1000

	runner, err := NewRunner(cfg)
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	defer runner.consumer.Close()

	if err := runner.bootstrapBackfillDirect(); err != nil {
		t.Fatalf("bootstrapBackfillDirect: %v", err)
	}

	api := NewQueryAPI(runner.consumer)
	balance, err := api.GetBalance(context.Background(), addr, nil)
	if err != nil {
		t.Fatalf("GetBalance: %v", err)
	}
	if balance.ToInt().Cmp(afterSeq0Bal) != 0 {
		t.Fatalf("balance mismatch after seq0 catch-up: have=%s want=%s", balance.ToInt(), afterSeq0Bal)
	}
	if runner.consumer.state.AppliedSeq != 0 {
		t.Fatalf("expected appliedSeq=0 after seq0 bootstrap catch-up, have=%d", runner.consumer.state.AppliedSeq)
	}
	if !runner.consumer.hasState {
		t.Fatal("expected hasState=true after bootstrap catch-up commit")
	}
}
