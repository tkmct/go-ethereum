# UBT Docker Test Environment

Test environment for validating UBT (Universal Binary Trie) **sidecar** state using Geth with Lighthouse consensus client.

## Overview

This environment provides:
- Geth built from source with UBT sidecar support (MPT remains consensus state)
- Lighthouse for consensus layer (checkpoint sync)
- Monitoring and validation scripts
- Support for Hoodi and Mainnet

## Quick Start (Hoodi, Sidecar)

```bash
# If you previously synced without preimages or with --state.ubt, clean first:
# ./scripts/clean.sh --data-dir /mnt/q/ubt-sync

# Start Hoodi testnet (default; full sync + sidecar)
./scripts/start-sync.sh --hoodi

# Monitor sync + sidecar conversion progress
go run ./docker-ubt-test/cmd/ubtctl monitor

# Check UBT logs
go run ./docker-ubt-test/cmd/ubtctl logs --mode tail

# Quick sidecar sanity (finalized)
curl -s -X POST http://localhost:8545 \
  -H 'content-type: application/json' \
  --data '{"jsonrpc":"2.0","id":1,"method":"debug_getUBTProof","params":["0x0000000000000000000000000000000000000000",[], "finalized"]}'
```

After `eth_syncing` returns `false`, the sidecar auto-convert kicks in. Monitor conversion with:
```bash
go run ./docker-ubt-test/cmd/ubtctl logs --mode progress
```
When conversion completes, UBT RPCs like `debug_getUBTProof` and `debug_getUBTState` should return results.

## Directory Structure

```
docker-ubt-test/
├── docker-compose.yml        # Main compose file
├── docker-compose.override.yml  # Network-specific config (auto-generated)
├── cmd/
│   ├── ubtctl/               # Go monitoring tool (status/monitor/logs)
│   └── validate/             # Go validation tool
│       ├── main.go           # CLI entry point
│       ├── validator.go      # Core validation logic
│       ├── results.go        # Result formatting
│       └── build.sh          # Build script
├── geth/
│   ├── Dockerfile            # Builds Geth from source
│   └── config/
│       └── jwt.hex           # JWT secret for Engine API
├── lighthouse/
│   └── config/               # Lighthouse configuration
├── scripts/
│   ├── start-sync.sh         # Start the environment
│   ├── clean.sh              # Clear data directories
│   ├── validate-conversion.sh # Sidecar sanity (bash)
│   └── validate-ubt-complete.sh # Full sidecar validation (bash)
```

Data directory (default):
```
/mnt/q/ubt-sync/
├── geth-data/            # Geth chain data
└── lighthouse-data/      # Lighthouse beacon data
```

## Network Options

| Network | State Size | Sync Time | Conversion Time |
|---------|------------|-----------|-----------------|
| Hoodi (default) | ~10GB | ~2 hours | ~1 hour |
| Mainnet | ~300GB | ~8 hours | ~6 hours |

```bash
# Hoodi (default)
./scripts/start-sync.sh --hoodi

# Mainnet (production validation)
./scripts/start-sync.sh --mainnet
```

Times are rough estimates and will vary with hardware and network conditions.

## Scripts

### start-sync.sh

Builds and starts the Docker environment.

```bash
./scripts/start-sync.sh [--hoodi|--mainnet] [--data-dir /path] [--ubt-log-interval N] [--build-only]

Options:
  --hoodi       Use Hoodi testnet (default)
  --mainnet     Use Ethereum mainnet
  --data-dir    Host data directory (default: /mnt/q/ubt-sync)
  --ubt-log-interval  Log UBT state progress every N blocks (default: 1000)
  --build-only  Build images only, don't start containers
```

### ubtctl (Go Monitoring Tool)

Unified monitoring CLI for UBT sidecar conversion.

```bash
# One-shot status (includes UBT log summary)
go run ./docker-ubt-test/cmd/ubtctl status

# Live monitoring (sync + UBT progress)
go run ./docker-ubt-test/cmd/ubtctl monitor

# Log filtering (tail/all/errors/progress)
go run ./docker-ubt-test/cmd/ubtctl logs --mode tail
```

### validate-conversion.sh (basic health check)

Run basic RPC checks plus sidecar sanity (`debug_getUBTProof` / `debug_getUBTState`).

```bash
./scripts/validate-conversion.sh
```

### validate-ubt-complete.sh (sidecar health check)

Comprehensive validation of UBT sidecar conversion completion (bash script). Runs multiple checks:

```bash
./scripts/validate-ubt-complete.sh

Checks performed:
  1. Connectivity - Verifies Geth is accessible
  2. Sync Status - Confirms sync is complete
  3. Sidecar RPCs - Tests debug_getUBTProof, debug_getUBTState
  4. State Reads - Tests eth_getBalance, eth_getStorageAt
  5. Block Execution - Verifies new blocks are being processed
  6. Witness Generation - Tests debug_executionWitnessUBT
```

