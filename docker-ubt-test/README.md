# UBT Docker Test Environment

Test environment for validating MPT to UBT (Universal Binary Trie) conversion using Geth with Lighthouse consensus client.

## Overview

This environment provides:
- Geth built from source with UBT support
- Lighthouse for consensus layer (checkpoint sync)
- Monitoring and validation scripts
- Support for Sepolia, Holesky, and Mainnet

## Quick Start

```bash
# Start Sepolia testnet (fastest, ~2 hours sync)
./scripts/start-sync.sh --sepolia

# Monitor sync progress
./scripts/monitor-sync.sh

# Check UBT logs
./scripts/check-ubt-logs.sh
```

## Directory Structure

```
docker-ubt-test/
├── docker-compose.yml        # Main compose file
├── docker-compose.override.yml  # Network-specific config (auto-generated)
├── geth/
│   ├── Dockerfile            # Builds Geth from source
│   └── config/
│       └── jwt.hex           # JWT secret for Engine API
├── lighthouse/
│   └── config/               # Lighthouse configuration
├── scripts/
│   ├── start-sync.sh         # Start the environment
│   ├── monitor-sync.sh       # Monitor sync progress
│   ├── check-ubt-logs.sh     # Filter UBT-related logs
│   ├── validate-conversion.sh # Validate conversion
│   └── test-interrupt-resume.sh # Test interrupt/resume
└── data/
    ├── geth-data/            # Geth chain data
    └── lighthouse-data/      # Lighthouse beacon data
```

## Network Options

| Network | State Size | Sync Time | Conversion Time |
|---------|------------|-----------|-----------------|
| Sepolia | ~5GB | ~1 hour | ~30 min |
| Holesky | ~50GB | ~4 hours | ~2 hours |
| Mainnet | ~300GB | ~8 hours | ~6 hours |

```bash
# Sepolia (default, fastest)
./scripts/start-sync.sh --sepolia

# Holesky (medium scale)
./scripts/start-sync.sh --holesky

# Mainnet (production validation)
./scripts/start-sync.sh --mainnet
```

## Scripts

### start-sync.sh

Builds and starts the Docker environment.

```bash
./scripts/start-sync.sh [--sepolia|--holesky|--mainnet] [--build-only]

Options:
  --sepolia     Use Sepolia testnet (default)
  --holesky     Use Holesky testnet
  --mainnet     Use Ethereum mainnet
  --build-only  Build images only, don't start containers
```

### monitor-sync.sh

Real-time monitoring of Geth and Lighthouse sync progress.

```bash
./scripts/monitor-sync.sh

Environment variables:
  GETH_RPC=http://localhost:8545
  LIGHTHOUSE_API=http://localhost:5052
  INTERVAL=10
```

### check-ubt-logs.sh

Filter Geth logs for UBT-related entries.

```bash
./scripts/check-ubt-logs.sh [tail|all|errors|progress]

Modes:
  tail      Follow logs in real-time (default)
  all       Show all UBT-related entries
  errors    Show only error entries
  progress  Show progress-related entries
```

### validate-conversion.sh

Run validation checks on the converted state.

```bash
./scripts/validate-conversion.sh
```

### test-interrupt-resume.sh

Test that UBT conversion resumes correctly after node restart.

```bash
./scripts/test-interrupt-resume.sh
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

### 1. Fresh Sync with UBT Conversion

```bash
# Start fresh
./scripts/start-sync.sh --sepolia

# Monitor until sync completes
./scripts/monitor-sync.sh

# Validate conversion
./scripts/validate-conversion.sh
```

### 2. Interrupt and Resume

```bash
# Start sync
./scripts/start-sync.sh --sepolia

# Wait for some progress, then run test
./scripts/test-interrupt-resume.sh
```

### 3. Crash Recovery

```bash
# Start sync
./scripts/start-sync.sh --sepolia

# Simulate crash
docker kill geth-ubt-test

# Restart and verify recovery
docker-compose start geth
./scripts/check-ubt-logs.sh progress
```

### 4. Full Mainnet Validation

```bash
# Start mainnet sync (long running)
./scripts/start-sync.sh --mainnet

# Monitor in background
./scripts/monitor-sync.sh > sync.log 2>&1 &

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
docker exec -it geth-ubt-test geth attach /root/.ethereum/geth.ipc

# Stop everything
docker-compose down

# Clean all data and start fresh
docker-compose down -v
rm -rf data/geth-data/* data/lighthouse-data/*
./scripts/start-sync.sh
```

## RPC Methods

### Standard Ethereum Methods
- `eth_syncing` - Check sync status
- `eth_blockNumber` - Get current block
- `eth_getBalance` - Get account balance
- `eth_getStorageAt` - Get storage value

### Debug Methods (TODO: Implement)
- `debug_ubtConversionStatus` - Get UBT conversion progress
- `debug_startUbtConversion` - Manually start conversion
- `debug_stopUbtConversion` - Stop ongoing conversion

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

- [ ] Snap sync completes successfully
- [ ] UBT conversion starts automatically after sync
- [ ] Progress is persisted across restarts
- [ ] Conversion resumes correctly after crash
- [ ] No memory leaks during long conversion
- [ ] Final UBT root is deterministic
- [ ] Account/storage data matches MPT state
- [ ] Block execution works with converted UBT
- [ ] Witness generation produces valid proofs

## UBT Flags

| Flag | Description |
|------|-------------|
| `--state.ubt` | Enable UBT/BinaryTrie state backend (experimental) |
| `--ubt.batchsize N` | Accounts per batch during conversion (default: 1000) |
| `--ubt.noconversion` | Disable automatic MPT→UBT conversion after snap sync |
| `--state.scheme path` | Required: use PathDB state scheme |
| `--state.skiproot` | Auto-enabled with --state.ubt |

## Notes

- UBT is experimental and requires `--state.scheme=path`
- Debug RPC methods for UBT status need to be added
- Current tests validate the conversion framework
