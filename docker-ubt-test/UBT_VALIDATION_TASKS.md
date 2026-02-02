# UBT Validation Implementation Tasks

このドキュメントは [UBT_VALIDATION_PLAN.md](./UBT_VALIDATION_PLAN.md) の実装タスクを分解・トラッキングするためのものです。

> **Note:** 本リポジトリでは **sidecar モードのみ**を対象とする。`state.ubt` や
> `CommitStateRootToHeader=true` を前提とする項目は不要/廃止。

## 凡例

- `[ ]` 未着手
- `[x]` 完了
- `[~]` 進行中
- `[-]` スキップ/不要

---

## 1. 前提条件・環境構築

### 1.1 開発環境

- [ ] Go 開発環境のセットアップ確認
- [ ] `docker-ubt-test/cmd/validate/` ディレクトリ作成
- [ ] **既存モジュールを使用**（`go mod init` は実行しない）
- [ ] 依存パッケージの追加
  - [ ] `github.com/urfave/cli/v2` (CLI)
  - [ ] `golang.org/x/sync/errgroup` (並列処理)

### 1.2 テストノード環境

- [ ] UBT ノードの起動確認 (`--ubt.sidecar` 有効)
- [ ] **full sync 前提の起動確認**（`--syncmode=full`）
- [ ] リファレンス MPT ノードの起動確認
- [ ] 両ノード間のブロック同期確認
- [ ] JWT 認証設定確認
- [ ] **CommitStateRootToHeader が無効**であることを確認（sidecar）

---

## 2. 基盤コード実装

### 2.1 型定義 (`types.go`)

- [ ] `BlockAnchor` 構造体
- [ ] `SamplingConfig` 構造体
- [ ] `UBTProofResult` 構造体
- [ ] `UBTStorageProof` 構造体
- [ ] `RPCTest` 構造体
- [ ] `ExtUBTWitness` 構造体（**core/stateless と同一定義を共有**）
- [ ] `PathNode` 構造体（**core/stateless と同一定義を共有**）

### 2.2 Validator コア (`validator.go`)

- [ ] `Validator` 構造体定義
  ```go
  type Validator struct {
      ubt         *rpc.Client
      ref         *rpc.Client
      chainConfig *params.ChainConfig
  }
  ```
- [ ] `NewValidator(ubtRPC, refRPC string) (*Validator, error)`
- [ ] RPC クライアント初期化

### 2.3 ヘルパー関数 (`helpers.go`)

- [ ] `randomHash(rng *rand.Rand) common.Hash`
- [ ] `waitForBlock(ctx, targetBlock) error`
- [ ] `compareAccountValues(ctx, blockTag, addr) error`
- [ ] `compareBlocks(ubt, ref *types.Block) error`
- [ ] `compareBytes(a, b []byte) error`
- [ ] `getSampleAddresses(count int) []common.Address`
- [ ] `abs(n int64) int64`

### 2.4 アンカーブロック (`anchor.go`)

- [ ] `getAnchorBlock(ctx) (*BlockAnchor, error)`
  - [ ] finalized 優先
  - [ ] safe フォールバック
  - [ ] latest-32 フォールバック

---

## 3. Phase 0: 前提チェック (`phase0_precondition.go`)

### 3.1 実装

- [ ] `Phase0_PreconditionCheck(ctx) error`
- [ ] UBT ノード接続確認 (`web3_clientVersion`)
- [ ] リファレンスノード接続確認
- [ ] ブロック高比較（差 100 以内）
- [ ] アンカーブロック取得
- [ ] `debug_accountRange` 対応確認
- [ ] `checkPreimageSupport(ctx, client, name, blockTag) error`
- [ ] **UBT root が取得できることの確認**（`debug_getUBTProof` の `UbtRoot` が非ゼロ）

### 3.2 テスト

- [ ] 正常系テスト
- [ ] UBT ノード未起動時のエラー確認
- [ ] リファレンスノード未起動時のエラー確認
- [ ] ブロック高不一致時のエラー確認

---

