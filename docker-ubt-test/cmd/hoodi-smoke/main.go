// Command hoodi-smoke runs an automated smoke test against a Hoodi sync.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/rpc"
)

const zeroRoot = "0x0000000000000000000000000000000000000000000000000000000000000000"

type ubtProofResult struct {
	UBTRoot string `json:"ubtRoot"`
}

type ubtStateResult struct {
	UbtRoot string `json:"ubtRoot"`
}

type client struct {
	rpc     *rpc.Client
	timeout time.Duration
}

func (c *client) call(result interface{}, method string, args ...interface{}) error {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()
	return c.rpc.CallContext(ctx, result, method, args...)
}

func main() {
	rpcURL := flag.String("rpc", "http://localhost:8545", "Geth RPC endpoint")
	blockTag := flag.String("block-tag", "latest", "Block tag for UBT RPCs")
	connectTimeout := flag.Duration("connect-timeout", 2*time.Minute, "How long to wait for RPC to be available")
	ubtTimeout := flag.Duration("ubt-timeout", 5*time.Minute, "How long to wait for non-zero UBT root")
	pollInterval := flag.Duration("poll-interval", 5*time.Second, "Polling interval")
	ubtcheckMode := flag.String("ubtcheck-mode", "quick", "ubtcheck mode: quick or full")
	ubtcheckTimeout := flag.Duration("ubtcheck-timeout", 30*time.Minute, "Timeout for ubtcheck execution")
	flag.Parse()

	if *ubtcheckMode != "quick" && *ubtcheckMode != "full" {
		exitf("invalid --ubtcheck-mode: %s", *ubtcheckMode)
	}

	fmt.Println("Hoodi smoke test")
	fmt.Println("================")
	fmt.Printf("RPC: %s\n", *rpcURL)
	fmt.Printf("Block tag: %s\n", *blockTag)
	fmt.Printf("Connect timeout: %s\n", *connectTimeout)
	fmt.Printf("UBT root timeout: %s\n", *ubtTimeout)
	fmt.Printf("Poll interval: %s\n", *pollInterval)
	fmt.Printf("ubtcheck mode: %s\n", *ubtcheckMode)
	fmt.Println("")

	client, err := waitForRPC(*rpcURL, *connectTimeout, *pollInterval)
	if err != nil {
		exitf("RPC not available: %v", err)
	}
	defer client.rpc.Close()
	fmt.Println("OK: RPC is reachable")

	if err := waitForUBTRoot(client, *blockTag, *ubtTimeout, *pollInterval); err != nil {
		exitf("UBT root not ready: %v", err)
	}
	fmt.Println("OK: UBT root is non-zero")

	if err := runUBTCheck(*rpcURL, *blockTag, *ubtcheckMode, *ubtcheckTimeout); err != nil {
		exitf("ubtcheck failed: %v", err)
	}
	fmt.Println("OK: ubtcheck completed")
}

func waitForRPC(url string, timeout, interval time.Duration) (*client, error) {
	deadline := time.Now().Add(timeout)
	for {
		if time.Now().After(deadline) {
			return nil, errors.New("timeout waiting for RPC")
		}
		rpcClient, err := rpc.Dial(url)
		if err == nil {
			c := &client{rpc: rpcClient, timeout: 10 * time.Second}
			var version string
			if err := c.call(&version, "web3_clientVersion"); err == nil && version != "" {
				return c, nil
			}
			rpcClient.Close()
		}
		time.Sleep(interval)
	}
}

func waitForUBTRoot(c *client, blockTag string, timeout, interval time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if time.Now().After(deadline) {
			return errors.New("timeout waiting for non-zero UBT root")
		}
		var state ubtStateResult
		if err := c.call(&state, "debug_getUBTState", "0x0000000000000000000000000000000000000000", []string{}, blockTag); err == nil {
			if state.UbtRoot != "" && state.UbtRoot != zeroRoot {
				return nil
			}
		}

		var proof ubtProofResult
		err := c.call(&proof, "debug_getUBTProof", "0x0000000000000000000000000000000000000000", []string{}, blockTag)
		if err == nil && proof.UBTRoot != "" && proof.UBTRoot != zeroRoot {
			return nil
		}
		if err != nil && !isMissingKeyErr(err) {
			return err
		}
		time.Sleep(interval)
	}
}

func runUBTCheck(rpcURL, blockTag, mode string, timeout time.Duration) error {
	args := []string{"run", "./cmd/ubtcheck", "--mode", mode, "--block-tag", blockTag, "--rpc", rpcURL}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", args...)
	cwd, err := os.Getwd()
	if err == nil && filepath.Base(cwd) != "docker-ubt-test" {
		cmd.Dir = "docker-ubt-test"
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	return nil
}

func exitf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "ERROR: "+format+"\n", args...)
	os.Exit(1)
}

func isMissingKeyErr(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "key not found in trie")
}
