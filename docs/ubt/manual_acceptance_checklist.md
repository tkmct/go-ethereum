# UBT Manual Acceptance Checklist

This document defines the manual acceptance flow for UBT conversion + debug proxy.
It complements `docs/ubt/ubtconv_test_matrix.md` by providing concrete commands.

## 1. Prerequisites

1. `geth` and `ubtconv` binaries are built from this repository.
2. `curl` and `jq` are installed.
3. Optional: Go toolchain (`go`) for building binaries.

## 2. Start Services

## 2.1 Mainnet startup (recommended)

Use the integrated launcher (`geth + lighthouse + ubtconv`):

```bash
CHECKPOINT_SYNC_URL='https://mainnet.checkpoint.sigp.io' \
scripts/run_mainnet_geth_ubtconv.sh \
  --action up \
  --enable-execution-rpc \
  --detach
```

Status/stop:

```bash
scripts/run_mainnet_geth_ubtconv.sh --action status --skip-build
scripts/run_mainnet_geth_ubtconv.sh --action down --skip-build
```

Mainnet notes:
1. This branch supports full-sync only (`--syncmode full`).
2. During catch-up, `eth_getBalance`/`eth_call` at old block selectors may fail with historical-state errors.
3. For deterministic parity, use a fixed selector such as `ubt_status.appliedBlock`.
4. `run_manual_check.sh` may require explicit `--address` on mainnet (because `eth_accounts` / `eth_coinbase` can be empty).

## 2.2 Start geth (UBT outbox + debug proxy enabled, `--dev`)

```bash
build/bin/geth \
  --dev --dev.period 1 \
  --datadir /tmp/ubt-manual/geth \
  --http --http.addr 127.0.0.1 --http.port 8545 \
  --http.api eth,net,web3,admin,debug,ubt,txpool \
  --ubt.conversion-enabled \
  --ubt.decoupled \
  --ubt.outbox-db-path /tmp/ubt-manual/geth/ubt-outbox \
  --ubt.outbox-retention-seq-window 100000 \
  --ubt.debug-endpoint http://127.0.0.1:8560 \
  --ubt.debug-timeout 5s
```

## 2.3 Start ubtconv (`--dev`)

```bash
build/bin/ubtconv \
  --outbox-rpc-endpoint http://127.0.0.1:8545 \
  --datadir /tmp/ubt-manual/ubtconv \
  --query-rpc-enabled \
  --query-rpc-listen-addr 127.0.0.1:8560 \
  --triedb-state-history 90000
```

`ubtconv` は full-sync 前提のため、bootstrap mode の指定は不要（非対応）です。

Execution RPC を有効モードで確認する場合は、上記に `--execution-class-rpc-enabled` を追加する。

## 3. Automated Subset

Run the helper script from repo root:

```bash
./run_manual_check.sh
```

No-manual-start full session runner:

```bash
scripts/ubt_sessions/run_sessions.sh --session all
```

This command includes:
1. execution-class OFF checks
2. execution-class ON checks
3. deterministic restart/resume verification
4. deterministic reorg recovery verification

Optional parameters:

```bash
./run_manual_check.sh \
  --geth http://127.0.0.1:8545 \
  --ubt http://127.0.0.1:8560 \
  --address 0x... \
  --storage-address 0x... \
  --storage-slot 0x...
```

Execution RPC 有効モードの検証:

```bash
./run_manual_check.sh --execution-enabled
```

What this script checks:

1. `ubt_latestSeq` is increasing.
2. `ubt_status` (`appliedSeq`/`appliedBlock`) is moving.
3. `eth_getBalance` and `debug_getUBTBalance` match for selected account.
4. `debug_getUBTCode` returns empty code for a no-code account.
5. `debug_getUBTProof` returns non-empty account proof for the check address.
6. `debug_callUBT` と `debug_executionWitnessUBT` は
   - default（flag OFF）: `execution-class RPC disabled` エラー
   - `--execution-enabled` 指定時: 成功
   の両方を確認する。

## 4. Manual-Only Checks

## 4.1 Storage equivalence (`debug_getUBTStorageAt`)

Primary acceptance path (deterministic):

```bash
go test ./cmd/ubtconv -run TestVerify_FullPipelineIntegration -count=1
go test ./trie/bintrie -run TestGetStorage_RoundTrip -count=1
```

Optional live path (known contract + slot):

```bash
curl -s -H 'Content-Type: application/json' \
  --data '{"jsonrpc":"2.0","id":1,"method":"eth_getStorageAt","params":["0x<contract>","0x<slot>","latest"]}' \
  http://127.0.0.1:8545

curl -s -H 'Content-Type: application/json' \
  --data '{"jsonrpc":"2.0","id":1,"method":"debug_getUBTStorageAt","params":["0x<contract>","0x<slot>","latest"]}' \
  http://127.0.0.1:8545
```

Pass condition: both values are identical 32-byte hex words.
If live path is skipped, deterministic path must pass and be recorded in the test matrix.

## 4.2 Restart/resume behavior

Deterministic path:

```bash
scripts/ubt_sessions/run_sessions.sh --session restart
```

Optional live path:
1. Record current progress via `ubt_status`.
2. Stop `ubtconv`, wait a few seconds while `geth` keeps importing blocks.
3. Restart `ubtconv` with the same `--datadir`.
4. Check `ubt_status` repeatedly.

Pass condition:
1. `appliedSeq` resumes and continues increasing.
2. No persistent stuck state or silent divergence.

## 4.3 Controlled short reorg

Deterministic path:

```bash
scripts/ubt_sessions/run_sessions.sh --session reorg
```

Optional live path:
Use a 2-node private dev setup to force a short canonical switch.
After the reorg:

1. `ubt_status` continues to advance.
2. `debug_getUBTBalance` matches `eth_getBalance` on the new canonical chain.
3. No repeated consumer crash loop.

## 5. Acceptance Decision

All items below must be true:

1. Automated subset script passes.
2. Storage equivalence check passes (deterministic path required; live path optional).
3. Restart/resume check passes (deterministic path required; live path optional).
4. Controlled reorg check passes (deterministic path required; live path optional).
5. `debug_callUBT` と `debug_executionWitnessUBT` について、flag OFF の disabled エラーと flag ON の成功を確認できる。
