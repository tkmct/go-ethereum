# UBT Docker Test Environment

Test environment for validating UBT (Universal Binary Trie) sidecar state using Geth with Lighthouse consensus client. The primary workflow is syncing against a public testnet.

## Architecture

### Mode A: Public Testnet (Hoodi / Mainnet)

Syncs a single Geth+Lighthouse pair from a public network using checkpoint sync.
Geth runs with `--ubt.sidecar` so it builds a shadow UBT state alongside the
canonical MPT state starting from genesis.

```
                    Public Network
                         |
                   checkpoint-sync
                         |
              +----------+----------+
              |                     |
     +--------v--------+  +--------v--------+
     | lighthouse       |  | geth             |
     | (beacon node)    |  | (execution layer)|
     | container:       |  | container:       |
     |  lighthouse-     |  |  geth-ubt-test   |
     |  ubt-test        |  |                  |
     |                  |  | --ubt.sidecar    |
     |  :5052 HTTP API  |  |                  |
     |  :9000 P2P       |  |                  |
     +--------+---------+  |                  |
              |             |  :8545 RPC       |
              +--Engine API-+  :8551 Engine    |
                (jwt.hex)      :30303 P2P      |
                            +------------------+
                                    |
                           /mnt/q/ubt-sync/
                          (geth-data, lighthouse-data)
```

**Compose files:** `docker-compose.yml` + `docker-compose.override.yml`

**Flow:**
1. `scripts/public/start-sync.sh --hoodi` builds Geth from source, generates override, starts containers
2. Lighthouse checkpoint-syncs the beacon chain, drives Geth via Engine API
3. Geth full-syncs the execution layer (MPT state)
4. UBT sidecar is seeded from genesis at startup and updated per block during sync
5. UBT RPCs (`debug_getUBTProof`, `debug_getUBTState`) are available throughout sync

## Quick Start

### Public Testnet (Hoodi)

```bash
cd docker-ubt-test

# Start (builds Geth from source + Lighthouse)
./scripts/public/start-sync.sh --hoodi

# Monitor
go run ./cmd/ubtctl monitor

# Validate after sync
go run ./cmd/ubtcheck --mode quick
```

## Hoodi Checklist

1) Start sync:

```bash
./scripts/public/start-sync.sh --hoodi
```

Optional: enable per-block UBT vs MPT sanity checks:

```bash
./scripts/public/start-sync.sh --hoodi --ubt-sanity
```

2) Watch progress:

```bash
go run ./cmd/ubtctl monitor
```

3) Confirm UBT is live during sync (use `latest`):

```bash
curl -s http://localhost:8545 -X POST -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"debug_getUBTProof","params":["0x0000000000000000000000000000000000000000",[],"latest"]}' \
  | jq -r .result.ubtRoot
```

Expect a non-zero root.

4) Run quick validator:

```bash
go run ./cmd/ubtcheck --mode quick --block-tag latest
```

5) (Optional) After sync, run full validator:

```bash
go run ./cmd/ubtcheck --mode full --block-tag latest
```

## Hoodi Automated Smoke Test

Run a single command to wait for RPC, wait for a non-zero UBT root, then invoke
`ubtcheck`.

```bash
go run ./cmd/hoodi-smoke --ubtcheck-mode quick --block-tag latest
```

Flags you may want to tune:

- `--connect-timeout` (default: 2m)
- `--ubt-timeout` (default: 5m)
- `--poll-interval` (default: 5s)
- `--ubtcheck-timeout` (default: 30m)

## Directory Structure

```
docker-ubt-test/
├── docker-compose.yml            # Public testnet compose
├── docker-compose.override.yml   # Network-specific overrides (auto-generated)
├── cmd/
│   ├── ubtctl/                   # Go monitoring tool (status/monitor/logs)
│   │   └── main.go
│   ├── ubtcheck/                 # Go sidecar validation (quick/full)
│   │   └── main.go
│   ├── hoodi-smoke/              # Hoodi smoke test (auto)
│   │   └── main.go
│   └── validate/                 # Go validation tool (multi-phase)
│       ├── main.go
│       ├── validator.go
│       ├── phase0_precondition.go
│       ├── phase1_ubt_status.go
│       ├── phase2_values.go
│       ├── phase3_transition.go
│       ├── phase4_witness.go
│       ├── phase5_rpc.go
│       ├── results.go
│       └── types.go
├── geth/
│   ├── Dockerfile                # Builds Geth from source with UBT support
│   └── config/
│       └── jwt.hex               # JWT secret for Engine API
├── scripts/
│   └── public/                   # Public testnet helpers
│       ├── start-sync.sh          # Start public testnet environment
│       └── clean.sh               # Clear data directories
└── data/                         # Data dir for public testnet sync (gitignored)
```