## 4. Phase 1: UBT 状態確認 (`phase1_ubt_status.go`)

### 4.1 実装

- [ ] `Phase1_UBTStatusCheck(ctx, anchor) error`
- [ ] `eth_syncing` が false であることを確認
- [ ] アンカーブロックで `eth_getBalance` が読めることを確認

### 4.2 テスト

- [ ] 正常系（full sync 完了）
- [ ] 同期中（eth_syncing=true）のエラー
- [ ] アンカーブロックで state が読めない場合のエラー

---

## 5. Phase 2: サンプリング値検証 (`phase2_values.go`)

### 5.1 アカウント検証

- [ ] `Phase2_ValueValidation(ctx, cfg) error`
- [ ] ランダムサンプリングループ
- [ ] `debug_accountRange` 呼び出し
  - [ ] フラグ設定: `nocode=false, nostorage=true, incompletes=false`
- [ ] アドレスパース (`common.HexToAddress`)
- [ ] `compareAccount(ctx, blockTag, addr, refAcc) error`
  - [ ] Balance 比較（decimal string → big.Int）
  - [ ] Nonce 比較
  - [ ] Code 比較

### 5.2 ストレージ検証

- [ ] `compareStorage(ctx, blockTag, addr, maxSlots) error`
- [ ] `debug_storageRangeAt` でキー列挙
- [ ] `entry.Key == nil` 時のスキップ処理
- [ ] `eth_getStorageAt` で値取得・比較

### 5.3 テスト

- [ ] 正常系（値一致）
- [ ] Balance 不一致検出
- [ ] Storage 不一致検出
- [ ] preimage 欠損時のスキップ動作

---

## 6. Phase 3: State 遷移検証 (`phase3_transition.go`)

### 6.1 実装

- [ ] `Phase3_TransitionValidation(ctx, blocks) error`
- [ ] ブロック待機ループ
- [ ] `debug_getModifiedAccountsByNumber` 呼び出し
- [ ] フォールバック: `extractBlockModifiedAddresses`
  - [ ] Coinbase 抽出
  - [ ] tx from/to 抽出
  - [ ] AccessList アドレス抽出
  - [ ] Withdrawals アドレス抽出
  - [ ] システムコントラクト (EIP-4788, EIP-2935)
- [ ] 各アドレスの値検証

### 6.2 テスト

- [ ] 正常系
- [ ] debug_getModifiedAccountsByNumber 失敗時のフォールバック

---

## 7. Phase 4: Witness Stateless 検証 (`phase4_witness.go`)

### 7.1 前提: 新規 RPC 実装（go-ethereum 本体）

- [ ] **`debug_executionWitnessUBT`** RPC 追加 (`eth/api_debug.go`)
  - [ ] `ExtUBTWitness` 型定義
  - [ ] `StatePaths` (path → node マッピング) を含める
  - [ ] 既存 `debug_executionWitness` は変更しない

### 7.2 前提: ExecuteStateless 修正（go-ethereum 本体）

- [ ] `core/stateless.go` に `usePathDB` パラメータ追加
  ```go
  func ExecuteStateless(..., usePathDB bool) (common.Hash, common.Hash, error)
  ```
- [ ] `usePathDB=true` 時に `witness.MakePathDB()` を使用
- [ ] **既存呼び出しの更新**（self-validation 等）
- [ ] **互換ラッパーの追加**（例: `ExecuteStatelessWithPathDB`）※選択肢

### 7.3 前提: Witness 変換関数（go-ethereum 本体）

- [ ] `NewWitnessFromUBTWitness(ext *ExtUBTWitness) (*Witness, error)` 追加
  - [ ] `StatePaths` を復元
  - [ ] 後方互換のため `State` にも blob を入れる

### 7.4 検証ツール実装

- [ ] `Phase4_WitnessValidation(ctx, blockNumbers) error`
- [ ] `debug_executionWitnessUBT` 呼び出し
- [ ] Block 取得
- [ ] `NewWitnessFromUBTWitness` で変換
- [ ] `ExecuteStateless(..., usePathDB=true)` 実行
- [ ] UBT root 比較（`debug_getUBTProof` から取得）
- [ ] Receipt root 比較

