# ubtconv - External UBT Conversion Daemon

`ubtconv` is an external daemon that consumes UBT outbox events from `geth` and maintains a UBT (Unified Binary Trie) view with query APIs.

## Scope and Current Assumptions

- `geth` sync mode is **full-sync only** (`--syncmode full`).
- Snap-sync/bootstrap-backfill flows are out of scope.
- Mainnet operation requires an EL+CL stack (`geth` + consensus client such as `lighthouse`) and `ubtconv`.
- Execution-class RPC methods are implemented but disabled by default; enable with `--execution-class-rpc-enabled`.

## Architecture

### Main Components

- `main.go`: CLI entrypoint and signal handling.
- `runner.go`: daemon lifecycle and retry loop.
- `consumer.go`: event consumption and checkpointing.
- `applier.go`: UBT trie apply/revert/proof operations.
- `outbox_reader.go`: RPC reader for geth outbox methods.
- `query_server.go`: JSON-RPC query server (`ubt_*`).
- `validate.go`: strict validation against MPT via geth RPC.
- `slot_index.go`: pre-Cancun slot tracking for replay correctness.
- `replay_client.go`: archive replay client for deep recovery.

### Data Flow

```text
geth outbox RPC (ubt_getEvent/ubt_getEvents)
    -> OutboxReader
    -> Consumer
    -> Applier
    -> UBT trie DB (triedb)

Consumer checkpoints are persisted in a separate DB (consumer/).
```

## Consumer State and Recovery

Persisted state fields:

- `PendingSeq`, `PendingState`, `PendingUpdatedAt`
- `AppliedSeq`, `AppliedBlock`, `AppliedRoot`

Startup behavior:

1. Load persisted checkpoint.
2. If pending state indicates interrupted apply, clear pending metadata and resume from `AppliedSeq + 1`.
3. Open trie at `AppliedRoot`.
4. If opening fails, attempt anchor recovery.

Commit policy:

- Commit by block interval: `--apply-commit-interval` (default `128`).
- Commit by time: `--apply-commit-max-latency` (default `10s`).
- Backpressure commit trigger: `outboxLag > --backpressure-lag-threshold`.

## Mainnet Node Startup

### Option A: Recommended one-command script

Use the integrated startup script for `geth + lighthouse + ubtconv`:

```bash
CHECKPOINT_SYNC_URL='https://mainnet.checkpoint.sigp.io' \
scripts/run_mainnet_geth_ubtconv.sh \
  --action up \
  --enable-execution-rpc \
  --detach
```

Useful commands:

```bash
scripts/run_mainnet_geth_ubtconv.sh --action status --skip-build
scripts/run_mainnet_geth_ubtconv.sh --action down --skip-build
```

Notes:

- Script forces geth full-sync (`--syncmode full`).
- Script waits for `Lighthouse started` in lighthouse log before continuing.
- Lighthouse HTTP readiness can lag after process startup; script warns and continues if process is healthy.
- Script defaults ubtconv outbox ingest to geth IPC (`${GETH_DATADIR}/geth.ipc`) for lower RPC overhead.
  Override with `UBT_OUTBOX_RPC_ENDPOINT=http://127.0.0.1:8545` when HTTP is preferred.
- Logs are under `${WORKDIR:-$HOME/.ubt-mainnet}/logs/`.

### Option B: Manual startup

### 1. Build binaries

```bash
go build -o build/bin/geth ./cmd/geth
go build -o build/bin/ubtconv ./cmd/ubtconv
```

### 2. Create JWT secret for EL<->CL auth

```bash
mkdir -p /tmp/ubt-mainnet
head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n' > /tmp/ubt-mainnet/jwtsecret.hex
```

### 3. Start geth (mainnet full-sync + UBT outbox/debug proxy)