## Exposed Ports

### Public Testnet

| Port | Service | Description |
|------|---------|-------------|
| 8545 | Geth | HTTP RPC |
| 8546 | Geth | WebSocket |
| 8551 | Geth | Engine API |
| 6060 | Geth | pprof |
| 6061 | Geth | Metrics |
| 30303 | Geth | P2P |
| 5052 | Lighthouse | HTTP API |
| 5054 | Lighthouse | Metrics |
| 9000 | Lighthouse | P2P |
| 8552 | geth-seed | Engine API (seed) |
| 6061 | geth-ubt | Metrics (UBT) |
| 6062 | geth-seed | Metrics (seed) |
| 5052 | lighthouse-bn-ubt | HTTP API (UBT) |
| 5053 | lighthouse-bn-seed | HTTP API (seed) |
| 30303 | geth-ubt | P2P (UBT) |
| 30305 | geth-seed | P2P (seed) |
| 9000 | lighthouse-bn-ubt | P2P (UBT) |
| 9001 | lighthouse-bn-seed | P2P (seed) |

## Tools

### ubtctl

Monitoring CLI for UBT sidecar status (public testnet mode).

```bash
go run ./cmd/ubtctl status    # One-shot status
go run ./cmd/ubtctl monitor   # Live monitoring
go run ./cmd/ubtctl logs --mode tail      # Follow logs
go run ./cmd/ubtctl logs --mode progress  # Sidecar progress
```

### ubtcheck

Quick sidecar validation (single node, no reference required).

```bash
go run ./cmd/ubtcheck --mode quick   # Fast checks
go run ./cmd/ubtcheck --mode full    # Includes block progression + witness
```

### validate

Multi-phase validation tool. Compares UBT sidecar node against a reference MPT node.

```bash
go run ./cmd/validate \
  --ubt-rpc http://localhost:8545 \
  --reference-rpc http://<REF_NODE>:8545 \
  --phases all
```

Phases: 0 (preconditions), 1 (UBT status), 2 (values), 3 (transition), 4 (witness), 5 (RPC).

### Sidecar RPC Checks

```bash
# UBT proof
curl -s -X POST http://localhost:8545 \
  -H 'content-type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"debug_getUBTProof","params":["0x0000000000000000000000000000000000000000",[],"finalized"]}'

# UBT state read
curl -s -X POST http://localhost:8545 \
  -H 'content-type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"debug_getUBTState","params":["0x0000000000000000000000000000000000000000",[],"finalized"]}'

# UBT execution witness
curl -s -X POST http://localhost:8545 \
  -H 'content-type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"debug_executionWitnessUBT","params":["latest"]}'
```

## UBT Geth Flags

| Flag | Description |
|------|-------------|
| `--ubt.sidecar` | Enable UBT sidecar (shadow UBT state; MPT remains consensus) |
| `--ubt.log-interval N` | Log UBT state progress every N blocks (0 = disable) |
| `--state.scheme path` | Required: use PathDB state scheme |
| `--cache.preimages` | Required: enable preimage storage |

## Troubleshooting

**Geth not starting:** `docker compose logs geth` -- common causes are port conflicts or disk full.

**Lighthouse can't reach Geth:** Verify the JWT secret matches on both sides (`cat geth/config/jwt.hex`).

**Sync stuck:** Check peer count with `net_peerCount` RPC, restart with `docker compose restart geth`.

**Devnet genesis fails:** The genesis generator image may have changed its internal paths. The orchestrator tries three known locations for `defaults.env`; if all fail it logs the image name so you can inspect it with `docker run --rm -it <image> sh`.
