# UBTConv Midpoint Recovery v1 詳細設計

## 1. スコープ
本設計は、`ubtconv` の `triedb` が不整合/破損した場合でも、genesis/floor 再開に頼らず途中状態から復旧する仕組みを定義する。  
対象は `cmd/ubtconv` と `core/rawdb`。`geth` 側 outbox 実装は変更しない。

## 2. 要件
1. `triedb` オープン失敗時、最新の復旧ポイントから再開できること。
2. root 連続性を維持できない復旧を既定経路にしないこと。
3. outbox compaction と復旧可能範囲の整合を機械的に保証すること。
4. 失敗時に fail-fast 可能であること。
5. 既存運用と互換を保つこと（段階導入可能）。

## 3. 方式概要
既存 anchor（`seq/block/root` のメタデータ）のみでは復旧できないため、**Materialized Recovery Anchor (MRA)** を導入する。

1. commit 境界で `seq/block/root` を固定した「復旧可能 triedb 実体」を別領域に保存する。
2. 起動時に live triedb が壊れていたら、最新の利用可能 MRA を昇格して復旧する。
3. 復旧後は `anchor.seq + 1` から outbox replay で先頭へ追随する。

非目標:
1. 壊れた triedb の中身比較・差分同期・修復。
2. 壊れた DB を自動で書き換えて正す機能。

## 4. データレイアウト
### 4.1 ファイル配置
`<datadir>/recovery/anchors/<anchorID>/` を追加する。

1. `manifest.rlp`  
2. `triedb/`（当該 anchor root を開ける独立 DB コピー）

`<datadir>/recovery/staging/<anchorID>.tmp/` は作成途中の一時領域。

### 4.2 Manifest 構造
`core/rawdb` に新規型を追加する。

1. `AnchorID uint64`
2. `Seq uint64`
3. `Block uint64`
4. `Root common.Hash`
5. `CreatedAt uint64`
6. `FormatVersion uint16`
7. `State uint8`（`creating/ready/broken`）
8. `TrieDigest [32]byte`（任意、整合確認用）
9. `FailureReason string`（`broken` 時）

### 4.3 メタキー（consumer DB）
`core/rawdb/schema.go` に新規 prefix を追加する。

1. `ubt-recovery-anchor-<id>` -> manifest
2. `ubt-recovery-anchor-cnt` -> 総作成数
3. `ubt-recovery-anchor-latest-ready` -> 最新 ready id

既存 `ubt-anchor-*`（メタ anchor）は互換維持のため残す。

## 5. Anchor 生成プロトコル
### 5.1 生成トリガ
`commitCount % RecoveryAnchorInterval == 0` でトリガする。  
既存 `AnchorSnapshotInterval` と独立設定にする。

### 5.2 生成手順
1. commit 完了直後の `appliedSeq/appliedBlock/appliedRoot` を固定する。
2. manifest を `creating` で DB に記録する。
3. `<staging>` に triedb コピーを作成する。
4. コピーを開いて `root` が開けることを検証する。
5. `manifest.rlp` を書いて `fsync` し、`staging -> anchors/<id>` を `rename`。
6. manifest を `ready` に更新し `latest-ready` を進める。

### 5.3 故障時挙動
1. 途中失敗時は `state=broken` を記録し `latest-ready` は更新しない。
2. `staging` は次回起動時に掃除する。
3. 既存 ready anchor は保持し続ける。

## 6. 起動復旧アルゴリズム
`NewConsumer()` の起動時分岐を以下に変更する。

1. `expectedRoot` で live triedb open を試行。
2. 成功時は通常起動。
3. 失敗時:
   1. `RecoveryStrict=true` の場合、MRA 復旧を優先。
   2. `latest-ready` から古い順へ試行:
      1. `anchor.seq + 1` が outbox で読めるか（`lowestSeq <= anchor.seq+1 <= latestSeq+1`）を確認。
      2. anchor の triedb を昇格（live triedb へ atomic swap）。
      3. `Applied*` を anchor 値に更新して applier open。
      4. 成功した時点で起動継続。
