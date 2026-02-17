# UBT 運用マニュアル（このブランチ実装対応）

## 1. このマニュアルの目的
このドキュメントは、今回の開発で実装された UBT 機能を、実際に起動・確認・運用するための手順としてまとめたものです。

対象:
1. `geth` 側の UBT outbox/emitter/debug proxy。
2. `ubtconv` 側の consumer/query/recovery/compaction 連携。
3. 実際に使える RPC と、feature flag による有効/無効の切り分け。

## 2. できること（実装済み）
### 2.1 geth 側
1. canonical block import に追従して UBT outbox イベントを永続化。
2. `ubt_*` outbox 読み出し RPC を提供:
   1. `ubt_getEvent`
   2. `ubt_getEvents`
   3. `ubt_latestSeq`
   4. `ubt_compactOutboxBelow`
   5. `ubt_status`
3. `debug_*` UBT proxy RPC を提供（ubtconv へ転送）:
   1. `debug_getUBTBalance`
   2. `debug_getUBTStorageAt`
   3. `debug_getUBTCode`
   4. `debug_getUBTStatus`
   5. `debug_getUBTProof`
   6. `debug_getUBTRawProof`
   7. `debug_getUBTProofByKey`（互換用）
   8. `debug_callUBT`（`--execution-class-rpc-enabled` が false の場合は disabled エラー）
   9. `debug_executionWitnessUBT`（`--execution-class-rpc-enabled` が false の場合は disabled エラー）

### 2.2 ubtconv 側
1. outbox を seq 順に消費して UBT trie を更新・commit。
2. crash consistency (`pending/applied`) を永続化。
3. reorg 時の fast/slow recovery。
4. query RPC (`ubt_*`) を提供:
   1. `ubt_status`
   2. `ubt_getBalance`
   3. `ubt_getStorageAt`
   4. `ubt_getCode`
   5. `ubt_getProof`
   6. `ubt_getAccountProof`
   7. `ubt_verifyProof`
   8. `ubt_safeCompactSeq`
   9. `ubt_callUBT`（`--execution-class-rpc-enabled` が false の場合は disabled エラー）
   10. `ubt_executionWitnessUBT`（`--execution-class-rpc-enabled` が false の場合は disabled エラー）
5. outbox compaction 連携（`ubt_safeCompactSeq` + geth 側 `ubt_compactOutboxBelow`）。

## 3. 前提
1. 同一リポジトリで `geth` と `ubtconv` をビルド可能。
2. `curl`, `jq` が利用可能。
3. UBT 連携は `geth --syncmode full` 前提（snap/backfill は対象外）。
4. mainnet では EL+CL+UBT の 3 プロセス（`geth` + `lighthouse` + `ubtconv`）を起動する。
5. ローカル最小検証は `--dev` ネットワークでも実施可能。

## 4. ビルド
リポジトリルートで実行:

```bash
go build -o build/bin/geth ./cmd/geth
go build -o build/bin/ubtconv ./cmd/ubtconv
```

## 5. 起動手順（mainnet 推奨）
## 5.1 一括起動スクリプト（推奨）
```bash
CHECKPOINT_SYNC_URL='https://mainnet.checkpoint.sigp.io' \
scripts/run_mainnet_geth_ubtconv.sh \
  --action up \
  --enable-execution-rpc \
  --detach
```

補助コマンド:
```bash
scripts/run_mainnet_geth_ubtconv.sh --action status --skip-build
scripts/run_mainnet_geth_ubtconv.sh --action down --skip-build
```

ポイント:
1. geth は強制的に `--syncmode full` で起動。
2. lighthouse は `Lighthouse started` バナー確認後に次工程へ進む。
3. lighthouse HTTP API の起動遅延はあり得るため、script は process 健全なら継続する。
4. 既定ログ: `${HOME}/.ubt-mainnet/logs/geth.log`, `lighthouse.log`, `ubtconv.log`。

## 5.2 手動起動（mainnet）
### geth
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

