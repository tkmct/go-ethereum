# UBT Sidecar 再実装仕様書

## 1. 目的

MPT（Merkle Patricia Trie）をUBT（Unified Binary Trie）に変換し、head stateに追いついてからはUBTのshadow stateでchainに追従する。

UBT stateは将来的にゼロ知識証明（ZKP）で独立に検証可能であり、state providerを信頼せずにstateの正当性を保証できる。本実装ではZKP検証そのものはスコープ外とし、**shadow stateの構築・維持・クエリ**に集中する。

---

## 2. スコープ

### 含むもの

| 機能 | 概要 |
|------|------|
| BinaryTrie | UBTのコアデータ構造（EIP-7864キーエンコーディング、SHA256ハッシュ、proof生成） |
| UBT Sidecar | MPTと並行してUBT shadow stateを管理するライフサイクル全体 |
| MPT→UBT変換 | pathdbのsnapshotを走査し、BinaryTrieに挿入。disk queue + catch-up |
| Core統合 | blockchain.goへのstate update転送フック、reorgハンドリング |
| RPC API | UBT stateに対するクエリ・proof生成エンドポイント（Ready状態のみ応答） |
| rawdb metadata | sidecar状態の永続化（currentRoot, conversion progress, queue） |
| CLIフラグ | sidecar有効化 |

### 含まないもの

| 除外項目 | 理由 |
|---------|------|
| `--state.ubt` モード（UBTをprimary backendとして使用） | sidecarモードのみ |
| executionWitness生成・検証 | 別途設計が必要（sidecar triedbでのblock再実行） |
| StatePaths / MakePathDB / ExtUBTWitness | executionWitnessと連動 |
| TransitionTrie変更 | executionWitnessと連動 |
| commitRootOverride / SkipStateRootValidation | primary backendモード専用 |
| rpc/service.go変更 | debug namespaceに統合し、multi-level namespace不要 |
| フロントエンドUI | 別リポジトリ |
| docker-ubt-test / E2Eテスト | 別リポジトリ管理 |
| ZKP検証 | 将来スコープ |
| snap sync対応 | preimage不在のため実現不可。full sync前提 |
| 計画ドキュメント（*.md） | コードベースに含めない |
| SanityCheck (`--ubt.sanity`) | 将来スコープ |
| `--ubt.batchsize` フラグ | ハードコード定数で十分 |
| `debug_ubtCall` | Phase 2。UBT state上でのEVM re-executionが必要で複雑度が桁違い |
| 巨大storage対応のサブバッチ | Phase 2。mutation capで当面は防護 |
| prometheus/expvarメトリクス | Phase 2。初期はログベースの観測で十分 |
| converting中のRPC応答 | Phase 2。MVPではconverting中は全RPC拒否 |
| Converting中のreorg後のconversion resume | Phase 2。Converting中のreorgはfull reconvert |
| PathDB NodeReader fallback | Phase 2。MVPは diff layer 内の root のみでRPC応答 |
| verkle対応 triedb.Recover | Phase 2。現在の pathdb.Recover は MPT (Keccak256) ハードコードで verkle 非対応 |

---

## 3. 前提条件

- **full sync** (`--syncmode=full`) でノードが同期済み、またはこれから同期する
- **path scheme** (`--state.scheme=path`) が有効
- **preimages** が有効（`--cache.preimages`、`--ubt` 設定時に自動有効化）
- ディスク容量: 4〜8TB程度

### 技術的前提（コード調査で確認済み）

- **pathdb.Recover() は verkle (isVerkle=true) で動作しない**: 内部の `apply()` 関数が MPT (`trie.Trie`, Keccak256) をハードコードしており、verkle root のハッシュ検証が必ず失敗する。Ready 中の reorg rollback には `triedb.Recover` を使わず、diff layer 直接参照方式を採用する（6.6節参照）
- **pathdb journal replay は writeBlockWithState を呼ばない**: pathdb の journal replay は pathdb 内部で diff layer を再構築するのみ。blockchain レベルのブロック再実行は行わない。sidecar の crash recovery は sidecar 独自の pathdb journal に依存する（12節参照）
- **verkle namespace 削除 API は既存**: `rawdb.NewTable(chainDB, string(rawdb.VerklePrefix)).DeleteRange(nil, nil)` で一括削除可能。既存の `resetVerkleTrie()` パターンを使用する
- **triedb.DiskRoot() は未公開**: disk layer root 取得の public API がないため、`triedb/database.go` に `DiskRoot()` メソッドを追加する

### ライフサイクル開始ルール

sidecarのライフサイクルはノード起動時に `--ubt` が有効であれば**即座に開始**する。full syncの完了を待たない。MPT stateがまだ存在しない場合（sync途中）はStale状態で待機し、stateが利用可能になった時点でauto-convertがトリガーされる。

---

## 4. CLIフラグ

| フラグ | 型 | デフォルト | 用途 | 公開理由 |
|--------|-----|-----------|------|---------|
| `--ubt` | bool | false | UBT shadow stateを有効化。auto-convertは常にon | UBT機能全体のon/off |

### `--ubt` が有効な場合の自動設定

- `--state.scheme=path` が強制される
- `--syncmode=full` が必須（それ以外はエラー）
- `--cache.preimages` が自動有効化
- MPT→UBT自動変換が常にon（別フラグ不要）

### バリデーション

```
--ubt=true かつ --syncmode!=full → エラー
```

---

## 5. ステートマシン

### 5.1 状態定義

```go
type SidecarState uint8

const (
    StateDisabled   SidecarState = iota // --ubt 未設定
    StateStale                          // UBT stateが無い or 破損。変換が必要
    StateConverting                     // MPT→UBT変換中
    StateReady                          // UBT stateが有効。ブロック追従中
)
```

| 状態 | 説明 |
|------|------|
| **Disabled** | `--ubt` 未設定。全操作no-op |
| **Stale** | UBT stateが無い or 破損。変換が必要 |
| **Converting** | MPT→UBT変換中。disk queueにブロックを蓄積 |
| **Ready** | UBT stateが有効。ブロックごとにApplyStateUpdateで追従 |

### 5.2 状態遷移

**起動時（InitFromDB）**:
```
Disabled  → Stale       : --ubt有効、valid stateなし
Disabled  → Ready       : --ubt有効、valid currentRoot発見
```

