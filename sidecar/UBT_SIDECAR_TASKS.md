# UBT Sidecar Implementation Tasks

このドキュメントは [UBT_SIDECAR_PLAN.md](./UBT_SIDECAR_PLAN.md) の実装タスクを分解・トラッキングするためのものです。

## 凡例

- `[ ]` 未着手
- `[x]` 完了
- `[~]` 進行中
- `[-]` スキップ/不要

---

## 0. 事前確認（全体前提）

- [x] sidecar 前提の確認（full sync / path scheme / preimages）
- [x] 非ブロッキング conversion 方針の最終確認（更新キュー方式）
- [x] validator 側の運用モード（sidecar）での実行想定を共有
- [x] sidecar を独立モジュールとして実装する方針（`sidecar/`）の共有

---

## 1. DBスキーマ / Accessor（並列可）

### 1.1 rawdb schema
- [x] `core/rawdb/schema.go` に UBT sidecar キー追加
  - [x] `UBTSidecarPrefix`
  - [x] `UBTConversionProgressKey`
  - [x] `UBTCurrentRootKey`
  - [x] `UBTBlockRootPrefix`
  - [x] `UBTUpdateQueuePrefix`
  - [x] `UBTUpdateQueueMetaKey`

### 1.2 rawdb accessors
- [x] `core/rawdb/accessors_ubt_sidecar.go` 追加
  - [x] `WriteUBTCurrentRoot` / `ReadUBTCurrentRoot`
  - [x] `WriteUBTBlockRoot` / `ReadUBTBlockRoot`
  - [x] `Write/Read/DeleteUBTConversionProgress`
  - [x] UpdateQueue の read/write/iterate API（blockNum+hash）

---

## 2. Config / CLI（並列可）

### 2.1 Config追加
- [x] `eth/ethconfig/config.go`
  - [x] `UBTSidecar` / `UBTSidecarAutoConvert`

### 2.2 CLIフラグ
- [x] `cmd/utils/flags.go`
  - [x] `--ubt.sidecar`
  - [x] `--ubt.sidecar.autoconvert`
  - [x] full sync / path scheme / preimages の強制

---

## 3. StateUpdate Accessor / Queue用構造体（並列可）

### 3.1 StateUpdate Accessor
- [x] `core/state` に exported accessor を追加
  - [x] Accounts / AccountsOrigin
  - [x] Storages / StoragesOrigin
  - [x] RawStorageKey
  - [x] Codes（addr → code blob）

### 3.2 UBTUpdate 構造体
- [x] `sidecar/ubt_update.go` に UBTUpdate 定義
- [x] RLPエンコード/デコード実装
- [x] `StateUpdate` → `UBTUpdate` 変換ヘルパー

---

## 4. UBTSidecar Core 実装（並列可）

### 4.1 基本構造
- [x] `sidecar/` パッケージ作成（自己完結モジュール）
- [x] `sidecar/ubt_sidecar.go` 新規追加
  - [x] `UBTSidecar` 最小インターフェイス定義（PLAN準拠）
  - [x] `UBTAccount` 構造体（balance/nonce/codeHash）
  - [x] 状態: enabled / converting / ready / stale
  - [x] currentRoot/currentBlock/currentHash
  - [x] InitFromDB
  - [x] Converting() / Ready() / Enabled()
  - [x] ReadAccount / ReadStorage（UBT state access）

### 4.2 MPT→UBT変換
- [x] `ConvertFromMPT(...)` 実装
  - [x] preimage欠損で即エラー
  - [x] iterator.Error() 監視
  - [x] コード長計算
  - [x] UBT root 保存

### 4.3 非ブロッキング conversion
- [x] UpdateQueue の enqueue / replay 実装
- [x] replay時の canonical hash 検証
- [x] queue overflow / missing entry 時の stale化

### 4.4 ApplyStateUpdate
- [x] AccountsOrigin ベースの反映
- [x] RawStorageKey / preimage解決
- [x] RLPデコード（trimmed bytes → 32 bytes）
- [x] Delete account marker + code deletion
- [x] `triedb.Update` に StateSet を渡す

---

## 5. Blockchain / Reorg 統合（並列可）

### 5.1 Commitフック
- [x] `core/blockchain.go` で sidecar hook
  - [x] Ready時 ApplyStateUpdate
  - [x] Converting時 EnqueueUpdate
- [x] sidecar有効時 `CommitWithUpdate` を強制（NoCode禁止）
- [x] 既存モジュールへの変更を最小化（sidecar 呼び出しのみ）

### 5.2 Reorg処理
- [x] `core/blockchain.go` の reorg で sidecar通知
  - [x] `UBTBlockRoot(ancestorHash)` 取得
  - [x] Recoverable(root) の判定 → `triedb.Recover`
  - [x] 不可なら stale

---

## 6. bintrie ヘルパー（並列可）

- [x] `bintrie.DeleteContractCode(addr, codeSize)` 実装
- [x] account delete marker 作成（BasicData/CodeHash zero）
- [x] 既存 UpdateAccount ロジックとの整合確認

---

## 7. RPC 統合（並列可）

- [x] `eth/api_debug.go` openBinaryTrie の sidecar利用
  - [x] sidecar未ready時は明示エラー
  - [x] `debug_getUBTProof` が **sidecar UBT root** を使うよう修正
  - [x] header.Root を使わない
  - [x] `GetUBTRoot(blockHash)` 経由
  - [x] **新規** `debug_getUBTState` 実装
  - [x] sidecar無効/未ready時は明示エラー（no fallback）
  - [x] UBT root を指定して ReadAccount/ReadStorage から取得
  - [x] 既存 RPC は MPT-backed のまま維持

---

## 8. CLI / Conversion コマンド（並列可）

- [x] `cmd/geth/ubt_cmd.go` 追加
  - [x] `geth ubt convert --datadir` 実装
- [x] AutoConvert フロー接続
  - [x] full sync 完了時に `ConvertFromMPT` 起動
  - [x] 非ブロッキング conversion + queue replay

---

## 9. 検証 / テスト

### 9.1 Unit Tests
- [x] preimage欠損で conversion fail
- [x] ApplyStateUpdate: balance/nonceのみ更新
- [x] ApplyStateUpdate: storage update/delete
- [x] ApplyStateUpdate: account deletion
- [x] UpdateQueue replay の整合性
- [x] debug_getUBTState の基本動作

### 9.2 Integration
- [ ] sidecar付き full sync
- [ ] conversion後の UBT root 取得
- [ ] local MPT vs UBT 比較（Account/Storage サンプリング）
- [ ] validator 実行（Phase1/2, sidecar モード）
- [ ] reorg時の Recover / stale 動作

---

## 10. ドキュメント

- [ ] Plan/Task更新の最終レビュー
- [ ] validator docs と sidecar docs の整合性確認