### lighthouse
```bash
lighthouse bn \
  --network mainnet \
  --datadir /path/to/lighthouse-datadir \
  --execution-endpoint http://127.0.0.1:8551 \
  --execution-jwt /tmp/ubt-mainnet/jwtsecret.hex \
  --http --http-address 127.0.0.1 --http-port 5052 \
  --checkpoint-sync-url https://mainnet.checkpoint.sigp.io
```

### ubtconv
```bash
build/bin/ubtconv \
  --outbox-rpc-endpoint http://127.0.0.1:8545 \
  --datadir /path/to/ubtconv-datadir \
  --query-rpc-enabled \
  --query-rpc-listen-addr 127.0.0.1:8560 \
  --triedb-state-history 90000 \
  --execution-class-rpc-enabled
```

## 6. 起動手順（`--dev` 最短構成）
## 6.1 geth を UBT 有効で起動
```bash
build/bin/geth \
  --dev --dev.period 1 \
  --datadir /tmp/ubt-manual/geth \
  --http --http.addr 127.0.0.1 --http.port 8545 \
  --http.api eth,net,web3,debug,ubt,txpool \
  --ubt.conversion-enabled \
  --ubt.decoupled \
  --ubt.outbox-db-path /tmp/ubt-manual/geth/ubt-outbox \
  --ubt.outbox-retention-seq-window 100000 \
  --ubt.reorg-marker-enabled \
  --ubt.outbox-read-rpc-enabled \
  --ubt.debug-rpc-proxy-enabled \
  --ubt.debug-endpoint http://127.0.0.1:8560 \
  --ubt.debug-timeout 5s
```

ポイント:
1. `--ubt.conversion-enabled` が outbox/emitter 有効化の本体。
2. `--http.api` に `ubt` と `debug` を入れないと対応 RPC が叩けません。
3. debug proxy は `--ubt.debug-endpoint` を指定（または `--ubt.debug-rpc-proxy-enabled`）。
   1. 実運用では endpoint 指定を推奨。

## 6.2 ubtconv を起動
```bash
build/bin/ubtconv \
  --outbox-rpc-endpoint http://127.0.0.1:8545 \
  --datadir /tmp/ubt-manual/ubtconv \
  --query-rpc-enabled \
  --query-rpc-listen-addr 127.0.0.1:8560 \
  --triedb-state-history 90000
```

ポイント:
1. `--datadir` は restart/recovery のため固定してください。
2. full-sync 前提の実装なので bootstrap mode の指定は不要（非対応）です。
3. execution 系 RPC を使う場合は `--execution-class-rpc-enabled` を追加してください。

## 7. 起動確認（最初にやること）
### 7.1 RPC namespace 確認
```bash
curl -s -H 'Content-Type: application/json' \
  --data '{"jsonrpc":"2.0","id":1,"method":"rpc_modules","params":[]}' \
  http://127.0.0.1:8545 | jq
```

期待:
1. geth 側に `ubt` と `debug` が見えること。

### 7.2 geth outbox 側の状態確認
```bash
curl -s -H 'Content-Type: application/json' \
  --data '{"jsonrpc":"2.0","id":1,"method":"ubt_status","params":[]}' \
  http://127.0.0.1:8545 | jq
```

主な確認項目:
1. `enabled: true`
2. `latestSeq` が進む
3. `degraded`（出る場合は emitter 側の劣化状態）
4. mainnet full-sync from genesis では pre-Cancun 期間でも `latestSeq` は進む想定（`ErrRawStorageKeyMissing` は通常発生しない）

### 7.3 ubtconv 側の状態確認
```bash
curl -s -H 'Content-Type: application/json' \
  --data '{"jsonrpc":"2.0","id":1,"method":"ubt_status","params":[]}' \
  http://127.0.0.1:8560 | jq
```