**ランタイム**:
```
Stale     → Converting  : auto-convertがBeginConversion()を呼ぶ
Converting → Ready      : 変換 + queue replay完了
Converting → Stale      : 回復不能エラー or reorg
Ready     → Ready       : reorg時、ancestor rootがdiff layerに存在（diff layer rollback）
Ready     → Stale       : 回復不能エラー or reorg時ancestor rootがdiff layerに不在
```

### 5.3 禁止遷移（ランタイム）

- `Ready → Converting`: Readyから直接変換を開始しない。必ずStaleを経由
- `Disabled → Converting`: Disabledから直接変換を開始しない
- `Disabled → Stale/Ready`: Disabledはランタイム中に他の状態に遷移しない（起動時のInitFromDBのみ）
- `Stale → Ready`: Converting を経由せずReadyにはならない

### 5.4 起動時のsync状態との関係

- full sync未完了（MPT stateなし）→ Stale で待機。auto-convertは `headBlockFn()` が nil を返す場合スキップ
- full sync完了済み → 通常の InitFromDB 判定（5.2の状態遷移に従う）

---

## 6. コンポーネント詳細

### 6.1 BinaryTrie (`trie/bintrie/`)

既存実装をそのまま維持。UBTのコアデータ構造。

#### ノード型

| ノード | 構造 | 役割 |
|--------|------|------|
| `InternalNode` | Left, Right 子ノード | ビット深度での二分岐 |
| `StemNode` | 31byte stem + 256個のvalue slot | 葉ノード。共有パスの表現 |
| `HashedNode` | 32byte hash | DB上のノードへの遅延参照 |
| `Empty` | - | 空ノード |

#### キーエンコーディング (EIP-7864)

- アカウント `BasicData` = stem(31byte) + suffix `0x00`
- アカウント `CodeHash` = stem(31byte) + suffix `0x01`
- BasicDataとCodeHashは同一stemを共有
- Storage slot = address由来stem + slot由来suffix

#### ハッシュ計算

- `InternalNode`: `SHA256(left_hash || right_hash)`
- `StemNode`: 256値をMerkle tree化（8段、2^8=256葉）→ `SHA256(stem || 0x00 || merkle_root)`

#### Proof生成

```go
type ProofSibling struct {
    Depth uint16       // siblingが位置するビット深度
    Hash  common.Hash  // siblingのhash
}
```

検証: leafからrootに向かってsiblingを使いhash再計算。keyのビットでsiblingの左右を判定。

#### 削除セマンティクス

BinaryTrieのアカウント削除は、そのアカウントのstorage slotとcontract codeを自動削除する（同一stemの全suffix値がクリアされる）。applyUBTUpdateにおける削除順序は不問。

#### Mutation Count（追加）

OOM防止のため `mutationCount uint64` フィールドを BinaryTrie に追加する。`UpdateAccount`、`UpdateStorage`、`UpdateContractCode`、`DeleteAccount`、`DeleteStorage` の各呼び出しで +1 インクリメント。`Commit()` で 0 にリセット。変換バッチ内で `mutationCount > maxMutationsPerBatch (10_000_000)` の場合、バッチ途中であっても flush を実行する（6.4節参照）。

### 6.2 UBT Sidecar (`sidecar/ubt_sidecar.go`)

#### 構造体

```go
type UBTSidecar struct {
    mu sync.RWMutex

    // 状態（enum）
    state SidecarState

    // 変換制御
    convertCtx    context.Context
    convertCancel context.CancelFunc
    convertWg     sync.WaitGroup

    // 現在のUBT state
    currentRoot  common.Hash
    currentBlock uint64
    currentHash  common.Hash

    // DB
    triedb    *triedb.Database   // UBT専用（既存triedb.Database, isVerkle=true）
    chainDB   ethdb.Database     // メインchainDB（metadata, disk queue）
    mptTrieDB *triedb.Database   // MPT TrieDB（preimage解決、iterator取得）
}
```

**注記**: queueStart/queueEndのstructキャッシュは設けない。queue metadataはrawdbを直接読み書きする（EnqueueUpdateは最大12s/1回の頻度でありrawdb read/writeの負荷は無視できる）。

#### 並行性・ロック戦略

**原則**: `mu` (RWMutex) は `state` と currentRoot/currentBlock/currentHash を保護する。triedbとchainDBは内部でスレッドセーフ。ロックは `mu` のみ。

| 操作 | ロック | 競合する操作 | 排他メカニズム |
|------|--------|-------------|---------------|
| ApplyStateUpdate | mu.Lock (メタデータ更新時) | RPC読み取り | 状態遷移で排他。Readyでなければ呼ばれない |
| ConvertFromMPT | mu.Lock (state更新時のみ) | ApplyStateUpdate | **状態遷移で排他**: Converting中はApplyは呼ばれない |
| EnqueueUpdate | chainDB write | replayUpdateQueue | **時系列で排他**: Enqueueはconvertingフェーズ、replayは変換完了後 |
| HandleReorg | mu.Lock + convertCancel | ConvertFromMPT | context cancelでgoroutine終了 → WaitGroup待機 → reorg実行 |
| RPC読み取り | mu.RLock (root取得時) | ApplyStateUpdate | RPCはcurrentRootで新BinaryTrieインスタンスを開く |

**キーポイント**:
- ConvertFromMPTとApplyStateUpdateは同時に実行されない（blockchain.goが `State()` で分岐）
- EnqueueUpdateはchainDB writeのみ。muの取得は不要（stateは変更しない）。chainDBは内部でスレッドセーフ

#### Public API

blockchain.go / RPC層から呼び出す公開メソッド一覧。

**クエリ系**:
- `State() SidecarState`
- `CurrentRoot() common.Hash`
- `CurrentInfo() (root common.Hash, block uint64, hash common.Hash)`
- `GetUBTRoot(blockHash common.Hash) (common.Hash, bool)`
- `OpenBinaryTrie(root common.Hash) (*bintrie.BinaryTrie, error)`

**更新系**:
- `ApplyStateUpdate(block *types.Block, update *state.StateUpdate) error` — code解決等のDB参照は `sc.chainDB` を使用
- `HandleReorg(ancestorHash common.Hash, ancestorNum uint64) error` — converting中のgoroutine停止も内部で処理
- `Shutdown()` — convertCancel → convertWg.Wait → triedb.Commit → triedb.Close