4. 全 anchor 失敗時:
   1. `RecoveryAllowGenesisFallback=false` なら起動失敗（fail-fast）。
   2. true の場合のみ現行の genesis/floor 復旧へフォールバック。

## 7. Compaction 連携
復旧可能性を守るため、compaction 上限を追加する。

1. `recoveryFloor = latestReadyAnchorSeq + 1`
2. `ubt_compactOutboxBelow` の引数は `compactBelow <= recoveryFloor` を必須化
3. 現行 `safeSeq - margin` と併用時は `min(currentPolicy, recoveryFloor)` を採用

これにより、常に「最新 ready anchor から replay できる範囲」を保持する。

## 8. 設定追加（案）
`cmd/ubtconv/config.go` / `main.go` に以下を追加する。

1. `--recovery-anchor-interval`（0=無効, 推奨>=128）
2. `--recovery-anchor-retention`（0=無制限）
3. `--recovery-strict`（default: true）
4. `--recovery-allow-genesis-fallback`（default: false）
5. `--recovery-anchor-max-copy-seconds`（copy 上限）

既存 `--anchor-snapshot-*` は互換維持しつつ将来 deprecate 候補。

## 9. 観測性
### 9.1 ログ
1. `MRA creation started/completed/failed`
2. `MRA restore candidate rejected`（理由付き）
3. `MRA restore succeeded`（anchor id/seq/block/root）
4. `Fail-fast due to no usable MRA`

### 9.2 メトリクス
1. `ubt/recovery_anchor/create/attempts|success|failures`
2. `ubt/recovery_anchor/restore/attempts|success|failures`
3. `ubt/recovery_anchor/latest/seq` gauge
4. `ubt/recovery_anchor/replay/required_seq_span` gauge

### 9.3 `ubt_status` 拡張
1. `recoveryMode`（`normal|anchor-restore|genesis-fallback`）
2. `latestRecoveryAnchorSeq`
3. `latestRecoveryAnchorBlock`

## 10. 互換・移行
1. 既存 DB はそのまま起動可能（MRA 無効なら現行動作）。
2. MRA 有効後も既存 anchor メタは参照可能。
3. 段階導入:
   1. Phase 1: MRA 作成のみ有効（復旧未使用）
   2. Phase 2: 起動時 MRA 復旧有効
   3. Phase 3: strict を既定化し genesis fallback を明示 opt-in 化

## 11. 実装差分（ファイル単位）
1. `cmd/ubtconv/config.go`: 新設定・validation
2. `cmd/ubtconv/main.go`: 新CLI flag
3. `cmd/ubtconv/consumer.go`: 起動復旧分岐、MRA 作成呼び出し
4. `cmd/ubtconv/runner.go`: compaction 上限を recoveryFloor と連動
5. `core/rawdb/schema.go`: recovery anchor key 定義
6. `core/rawdb/accessors_ubt_outbox.go`: manifest read/write accessors
7. `cmd/ubtconv/metrics.go`: recovery anchor metrics
8. `cmd/ubtconv/query_server.go`: `ubt_status` 追加項目

## 12. テスト計画
1. Unit:
   1. manifest encode/decode
   2. recoveryFloor compaction policy
   3. startup decision matrix（live OK / MRA OK / 全滅）
2. Integration:
   1. MRA 作成後に live triedb を故意破壊し、MRA から復旧
   2. outbox lowestSeq を進め、coverage 不足時に fail-fast
3. Fault:
   1. MRA 作成中 kill -9 して再起動時 cleanup
   2. broken anchor 混在時に次候補へフォールバック

## 13. 受け入れ基準
1. `triedb` 破損後でも genesis/floor に落ちず MRA から復旧できる。
2. compaction により MRA replay 範囲が欠損しない。
3. 復旧不能時は strict モードで起動失敗し、原因がログで特定できる。