主な確認項目:
1. `appliedSeq`, `appliedBlock`, `appliedRoot`
2. `pendingSeq`, `pendingState`, `pendingUpdatedAt`
3. `outboxLag`, `backpressureLagThreshold`, `backpressureTriggered`

## 8. 値の同値確認（eth と UBT）
## 8.1 Balance
```bash
ADDR="0x0000000000000000000000000000000000000001"

curl -s -H 'Content-Type: application/json' \
  --data "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"eth_getBalance\",\"params\":[\"${ADDR}\",\"latest\"]}" \
  http://127.0.0.1:8545 | jq

curl -s -H 'Content-Type: application/json' \
  --data "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"debug_getUBTBalance\",\"params\":[\"${ADDR}\",\"latest\"]}" \
  http://127.0.0.1:8545 | jq
```

## 8.2 Storage（既知の contract/slot が必要）
```bash
CONTRACT="0x<contract>"
SLOT="0x<slot32>"

curl -s -H 'Content-Type: application/json' \
  --data "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"eth_getStorageAt\",\"params\":[\"${CONTRACT}\",\"${SLOT}\",\"latest\"]}" \
  http://127.0.0.1:8545 | jq

curl -s -H 'Content-Type: application/json' \
  --data "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"debug_getUBTStorageAt\",\"params\":[\"${CONTRACT}\",\"${SLOT}\",\"latest\"]}" \
  http://127.0.0.1:8545 | jq
```

## 8.3 Code
```bash
ADDR="0x<address>"
curl -s -H 'Content-Type: application/json' \
  --data "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"debug_getUBTCode\",\"params\":[\"${ADDR}\",\"latest\"]}" \
  http://127.0.0.1:8545 | jq
```

## 9. outbox API の直接確認（geth:8545）
## 9.1 latest seq
```bash
curl -s -H 'Content-Type: application/json' \
  --data '{"jsonrpc":"2.0","id":1,"method":"ubt_latestSeq","params":[]}' \
  http://127.0.0.1:8545 | jq
```

## 9.2 単一イベント取得
```bash
curl -s -H 'Content-Type: application/json' \
  --data '{"jsonrpc":"2.0","id":1,"method":"ubt_getEvent","params":["0x0"]}' \
  http://127.0.0.1:8545 | jq
```

## 9.3 範囲取得
```bash
curl -s -H 'Content-Type: application/json' \
  --data '{"jsonrpc":"2.0","id":1,"method":"ubt_getEvents","params":["0x0","0x10"]}' \
  http://127.0.0.1:8545 | jq
```

## 10. proof 系の確認
## 10.1 geth proxy 経由（推奨）
```bash
ADDR="0x<address>"
curl -s -H 'Content-Type: application/json' \
  --data "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"debug_getUBTProof\",\"params\":[\"${ADDR}\",[],\"latest\"]}" \
  http://127.0.0.1:8545 | jq
```

### 補足
1. raw key 版: `debug_getUBTRawProof`
2. 互換 alias: `debug_getUBTProofByKey`

## 10.2 ubtconv 直叩きで verify
```bash
# raw key proof を直接取得
RAW_KEY="0x<32byte-key>"
curl -s -H 'Content-Type: application/json' \
  --data "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"ubt_getProof\",\"params\":[\"${RAW_KEY}\",\"latest\"]}" \
  http://127.0.0.1:8560 | jq

# account + storage key で proof 取得
ADDR="0x<address>"
SLOT1="0x<slot32>"
curl -s -H 'Content-Type: application/json' \
  --data "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"ubt_getAccountProof\",\"params\":[\"${ADDR}\",[\"${SLOT1}\"],\"latest\"]}" \
  http://127.0.0.1:8560 | jq

# 例: ubt_getProof の結果から root/key/proofNodes を渡して検証
curl -s -H 'Content-Type: application/json' \
  --data '{"jsonrpc":"2.0","id":1,"method":"ubt_verifyProof","params":["0x<root>","0x<key>",{"0x<nodeHash>":"0x<rlpNode>"}]}' \
  http://127.0.0.1:8560 | jq
```