```bash
build/bin/geth \
  --mainnet \
  --syncmode full \
  --datadir /path/to/geth-datadir \
  --http --http.addr 127.0.0.1 --http.port 8545 \
  --http.api eth,net,web3,debug,ubt \
  --authrpc.addr 127.0.0.1 --authrpc.port 8551 \
  --authrpc.jwtsecret /tmp/ubt-mainnet/jwtsecret.hex \
  --ubt.conversion-enabled \
  --ubt.decoupled \
  --ubt.outbox-db-path /path/to/geth-outbox \
  --ubt.outbox-retention-seq-window 100000 \
  --ubt.reorg-marker-enabled \
  --ubt.outbox-read-rpc-enabled \
  --ubt.debug-rpc-proxy-enabled \
  --ubt.debug-endpoint http://127.0.0.1:8560 \
  --ubt.debug-timeout 5s
```

### 4. Start lighthouse beacon node

```bash
lighthouse bn \
  --network mainnet \
  --datadir /path/to/lighthouse-datadir \
  --execution-endpoint http://127.0.0.1:8551 \
  --execution-jwt /tmp/ubt-mainnet/jwtsecret.hex \
  --http --http-address 127.0.0.1 --http-port 5052 \
  --checkpoint-sync-url https://mainnet.checkpoint.sigp.io
```

### 5. Start ubtconv

```bash
build/bin/ubtconv \
  --outbox-rpc-endpoint /path/to/geth-datadir/geth.ipc \
  --datadir /path/to/ubtconv-datadir \
  --query-rpc-enabled \
  --query-rpc-listen-addr 127.0.0.1:8560 \
  --triedb-scheme path \
  --triedb-state-history 90000 \
  --validation-strict=true \
  --require-archive-replay=true \
  --execution-class-rpc-enabled
```

## First Health Checks

### 1. RPC modules

```bash
curl -s -H 'Content-Type: application/json' \
  --data '{"jsonrpc":"2.0","id":1,"method":"rpc_modules","params":[]}' \
  http://127.0.0.1:8545 | jq

curl -s -H 'Content-Type: application/json' \
  --data '{"jsonrpc":"2.0","id":1,"method":"rpc_modules","params":[]}' \
  http://127.0.0.1:8560 | jq
```

Expected:

- geth: includes `ubt`, `debug`, `eth`.
- ubtconv: includes `ubt`.

### 2. Progress checks

```bash
curl -s -H 'Content-Type: application/json' \
  --data '{"jsonrpc":"2.0","id":1,"method":"ubt_status","params":[]}' \
  http://127.0.0.1:8545 | jq

curl -s -H 'Content-Type: application/json' \
  --data '{"jsonrpc":"2.0","id":1,"method":"ubt_latestSeq","params":[]}' \
  http://127.0.0.1:8545 | jq

curl -s -H 'Content-Type: application/json' \
  --data '{"jsonrpc":"2.0","id":1,"method":"ubt_status","params":[]}' \
  http://127.0.0.1:8560 | jq
```

Expected:

- geth side: `enabled=true`, `latestSeq` increases.
- ubtconv side: `appliedSeq/appliedBlock/appliedRoot` advance.

## Query RPC API (`ubt_*`)

| Method | Description |
|---|---|
| `ubt_status` | Daemon status (`appliedSeq`, `appliedBlock`, `appliedRoot`, lag) |
| `ubt_getBalance` | Balance at selected block/root |
| `ubt_getStorageAt` | Storage slot value at selected block/root |
| `ubt_getCode` | Account bytecode at selected block/root |
| `ubt_getProof` | Raw-key UBT proof |
| `ubt_getAccountProof` | Account + storage proofs |
| `ubt_verifyProof` | Proof verification against root |
| `ubt_safeCompactSeq` | Safe seq for outbox compaction |
| `ubt_callUBT` | EVM call on UBT state (flag-gated) |
| `ubt_executionWitnessUBT` | Execution witness snapshot (flag-gated) |

## Execution-class RPC (`ubt_callUBT`, `ubt_executionWitnessUBT`)

- Disabled by default.
- Enable with `--execution-class-rpc-enabled`.
- Disabled error message includes: `execution-class RPC disabled`.

