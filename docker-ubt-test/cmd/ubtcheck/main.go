// Command ubtcheck performs quick or full validation of a UBT sidecar node.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/rpc"
)

const zeroHash = "0x0000000000000000000000000000000000000000000000000000000000000000"

type counters struct {
	pass int
	fail int
	warn int
}

func (c *counters) passf(format string, args ...interface{}) {
	c.pass++
	fmt.Printf("  OK   "+format+"\n", args...)
}

func (c *counters) failf(format string, args ...interface{}) {
	c.fail++
	fmt.Printf("  FAIL "+format+"\n", args...)
}

func (c *counters) warnf(format string, args ...interface{}) {
	c.warn++
	fmt.Printf("  WARN "+format+"\n", args...)
}

func header(title string) {
	fmt.Println("==========================================")
	fmt.Println(title)
	fmt.Println("==========================================")
	fmt.Println("")
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

type ubtProofResult struct {
	UBTRoot string `json:"ubtRoot"`
}

type ubtStateResult struct {
	Balance string `json:"balance"`
	UbtRoot string `json:"ubtRoot"`
}

type blockResult struct {
	Number       string            `json:"number"`
	Hash         string            `json:"hash"`
	StateRoot    string            `json:"stateRoot"`
	Transactions []json.RawMessage `json:"transactions"`
}

type nodeInfo struct {
	Protocols map[string]interface{} `json:"protocols"`
}

func main() {
	rpcURL := flag.String("rpc", "http://localhost:8545", "Geth RPC endpoint")
	mode := flag.String("mode", "quick", "Validation mode: quick or full")
	blockTag := flag.String("block-tag", "latest", "Block tag for UBT RPCs")
	timeout := flag.Duration("timeout", 10*time.Second, "RPC timeout")
	wait := flag.Duration("wait", 15*time.Second, "Wait time for block progression checks (full mode)")
	flag.Parse()

	if *mode != "quick" && *mode != "full" {
		fmt.Printf("Invalid --mode: %s (expected quick or full)\n", *mode)
		return
	}

	rpcClient, err := rpc.Dial(*rpcURL)
	if err != nil {
		fmt.Printf("Failed to connect to RPC %s: %v\n", *rpcURL, err)
		return
	}
	defer rpcClient.Close()

	c := &client{rpc: rpcClient, timeout: *timeout}
	counts := &counters{}

	header("UBT Sidecar Validation")
	fmt.Printf("Geth RPC: %s\n", *rpcURL)
	fmt.Printf("Mode: %s\n", *mode)
	fmt.Printf("Block tag: %s\n\n", *blockTag)

	// Phase 1: Connectivity
	fmt.Println("=== Phase 1: Connectivity ===")
	var version string
	if err := c.call(&version, "web3_clientVersion"); err != nil || version == "" {
		counts.failf("Cannot connect to Geth (%v)", err)
		return
	}
	counts.passf("Connected to %s", version)
	fmt.Println("")

	// Phase 2: Sync status
	fmt.Println("=== Phase 2: Sync Status ===")
	var syncing interface{}
	if err := c.call(&syncing, "eth_syncing"); err != nil {
		counts.warnf("Unable to fetch sync status (%v)", err)
	} else if syncing == false {
		counts.passf("Node is fully synced")
	} else if m, ok := syncing.(map[string]interface{}); ok {
		current := fmt.Sprintf("%v", m["currentBlock"])
		highest := fmt.Sprintf("%v", m["highestBlock"])
		counts.warnf("Node is still syncing: %s / %s", current, highest)
	} else {
		counts.warnf("Unexpected eth_syncing response")
	}
	fmt.Println("")

	// Phase 3: Sidecar RPCs
	fmt.Println("=== Phase 3: Sidecar RPCs ===")
	var proof ubtProofResult
	if err := c.call(&proof, "debug_getUBTProof", "0x0000000000000000000000000000000000000000", []string{}, *blockTag); err != nil {
		if isMissingKeyErr(err) {
			counts.warnf("debug_getUBTProof missing key (non-membership proof not supported)")
		} else {
			counts.failf("debug_getUBTProof error: %v", err)
		}
	} else if proof.UBTRoot == "" || proof.UBTRoot == zeroHash {
		counts.failf("debug_getUBTProof returned empty UBT root (sidecar not ready?)")
	} else {
		counts.passf("debug_getUBTProof works (ubtRoot: %s)", proof.UBTRoot)
	}

	var state ubtStateResult
	if err := c.call(&state, "debug_getUBTState", "0x0000000000000000000000000000000000000000", []string{}, *blockTag); err != nil {
		counts.failf("debug_getUBTState error: %v", err)
	} else if state.UbtRoot == "" || state.UbtRoot == zeroHash {
		counts.failf("debug_getUBTState returned empty UBT root (sidecar not ready?)")
	} else if state.Balance == "" {
		counts.warnf("debug_getUBTState returned empty result")
	} else {
		counts.passf("debug_getUBTState works (ubtRoot: %s)", state.UbtRoot)
	}
	fmt.Println("")

	// Phase 4: Block info
	fmt.Println("=== Phase 4: Block Info ===")
	var block blockResult
	if err := c.call(&block, "eth_getBlockByNumber", "latest", false); err != nil {
		counts.failf("eth_getBlockByNumber failed: %v", err)
	} else {
		blockNum := blockNumberToUint64(block.Number)
		fmt.Printf("  Block Number: %d\n", blockNum)
		fmt.Printf("  Block Hash: %s\n", block.Hash)
		fmt.Printf("  State Root: %s\n", block.StateRoot)
		if block.Hash != "" && block.Hash != "unknown" {
			counts.passf("Block data accessible")
		} else {
			counts.failf("Cannot read block data")
		}
	}
	fmt.Println("")

	// Phase 5: State reads
	fmt.Println("=== Phase 5: State Reads ===")
	if err := checkBalance(c, counts, "0x0000000000000000000000000000000000000000", "zero address"); err != nil {
		counts.failf("eth_getBalance failed: %v", err)
	}
	if err := checkBalance(c, counts, "0x4242424242424242424242424242424242424242", "deposit contract"); err != nil {
		counts.failf("eth_getBalance failed for deposit contract: %v", err)
	}
	if err := checkStorage(c, counts); err != nil {
		counts.failf("eth_getStorageAt failed: %v", err)
	}
	fmt.Println("")

	// Phase 6: Node info
	fmt.Println("=== Phase 6: Node Info ===")
	var info nodeInfo
	if err := c.call(&info, "admin_nodeInfo"); err != nil {
		counts.warnf("admin_nodeInfo failed: %v", err)
	} else if len(info.Protocols) == 0 {
		counts.warnf("admin_nodeInfo returned empty protocols")
	} else {
		counts.passf("admin_nodeInfo works (protocols: %d)", len(info.Protocols))
	}
	fmt.Println("")

	if *mode == "full" {
		if err := fullModeChecks(c, counts, *wait); err != nil {
			counts.failf("Full mode checks failed: %v", err)
		}
	}

	header("Validation Summary")
	fmt.Printf("  Passed:   %d\n", counts.pass)
	fmt.Printf("  Failed:   %d\n", counts.fail)
	fmt.Printf("  Warnings: %d\n\n", counts.warn)

	if counts.fail == 0 {
		if counts.warn == 0 {
			fmt.Println("All checks passed.")
		} else {
			fmt.Println("Validation passed with warnings.")
		}
	} else {
		fmt.Println("Validation failed.")
	}
}

func checkBalance(c *client, counts *counters, addr string, label string) error {
	var balance string
	if err := c.call(&balance, "eth_getBalance", addr, "latest"); err != nil {
		return err
	}
	if balance == "" {
		counts.warnf("eth_getBalance returned empty result (%s)", label)
		return nil
	}
	counts.passf("eth_getBalance works (%s: %s)", label, balance)
	return nil
}

func checkStorage(c *client, counts *counters) error {
	var storage string
	if err := c.call(&storage, "eth_getStorageAt", "0x0000000000000000000000000000000000000000", "0x0", "latest"); err != nil {
		return err
	}
	if storage == "" {
		counts.warnf("eth_getStorageAt returned empty result")
		return nil
	}
	counts.passf("eth_getStorageAt works")
	return nil
}

func fullModeChecks(c *client, counts *counters, wait time.Duration) error {
	fmt.Println("=== Phase 7: Block Execution ===")
	start, err := fetchBlockNumber(c)
	if err != nil {
		counts.warnf("Unable to read block number: %v", err)
		fmt.Println("")
		return err
	}
	fmt.Printf("  Current block: %d\n", start)
	fmt.Printf("  Waiting %s for new blocks...\n", wait)
	time.Sleep(wait)
	end, err := fetchBlockNumber(c)
	if err != nil {
		counts.warnf("Unable to read block number after wait: %v", err)
		fmt.Println("")
		return err
	}
	fmt.Printf("  Block after wait: %d\n", end)
	if end > start {
		counts.passf("New blocks are being processed (advanced %d blocks)", end-start)
	} else {
		counts.warnf("No new blocks processed in %s", wait)
	}

	var block blockResult
	if err := c.call(&block, "eth_getBlockByNumber", "latest", false); err != nil {
		counts.failf("eth_getBlockByNumber failed: %v", err)
	} else {
		fmt.Printf("  Latest block hash: %s\n", block.Hash)
		fmt.Printf("  State root: %s\n", block.StateRoot)
		fmt.Printf("  Transaction count: %d\n", len(block.Transactions))
		if block.Hash != "" && block.Hash != "unknown" {
			counts.passf("Block data accessible")
		} else {
			counts.failf("Cannot read block data")
		}
	}
	fmt.Println("")

	fmt.Println("=== Phase 8: Witness Generation ===")
	witnessBlock := int64(end) - 5
	if witnessBlock < 0 {
		witnessBlock = 0
	}
	blockHex := fmt.Sprintf("0x%x", witnessBlock)

	var witness json.RawMessage
	if err := c.call(&witness, "debug_executionWitnessUBT", blockHex); err != nil {
		counts.warnf("UBT witness generation: %v", err)
	} else if len(witness) == 0 || string(witness) == "null" {
		counts.warnf("UBT witness generation returned empty result")
	} else {
		counts.passf("UBT witness generation works for block %d", witnessBlock)
	}
	fmt.Println("")
	return nil
}

func fetchBlockNumber(c *client) (uint64, error) {
	var hex string
	if err := c.call(&hex, "eth_blockNumber"); err != nil {
		return 0, err
	}
	if hex == "" {
		return 0, errors.New("empty block number")
	}
	return blockNumberToUint64(hex), nil
}

func blockNumberToUint64(hex string) uint64 {
	n, err := hexutil.DecodeUint64(hex)
	if err != nil {
		return 0
	}
	return n
}

func isMissingKeyErr(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "key not found in trie")
}