## 11. compaction の手動確認
自動 compaction とは別に、手動でも確認できます。

## 11.1 ubtconv から safe sequence を取得
```bash
SAFE_RAW=$(curl -s -H 'Content-Type: application/json' \
  --data '{"jsonrpc":"2.0","id":1,"method":"ubt_safeCompactSeq","params":[]}' \
  http://127.0.0.1:8560 | jq -r '.result')
echo "safe seq(raw)=${SAFE_RAW}"
```

## 11.2 geth に compact を依頼
```bash
if [[ "${SAFE_RAW}" == 0x* ]]; then
  SAFE_HEX="${SAFE_RAW}"
else
  SAFE_HEX=$(printf "0x%x" "${SAFE_RAW}")
fi
curl -s -H 'Content-Type: application/json' \
  --data "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"ubt_compactOutboxBelow\",\"params\":[\"${SAFE_HEX}\"]}" \
  http://127.0.0.1:8545 | jq
```

境界条件:
1. `safeSeq <= latestSeq + 1`
2. `safeSeq = latestSeq + 1` は「現在あるイベントを全削除」の意味。

## 12. restart/recovery の確認
推奨（deterministic）:

```bash
scripts/ubt_sessions/run_sessions.sh --session restart
scripts/ubt_sessions/run_sessions.sh --session reorg
```

手動確認（任意）:
1. `ubt_status` で `appliedSeq/appliedBlock` を記録。
2. `ubtconv` を停止（geth は継続）。
3. 数秒後に同じ `--datadir` で `ubtconv` 再起動。
4. `ubt_status` を再確認。

期待:
1. `appliedSeq` が後退せず再開。
2. `pendingState` が永続不整合で張り付き続けない。

storage 同値の推奨確認:

```bash
go test ./trie/bintrie -run TestGetStorage_RoundTrip -count=1
go test ./cmd/ubtconv -run TestVerify_FullPipelineIntegration -count=1
```

## 13. validation モード
### 13.1 strict validation（既定有効）
```bash
build/bin/ubtconv \
  --outbox-rpc-endpoint http://127.0.0.1:8545 \
  --datadir /tmp/ubt-manual/ubtconv \
  --validation-strict \
  --validation-halt-on-mismatch=false
```

## 14. execution 系 RPC の現状
### 14.1 `debug_callUBT` / `ubt_callUBT`
1. 実装済み。ただし既定は無効で、`ubtconv --execution-class-rpc-enabled` 指定時のみ有効。
2. disabled 時の期待エラー:
   1. `execution-class RPC disabled`

```bash
ADDR="0x0000000000000000000000000000000000000001"

# geth debug proxy 経由
curl -s -H 'Content-Type: application/json' \
  --data "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"debug_callUBT\",\"params\":[{\"to\":\"${ADDR}\",\"data\":\"0x\"}]}" \
  http://127.0.0.1:8545 | jq

# ubtconv 直叩き
curl -s -H 'Content-Type: application/json' \
  --data "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"ubt_callUBT\",\"params\":[{\"to\":\"${ADDR}\",\"data\":\"0x\"}]}" \
  http://127.0.0.1:8560 | jq
```

### 14.2 `debug_executionWitnessUBT` / `ubt_executionWitnessUBT`
1. 実装済み。ただし既定は無効で、`ubtconv --execution-class-rpc-enabled` 指定時のみ有効。
2. disabled 時の期待エラー:
   1. `execution-class RPC disabled`

```bash
curl -s -H 'Content-Type: application/json' \
  --data '{"jsonrpc":"2.0","id":1,"method":"debug_executionWitnessUBT","params":["latest"]}' \
  http://127.0.0.1:8545 | jq

curl -s -H 'Content-Type: application/json' \
  --data '{"jsonrpc":"2.0","id":1,"method":"ubt_executionWitnessUBT","params":["latest"]}' \
  http://127.0.0.1:8560 | jq
```

