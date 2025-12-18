# UBT Snap Sync Local Testing Guide

This guide explains how to test the MPT→UBT conversion after snap sync in a local development environment.

## Prerequisites

1. Build geth with UBT support:
   ```bash
   cd go-ethereum
   make geth
   ```

2. Create data directories:
   ```bash
   mkdir -p ~/ubt-test/node-a  # Full node (MPT)
   mkdir -p ~/ubt-test/node-b  # Snap sync node (UBT)
   ```

## Architecture

```
┌─────────────────────────┐        snap sync        ┌─────────────────────────┐
│      Node A (MPT)       │  ────────────────────>  │      Node B (UBT)       │
│  - Full sync            │                         │  - Snap sync            │
│  - Mining/Clique PoA    │                         │  - --state.ubt          │
│  - Provides state       │                         │  - --state.skiproot     │
│                         │                         │  - --syncmode snap      │
└─────────────────────────┘                         └─────────────────────────┘
```

## Step 1: Create Genesis File

Create a genesis file for Clique PoA network:

```bash
cat > ~/ubt-test/genesis.json << 'EOF'
{
  "config": {
    "chainId": 12345,
    "homesteadBlock": 0,
    "eip150Block": 0,
    "eip155Block": 0,
    "eip158Block": 0,
    "byzantiumBlock": 0,
    "constantinopleBlock": 0,
    "petersburgBlock": 0,
    "istanbulBlock": 0,
    "berlinBlock": 0,
    "londonBlock": 0,
    "clique": {
      "period": 5,
      "epoch": 30000
    }
  },
  "difficulty": "1",
  "gasLimit": "30000000",
  "extradata": "0x0000000000000000000000000000000000000000000000000000000000000000YOUR_SIGNER_ADDRESS_HERE0000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000",
  "alloc": {
    "YOUR_ACCOUNT_ADDRESS_HERE": { "balance": "1000000000000000000000000" }
  }
}
EOF
```

## Step 2: Generate Accounts and Update Genesis

```bash
# Create signer account for Node A
./build/bin/geth --datadir ~/ubt-test/node-a account new
# Note the address (e.g., 0x1234...)

# Update genesis.json with the signer address
# Replace YOUR_SIGNER_ADDRESS_HERE with the address (without 0x prefix)
# Replace YOUR_ACCOUNT_ADDRESS_HERE with the full address

# Initialize both nodes with genesis
./build/bin/geth --datadir ~/ubt-test/node-a init ~/ubt-test/genesis.json
./build/bin/geth --datadir ~/ubt-test/node-b init ~/ubt-test/genesis.json
```

## Step 3: Start Node A (Full Node with MPT)

```bash
./build/bin/geth \
    --datadir ~/ubt-test/node-a \
    --networkid 12345 \
    --port 30303 \
    --http \
    --http.port 8545 \
    --http.api eth,net,web3,admin,debug,txpool \
    --authrpc.port 8551 \
    --mine \
    --miner.etherbase YOUR_SIGNER_ADDRESS \
    --unlock YOUR_SIGNER_ADDRESS \
    --password /dev/null \
    --allow-insecure-unlock \
    --nodiscover \
    --state.scheme path \
    --preimages \
    --verbosity 4 \
    console
```

**Note:** `--preimages` is **required** for UBT conversion to work.

## Step 4: Generate Some State

In the Node A console, create some transactions:

```javascript
// Check balance
eth.getBalance(eth.accounts[0])

// Send some transactions to create state
for (var i = 0; i < 10; i++) {
    eth.sendTransaction({from: eth.accounts[0], to: "0x" + "1".repeat(40), value: web3.toWei(0.1, "ether")})
}

// Wait for blocks to be mined
eth.blockNumber

// Deploy a simple contract
var code = "0x6080604052348015600f57600080fd5b5060998061001e6000396000f3fe6080604052348015600f57600080fd5b506004361060285760003560e01c80632e64cec114602d575b600080fd5b60336045565b60405190815260200160405180910390f35b60005490565b60008054600101908190559056fea264697066735822122000000000000000000000000000000000000000000000000000000000000000"
var tx = eth.sendTransaction({from: eth.accounts[0], data: code, gas: 1000000})
```

Wait for at least 100 blocks to ensure enough state for snap sync.

## Step 5: Get Node A's Enode

In Node A console:
```javascript
admin.nodeInfo.enode
// Copy this value
```

## Step 6: Start Node B (UBT Snap Sync)

```bash
./build/bin/geth \
    --datadir ~/ubt-test/node-b \
    --networkid 12345 \
    --port 30304 \
    --http \
    --http.port 8546 \
    --http.api eth,net,web3,admin,debug \
    --authrpc.port 8552 \
    --syncmode snap \
    --state.scheme path \
    --state.ubt \
    --ubt.batchsize 100 \
    --preimages \
    --bootnodes "ENODE_FROM_NODE_A" \
    --verbosity 4 \
    console
```

**Key flags:**
- `--syncmode snap`: Use snap sync
- `--state.ubt`: Enable UBT mode
- `--state.skiproot`: Automatically enabled with --state.ubt
- `--ubt.batchsize 100`: Process 100 accounts per commit (smaller for testing)
- `--preimages`: Required for UBT conversion

## Step 7: Monitor Conversion Progress

Watch the logs for:

```
INFO [xx-xx|xx:xx:xx.xxx] Started background UBT conversion root=0x... batchSize=100
INFO [xx-xx|xx:xx:xx.xxx] UBT conversion batch committed root=0x... accounts=100 slots=...
INFO [xx-xx|xx:xx:xx.xxx] UBT conversion completed accounts=... slots=... ubtRoot=0x...
```

In Node B console, check conversion status:
```javascript
// Check if using UBT (after conversion completes)
debug.triedbInfo()
```

## Step 8: Verify Conversion

After conversion completes:

1. **Check state access works:**
   ```javascript
   eth.getBalance("0x" + "1".repeat(40))
   eth.getStorageAt(CONTRACT_ADDRESS, 0)
   ```

2. **Check conversion status via RPC:**
   The conversion status is stored in the database and can be queried.

## Troubleshooting

### Missing Preimages Error
```
UBT conversion failed error="missing preimage for account ..."
```
**Solution:** Ensure Node A is running with `--preimages` flag before generating state.

### Conversion Not Starting
Check that:
1. `--state.ubt` is enabled
2. `--syncmode snap` is used
3. Snap sync has completed (check logs for "Committed new head block")

### Slow Conversion
Increase `--ubt.batchsize` for faster conversion (at the cost of more memory).

## CLI Flags Reference

| Flag | Description | Default |
|------|-------------|---------|
| `--state.ubt` | Enable UBT/BinaryTrie state storage | false |
| `--state.skiproot` | Skip state root validation (auto-enabled with --state.ubt) | false |
| `--ubt.batchsize` | Accounts per commit during conversion | 1000 |
| `--ubt.noconversion` | Disable automatic conversion after snap sync | false |
| `--preimages` | Store preimages (required for conversion) | false |

## Next Steps

After successful local testing:
1. Test on Sepolia testnet with real consensus client (Lighthouse, Prysm, etc.)
2. Test on Mainnet
