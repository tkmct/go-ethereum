# ubtconv - External UBT Conversion Daemon

The `ubtconv` daemon is a separate process that maintains a UBT (Unified Binary Trie) by consuming state diff events from a geth node via RPC.

## Architecture

The daemon consists of the following components:

### Core Components

- **main.go** - CLI entry point with flag parsing and signal handling
- **config.go** - Configuration validation and defaults
- **runner.go** - Daemon lifecycle manager that orchestrates the consumer loop
- **consumer.go** - Event consumption orchestration with crash-consistent checkpointing
- **applier.go** - UBT trie operations (applies diffs to the binary trie)
- **outbox_reader.go** - RPC client for reading outbox events from geth
- **query_server.go** - JSON-RPC server for querying UBT state
- **validate.go** - Account, storage, and code validation against MPT via geth RPC
- **phase.go** - Phase state machine for migration workflow
- **slot_index.go** - Storage slot index for pre-Cancun replay correctness
- **replay_client.go** - Block replay via `debug_traceBlockByNumber` for deep recovery
- **state_adapter.go** - StateDB adapter for UBT-backed EVM execution
- **metrics.go** - Prometheus-compatible metrics for daemon observability

### Data Flow

```
geth (outbox) --> [RPC] --> OutboxReader --> Consumer --> Applier --> UBT Trie DB
                                                 |
                                                 v
                                         Consumer State DB
                                         (checkpoint: pendingSeq, appliedSeq, appliedRoot)
```

### Consumer State Management

The consumer maintains crash-consistent state with these fields:

- **PendingSeq**: The sequence number currently being processed (0 if none)
- **AppliedSeq**: The last fully applied sequence number
- **AppliedRoot**: The UBT root hash after applying AppliedSeq
- **AppliedBlock**: The block number corresponding to AppliedSeq

On startup, the consumer:
1. Loads the checkpoint from disk
2. If PendingSeq > 0, it means the last consume was interrupted - restart from AppliedSeq + 1
3. If the trie DB root doesn't match AppliedRoot, attempt anchor snapshot recovery
4. Otherwise, continue from AppliedSeq + 1

### Commit Policy

The daemon commits UBT state to disk based on two conditions (whichever comes first):

1. **Block interval**: After `apply-commit-interval` blocks (default: 128)
2. **Time latency**: After `apply-commit-max-latency` time has passed (default: 10s)

### Reorg Handling

Reorg recovery uses a two-path strategy:

1. **Fast path**: If the common ancestor root exists in the trie DB diff layers, revert directly
2. **Slow path**: Restore from the nearest anchor snapshot, then replay blocks forward via archive node

### Anchor Snapshots

Periodic anchor snapshots capture `(blockNumber, blockRoot, seq)` tuples. These enable recovery from deep reorgs and startup corruption by providing known-good rollback points.

## Query RPC API

When `--query-rpc-enabled` is set, the daemon exposes a JSON-RPC server with these methods:

| Method | Description |
|--------|-------------|
| `ubt_status` | Current daemon status, phase, lag, and root |
| `ubt_getBalance` | Account balance from UBT state |
| `ubt_getStorageAt` | Storage slot value from UBT state |
| `ubt_getCode` | Contract bytecode from UBT state |
| `ubt_getProof` | Merkle inclusion proof for an account |
| `ubt_verifyProof` | Verify a proof against a given root |
| `ubt_safeCompactSeq` | Safe sequence for outbox compaction |
| `ubt_executionWitnessUBT` | Execution witness for a block |

## Validation Modes

### Standard Validation
By default, the consumer validates each applied diff's root hash against the expected UBT root provided in the outbox event.

### Strict Validation (`--validation-strict`)
Cross-checks every account balance, nonce, storage slot, and code against MPT state via geth RPC. Enable `--validation-halt-on-mismatch` to halt on any discrepancy.

### Validate-Only Mode (`--validate-only-mode`)
Reads outbox events and validates against MPT but does NOT apply changes to the UBT trie. Useful for shadow verification before production cutover.

## Migration Workflow

The daemon tracks its operational phase via a state machine:

| Phase | Meaning |
|-------|---------|
| `initializing` | Daemon is starting up |
| `catching-up` | Processing backlog (lag > threshold) |
| `synced` | Within threshold of chain head |
| `diverged` | Error or validation failure detected |
| `validate-only` | Running in validate-only mode |

**Production readiness** requires: synced phase sustained for `--production-readiness-min` (default: 10m) with >100 consecutive successful validations.

## Slot Index Policy

The slot index tracks which storage slots were created/modified before the Cancun hard fork. This metadata supports correct replay of pre-Cancun state transitions.