**変換系**:
- `BeginConversion() bool` — `true` = 変換開始成功、`false` = 既にConvertingまたはReady
- `ConvertFromMPT(ctx context.Context, chain ChainContext) error`
- `EnqueueUpdate(block *types.Block, update *state.StateUpdate) error`

#### ChainContext interface

```go
type ChainContext interface {
    HeadRoot() common.Hash
    HeadBlock() *types.Header
    CanonicalHash(uint64) common.Hash
}
```

blockchain.go が実装し、ConvertFromMPT に渡す。テスト時は mock で差し替え可能。

### 6.3 State Update適用 (`applyUBTUpdate`)

1. `currentRoot` でBinaryTrieを開く
2. アカウントを**deletions**と**updates**に分類（ソート済み）
3. Deletions: `trie.DeleteAccount()` を呼ぶ（BinaryTrieが同一stemのstorage/codeを自動削除）
4. Updates: `trie.UpdateAccount` → `trie.UpdateContractCode` → `trie.UpdateStorage`（各slot）
5. `trie.Commit()` → `triedb.Update()` で差分マージ
6. metadata書き込み: `WriteUBTCurrentRoot`, `WriteUBTBlockRoot`

`triedb.Update()` 内でpathdbの `tree.cap(root, maxDiffLayers)` が自動的に呼ばれ、diff layerがmaxDiffLayersを超えると古いlayerがdiskにflushされる。明示的な `triedb.Commit()` は不要（8.1節参照）。

**冪等性**: `BinaryTrie.UpdateAccount`、`UpdateStorage`、`UpdateContractCode` は同じデータの再挿入で正しく動作する（上書き）。これは変換再開時の安全性に必要（6.4節参照）。

### 6.4 MPT→UBT変換 (`sidecar/ubt_convert.go`)

#### ConvertFromMPT シグネチャ

```go
func (sc *UBTSidecar) ConvertFromMPT(ctx context.Context, chain ChainContext) error
```

- `ctx`: reorg時やshutdown時のgoroutine中断に使用
- `chain.HeadRoot()`: staleリトライ時に最新rootを動的取得
- `chain.HeadBlock()`: queue replay終了判定に使用。nilを返す場合はhead state未構築（sync途中）
- `chain.CanonicalHash()`: queue replay中のcanonical hash確認に使用
- iteratorは `sc.mptTrieDB`（コンストラクタで渡される）のsnapshot layerから取得
- code解決は `sc.chainDB` から `rawdb.ReadCode` で取得

#### コンストラクタ

```go
func NewUBTSidecar(chainDB ethdb.Database, mptTrieDB *triedb.Database, config *UBTConfig) *UBTSidecar
```

`mptTrieDB` はMPT側のtriedb.Database。preimage解決とsnapshot iterator取得に使用。

#### 変換アルゴリズム

1. **BeginConversion**:
   - conversion progress metadataが存在する場合 → **再開**（6.4.1節参照）
   - 存在しない場合 → verkle namespace全消去（既存の `resetVerkleTrie()` パターン: `rawdb.NewTable(chainDB, string(rawdb.VerklePrefix)).DeleteRange(nil, nil)` → triedb再作成）→ 新規変換開始
   - `state=Converting` → conversion metadata永続化 → `convertCtx`/`convertWg` 生成
2. **アカウント走査**: AccountIterator開始（再開時は `lastProcessedAccountKey` から）。各アカウントについて:
   - preimage解決（MPT TrieDB cache → chainDB fallback）
   - `trie.UpdateAccount` + `trie.UpdateContractCode` （in-memory操作）
   - `convertAccountStorage(accountHash, storageRoot)` でstorage走査: `mptTrieDB` からStorageIteratorを取得し、各storage slotを `trie.UpdateStorage` で挿入（in-memory操作）。iteratorがstaleになった場合はアカウントレベルのstaleリトライと同じバックオフで対処
3. **バッチコミット**: 以下のいずれかの条件で `trie.Commit() → triedb.Update() → triedb.Commit()` を実行:
   - 定数 `conversionBatchSize = 500_000` アカウント処理完了
   - `trie.MutationCount() > maxMutationsPerBatch (10_000_000)` — 巨大storageアカウントによるOOM防止

   バッチ内では `triedb.Update` を呼ばない（全てin-memory操作）。コミット後にconversion progress metadata更新。バッチ境界で `ctx.Done()` をチェック
4. **Staleリトライ**: iteratorがstaleになった場合、指数バックオフでリトライ（初期wait 1s、倍率 2x、個別wait上限 60s、総時間上限 30分）。30分の根拠: pathdb auto-capは~128ブロック×12s ≈ 25分周期でflush。30分は1 cap cycle + margin。総時間上限到達でconversion progressを保持したままStaleに遷移。auto-reconvertは前回のbatch境界から再開する（full reconvertではない）
5. **Queue replay**: 変換完了後、disk queueから順次適用（6.4.2節参照）
6. **Finalize**: `state=Ready`

#### 6.4.1 変換の再開（Conversion Resume）

conversion progress metadataが存在する場合、前回の進捗地点から再開する。

**ConversionProgress metadata**:
```go
type UBTConversionProgress struct {
    Root                    common.Hash  // 変換開始時のMPT root
    Block                   uint64       // 変換開始時のblock number
    BlockHash               common.Hash  // 変換開始時のblock hash
    Started                 uint64       // 開始時刻 (unix)
    LastProcessedAccountKey common.Hash  // iteratorの再開位置
    ProcessedAccounts       uint64       // 処理済みアカウント数
}
```

**再開ロジック**:
1. progress metadataを読み取り
2. verkle namespace上に前回のバッチコミットデータが存在するか検証
3. 存在する → 前回のcommitted rootでBinaryTrieを開く → `LastProcessedAccountKey` からiterator再開
4. 存在しない（データ破損）→ full reconversionにフォールバック（verkle namespace reset）

**整合性保証**:
- progress metadataはバッチコミット完了時に更新する。つまり中断はバッチ境界で起きる
- 最後のバッチコミット以降に処理されたアカウントはメモリ上のみで、crashで失われる
- 再開時にこれらのアカウントは再処理される → **冪等性**により安全（6.3節参照）

