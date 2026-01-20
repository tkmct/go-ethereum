package main

import (
	"context"
	"fmt"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
)

// SyncMode indicates how the node was synced.
type SyncMode string

const (
	ModeFullSync SyncMode = "fullsync"
	ModeSnapSync SyncMode = "snapsync"
)

// ValidatorConfig holds configuration for the validator.
type ValidatorConfig struct {
	Mode    SyncMode
	Verbose bool
}

// Validator performs validation checks on a geth node.
type Validator struct {
	rpcURL  string
	config  ValidatorConfig
	client  *ethclient.Client
	rpc     *rpc.Client
	results *Results
}

// NewValidator creates a new validator instance.
func NewValidator(rpcURL string, config ValidatorConfig) *Validator {
	return &Validator{
		rpcURL:  rpcURL,
		config:  config,
		results: NewResults(),
	}
}

// Run executes all validation checks and returns the results.
func (v *Validator) Run(ctx context.Context) *Results {
	v.printHeader()

	// Phase 1: Connectivity
	if !v.checkConnectivity(ctx) {
		return v.results
	}

	// Phase 2: Sync Status
	v.checkSyncStatus(ctx)

	// Phase 3: State Reads
	v.checkStateReads(ctx)

	// Phase 4: Block Execution
	v.checkBlockExecution(ctx)

	// Phase 5: Witness Generation
	v.checkWitnessGeneration(ctx)

	return v.results
}

func (v *Validator) printHeader() {
	fmt.Println("==========================================")
	fmt.Println("UBT Validation Tool")
	fmt.Println("==========================================")
	fmt.Println()
	fmt.Printf("RPC Endpoint: %s\n", v.rpcURL)
	fmt.Printf("Sync Mode:    %s\n", v.config.Mode)
	fmt.Println()
}

// checkConnectivity verifies the node is reachable.
func (v *Validator) checkConnectivity(ctx context.Context) bool {
	fmt.Println("=== Phase 1: Connectivity ===")

	rpcClient, err := rpc.DialContext(ctx, v.rpcURL)
	if err != nil {
		v.results.Fail("RPC Connection", fmt.Sprintf("Cannot connect: %v", err))
		return false
	}
	v.rpc = rpcClient
	v.client = ethclient.NewClient(rpcClient)

	var clientVersion string
	if err := v.rpc.CallContext(ctx, &clientVersion, "web3_clientVersion"); err != nil {
		v.results.Fail("Client Version", fmt.Sprintf("RPC call failed: %v", err))
		return false
	}

	v.results.Pass("Connected", clientVersion)
	fmt.Println()
	return true
}

// checkSyncStatus verifies the node is fully synced.
func (v *Validator) checkSyncStatus(ctx context.Context) {
	fmt.Println("=== Phase 2: Sync Status ===")

	progress, err := v.client.SyncProgress(ctx)
	if err != nil {
		v.results.Fail("Sync Status", fmt.Sprintf("RPC call failed: %v", err))
		return
	}

	if progress == nil {
		v.results.Pass("Sync Status", "Node is fully synced")
	} else {
		msg := fmt.Sprintf("Still syncing: block %d / %d (%.1f%%)",
			progress.CurrentBlock,
			progress.HighestBlock,
			float64(progress.CurrentBlock)/float64(progress.HighestBlock)*100,
		)
		v.results.Warn("Sync Status", msg)
	}
	fmt.Println()
}

// checkStateReads verifies state can be read correctly.
func (v *Validator) checkStateReads(ctx context.Context) {
	fmt.Println("=== Phase 3: State Reads ===")

	testAddresses := []struct {
		name    string
		address common.Address
	}{
		{"Zero Address", common.Address{}},
		{"Deposit Contract", common.HexToAddress("0x4242424242424242424242424242424242424242")},
	}

	allPassed := true
	for _, test := range testAddresses {
		balance, err := v.client.BalanceAt(ctx, test.address, nil)
		if err != nil {
			v.results.Fail(fmt.Sprintf("Balance (%s)", test.name), err.Error())
			allPassed = false
			continue
		}
		if v.config.Verbose {
			fmt.Printf("  %s: %s wei\n", test.name, balance.String())
		}
	}

	if allPassed {
		v.results.Pass("eth_getBalance", "All balance reads successful")
	}

	// Test storage read
	storageKey := common.Hash{}
	_, err := v.client.StorageAt(ctx, common.Address{}, storageKey, nil)
	if err != nil {
		v.results.Fail("eth_getStorageAt", err.Error())
	} else {
		v.results.Pass("eth_getStorageAt", "Storage read successful")
	}

	fmt.Println()
}

