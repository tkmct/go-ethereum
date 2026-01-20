// Command validate checks that a geth node with UBT state is functioning correctly.
//
// Usage:
//
//	go run ./cmd/validate --rpc http://localhost:8545
//	go run ./cmd/validate --rpc http://localhost:8545 --mode fullsync
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"
)

func main() {
	var (
		rpcURL  = flag.String("rpc", "http://localhost:8545", "Geth JSON-RPC endpoint")
		mode    = flag.String("mode", "fullsync", "Sync mode: 'fullsync' or 'snapsync'")
		timeout = flag.Duration("timeout", 30*time.Second, "RPC call timeout")
		verbose = flag.Bool("verbose", false, "Show detailed output")
	)
	flag.Parse()

	if *mode != "fullsync" && *mode != "snapsync" {
		fmt.Fprintf(os.Stderr, "Error: --mode must be 'fullsync' or 'snapsync'\n")
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	v := NewValidator(*rpcURL, ValidatorConfig{
		Mode:    SyncMode(*mode),
		Verbose: *verbose,
	})

	result := v.Run(ctx)
	result.Print()

	if result.Failed > 0 {
		os.Exit(1)
	}
}