**フォールバック**:
- iterator再開位置がsnapshot変更により無効（stale）→ 通常のstaleリトライで対処
- staleリトライも失敗 → full reconversionにフォールバック
- progress metadata自体が破損 → full reconversion

**reorg後の変換**: reorg後（Converting中のreorg）にBeginConversionが呼ばれた場合、**conversion progressを破棄**し、disk queueをクリアし、verkle namespace全消去の上で新規変換を開始する。理由: reorgによりcanonical chainが変わった場合、旧progressが参照するsnapshot rootが無効になりうるため。

#### 6.4.2 Disk Queue

変換中に到着するブロックのstate updateを蓄積するdisk queue。enqueue頻度は最大1回/12s（block time）であり、disk I/Oはボトルネックにならない。

**Disk Queue**:
- rawdb prefix方式: `UBTUpdateQueuePrefix + blockNum(8byte big-endian) + blockHash`
- 既存のrawdb accessorを活用
- queue metadata（start/end blockNum）はrawdbに永続化。structキャッシュは設けない（12s/1回のrawdb readで十分）

**EnqueueUpdate**:
```
1. UBTUpdate を生成
2. RLPエンコード → rawdb保存（UBTUpdateQueuePrefix + blockNum + blockHash）
3. queue metadata更新（queueEnd = blockNum）— rawdb直接書き込み
```

**replayUpdateQueue**:
```
1. Disk queueからブロック番号順に読み出し・適用（rawdb prefix iterationでqueueStart→queueEnd）
2. 各エントリ:
   - blockNum <= currentBlock → スキップ（snapshot時点で処理済み）
   - canonical hash確認（chain.CanonicalHash(blockNum)で取得）
   - parent hash連続性確認: UBTUpdate.ParentHash == 前エントリのBlockHash
   - applyUBTUpdate() で適用
3. 処理済みエントリを削除
4. 各ブロック処理後に ctx.Done() をチェック
```

#### Goroutineライフサイクル管理

```
blockchain.go: maybeStartUBTAutoConvert()
  │
  ├─ sc.BeginConversion() → convertCtx, convertCancel, convertWg 生成
  │   返り値 true → 開始成功、false → 既にConverting or Ready
  │
  └─ go func() {
       sc.convertWg.Add(1)
       defer sc.convertWg.Done()
       sc.ConvertFromMPT(sc.convertCtx, chainContextAdapter)
     }()

HandleReorg() 発生時:
  Converting中:
    1. sc.convertCancel() 呼び出し
    2. sc.convertWg.Wait() で goroutine 終了待機
    3. state=Stale → auto-convertが再変換トリガー（full reconvert）
  Ready中:
    1. diff layer rollback を試行（6.6節参照）
    2. 成功 → Ready維持。失敗 → state=Stale

shutdown時:
  1. sc.convertCancel() 呼び出し
  2. sc.convertWg.Wait() で goroutine 終了待機
  3. sc.triedb.Commit() → sc.triedb.Close()
  （disk queueは既に永続化済みのためflush不要）
```

**バッチコミット途中での中断**: `ctx.Done()` チェックはバッチ境界（`trie.Commit → triedb.Update → triedb.Commit → progress metadata更新` の完了後）で行う。バッチ処理の途中では中断しない。triedbの一貫性が保証される。

#### Queue replay中のReorg対応

1. `HandleReorg` が `convertCancel()` → `convertWg.Wait()` でgoroutine終了待ち
2. sidecarが Stale になり、auto-convertが再変換をトリガー
3. 新しいBeginConversionで **conversion progressを破棄** し、disk queueをクリアし、verkle namespace全消去の上で新規変換を開始

### 6.5 UBTUpdate (`sidecar/ubt_update.go`)

StateUpdateからUBT更新に必要な情報を抽出:

```go
type UBTUpdate struct {
    BlockNum   uint64
    BlockHash  common.Hash
    ParentHash common.Hash
    RawStorageKey bool
    Accounts      map[common.Hash][]byte
    AccountsOrigin map[common.Address][]byte
    Storages      map[common.Hash]map[common.Hash][]byte
    StoragesOrigin map[common.Address]map[common.Hash][]byte
    Codes         map[common.Address][]byte
}
```

- 二重エンコーディング: hashed form（trie更新用）+ raw form（preimage解決用）
- RLP直列化: 全エントリを `common.Hash` のbyte lexicographic orderでソートして決定論的エンコード（disk queue保存用）

### 6.6 Core統合

#### blockchain.go

**追加フィールド**:
```go
ubtSidecar             *sidecar.UBTSidecar
ubtAutoConvertMu       sync.Mutex
lastUBTConvertAttempt   time.Time
```

**ブロック処理フック** (`writeBlockWithState`):
```
StateUpdate取得（CommitWithUpdate経由）
  │
  ├─ sidecar.State() == Ready → ApplyStateUpdate(block, update)
  │     失敗時 → maybeStartUBTAutoConvert("apply update")
  │
  ├─ sidecar.State() == Converting → EnqueueUpdate(block, update)
  │
  └─ それ以外 → maybeStartUBTAutoConvert("sidecar stale")
```

**auto-convert**:
- 30秒クールダウン付き
- `ConvertFromMPT` を goroutine で実行。`convertCtx` / `convertWg` を使用
- head stateが利用不可の場合はスキップ（`chain.HeadBlock()` が nil）
- 失敗時は再度 `maybeStartUBTAutoConvert` を呼ぶ

**reorg** (`reorg()`内):

```
sidecar.HandleReorg(ancestorHash, ancestorNum):
  │
  ├─ Converting中:
  │   1. convertCancel() → convertWg.Wait() でgoroutine終了
  │   2. state=Stale
  │   3. blockchain.go側で maybeStartUBTAutoConvert("reorg")
  │
  └─ Ready中（diff layer rollback）:
      1. ancestorUBTRoot, found := GetUBTRoot(ancestorHash)
      2. found == false → fail("reorg", "ancestor UBT root not found") → Stale
      3. OpenBinaryTrie(ancestorUBTRoot) で検証
         ├─ 成功 → currentRoot=ancestorUBTRoot, currentBlock=ancestorNum,
         │         currentHash=ancestorHash → Ready維持
         │         (旧forkのorphaned diff layerはlayer treeに残るが、
         │          浅いreorgでは数MB。再起動時に消える。MVPで許容)
         └─ 失敗 → fail("reorg", err) → Stale
```