### Quick parity check (`eth_call` vs UBT call)

```bash
IDENTITY=0x0000000000000000000000000000000000000004
DATA=0x1122334455

curl -s -H 'Content-Type: application/json' \
  --data "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"eth_call\",\"params\":[{\"to\":\"${IDENTITY}\",\"data\":\"${DATA}\"},\"latest\"]}" \
  http://127.0.0.1:8545 | jq

curl -s -H 'Content-Type: application/json' \
  --data "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"debug_callUBT\",\"params\":[{\"to\":\"${IDENTITY}\",\"data\":\"${DATA}\"},\"latest\",null,null]}" \
  http://127.0.0.1:8545 | jq

curl -s -H 'Content-Type: application/json' \
  --data "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"ubt_callUBT\",\"params\":[{\"to\":\"${IDENTITY}\",\"data\":\"${DATA}\"},\"latest\",null,null]}" \
  http://127.0.0.1:8560 | jq
```

### Witness notes

- `ubt_executionWitnessUBT` currently returns `status: "partial"` by design.
- `accountsTouched/storageTouched/codeTouched` can be empty depending on selected block/root.
- For strict direct/proxy comparison, query both methods with the **same explicit block**.

## Catch-up Throughput Controls

When backlog is high (`outboxLag > backpressure-lag-threshold`), ubtconv now:

1. Samples strict validation by `--validation-strict-catchup-sample-rate`.
2. Uses prefetch for outbox reads via `--outbox-read-batch` (default disabled).
3. Coalesces duplicate account/storage/code mutations per diff (last-write-wins).
4. Applies adaptive write shedding for pending state and block-root index writes while lag is high.
5. Avoids per-block backpressure commits (uses a bounded faster-commit policy).
6. Skips strict block validation early when geth returns `historical state ... is not available`.

Mainnet full-sync recommendation:
- Keep `--outbox-read-batch=1` unless profiling proves improvement in your environment.
- Keep `--validation-strict-catchup-sample-rate=1` by default.

## Profiling and Bottleneck Analysis

`ubtconv` now exposes per-stage metrics for catch-up analysis:

- Outbox read path: event/range RPC latency and queue-hit counters.
- Decode/apply path: diff decode, reorg decode, diff apply latency.
- Apply internals: account/storage/code phase latency and entry counters.
- Commit/compaction: trie commit latency, batch write latency, compaction total/RPC latency.

Enable pprof when deep profiling is needed:

```bash
build/bin/ubtconv \
  ... \
  --pprof-enabled \
  --pprof-listen-addr 127.0.0.1:6061
```

Then capture profiles:

```bash
go tool pprof http://127.0.0.1:6061/debug/pprof/profile?seconds=30
go tool pprof http://127.0.0.1:6061/debug/pprof/heap
```

## Selector and Parity Guidance

Supported selectors:

- `latest`
- explicit block number
- block hash (canonical)

Rejected selectors:

- `pending`, `safe`, `finalized`

For deterministic comparisons between geth debug proxy and ubtconv direct RPC:

1. Read `ubt_status` from ubtconv.
2. Use `appliedBlock` as fixed selector (for both endpoints).
3. Avoid comparing two independent `latest` calls taken at different timestamps.

## Full-sync caveats

1. geth full-sync does not serve arbitrary historical state forever.
2. During initial catch-up, `eth_getBalance`/`eth_call` on older block selectors can fail with `historical state ... is not available`.
3. UBT has its own retained history window (`--triedb-state-history`); requests outside that window return state-not-available errors.

## Pre-Cancun storage preimage behavior

- This branch persists `(address, slotHash) -> rawSlot` mappings needed for pre-Cancun UBT diff emission.
- Mainnet full-sync from genesis is the expected steady-state path.
- If geth `ubt_status` reports `degradedReasonCode=ErrRawStorageKeyMissing`, treat it as an operational/invariant issue (for example non-genesis bootstrap or missing historical preimages).

## Outbox Compaction Semantics

