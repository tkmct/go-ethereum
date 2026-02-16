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
	"flag"
	"testing"

	"github.com/urfave/cli/v2"
)

func TestBuildConfigFromCLI_ExecutionClassRPCEnabled_DefaultFalse(t *testing.T) {
	set := flag.NewFlagSet("ubtconv-test", flag.ContinueOnError)
	if err := executionClassRPCEnabledFlag.Apply(set); err != nil {
		t.Fatalf("apply execution-class-rpc-enabled flag: %v", err)
	}

	ctx := cli.NewContext(app, set, nil)
	cfg := buildConfigFromCLI(ctx)
	if cfg.ExecutionClassRPCEnabled {
		t.Fatalf("expected ExecutionClassRPCEnabled=false by default")
	}
}

func TestBuildConfigFromCLI_ExecutionClassRPCEnabled_True(t *testing.T) {
	set := flag.NewFlagSet("ubtconv-test", flag.ContinueOnError)
	if err := executionClassRPCEnabledFlag.Apply(set); err != nil {
		t.Fatalf("apply execution-class-rpc-enabled flag: %v", err)
	}
	if err := set.Set(executionClassRPCEnabledFlag.Name, "true"); err != nil {
		t.Fatalf("set execution-class-rpc-enabled=true: %v", err)
	}

	ctx := cli.NewContext(app, set, nil)
	cfg := buildConfigFromCLI(ctx)
	if !cfg.ExecutionClassRPCEnabled {
		t.Fatalf("expected ExecutionClassRPCEnabled=true when flag is set")
	}
}

func TestBuildConfigFromCLI_Pprof_DefaultDisabled(t *testing.T) {
	set := flag.NewFlagSet("ubtconv-test", flag.ContinueOnError)
	if err := pprofEnabledFlag.Apply(set); err != nil {
		t.Fatalf("apply pprof-enabled flag: %v", err)
	}
	if err := pprofListenAddrFlag.Apply(set); err != nil {
		t.Fatalf("apply pprof-listen-addr flag: %v", err)
	}

	ctx := cli.NewContext(app, set, nil)
	cfg := buildConfigFromCLI(ctx)
	if cfg.PprofEnabled {
		t.Fatalf("expected PprofEnabled=false by default")
	}
	if cfg.PprofListenAddr == "" {
		t.Fatalf("expected default pprof listen addr to be set")
	}
}

func TestBuildConfigFromCLI_Pprof_Enabled(t *testing.T) {
	set := flag.NewFlagSet("ubtconv-test", flag.ContinueOnError)
	if err := pprofEnabledFlag.Apply(set); err != nil {
		t.Fatalf("apply pprof-enabled flag: %v", err)
	}
	if err := pprofListenAddrFlag.Apply(set); err != nil {
		t.Fatalf("apply pprof-listen-addr flag: %v", err)
	}
	if err := set.Set(pprofEnabledFlag.Name, "true"); err != nil {
		t.Fatalf("set pprof-enabled=true: %v", err)
	}
	wantAddr := "127.0.0.1:7061"
	if err := set.Set(pprofListenAddrFlag.Name, wantAddr); err != nil {
		t.Fatalf("set pprof-listen-addr: %v", err)
	}

	ctx := cli.NewContext(app, set, nil)
	cfg := buildConfigFromCLI(ctx)
	if !cfg.PprofEnabled {
		t.Fatalf("expected PprofEnabled=true when flag is set")
	}
	if cfg.PprofListenAddr != wantAddr {
		t.Fatalf("unexpected pprof listen addr: got %s want %s", cfg.PprofListenAddr, wantAddr)
	}
}