**triedb.Recover を使わない理由**: pathdb.Recover() 内部の `apply()` が MPT (Keccak256) をハードコードしており、verkle (SHA256) の root ハッシュ検証が必ず失敗する。代わりに、ancestor の UBT root が diff layer 内に存在することを確認し、currentRoot ポインタを直接巻き戻す。reorg は通常浅い（1-2ブロック）ため、ancestor root は maxDiffLayers (128) 内の diff layer に存在する。

**起動時**:
1. sidecar triedb open（内部でpathdb disk layer load + journal replay）
2. `InitFromDB()` 呼び出し（詳細は12節参照）
3. blockchain open（通常の起動処理）
4. `maybeStartUBTAutoConvert("startup")`

**MUST**: InitFromDB は blockchain の通常ブロック処理開始より前に完了すること。

**shutdown** (`Stop()`内):
1. `ubtSidecar.Shutdown()` を呼ぶ（内部でconvertCancel → convertWg.Wait → triedb.Commit → triedb.Close）
2. blockchain.goはconvertCancel/convertWgに直接アクセスしない
3. disk queueは既に永続化済みのためflush不要

#### stateupdate.go

- `type StateUpdate = stateUpdate` — 型エクスポート
- Public getter: `Accounts()`, `AccountsOrigin()`, `Storages()`, `StoragesOrigin()`, `RawStorageKey()`, `Codes()`
- `CommitWithUpdate()` — state commitと同時にStateUpdateを返す

#### rawdb

**スキーマ** (`schema.go`):
```go
UBTCurrentRootKey        = []byte("ubt-current-root")
UBTConversionProgressKey = []byte("ubt-conv-progress")
UBTBlockRootPrefix       = []byte("ubt-block-root-")     // + blockHash
UBTUpdateQueuePrefix     = []byte("ubt-update-queue-")   // + blockNum(8byte big-endian) + blockHash
UBTUpdateQueueMetaKey    = []byte("ubt-update-queue-meta")
```

**アクセサ** (`accessors_ubt_sidecar.go`):
- `Read/WriteUBTCurrentRoot` — (root, blockNum, blockHash)
- `Read/Write/DeleteUBTConversionProgress` — 6.4.1節のフィールド
- `Read/WriteUBTBlockRoot` — blockHash → UBT root
- `Read/Write/DeleteUBTUpdateQueueMeta` — (start, end blockNum)

### 6.7 RPC API

#### エンドポイント

| メソッド | パラメータ | 戻り値 |
|---------|-----------|--------|
| `debug_ubtGetBalance` | (address, blockNrOrHash) | `*hexutil.Big` |
| `debug_ubtGetAccount` | (address, blockNrOrHash) | `UBTAccountResult` |
| `debug_ubtGetStorageAt` | (address, slot, blockNrOrHash) | `hexutil.Bytes` |
| `debug_ubtGetProof` | (address, storageKeys[], blockNrOrHash) | `UBTProofResult` |
| `debug_ubtSyncing` | () | `UBTSyncStatus` |

#### レスポンス型

```go
type UBTAccountResult struct {
    Address  common.Address  `json:"address"`
    Balance  *hexutil.Big    `json:"balance"`
    Nonce    hexutil.Uint64  `json:"nonce"`
    CodeHash common.Hash     `json:"codeHash"`
    CodeSize hexutil.Uint64  `json:"codeSize"`
}

type UBTProofNode struct {
    Depth uint16      `json:"depth"`
    Hash  common.Hash `json:"hash"`
}

type UBTStorageProof struct {
    Key       common.Hash    `json:"key"`
    Value     hexutil.Bytes  `json:"value"`
    ProofPath []UBTProofNode `json:"proofPath"`
}

type UBTProofResult struct {
    Address          common.Address   `json:"address"`
    AccountProofPath []UBTProofNode   `json:"accountProofPath"`
    Balance          *hexutil.Big     `json:"balance"`
    CodeHash         common.Hash      `json:"codeHash"`
    Nonce            hexutil.Uint64   `json:"nonce"`
    BlockHash        common.Hash      `json:"blockHash"`
    BlockNumber      hexutil.Uint64   `json:"blockNumber"`
    StorageProof     []UBTStorageProof `json:"storageProof"`
    UbtRoot          common.Hash      `json:"ubtRoot"`
}

type UBTSyncStatus struct {
    State        string           `json:"state"`  // "disabled", "stale", "converting", "ready"
    CurrentBlock hexutil.Uint64   `json:"currentBlock"`
    CurrentRoot  common.Hash      `json:"currentRoot"`
    ChainHead    hexutil.Uint64   `json:"chainHead"`
}
```

**`debug_ubtSyncing` は常にオブジェクトを返す**（eth_syncingのようなboolショートカットは使わない）。`State` フィールドにより全状態を判別可能。disabled時もエラーではなく `State: "disabled"` のオブジェクトを返す。

#### Block tag 対応

| tag | 動作 |
|-----|------|
| `"latest"` | `CurrentRoot()` を返す（Ready時のみ） |
| `"earliest"`, `"finalized"`, `"safe"` | blockHash に解決し `GetUBTRoot(blockHash)` で lookup |
| `"pending"` | エラー: `"pending block not supported for UBT"` |
| explicit number/hash | `GetUBTRoot(resolvedBlockHash)` で lookup |

#### Root解決ロジック

| リクエスト | sidecar状態 | 動作 |
|-----------|-------------|------|
| explicit block | Ready | `GetUBTRoot(blockHash)` で lookup。見つからなければエラー |
| explicit block | それ以外 | エラー: `"ubt sidecar not ready"` |
| "latest" | Ready | `CurrentRoot()` を返す |
| "latest" | Converting / Stale | エラー: `"ubt sidecar not ready"` |
| 任意 | Disabled | エラー: `"ubt sidecar not enabled"`（`ubtSyncing` のみ `State: "disabled"` のオブジェクトを返す） |

**注記**: PathDB NodeReader fallback は Phase 2。MVPでは diff layer 内の root のみでRPC応答する。diff layer に存在しない古いブロックの root は open 失敗でエラーとなる。

#### RPCエラーコントラクト