| Mode | Behavior |
|------|----------|
| `auto` (default) | Index pre-Cancun slots, freeze at Cancun boundary |
| `on` | Always index all slots |
| `off` | Disable slot indexing |

Configure with `--slot-index-mode` and optionally limit disk usage with `--slot-index-disk-budget`.

## Usage

```bash
# Start the daemon with default settings
ubtconv --outbox-rpc-endpoint http://localhost:8545 --datadir ./ubtconv-data

# Customize commit policy
ubtconv \
  --outbox-rpc-endpoint http://localhost:8545 \
  --datadir ./ubtconv-data \
  --apply-commit-interval 256 \
  --apply-commit-max-latency 30s

# Use backfill mode (requires archive node)
ubtconv \
  --outbox-rpc-endpoint http://localhost:8545 \
  --datadir ./ubtconv-data \
  --bootstrap-mode backfill-direct

# Validate-only mode (shadow verification)
ubtconv \
  --outbox-rpc-endpoint http://localhost:8545 \
  --datadir ./ubtconv-data \
  --validate-only-mode

# Strict validation with halt on mismatch
ubtconv \
  --outbox-rpc-endpoint http://localhost:8545 \
  --datadir ./ubtconv-data \
  --validation-strict \
  --validation-halt-on-mismatch
```

## Configuration Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--outbox-rpc-endpoint` | `http://localhost:8545` | Geth RPC endpoint for outbox consumption |
| `--datadir` | `./ubtconv-data` | Data directory for UBT trie database |
| `--apply-commit-interval` | `128` | Number of blocks between UBT trie commits |
| `--apply-commit-max-latency` | `10s` | Maximum time between UBT trie commits |
| `--bootstrap-mode` | `tail` | Bootstrap mode: `tail` or `backfill-direct` |
| `--max-recoverable-reorg-depth` | `128` | Maximum reorg depth for fast-path recovery |
| `--triedb-scheme` | `path` | Trie database scheme (must be `path`) |
| `--triedb-state-history` | `90000` | Number of blocks of state history to retain |
| `--require-archive-replay` | `true` | Require archive node for deep replay |
| `--query-rpc-enabled` | `true` | Enable UBT query RPC server |
| `--query-rpc-listen-addr` | `localhost:8560` | Listen address for UBT query RPC server |
| `--query-rpc-max-batch` | `100` | Maximum batch size for list-style UBT RPC methods |
| `--outbox-disk-budget-bytes` | `0` (unlimited) | Maximum disk usage for outbox events |
| `--outbox-alert-threshold-pct` | `80` | Disk usage percentage to trigger compaction alert |
| `--slot-index-mode` | `auto` | Slot index mode: `auto`, `on`, or `off` |
| `--slot-index-disk-budget` | `0` (unlimited) | Maximum disk usage for slot index |
| `--validation-strict` | `true` | Enable strict cross-validation against MPT |
| `--validation-halt-on-mismatch` | `false` | Halt on validation mismatch |
| `--validate-only-mode` | `false` | Read and validate events without applying |
| `--synced-lag-threshold` | `10` | Block lag threshold to consider synced |
| `--production-readiness-min` | `10m` | Duration synced before production ready |

## Data Directory Structure

```
ubtconv-data/
├── consumer/        # LevelDB: Consumer checkpoint state
└── triedb/          # LevelDB: UBT trie nodes (path scheme)
```

## Building

```bash
cd cmd/ubtconv
go build -o ubtconv .
```

## Development Status

- [x] Basic daemon skeleton with lifecycle management
- [x] Consumer with crash-consistent checkpointing
- [x] Applier with UBT trie operations
- [x] Commit policy (block interval + time latency)
- [x] RPC client for reading outbox events
- [x] Reorg recovery (fast-path + slow-path with anchor snapshots)
- [x] Query RPC server with state/proof/witness endpoints
- [x] Bootstrap backfill mode (direct from archive node)
- [x] Strict validation (account, storage, code cross-check)
- [x] Validate-only mode for shadow verification
- [x] Phase state machine for migration workflow
- [x] Slot index for pre-Cancun storage tracking
- [x] Observability metrics (daemon, proxy, recovery)
- [x] Outbox compaction coordination with disk budget
- [x] Merkle proof generation and verification
- [x] Startup recovery from anchor snapshots

## Dependencies

- `github.com/ethereum/go-ethereum` - Core geth libraries
- `github.com/urfave/cli/v2` - CLI framework
- LevelDB - Storage backend

## License

Copyright 2024 The go-ethereum Authors. Licensed under the GNU General Public License v3.0.