## 15. 主要フラグ一覧
### 15.1 geth 側（UBT関連）
1. `--ubt.conversion-enabled`
2. `--ubt.decoupled`
3. `--ubt.outbox-db-path`
4. `--ubt.outbox-write-timeout`
5. `--ubt.reorg-marker-enabled`
6. `--ubt.outbox-read-rpc-enabled`
7. `--ubt.outbox-retention-seq-window`
8. `--ubt.debug-endpoint`
9. `--ubt.debug-timeout`
10. `--ubt.debug-rpc-proxy-enabled`

### 15.2 ubtconv 側（主要）
1. `--outbox-rpc-endpoint`
2. `--datadir`
3. `--apply-commit-interval`
4. `--apply-commit-max-latency`
5. `--query-rpc-enabled`
6. `--query-rpc-listen-addr`
7. `--triedb-state-history`
8. `--max-recoverable-reorg-depth`
9. `--require-archive-replay`
10. `--backpressure-lag-threshold`
11. `--validation-strict`
12. `--validation-halt-on-mismatch`
13. `--anchor-snapshot-interval`
14. `--anchor-snapshot-retention`
15. `--execution-class-rpc-enabled`

## 16. 運用時の典型チェックコマンド
### 16.1 geth outbox health
```bash
curl -s -H 'Content-Type: application/json' \
  --data '{"jsonrpc":"2.0","id":1,"method":"ubt_status","params":[]}' \
  http://127.0.0.1:8545 | jq
```

### 16.2 ubtconv health
```bash
curl -s -H 'Content-Type: application/json' \
  --data '{"jsonrpc":"2.0","id":1,"method":"ubt_status","params":[]}' \
  http://127.0.0.1:8560 | jq
```

### 16.3 geth proxy 経由の daemon status
```bash
curl -s -H 'Content-Type: application/json' \
  --data '{"jsonrpc":"2.0","id":1,"method":"debug_getUBTStatus","params":[]}' \
  http://127.0.0.1:8545 | jq
```

## 17. 既知の注意点
1. `debug` namespace を HTTP で開けると管理系 API も同居するため、公開環境ではアクセス制御を必須にしてください。
2. `--ubt.debug-rpc-proxy-enabled` を立てても `--ubt.debug-endpoint` が空だと proxy 呼び出しは接続できません。
3. block selector は UBT 側で制約があります。
   1. `latest` は利用可。
   2. `pending/safe/finalized` は明示的に unsupported エラー。
   3. 履歴ウィンドウ外は `state not available`。
4. deep recovery では archive 相当の履歴アクセスが必要です。
5. pre-Cancun raw storage key の永続化は実装済みで、mainnet full-sync from genesis では Cancun 前から diff emission 継続が期待動作です。
   1. `degradedReasonCode=ErrRawStorageKeyMissing` が出る場合は、想定動作ではなく運用上の問題（不整合データ、非genesis起点の履歴欠損、DB破損等）として扱ってください。
   2. 一時的な値比較ずれを避けるため、proxy/daemon比較では同一 block selector を固定してください（`ubt_status.appliedBlock` 推奨）。
   3. mainnet full-sync 初期の catch-up 中は geth 側 historical state 制約により古い block 指定の `eth_*` が失敗しうるため、`latest` または近傍 block で比較してください。
6. 新規 `ubtconv` datadir で既存・圧縮済み outbox を読むと `no event at seq N` で進まない場合があります。
   1. 既存 outbox に対応する `ubtconv` checkpoint を再利用する。
   2. もしくは outbox を新規作成して seq=0 から取り直す。

## 18. 参考
1. `docs/ubt/outbox_to_ubt_apply_flow.md`（outbox -> apply 詳細フロー）
2. `cmd/ubtconv/README.md`（daemon 実装詳細）
3. `docs/ubt/ubtconv_test_matrix.md`（検証証跡）
4. `docs/ubt/manual_acceptance_checklist.md`（手動受け入れチェック）