| 条件 | エラーメッセージ |
|------|----------------|
| sidecar Disabled | `"ubt sidecar not enabled"` |
| sidecar Stale / Converting | `"ubt sidecar not ready"` |
| pending block 指定 | `"pending block not supported for UBT"` |
| 指定ブロックが見つからない | `"block not found"` |
| 指定ブロックのUBT rootが存在しない | `"ubt root not found for block <hash>"` |
| trie open失敗 | `"failed to open ubt trie: <detail>"` |
| account/storage読み取り失敗 | `"failed to read <target>: <detail>"` |

### 6.8 PathDB 変更

以下の export を追加する。NodeReader fallback は Phase 2。

- `pathdb/errors.go`: `ErrSnapshotStale` を public export（変換リトライで使用）
- `pathdb/database.go`: `MaxDiffLayers` を public export（sidecar gap判定で使用）
- `triedb/database.go`: `DiskRoot() common.Hash` メソッドを追加（crash recovery で使用）

```go
// triedb/database.go
func (db *Database) DiskRoot() common.Hash {
    pdb, ok := db.backend.(*pathdb.Database)
    if !ok {
        return common.Hash{}
    }
    return pdb.DiskRoot()
}

// triedb/pathdb/database.go
func (db *Database) DiskRoot() common.Hash {
    return db.tree.bottom().rootHash()
}
```

---

## 7. エラー分類

### 7.1 エラー種別と対応

| 種別 | 例 | 対応 | 遷移先 |
|------|-----|------|--------|
| **ハードフェイル** | preimage欠損、trie corruption、triedb open失敗 | `fail()` → Stale → reconversion | Stale |
| **一時的エラー** | pathdb.ErrSnapshotStale、"snapshot not constructed"、"unknown layer" | 変換中: backoffリトライ。リトライ上限到達でハードフェイル扱い | (リトライ中は遷移なし) |
| **ロジックエラー** | RLPデコード失敗、queue内のparent hash不整合 | `fail()` → Stale → reconversion | Stale |
| **ユーザーエラー** | RPCで存在しないブロック指定、sidecar未有効 | RPCエラーを返す。sidecar状態は変化しない | (遷移なし) |

### 7.2 fail() の動作

```go
func (sc *UBTSidecar) fail(stage string, err error) error {
    sc.mu.Lock()
    sc.state = StateStale
    sc.mu.Unlock()
    log.Error("UBT sidecar failure", "stage", stage, "err", err)
    return fmt.Errorf("ubt sidecar %s: %w", stage, err)
}
```

すべての回復不能エラーは `fail()` を通る。一時的エラーのうちリトライで回復できたものは `fail()` を呼ばない。

### 7.3 明示的な前提

- **preimage欠損はハードフェイル**。full sync + `--cache.preimages` が前提であり、preimageが見つからない場合はデータ破損として扱う
- **一時的エラーのリトライ対象は変換時のみ**。通常のApplyStateUpdate中の一時的エラーはハードフェイル扱い（ブロック処理のクリティカルパスでリトライすると遅延が大きい）

---

## 8. Commit同期

sidecarの明示的commit logicは**設けない**。pathdbのauto-capメカニズムに統一する。

**メカニズム**: pathdbの `triedb.Update()` は内部で `tree.cap(root, maxDiffLayers)` を呼ぶ。diff layerがmaxDiffLayersを超えると古いlayerが自動的にdiskにflushされる。

```
writeBlockWithState (blockchain.go)
  │
  ├─ statedb.CommitWithUpdate()
  │   └─ MPT triedb.Update() → tree.cap() → disk flush (if >maxDiffLayers)
  │
  └─ sidecar.ApplyStateUpdate()
      └─ applyUBTUpdate()
          └─ UBT triedb.Update() → tree.cap() → disk flush (if >maxDiffLayers)
```

同一ブロックの `writeBlockWithState` 内で両方の `triedb.Update()` が呼ばれるため、disk flush blockが自然に一致する。shutdown時のみ `sidecar.triedb.Commit()` を明示的に呼ぶ。

変換バッチではauto-capとは別に `triedb.Commit()` を明示的に呼ぶ。これはバッチ境界でのチェックポイント目的であり、auto-capと非排他で共存する。

---

## 9. Observability

### ログ

| イベント | レベル | 内容 |
|---------|--------|------|
| 変換開始 | Info | `"Starting UBT conversion" block=N root=0x...` |
| 変換再開 | Info | `"Resuming UBT conversion" lastKey=0x... accounts=N` |
| バッチコミット | Info | `"UBT conversion progress" accounts=500000 elapsed=...` |
| mutation cap flush | Info | `"UBT conversion mutation cap flush" mutations=N` |
| 変換完了 | Info | `"UBT conversion complete" accounts=N duration=...` |
| 変換失敗 | Error | `"UBT conversion failed" err=...` |
| Stale遷移 | Warn | `"UBT sidecar marked stale" stage=... err=...` |
| Ready遷移 | Info | `"UBT sidecar ready" block=N root=0x...` |
| reorg rollback成功 | Info | `"UBT sidecar reorg rollback" ancestor=N` |
| reorg rollback失敗 | Warn | `"UBT sidecar reorg rollback failed, going stale" err=...` |
| iterator stale retry | Debug | `"UBT conversion iterator stale, retrying" attempt=N` |
| queue enqueue | Debug | `"UBT queue enqueue" block=N` |
| ApplyStateUpdate失敗 | Error | `"Failed to apply UBT state update" block=N err=...` |

### ガイドライン

- **steady state（通常追従時）**: ブロックごとのログは出力しない
- **変換中**: バッチコミットごとにInfoログ。個別アカウント処理はログしない
- **エラー時**: Errorレベル。stage名とerrを常に含める
- **Debug**: iterator retry等の詳細はDebugレベル

---

## 10. ファイル変更一覧

### 新規作成

| ファイル | 内容 |
|---------|------|
| `sidecar/ubt_sidecar.go` | UBTSidecar構造体、SidecarState enum、ChainContext interface、ライフサイクル |
| `sidecar/ubt_update.go` | UBTUpdate構造体とStateUpdate変換 |
| `sidecar/ubt_convert.go` | MPT→UBT変換 + disk queue replay |
| `sidecar/errors.go` | エラー型 |
| `sidecar/ubt_sidecar_test.go` | sidecar unit test |
| `sidecar/ubt_convert_test.go` | 変換 unit test |
| `sidecar/ubt_stateupdate_test.go` | state update unit test |
| `core/rawdb/accessors_ubt_sidecar.go` | metadata CRUD |
| `eth/api_ubt.go` | RPC API実装 + 型定義 |
| `eth/api_debug_ubt_test.go` | RPC unit test |