### Go Validation Tool (Recommended)

A Go-based validation tool with better structure and type safety:

```bash
# Build the tool
cd cmd/validate && ./build.sh

# Run validation (requires a reference MPT node with debug APIs)
./cmd/validate/ubt-validate \
  --ubt-rpc http://localhost:8545 \
  --reference-rpc http://<REF_NODE>:8545

# Select phases (0..5) or all (default)
./cmd/validate/ubt-validate --phases 0,1,2 --ubt-rpc http://localhost:8545 --reference-rpc http://<REF_NODE>:8545
```

Options:
| Flag | Default | Description |
|------|---------|-------------|
| `--ubt-rpc` | `http://localhost:8545` | UBT sidecar node RPC |
| `--reference-rpc` | (required) | Reference MPT node RPC (debug APIs required) |
| `--account-samples` | `30000` | Number of accounts to sample |
| `--storage-samples` | `500` | Storage slots per contract |
| `--phases` | `all` | Phases to run (0..5 or all) |

**Note:** The current validator compares standard eth/debug outputs (MPT). It does not yet compare `debug_getUBTState` directly. For sidecar-specific checks, use the RPC snippets below.

Example reference node (no sidecar, Hoodi):
```bash
geth --hoodi --syncmode=full --state.scheme=path --cache.preimages \
  --http --http.addr 0.0.0.0 --http.port 8545 \
  --http.api eth,net,web3,debug
```

### Sidecar RPC Checks (Direct)

```bash
# UBT root (finalized/safe recommended)
curl -s -X POST http://localhost:8545 \
  -H 'content-type: application/json' \
  --data '{"jsonrpc":"2.0","id":1,"method":"debug_getUBTProof","params":["0x0000000000000000000000000000000000000000",[], "finalized"]}'

# UBT account/storage reads
curl -s -X POST http://localhost:8545 \
  -H 'content-type: application/json' \
  --data '{"jsonrpc":"2.0","id":1,"method":"debug_getUBTState","params":["0x0000000000000000000000000000000000000000",[], "finalized"]}'

# UBT witness for latest block (uses sidecar)
curl -s -X POST http://localhost:8545 \
  -H 'content-type: application/json' \
  --data '{"jsonrpc":"2.0","id":1,"method":"debug_executionWitnessUBT","params":["latest"]}'
```

## Exposed Ports

| Port | Service | Description |
|------|---------|-------------|
| 8545 | Geth | HTTP RPC |
| 8546 | Geth | WebSocket |
| 8551 | Geth | Engine API (Auth RPC) |
| 6060 | Geth | Metrics/pprof |
| 30303 | Geth | P2P |
| 5052 | Lighthouse | HTTP API |
| 5054 | Lighthouse | Metrics |
| 9000 | Lighthouse | P2P |

## Test Scenarios

### 1. Fresh Sync with UBT Sidecar Conversion (Hoodi)

```bash
# Start fresh
./scripts/start-sync.sh --hoodi

# Monitor until sync completes (then sidecar auto-converts)
go run ./docker-ubt-test/cmd/ubtctl monitor

# Validate basic health + sidecar sanity
./scripts/validate-conversion.sh

# Sidecar sanity (finalized)
curl -s -X POST http://localhost:8545 \
  -H 'content-type: application/json' \
  --data '{"jsonrpc":"2.0","id":1,"method":"debug_getUBTProof","params":["0x0000000000000000000000000000000000000000",[], "finalized"]}'
```

## End-to-End Test (Hoodi, Sidecar)

This is the full, repeatable flow to get a Hoodi node syncing with UBT sidecar,
validate conversion, and run the validator end-to-end.

1. **Clean old data (optional but recommended)**
   ```bash
   ./scripts/clean.sh --data-dir /mnt/q/ubt-sync
   ```

2. **Start Hoodi (sidecar enabled by default)**
   ```bash
   ./scripts/start-sync.sh --hoodi
   ```

3. **Monitor sync + sidecar conversion**
   ```bash
   go run ./docker-ubt-test/cmd/ubtctl monitor
   ```

4. **Wait for sync completion**
   ```bash
   curl -s -X POST http://localhost:8545 \
     -H 'content-type: application/json' \
     --data '{"jsonrpc":"2.0","id":1,"method":"eth_syncing","params":[]}'
   ```
   When this returns `false`, full sync is complete and sidecar auto‑convert begins.

5. **Wait for sidecar readiness**
   ```bash
   curl -s -X POST http://localhost:8545 \
     -H 'content-type: application/json' \
     --data '{"jsonrpc":"2.0","id":1,"method":"debug_getUBTProof","params":["0x0000000000000000000000000000000000000000",[], "finalized"]}'
   ```
   A non‑zero `ubtRoot` means sidecar conversion is ready.