### 7.5 テスト

- [ ] 正常系（Witness 検証成功）
- [ ] UBT root 不一致検出
- [ ] Receipt root 不一致検出

---

## 8. Phase 5: RPC 整合性検証 (`phase5_rpc.go`)

### 8.1 基本 RPC テスト

- [ ] `Phase5_RPCConsistency(ctx) error`
- [ ] `eth_getBalance` 一致確認
- [ ] `eth_getCode` 一致確認
- [ ] `eth_getTransactionCount` 一致確認
- [ ] `eth_getStorageAt` 一致確認
- [ ] `eth_getBlockByNumber` 一致確認
- [ ] `eth_getBlockByHash` 一致確認
- [ ] `eth_getTransactionByHash` 一致確認
- [ ] `eth_getTransactionReceipt` 一致確認
- [ ] `eth_call` 一致確認

### 8.2 UBT Proof 検証

- [ ] `validateUBTProofs(ctx, blockTag) error`
- [ ] `debug_getUBTProof` 呼び出し
- [ ] Balance 値一致確認
- [ ] AccountProof 非空確認
- [ ] Merkle 検証（オプション）
- [ ] **BinaryTrie.Prove 未実装の回避方法を選定**（LeafProof 等）

### 8.3 テスト

- [ ] 各 RPC の正常系
- [ ] UBT Proof 検証正常系

---

## 9. 新規 RPC 実装（go-ethereum 本体）

### 9.1 `debug_getUBTProof` (`eth/api_debug.go`)

- [ ] `GetUBTProof(ctx, address, storageKeys, blockNrOrHash) (*UBTProofResult, error)`
- [ ] `StateAndHeaderByNumberOrHash` でステート取得
- [ ] アカウント情報取得 (balance, nonce, codeHash)
- [ ] `bintrie.NewBinaryTrie(header.Root, trieDB)` でトライオープン
- [ ] `bt.Hash()` で UBT root 計算
- [ ] アカウントプルーフ生成
  - [ ] `GetBinaryTreeKeyBasicData(address)` でキー導出
  - [ ] `generateProofFromBinaryTrie(bt, key)` でプルーフ取得
- [ ] ストレージプルーフ生成
  - [ ] `GetBinaryTreeKeyStorageSlot(address, key)` でキー変換
  - [ ] 各キーのプルーフ取得
- [ ] レスポンス構築

### 9.2 `debug_executionWitnessUBT` (`eth/api_debug.go`)

- [ ] `ExecutionWitnessUBT(ctx, blockNrOrHash) (*ExtUBTWitness, error)`
- [ ] 既存 Witness 生成ロジックを拡張
- [ ] `StatePaths` (path → node) を追加
- [ ] ルートノードを空パス `0x` で含める

### 9.3 プルーフ生成ヘルパー

- [ ] `generateProofFromBinaryTrie(bt, targetKey) ([]hexutil.Bytes, error)`
- [ ] **BinaryTrie.Prove 未実装の回避**（NodeIterator.LeafProof 方式等）
- [ ] `openBinaryTrie(root) (*bintrie.BinaryTrie, error)`

### 9.4 テスト

- [ ] `debug_getUBTProof` 単体テスト
- [ ] `debug_executionWitnessUBT` 単体テスト

---

## 10. core/stateless 修正（go-ethereum 本体）

### 10.1 Witness 変換

- [ ] `NewWitnessFromExtWitness(ext *ExtWitness) (*Witness, error)` 公開
- [ ] `NewWitnessFromUBTWitness(ext *ExtUBTWitness) (*Witness, error)` 追加

### 10.2 ExecuteStateless 拡張

- [ ] `usePathDB` パラメータ追加
- [ ] 条件分岐で `MakePathDB()` / `MakeHashDB()` 切り替え
- [ ] **既存APIとの互換性維持**（呼び出し全更新 or ラッパー）