### 既存ファイル変更

| ファイル | 変更内容 |
|---------|---------|
| `core/blockchain.go` | UBTSidecarフィールド追加、writeBlockWithStateフック、reorgフック（diff layer rollback）、auto-convert、shutdown |
| `core/state/stateupdate.go` | StateUpdate型export、public getter追加 |
| `core/rawdb/schema.go` | UBTキー定数追加 |
| `cmd/utils/flags.go` | `--ubt` 定義とバリデーション |
| `eth/backend.go` | config転送 + debug namespaceへのAPI登録 |
| `eth/ethconfig/config.go` | UBT関連configフィールド追加 |
| `eth/ethconfig/gen_config.go` | config自動生成ファイルの更新 |
| `triedb/database.go` | `DiskRoot()` メソッド追加 |
| `triedb/pathdb/database.go` | `DiskRoot()` メソッド追加、`MaxDiffLayers` public export |
| `triedb/pathdb/errors.go` | `ErrSnapshotStale` public export |
| `trie/bintrie/trie.go` | `mutationCount` フィールド追加、Update*/Delete* メソッドでインクリメント、`MutationCount()` getter、`Commit()` でリセット |

### 変更不要

| ファイル | 除外理由 |
|---------|---------|
| `rpc/service.go` | debug namespaceに統合、routing変更不要 |
| `core/block_validator.go` | SkipStateRootValidation不要 |
| `core/genesis.go` | commitRootOverride不要 |
| `core/state/statedb.go` | CommitWithRoot系不要（CommitWithUpdateのみ） |
| `core/state/database.go` | UBTモード分岐不要 |
| `trie/transitiontrie/transition.go` | executionWitnessスコープ外 |
| `core/stateless/*` | StatePaths, MakePathDB, ExtUBTWitness不要 |
| `params/config.go` | IsVerkleGenesis変更不要 |
| `triedb/pathdb/reader.go` | NodeReader fallback Phase 2 |

---

## 11. データフロー図

### ブロック処理時のstate update転送

```
Block Execution (StateProcessor)
         │
         ▼
  StateDB.CommitWithUpdate()
         │
         ├─ MPT state commit（通常通り）
         │   └─ MPT triedb.Update() → auto-cap (disk flush if >maxDiffLayers)
         └─ StateUpdate を返す
                │
                ▼
  blockchain.writeBlockWithState()
         │
         ├─ sidecar.State() == Ready?
         │   └─ Yes → sidecar.ApplyStateUpdate(block, update)
         │               ├─ NewUBTUpdate(block, update)
         │               ├─ applyUBTUpdate()
         │               │     ├─ OpenBinaryTrie(currentRoot)
         │               │     ├─ 削除/更新処理
         │               │     ├─ trie.Commit() → triedb.Update() → auto-cap
         │               │     └─ metadata書き込み (currentRoot, blockRoot)
         │               └─ (explicit commit不要。auto-capで同期済み)
         │
         ├─ sidecar.State() == Converting?
         │   └─ Yes → sidecar.EnqueueUpdate(block, update)
         │                └─ RLPエンコード → disk queue保存
         │
         └─ else → maybeStartUBTAutoConvert()
```

### クラッシュリカバリ

pathdb journal replay は pathdb 内部で diff layer を再構築するのみであり、writeBlockWithState を呼ばない。sidecar は自身の pathdb journal に依存して crash recovery を行う。

```
Node Restart
  │
  ├─ 1. sidecar triedb open
  │     └─ pathdb内部: disk layer load + sidecar独自journal replay
  │        → diff layers復元（sidecar pathdbがMPT pathdbと独立に管理）
  │
  ├─ 2. sidecar.InitFromDB()
  │     │
  │     ├─ ReadUBTCurrentRoot() → currentRoot, currentBlock, currentHash を取得
  │     │
  │     ├─ OpenBinaryTrie(currentRoot) 試行
  │     │   └─ 成功 → state=Ready
  │     │         （sidecar pathdb journalが正常に復元。gap無し）
  │     │
  │     ├─ 失敗 → DiskRoot() でdisk layer rootを取得
  │     │   ├─ disk root のblock number特定:
  │     │   │   currentBlock から1ずつ減らしながら
  │     │   │   GetCanonicalHash(blockNum) → ReadUBTBlockRoot(hash)
  │     │   │   で disk root と照合（最大 maxDiffLayers 回 = 128回のDB read）
  │     │   │
  │     │   ├─ 一致found → disk rootをcurrentに設定 → state=Ready
  │     │   └─ 不一致 or disk root開けない → state=Stale
  │     │
  │     ├─ conversion progress metadataが存在?
  │     │   └─ Yes → state=Stale → BeginConversionで再開
  │     │
  │     └─ stateなし、progressなし
  │           └─ state=Stale → auto-convert開始（新規変換）
  │
  ├─ 3. blockchain open（MPT pathdb open + journal replay）
  │     └─ 通常のブロック処理開始
  │        └─ writeBlockWithState → sidecar.ApplyStateUpdate（新ブロックのみ）
  │
  └─ 4. maybeStartUBTAutoConvert("startup")

Ungraceful shutdown (SIGKILL等):
  - sidecar pathdbのdiff layer（最大maxDiffLayersブロック分）が失われうる
  - sidecar pathdb journal が正常なら diff layer は復元される → currentRoot が開ける → Ready
  - journal が破損/不完全な場合 → currentRoot が開けない → disk layer root にフォールバック
  - disk layer root も使えない場合 → Stale → reconvert
  - disk queueは永続化済みのため失われない
```

---

## 12. 実装フェーズ

依存関係を考慮した段階的実装計画。各フェーズは独立してcommit可能。

### Phase 1: 基盤（Config / Flag / rawdb / pathdb export）
- `eth/ethconfig/config.go` — UBT関連フィールド追加
- `eth/ethconfig/gen_config.go` — 自動生成更新
- `cmd/utils/flags.go` — フラグ定義とバリデーション
- `core/rawdb/schema.go` — DBキー定義
- `core/rawdb/accessors_ubt_sidecar.go` — metadata CRUD
- `triedb/pathdb/errors.go` — ErrSnapshotStale export
- `triedb/pathdb/database.go` — MaxDiffLayers export、DiskRoot() 追加
- `triedb/database.go` — DiskRoot() 追加