// checkBlockExecution verifies new blocks are being processed.
func (v *Validator) checkBlockExecution(ctx context.Context) {
	fmt.Println("=== Phase 4: Block Execution ===")

	block1, err := v.client.BlockNumber(ctx)
	if err != nil {
		v.results.Fail("Block Number", fmt.Sprintf("RPC call failed: %v", err))
		return
	}
	fmt.Printf("  Current block: %d\n", block1)

	// Get latest block details
	header, err := v.client.HeaderByNumber(ctx, nil)
	if err != nil {
		v.results.Fail("Block Header", fmt.Sprintf("Cannot get header: %v", err))
	} else {
		fmt.Printf("  Block hash:    %s\n", header.Hash().Hex())
		fmt.Printf("  State root:    %s\n", header.Root.Hex())
		v.results.Pass("Block Data", fmt.Sprintf("Block %d accessible", header.Number.Uint64()))
	}

	// Wait and check for new blocks
	fmt.Println("  Waiting 12 seconds for new blocks...")
	if err := sleepContext(ctx, 12*time.Second); err != nil {
		v.results.Warn("Block Progress", "Timeout waiting for context")
		return
	}

	block2, err := v.client.BlockNumber(ctx)
	if err != nil {
		v.results.Fail("Block Number", fmt.Sprintf("RPC call failed: %v", err))
		return
	}
	fmt.Printf("  Block after wait: %d\n", block2)

	if block2 > block1 {
		v.results.Pass("Block Progress", fmt.Sprintf("Advanced %d blocks", block2-block1))
	} else {
		v.results.Warn("Block Progress", "No new blocks in 12 seconds")
	}

	fmt.Println()
}

// sleepContext sleeps for the given duration, respecting context cancellation.
func sleepContext(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// ExecutionWitness represents the response from debug_executionWitness.
type ExecutionWitness struct {
	Witness map[string]any `json:"witness"`
}

// checkWitnessGeneration verifies witness generation works.
func (v *Validator) checkWitnessGeneration(ctx context.Context) {
	fmt.Println("=== Phase 5: Witness Generation ===")

	blockNum, err := v.client.BlockNumber(ctx)
	if err != nil {
		v.results.Fail("Witness Generation", fmt.Sprintf("Cannot get block number: %v", err))
		return
	}

	// Try a block a few behind head to ensure it's finalized
	targetBlock := blockNum - 5
	if blockNum < 5 {
		targetBlock = 0
	}

	fmt.Printf("  Testing block: %d\n", targetBlock)

	var witness any
	// Pass block number as hex string (RPC expects hex)
	blockHex := fmt.Sprintf("0x%x", targetBlock)
	err = v.rpc.CallContext(ctx, &witness, "debug_executionWitness", blockHex)
	if err != nil {
		v.results.Warn("Witness Generation", fmt.Sprintf("RPC call failed: %v", err))
		return
	}

	if witness == nil {
		v.results.Warn("Witness Generation", "Empty witness returned")
		return
	}

	// Check if witness has expected structure
	if witnessMap, ok := witness.(map[string]any); ok {
		keys := make([]string, 0, len(witnessMap))
		for k := range witnessMap {
			keys = append(keys, k)
		}
		v.results.Pass("Witness Generation", fmt.Sprintf("Generated for block %d (keys: %v)", targetBlock, keys))
	} else {
		v.results.Pass("Witness Generation", fmt.Sprintf("Generated for block %d", targetBlock))
	}

	fmt.Println()
}