6. **Run local sanity checks**
   ```bash
   ./scripts/validate-conversion.sh
   ./scripts/validate-ubt-complete.sh
   ```

7. **Run full validator (requires reference MPT node)**
   Start a reference node on Hoodi (no sidecar):
   ```bash
   geth --hoodi --syncmode=full --state.scheme=path --cache.preimages \
     --http --http.addr 0.0.0.0 --http.port 8545 \
     --http.api eth,net,web3,debug
   ```

   Build + run the validator against both nodes:
   ```bash
   cd docker-ubt-test/cmd/validate && ./build.sh
   ./cmd/validate/ubt-validate \
     --ubt-rpc http://localhost:8545 \
     --reference-rpc http://<REF_NODE>:8545
   ```

If any step fails, check logs:
```bash
docker-compose logs -f geth
go run ./docker-ubt-test/cmd/ubtctl logs --mode progress
```

### 2. Interrupt and Resume

```bash
# Start sync
./scripts/start-sync.sh --hoodi

# Use status snapshots during conversion
go run ./docker-ubt-test/cmd/ubtctl status
```

### 3. Crash Recovery

```bash
# Start sync
./scripts/start-sync.sh --hoodi

# Simulate crash
docker kill geth-ubt-test

# Restart and verify recovery
docker-compose start geth
go run ./docker-ubt-test/cmd/ubtctl logs --mode progress
```

### 4. Full Mainnet Validation

```bash
# Start mainnet sync (long running)
./scripts/start-sync.sh --mainnet

# Monitor in background
go run ./docker-ubt-test/cmd/ubtctl monitor > sync.log 2>&1 &

# After completion (~12 hours)
./scripts/validate-conversion.sh
```

## Useful Commands

```bash
# View Geth logs
docker-compose logs -f geth

# View Lighthouse logs
docker-compose logs -f lighthouse

# Enter Geth container
docker exec -it geth-ubt-test sh

# Attach to Geth console
docker exec -it geth-ubt-test geth attach /tmp/geth.ipc

# Stop everything
docker-compose down

# Clean all data and start fresh
docker-compose down -v
./scripts/clean.sh --data-dir /mnt/q/ubt-sync
./scripts/start-sync.sh --data-dir /mnt/q/ubt-sync
```

## RPC Methods

### Standard Ethereum Methods
- `eth_syncing` - Check sync status
- `eth_blockNumber` - Get current block
- `eth_getBalance` - Get account balance
- `eth_getStorageAt` - Get storage value

### Debug Methods
- `debug_executionWitness` - Generate execution witness for a block
- `debug_executionWitnessUBT` - Generate UBT witness for a block (sidecar)
- `debug_getUBTProof` - Get UBT proof for account/storage (sidecar)
- `debug_getUBTState` - Read account/storage from UBT sidecar

## Hardware Requirements

| Component | Minimum | Recommended |
|-----------|---------|-------------|
| CPU | 4 cores | 8+ cores |
| RAM | 16GB | 32GB |
| Disk | 1TB SSD | 2TB NVMe |
| Network | 100 Mbps | 1 Gbps |

## Troubleshooting

### Geth not starting

Check logs:
```bash
docker-compose logs geth
```

Common issues:
- Port already in use: Change ports in docker-compose.yml
- Out of disk space: Clear data directory

### Lighthouse can't connect to Geth

1. Verify JWT secret matches:
   ```bash
   cat geth/config/jwt.hex
   ```

2. Check Engine API is accessible:
   ```bash
   curl -X POST http://localhost:8551 \
     -H "Content-Type: application/json" \
     -d '{"jsonrpc":"2.0","method":"engine_exchangeCapabilities","params":[[]],"id":1}'
   ```

### Sync stuck

1. Check peer count:
   ```bash
   curl -X POST http://localhost:8545 \
     -H "Content-Type: application/json" \
     -d '{"jsonrpc":"2.0","method":"net_peerCount","id":1}'
   ```

2. Restart with fresh peers:
   ```bash
   docker-compose restart geth
   ```

## Validation Checklist

- [ ] Full sync completes successfully with UBT enabled
- [ ] UBT state is built correctly during sync
- [ ] Account/storage data is accessible via RPC
- [ ] Block execution works with UBT state
- [ ] Witness generation produces valid proofs

## UBT Flags

| Flag | Description |
|------|-------------|
| `--ubt.sidecar` | Enable UBT sidecar (shadow UBT state; MPT remains consensus) |
| `--ubt.sidecar.autoconvert` | Auto-convert after full sync completes |
| `--ubt.log-interval N` | Log UBT state progress every N blocks (0 = disable) |
| `--state.scheme path` | Required: use PathDB state scheme |
| `--cache.preimages` | Required: enable preimage storage |

## Notes

- UBT sidecar is experimental and requires `--state.scheme=path` + `--cache.preimages`
- Sidecar RPC uses `debug_getUBTProof` and `debug_getUBTState`