- `safeSeq` means delete events with `seq < safeSeq`.
- Constraint: `safeSeq <= latestSeq + 1`.
- `safeSeq = latestSeq + 1` means compact all currently persisted events.

Recommended flow:

1. `ubt_safeCompactSeq` from ubtconv.
2. `ubt_compactOutboxBelow` on geth with that sequence.

## Troubleshooting

### `ubtconv` stuck with `no event at seq N`

Cause:

- Consumer starts from `AppliedSeq + 1`, but outbox lowest available seq is already greater than `N` (compaction boundary mismatch).

Actions:

1. Reuse matching `ubtconv` checkpoint datadir with that outbox.
2. Or recreate outbox from seq `0` and restart with fresh ubtconv datadir.

### `ubtconv` fails with DB lock (`resource temporarily unavailable`)

Cause:

- Multiple ubtconv processes using same `--datadir`.

Action:

- Stop duplicate process or use a different datadir.

### `Lighthouse started` log confusion

- Script marks lighthouse started when the startup banner is observed in lighthouse log.
- Lighthouse HTTP API may still take extra time; script warns and continues while process is healthy.

### `run_manual_check.sh` cannot resolve address on mainnet

Cause:

- Mainnet `eth_accounts`/`eth_coinbase` can be empty.

Action:

- Pass explicit `--address`.

## Configuration Flags

| Flag | Default | Description |
|---|---|---|
| `--outbox-rpc-endpoint` | `http://localhost:8545` | geth outbox endpoint (HTTP/WebSocket or IPC path) |
| `--outbox-read-batch` | `1` | Number of events prefetched per outbox read (1 disables prefetch, max 1000) |
| `--datadir` | `./ubtconv-data` | ubtconv data directory |
| `--apply-commit-interval` | `128` | Commit every N applied blocks |
| `--apply-commit-max-latency` | `10s` | Commit max latency |
| `--max-recoverable-reorg-depth` | `128` | Fast-path reorg depth bound |
| `--triedb-scheme` | `path` | Trie scheme (must be `path`) |
| `--triedb-state-history` | `90000` | Retained UBT state history blocks |
| `--require-archive-replay` | `true` | Require archive replay client for deep recovery |
| `--query-rpc-enabled` | `true` | Enable ubt query RPC server |
| `--query-rpc-listen-addr` | `localhost:8560` | Query RPC listen address |
| `--pprof-enabled` | `false` | Enable pprof HTTP server |
| `--pprof-listen-addr` | `127.0.0.1:6061` | pprof HTTP listen address |
| `--query-rpc-max-batch` | `100` | Max batch size for list-style RPC |
| `--validation-strict` | `true` | Strict validation against MPT |
| `--validation-halt-on-mismatch` | `false` | Stop daemon on strict mismatch |
| `--validation-strict-catchup-sample-rate` | `0` | Strict validation sampling while backlog is high (0 = disable strict validation during catch-up) |
| `--execution-class-rpc-enabled` | `false` | Enable `ubt_callUBT` and `ubt_executionWitnessUBT` |
| `--backpressure-lag-threshold` | `1000` | Force fast commit above lag threshold |
| `--block-root-index-stride-high-lag` | `16` | Base stride for block-root index writes while lag is high (adaptive, `1` disables) |
| `--outbox-disk-budget-bytes` | `0` | Outbox disk budget (0 = unlimited) |
| `--outbox-alert-threshold-pct` | `80` | Outbox compaction alert threshold |
| `--cancun-block` | `0` | Explicit Cancun block (`0` = estimate from chain config timestamp) |
| `--slot-index-disk-budget` | `0` | Slot index disk budget (0 = unlimited) |

## Data Directory Layout

```text
ubtconv-data/
  consumer/   # consumer state/checkpoints
  triedb/     # UBT trie DB (path scheme)
```

## Build

```bash
go build -o build/bin/ubtconv ./cmd/ubtconv
```

## License

Copyright 2024 The go-ethereum Authors. Licensed under GNU GPL v3.0.
