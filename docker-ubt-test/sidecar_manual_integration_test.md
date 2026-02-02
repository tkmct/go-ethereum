# Sidecar Manual Integration Test (9.2)

Prereqs: `docker`, `docker compose`, `go`, and `jq` installed.

## 1) sidecar付き full sync

Run a full sync with sidecar enabled, then proceed with the checks below.

Verify blocks advance:

```bash
curl -s http://localhost:8545 -X POST -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"eth_blockNumber","params":[]}'
```

Phase 1 check (UBT node not syncing and state readable):

```bash
go run ./cmd/validate \
  --reference-rpc http://localhost:8547 \
  --ubt-rpc http://localhost:8545 \
  --phases 1
```

## 2) UBT root 取得

Scripted check:

```bash
cd /home/tkmct/dev/go-ethereum/docker-ubt-test
go run ./cmd/ubtcheck --mode quick
```

Direct RPC check:

```bash
curl -s http://localhost:8545 -X POST -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"debug_getUBTProof","params":["0x0000000000000000000000000000000000000000",[],"latest"]}' \
  | jq -r .result.ubtRoot
```

Expected: non-zero `ubtRoot`.

## 3) local MPT vs UBT 比較（Account/Storage サンプリング）

```bash
cd /home/tkmct/dev/go-ethereum/docker-ubt-test
go run ./cmd/validate \
  --reference-rpc http://localhost:8547 \
  --ubt-rpc http://localhost:8545 \
  --phases 2 \
  --account-samples 2000 \
  --storage-samples 100
```

## 4) validator 実行（Phase1/2, sidecar モード）

```bash
cd /home/tkmct/dev/go-ethereum/docker-ubt-test
go run ./cmd/validate \
  --reference-rpc http://localhost:8547 \
  --ubt-rpc http://localhost:8545 \
  --phases 1,2
```

Optional RPC consistency (includes `debug_getUBTState`):

```bash
cd /home/tkmct/dev/go-ethereum/docker-ubt-test
go run ./cmd/validate \
  --reference-rpc http://localhost:8547 \
  --ubt-rpc http://localhost:8545 \
  --phases 5
```

## 5) reorg時の Recover / stale 動作

### Shallow rewind (expect recoverable)

```bash
HEAD_HEX=$(curl -s http://localhost:8545 -X POST -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"eth_blockNumber","params":[]}' | jq -r .result)
HEAD_DEC=$((16#${HEAD_HEX#0x}))
TARGET_DEC=$((HEAD_DEC-5))
TARGET_HEX=$(printf '0x%x' "$TARGET_DEC")

curl -s http://localhost:8545 -X POST -H 'Content-Type: application/json' \
  -d "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"debug_setHead\",\"params\":[\"$TARGET_HEX\"]}"
sleep 5

go run ./cmd/validate \
  --reference-rpc http://localhost:8547 \
  --ubt-rpc http://localhost:8545 \
  --phases 1,5
```

### Deep rewind (aim to trigger stale; requires head >= 200)

```bash
while true; do
  HEAD_HEX=$(curl -s http://localhost:8545 -X POST -H 'Content-Type: application/json' \
    -d '{"jsonrpc":"2.0","id":1,"method":"eth_blockNumber","params":[]}' | jq -r .result)
  HEAD_DEC=$((16#${HEAD_HEX#0x}))
  if [ "$HEAD_DEC" -ge 200 ]; then break; fi
  sleep 2
done

TARGET_DEC=$((HEAD_DEC-128))
TARGET_HEX=$(printf '0x%x' "$TARGET_DEC")
curl -s http://localhost:8545 -X POST -H 'Content-Type: application/json' \
  -d "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"debug_setHead\",\"params\":[\"$TARGET_HEX\"]}"

curl -s http://localhost:8545 -X POST -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"debug_getUBTState","params":["0x0000000000000000000000000000000000000000",[],"latest"]}'
```

Expected: if reorg exceeds recoverable history, `debug_getUBTState` should return “ubt sidecar not ready”. If it recovers, increase the rewind offset.
