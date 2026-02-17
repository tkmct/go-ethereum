# Outbox イベントを UBT Tree に適用するフロー（Step by Step）

## 1. 目的
このドキュメントは、`geth` が outbox に出力したイベントを `ubtconv` が読み取り、UBT trie に apply / commit するまでの処理を順番に説明します。

対象コード:
1. `core/ubtemit/*`（emit + outbox 永続化）
2. `cmd/ubtconv/*`（consume + apply + commit + recovery）

## 2. 前提となるデータ構造
1. 外側イベントは `OutboxEnvelope`（`Seq`, `Kind`, `BlockNumber`, `Payload` など）です。
2. `Payload` は RLP バイト列で、`Kind` に応じて中身が変わります。
3. `Kind=diff` の payload は `QueuedDiffV1`（`OriginRoot`, `Root`, `Accounts`, `Storage`, `Codes`）です。
4. `Kind=reorg` の payload は `ReorgMarkerV1`（from/to/ancestor 情報）です。

## 3. geth 側: outbox にイベントを書き込む
1. canonical block import 後、UBT emitter が差分を組み立てます。
2. diff を `EncodeDiff` で RLP 化します。
3. `OutboxEnvelope{Kind: diff}` に詰めます。
4. `OutboxStore.Append` が `Seq` を採番し、DBに永続化します。
5. reorg 発生時は `EncodeReorgMarker` した `Kind=reorg` イベントを同様に append します。

補足:
1. `Seq` は単調増加で、`ubtconv` はこれを唯一の処理順序キーとして使います。

## 4. ubtconv 側: 取り込みループ開始
1. `Runner.loop()` が継続実行されます。
2. 各ループで `ConsumeNext()` を呼びます。
3. consumer は `targetSeq = processedSeq + 1` を計算します。
4. `readNextEnvelope(targetSeq)` で outbox RPC からイベントを取ります。
5. イベント欠損時は idle/backoff 判定、必要なら floor bootstrap を行います。

## 5. diff イベントの処理（`Kind=diff`）
1. `DecodeDiff(payload)` で差分を復元します。
2. クラッシュ復旧用に `markPendingSeq(targetSeq)` を先に記録します。
3. strict validation ポリシー（同期/非同期/サンプリング）を決定します。
4. applier が UBT trie に差分を適用します。
5. 適用内容:
- account 更新/削除
- storage 更新（raw slot key）
- code 更新
6. in-memory の pending 状態（`pendingBlock`, `pendingRoot` など）を更新します。
7. `processedSeq` を進めます。
8. `shouldCommit()` で commit 条件を評価します。

## 6. commit 処理
commit 条件を満たしたとき:
1. `CommitAt(pendingBlock)` で trie の変更を確定し、committed root を得ます。
2. durable state を更新します。
- `AppliedSeq = processedSeq`
- `AppliedBlock = pendingBlock`
- `AppliedRoot = committedRoot`
3. batch write で consumer state と block-root index などを永続化します。
4. `clearPendingSeq()` で inflight 状態を解除します。
5. `uncommittedBlocks` をリセットします。

重要:
1. `applied*` が「commit 済みの真値」です。
2. `pending*` は「処理途中の状態」です。

## 7. reorg イベントの処理（`Kind=reorg`）
1. `DecodeReorgMarker(payload)` で reorg 情報を復元します。
2. `markPendingSeq` を記録してから `handleReorg` に入ります。
3. reorg 深度に応じて fast-path revert か slow-path（anchor restore + replay）を実行します。
4. 復旧した状態を commit します。
5. `processedSeq` を進め、pending を clear します。

## 8. outboxLag と backpressure
1. Runner は定期的に `latestSeq` を取得し、`outboxLag = latestSeq - processedSeq` を更新します。
2. `outboxLag > backpressure-lag-threshold` の間は throughput 優先モードになります。
3. 主な挙動:
- commit を粗くしてIO頻度を下げる
- strict validation をサンプリング/抑制
- 一部メンテナンス処理を後回し

## 9. compaction 連携
1. `ubtconv` は commit 済み進捗から safe compaction seq を計算します。
2. geth 側 `ubt_compactOutboxBelow` を呼んで古いイベントを削除します。
3. この設計により、追従中の `ubtconv` が必要な範囲は保持されます。

## 10. 運用時の確認ポイント
`ubt_status` を見ると状態を判断できます:
1. `appliedSeq/appliedBlock/appliedRoot`: commit 済み進捗
2. `pendingSeq/pendingState`: inflight 状態
3. `outboxLag`: backlog
4. `backpressureTriggered`: backpressure モード有無

実務上の判定:
1. `appliedSeq` が継続的に増えていれば、apply/commit は前進中です。
2. `pendingState=inflight` が短時間で `none` に戻るのは正常です。