### Phase 2: BinaryTrie mutation count
- `trie/bintrie/trie.go` — mutationCount フィールド、MutationCount() getter

### Phase 3: Sidecar コア
- `sidecar/errors.go`
- `sidecar/ubt_update.go` — UBTUpdate構造体
- `sidecar/ubt_sidecar.go` — SidecarState enum、ChainContext interface、ライフサイクル（ApplyStateUpdate、HandleReorg with diff layer rollback、disk queue）
- `sidecar/ubt_sidecar_test.go`
- `sidecar/ubt_stateupdate_test.go`
- `core/state/stateupdate.go` — StateUpdate export

### Phase 4: MPT→UBT変換
- `sidecar/ubt_convert.go` — ConvertFromMPT + 再開ロジック + mutation cap flush + queue replay
- `sidecar/ubt_convert_test.go`

### Phase 5: Core統合
- `core/blockchain.go` — sidecarフック、auto-convert、reorgフック（diff layer rollback）

### Phase 6: RPC API + 配線
- `eth/api_ubt.go` — エンドポイント実装 + 型定義
- `eth/api_debug_ubt_test.go` — テスト
- `eth/backend.go` — config転送 + API登録

### Phase 7: テスト

必須テストケース:

1. 変換中のpreimage欠損でhard failure → Stale遷移
2. update適用の正確性（account/storage/code の作成・更新・削除）
3. disk queue enqueue + replay（blockNum順序保証）
4. restart時のdisk queueからの再開
5. 変換中断後の進捗からの再開
6. explicit blockのUBT root不在時のエラーパス
7. Converting/Stale状態でのRPC全拒否確認
8. `ubtSyncing` のレスポンス形式検証（全状態でオブジェクト返却）
9. reorg (Ready): diff layer rollback成功 → Ready維持
10. reorg (Ready): ancestor root不在 → Stale遷移
11. reorg (Converting): goroutine停止 → Stale → full reconvert
12. mutation cap超過時のバッチ途中flush
13. crash recovery: currentRoot開ける → Ready
14. crash recovery: currentRoot開けない → disk layer root fallback

---

## 13. 完了条件

### 機能要件
- [ ] `--ubt` フラグでsidecar有効化、UBT shadow stateが構築される
- [ ] MPT→UBT変換が完了後、ブロックごとにUBT stateが追従する
- [ ] Ready中のreorgでdiff layer rollback成功時はReady維持
- [ ] Ready中のreorgでrollback失敗時はStaleに遷移しreconvert
- [ ] Converting中のreorgでStaleに遷移しfull reconvert
- [ ] Ready状態で全RPCエンドポイントが定義通りに動作する
- [ ] Converting/Stale状態でRPCが適切なエラーを返す
- [ ] ノード再起動後にsidecarが正しく復帰する（sidecar pathdb journal経由）
- [ ] 変換中断後の再開が正しく動作する（初回変換、staleリトライ後）
- [ ] disk queue（enqueue + replay + restart recovery）が正しく動作する
- [ ] 回復不能エラー発生時に Stale → reconvert パスが機能する
- [ ] `ubtSyncing` が全状態で構造化オブジェクトを返す
- [ ] mutation cap超過時にバッチ途中flushが実行される

### 品質要件
- [ ] 各コンポーネントのunit testがパスする
- [ ] reorgシナリオのテストがパスする（Ready rollback成功/失敗、Converting）
- [ ] 変換中断・再開のテストがパスする
- [ ] masterに対するdiffが本仕様のスコープに限定され、読みやすい
- [ ] `debug_ubtCall` が実装されていないこと（スコープ外確認）

### 非機能要件
- [ ] 変換中もchainの追従が阻害されない（goroutine分離）
- [ ] steady stateでのブロック処理遅延が許容範囲内
- [ ] 変換完了後のメモリ使用量が安定している
- [ ] steady stateの `ApplyStateUpdate`: p95 < 50ms（benchmark test。100万アカウント/100万storage trieサイズ、NVMe SSD環境）
- [ ] `EnqueueUpdate`（disk write込み）: p95 < 5ms（benchmark test）
- [ ] 変換速度: 50万アカウント/時間以上（NVMe SSD, 100K+ random read IOPS環境）
- [ ] Ready中のreorg rollback: < 1秒
- [ ] crash recovery起動時間: < 30秒（SIGKILL後のReady or Stale遷移まで）

---

## 14. 用語集

| 用語 | 説明 |
|------|------|
| UBT | Unified Binary Trie。SHA256ベースのbinary trie |
| MPT | Merkle Patricia Trie。Ethereumの標準state trie |
| Sidecar | MPTと並行して維持されるUBT shadow state |
| BinaryTrie | UBTの実装。InternalNode + StemNodeで構成 |
| StemNode | 31byte stem + 256値。EIP-7864のleaf node |
| PathDB | path-basedのtrie database scheme |
| Verkle namespace | UBT用のDB名前空間（prefix "v"。MPTと分離） |
| StateUpdate | ブロック処理で生じたstate変更のスナップショット |
| UBTUpdate | StateUpdateからUBT更新に必要な情報を抽出したもの |
| Update Queue | MPT→UBT変換中に到着するブロックのdisk queue |
| Batch commit | 変換時のメモリ制御のための定期的なdisk flush |
| Mutation cap | BinaryTrieのin-memory mutation数の上限。OOM防止 |
| Stale | sidecarが古いまたは破損した状態。変換が必要 |
| Auto-cap | pathdbのdiff layer自動flush機構。maxDiffLayersを超えると古いlayerをdiskに書き込む |
| maxDiffLayers | pathdbのdiff layer上限。`pathdb.MaxDiffLayers` 定数（128） |
| Diff layer rollback | reorg時にcurrentRootをancestor rootに巻き戻す方式。triedb.Recoverの代替 |
| Conversion resume | 変換中断後に前回のバッチ境界から再開する機構（Converting中のreorg後は使用しない） |
| SidecarState | sidecarの状態を表すenum（Disabled, Stale, Converting, Ready） |
| ChainContext | blockchain.goが実装するinterface。HeadRoot/HeadBlock/CanonicalHashを提供 |