### 10.3 テスト

- [ ] Witness 変換の単体テスト
- [ ] `ExecuteStateless(usePathDB=true)` の統合テスト

---

## 11. CLI エントリポイント (`main.go`)

### 11.1 フラグ定義

- [ ] `--ubt-rpc` (default: http://localhost:8545)
- [ ] `--reference-rpc` (required)
- [ ] `--account-samples` (default: 30000)
- [ ] `--storage-samples` (default: 500)
- [ ] `--seed` (default: time.Now().UnixNano())
- [ ] `--transition-blocks` (default: 5)
- [ ] `--witness-blocks` (default: 5)
- [ ] `--phases` (default: "all")

### 11.2 メインロジック

- [ ] `runValidator(c *cli.Context) error`
- [ ] 設定読み込み
- [ ] Validator 初期化
- [ ] Phase 選択・実行
- [ ] 結果出力

### 11.3 ビルド

- [ ] `Makefile` 作成
- [ ] `go build` 確認
- [ ] バイナリ動作確認

---

## 12. スクリプト・CI

### 12.1 実行スクリプト

- [ ] `scripts/run-validation.sh`
- [ ] `scripts/setup-reference-node.sh`

### 12.2 CI 統合

- [ ] GitHub Actions ワークフロー（オプション）
- [ ] Docker Compose での自動テスト（オプション）

---

## 13. ドキュメント

- [ ] README.md に検証ツールの使用方法追記
- [ ] UBT_VALIDATION_PLAN.md の最終確認
- [ ] コード内コメントの整備

---

## 実装順序の推奨

### Step 1: 基盤（Phase 0-3 + Phase 5 基本 RPC）

新規 RPC 不要で実装可能な部分を先行実装。

1. [ ] 基盤コード (型定義、Validator、ヘルパー)
2. [ ] Phase 0: 前提チェック
3. [ ] Phase 1: UBT 状態確認
4. [ ] Phase 2: サンプリング値検証
5. [ ] Phase 3: State 遷移検証
6. [ ] Phase 5: 基本 RPC 検証（eth_* のみ）
7. [ ] CLI エントリポイント
8. [ ] 動作確認・デバッグ

### Step 2: UBT Proof 対応

`debug_getUBTProof` を実装し、Phase 5 を完成させる。

1. [ ] `debug_getUBTProof` RPC 実装
2. [ ] プルーフ生成ヘルパー実装
3. [ ] Phase 5: UBT Proof 検証追加
4. [ ] 単体テスト・統合テスト

### Step 3: UBT Witness 対応

Phase 4 を完全実装。

1. [ ] **UBT root 取得経路を先に用意**（`debug_getUBTProof` or `debug_ubtRootByNumber`）
2. [ ] `debug_executionWitnessUBT` RPC 実装
3. [ ] `NewWitnessFromUBTWitness` 実装
4. [ ] `ExecuteStateless` の `usePathDB` 拡張
5. [ ] Phase 4: Witness Stateless 検証
6. [ ] 単体テスト・統合テスト

---

## 進捗サマリー

| カテゴリ | 完了 | 合計 | 進捗率 |
|----------|------|------|--------|
| 環境構築 | 0 | 12 | 0% |
| 基盤コード | 0 | 21 | 0% |
| Phase 0 | 0 | 12 | 0% |
| Phase 1 | 0 | 6 | 0% |
| Phase 2 | 0 | 17 | 0% |
| Phase 3 | 0 | 12 | 0% |
| Phase 4 | 0 | 21 | 0% |
| Phase 5 | 0 | 18 | 0% |
| 新規 RPC | 0 | 21 | 0% |
| core/stateless | 0 | 7 | 0% |
| CLI | 0 | 16 | 0% |
| スクリプト・CI | 0 | 4 | 0% |
| ドキュメント | 0 | 3 | 0% |
| **合計** | **0** | **170** | **0%** |

---

## 更新履歴

| 日付 | 内容 |
|------|------|
| 2026-01-19 | 初版作成 |
