# UBT Conversion Design: Hybrid MPT→UBT Snap Sync

## 概要

このドキュメントは、snap sync完了後にMPT flat stateからUBT (Binary Trie) へ変換するハイブリッドアプローチの設計を記述する。

### 問題

- Snap syncは `common.Hash` ベースでデータを受信・保存する
- UBT (`BinaryTrie`) は `common.Address` を必要とするAPI設計
- Snap sync中はMPTノードが生成されるが、UBTノードは生成されない
- UBTモードで`OpenTrie`を呼ぶとMPTデータをUBTとして解釈しようとしてクラッシュ

### 解決策

1. Snap syncは通常通りMPT flat stateを構築
2. Snap sync完了後、バックグラウンドでMPT→UBT変換を実行
3. 変換完了まではMPT/flat stateを使用してブロック処理を継続

---

## アーキテクチャ

```
┌─────────────────────────────────────────────────────────────────┐
│                       BlockChain                                │
│  ┌───────────────────────────────────────────────────────────┐  │
│  │ SnapSyncCommitHead(hash)                                  │  │
│  │   └── triedb.Enable(root)                                 │  │
│  │         └── StartUBTConversion(root) ◄─── NEW             │  │
│  └───────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│                       triedb.Database                           │
│  ┌───────────────────────────────────────────────────────────┐  │
│  │ StartUBTConversion(root)                                  │  │
│  │ UBTConversionStatus() *UBTConversionProgress              │  │
│  │ StopUBTConversion()                                       │  │
│  └───────────────────────────────────────────────────────────┘  │
│                              │                                  │
│                              ▼                                  │
│  ┌───────────────────────────────────────────────────────────┐  │
│  │               pathdb.Database (IsVerkle=true)             │  │
│  │  ┌─────────────────────────────────────────────────────┐  │  │
│  │  │              ubtConverter (goroutine)               │  │  │
│  │  │                                                     │  │  │
│  │  │  1. AccountIterator でフラットstate走査            │  │  │
│  │  │  2. Preimage DB でhash→address解決                 │  │  │
│  │  │  3. BinaryTrie.UpdateAccount/UpdateStorage          │  │  │
│  │  │  4. 定期的にCommit → triedb.Update                  │  │  │
│  │  │  5. 進捗をrawdbに永続化                             │  │  │
│  │  └─────────────────────────────────────────────────────┘  │  │
│  └───────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────┘
```

---

## データ構造

### UBTConversionProgress

```go
// triedb/pathdb/ubt_converter.go

type UBTConversionStage uint8

const (
    UBTStageIdle UBTConversionStage = iota
    UBTStageRunning
    UBTStageFailed
    UBTStageDone
)

type UBTConversionProgress struct {
    Version   uint8              // スキーマバージョン (将来の互換性用)
    Stage     UBTConversionStage // 現在のステージ

    // 変換対象のMPT state root
    StateRoot common.Hash

    // 最後にコミットされたUBT root
    UbtRoot common.Hash

    // アカウント走査の進捗
    NextAccountHash common.Hash // AccountIteratorのseek位置

    // ストレージ走査の進捗 (アカウント処理中の場合)
    CurrentAccount  common.Hash // 現在処理中のアカウント
    NextStorageHash common.Hash // StorageIteratorのseek位置

    // 統計情報
    AccountsDone uint64
    SlotsDone    uint64

    // エラー情報
    LastError string
    UpdatedAt uint64 // Unix timestamp
}
```

### rawdb キー

```go
// core/rawdb/schema.go

var (
    ubtConversionStatusKey = []byte("ubtConversionStatus")
)

// core/rawdb/accessors_state.go

func ReadUBTConversionStatus(db ethdb.KeyValueReader) *UBTConversionProgress
func WriteUBTConversionStatus(db ethdb.KeyValueWriter, status *UBTConversionProgress)
func DeleteUBTConversionStatus(db ethdb.KeyValueWriter)
```

---

## 変換ワーカー

### ubtConverter 構造体

```go
// triedb/pathdb/ubt_converter.go

type ubtConverter struct {
    db       *Database           // pathdb backend
    disk     ethdb.Database      // 基盤となるdisk DB
    triedb   *triedb.Database    // 上位のtriedb (preimage access用)
    
    root     common.Hash         // 変換対象のStateRoot
    progress *UBTConversionProgress
    
    // BinaryTrie instance
    bt       *bintrie.BinaryTrie
    
    // 制御チャネル
    stopCh   chan struct{}
    doneCh   chan struct{}
    
    // 設定
    batchSize int  // 1回のコミットで処理するアカウント数
}
```

### 変換アルゴリズム

```go
func (c *ubtConverter) run() {
    defer close(c.doneCh)
    
    // 1. 既存の進捗からBinaryTrieを再構築
    if err := c.initializeTrie(); err != nil {
        c.fail(err)
        return
    }
    
    // 2. アカウント走査を開始/再開
    acctIter, err := c.db.AccountIterator(c.root, c.progress.NextAccountHash)
    if err != nil {
        c.fail(err)
        return
    }
    defer acctIter.Release()
    
    processed := 0
    for acctIter.Next() {
        select {
        case <-c.stopCh:
            c.saveProgress()
            return
        default:
        }
        
        accountHash := acctIter.Hash()
        
        // 2a. Preimage lookup: hash → address
        addrBytes := c.triedb.Preimage(accountHash)
        if len(addrBytes) != common.AddressLength {
            c.fail(fmt.Errorf("missing preimage for account %x", accountHash))
            return
        }
        addr := common.BytesToAddress(addrBytes)
        
        // 2b. アカウントデータをデコード
        accData := acctIter.Account()
        acc, err := types.FullAccount(accData)
        if err != nil {
            c.fail(err)
            return
        }
        
        // 2c. コードサイズを取得 (必要な場合)
        codeLen := 0
        if acc.CodeHash != types.EmptyCodeHash {
            code := rawdb.ReadCode(c.disk, common.BytesToHash(acc.CodeHash))
            codeLen = len(code)
        }
        
        // 2d. BinaryTrie にアカウントを挿入
        if err := c.bt.UpdateAccount(addr, acc, codeLen); err != nil {
            c.fail(err)
            return
        }
        
        // 2e. ストレージスロットを処理
        if acc.Root != types.EmptyRootHash {
            if err := c.processStorage(accountHash, addr); err != nil {
                c.fail(err)
                return
            }
        }
        
        c.progress.AccountsDone++
        c.progress.NextAccountHash = incHash(accountHash)
        processed++
        
        // 2f. バッチサイズに達したらコミット
        if processed >= c.batchSize {
            if err := c.commit(); err != nil {
                c.fail(err)
                return
            }
            processed = 0
        }
    }
    
    // 3. 最終コミット
    if err := c.commit(); err != nil {
        c.fail(err)
        return
    }
    
    // 4. 完了
    c.progress.Stage = UBTStageDone
    c.saveProgress()
    log.Info("UBT conversion completed", 
        "accounts", c.progress.AccountsDone, 
        "slots", c.progress.SlotsDone,
        "ubtRoot", c.progress.UbtRoot)
}

func (c *ubtConverter) processStorage(accountHash common.Hash, addr common.Address) error {
    storageIter, err := c.db.StorageIterator(c.root, accountHash, c.progress.NextStorageHash)
    if err != nil {
        return err
    }
    defer storageIter.Release()
    
    c.progress.CurrentAccount = accountHash
    
    for storageIter.Next() {
        select {
        case <-c.stopCh:
            return nil // 中断、進捗は保存される
        default:
        }
        
        slotHash := storageIter.Hash()
        value := storageIter.Slot()
        
        // Preimage lookup: slotHash → raw key
        slotKey := c.triedb.Preimage(slotHash)
        if len(slotKey) != common.HashLength {
            return fmt.Errorf("missing preimage for slot %x of account %x", slotHash, accountHash)
        }
        
        // BinaryTrie にストレージを挿入
        if err := c.bt.UpdateStorage(addr, slotKey, value); err != nil {
            return err
        }
        
        c.progress.SlotsDone++
        c.progress.NextStorageHash = incHash(slotHash)
    }
    
    // アカウントのストレージ処理完了、次のアカウントへ
    c.progress.CurrentAccount = common.Hash{}
    c.progress.NextStorageHash = common.Hash{}
    
    return nil
}

func (c *ubtConverter) commit() error {
    // BinaryTrie からノードセットを取得
    newRoot, nodeset := c.bt.Commit(false)
    
    // triedb.Update 経由で永続化
    merged := trienode.NewMergedNodeSet()
    if nodeset != nil {
        if err := merged.Merge(nodeset); err != nil {
            return err
        }
    }
    
    // 注: pathdb の VerklePrefix namespace に書き込まれる
    // 現状の実装では Update 経由での書き込みが必要
    // 代替: 直接 rawdb 経由で書き込み
    
    // 進捗を更新
    c.progress.UbtRoot = newRoot
    c.progress.UpdatedAt = uint64(time.Now().Unix())
    c.saveProgress()
    
    log.Debug("UBT conversion batch committed",
        "root", newRoot,
        "accounts", c.progress.AccountsDone,
        "slots", c.progress.SlotsDone)
    
    return nil
}
```

---

## API インターフェース

### triedb.Database

```go
// triedb/database.go

// StartUBTConversion starts background conversion from MPT flat state to UBT.
// Only supported for pathdb backend with IsVerkle=true.
func (db *Database) StartUBTConversion(root common.Hash) error {
    pdb, ok := db.backend.(*pathdb.Database)
    if !ok || !db.config.IsVerkle {
        return errors.New("UBT conversion only supported for verkle/pathdb")
    }
    return pdb.startUBTConversion(root, db)
}

// UBTConversionStatus returns the current conversion progress.
func (db *Database) UBTConversionStatus() (*pathdb.UBTConversionProgress, error) {
    pdb, ok := db.backend.(*pathdb.Database)
    if !ok || !db.config.IsVerkle {
        return nil, errors.New("UBT conversion only supported for verkle/pathdb")
    }
    return pdb.ubtConversionStatus(), nil
}

// StopUBTConversion gracefully stops the conversion worker.
func (db *Database) StopUBTConversion() error {
    pdb, ok := db.backend.(*pathdb.Database)
    if !ok || !db.config.IsVerkle {
        return nil
    }
    return pdb.stopUBTConversion()
}
```

### pathdb.Database

```go
// triedb/pathdb/database.go

type Database struct {
    // ... existing fields ...
    
    // UBT conversion worker (verkle mode only)
    ubtConv     *ubtConverter
    ubtConvLock sync.Mutex
}

func (db *Database) startUBTConversion(root common.Hash, triedb *triedb.Database) error {
    db.ubtConvLock.Lock()
    defer db.ubtConvLock.Unlock()
    
    // 既存の変換をチェック
    if db.ubtConv != nil {
        return errors.New("UBT conversion already running")
    }
    
    // 進捗を読み込み
    progress := rawdb.ReadUBTConversionStatus(db.diskdb)
    if progress == nil {
        progress = &UBTConversionProgress{
            Version:   1,
            Stage:     UBTStageIdle,
            StateRoot: root,
        }
    }
    
    // ステージを確認
    switch progress.Stage {
    case UBTStageDone:
        if progress.StateRoot == root {
            log.Info("UBT conversion already completed for this root")
            return nil
        }
        // 新しいrootなら再変換
        progress = &UBTConversionProgress{
            Version:   1,
            Stage:     UBTStageIdle,
            StateRoot: root,
        }
    case UBTStageFailed:
        // 失敗した場合はエラー状態をクリアして再試行可能にする
        if progress.StateRoot != root {
            progress = &UBTConversionProgress{
                Version:   1,
                Stage:     UBTStageIdle,
                StateRoot: root,
            }
        }
        // 同じrootなら進捗を維持して再開
        progress.Stage = UBTStageRunning
        progress.LastError = ""
    case UBTStageRunning:
        if progress.StateRoot != root {
            return fmt.Errorf("conversion in progress for different root %x", progress.StateRoot)
        }
        // 同じrootなら再開
    case UBTStageIdle:
        progress.StateRoot = root
    }
    
    progress.Stage = UBTStageRunning
    rawdb.WriteUBTConversionStatus(db.diskdb, progress)
    
    db.ubtConv = newUBTConverter(db, triedb, root, progress)
    go db.ubtConv.run()
    
    log.Info("Started UBT conversion", "root", root)
    return nil
}
```

---

## 統合ポイント

### blockchain.go の変更

```go
// core/blockchain.go

func (bc *BlockChain) SnapSyncCommitHead(hash common.Hash) error {
    block := bc.GetBlockByHash(hash)
    if block == nil {
        return fmt.Errorf("non existent block [%x..]", hash[:4])
    }
    
    root := block.Root()
    if bc.triedb.Scheme() == rawdb.PathScheme {
        if err := bc.triedb.Enable(root); err != nil {
            return err
        }
        
        // UBTモードの場合、変換を開始
        if bc.triedb.IsVerkle() {
            if err := bc.triedb.StartUBTConversion(root); err != nil {
                log.Error("Failed to start UBT conversion", "err", err)
                // 変換失敗は致命的ではない - MPT/flat stateで動作継続
            }
        }
    }
    
    // ... rest of the function ...
}
```

### database.go (state) の変更

```go
// core/state/database.go

func (db *CachingDB) OpenTrie(root common.Hash) (Trie, error) {
    if db.triedb.IsVerkle() {
        // UBT変換が完了しているか確認
        status, err := db.triedb.UBTConversionStatus()
        if err != nil || status == nil || status.Stage != pathdb.UBTStageDone {
            // 変換未完了 - MPTを使用
            log.Debug("UBT not ready, falling back to MPT", "stage", status.Stage)
            tr, err := trie.NewStateTrie(trie.StateTrieID(root), db.triedb)
            if err != nil {
                return nil, err
            }
            return tr, nil
        }
        
        // 変換完了 - UBTを使用
        ts := overlay.LoadTransitionState(db.TrieDB().Disk(), root, db.triedb.IsVerkle())
        if ts.Transitioned() {
            return bintrie.NewBinaryTrie(root, db.triedb)
        }
    }
    
    tr, err := trie.NewStateTrie(trie.StateTrieID(root), db.triedb)
    if err != nil {
        return nil, err
    }
    return tr, nil
}
```

---

## エラーハンドリング

### エラーシナリオ

| シナリオ | 対応 |
|---------|------|
| Preimageが見つからない | 変換を失敗状態にして停止。ログ出力。MPTモードで継続。 |
| DB書き込みエラー | リトライ後、失敗状態にして停止。 |
| プロセスクラッシュ | 再起動時に進捗から再開。 |
| Snap syncがリオーグ | 新しいrootで変換を再開。古い進捗は破棄。 |

### リカバリフロー

```
1. ノード再起動
   │
   ▼
2. pathdb.New() で進捗を読み込み
   │
   ├── Stage == Done && StateRoot == head.Root
   │   └── 何もしない
   │
   ├── Stage == Running && StateRoot == head.Root
   │   └── 進捗から変換を再開
   │
   ├── Stage == Failed
   │   └── ログ出力、手動クリアを待つ
   │
   └── StateRoot != head.Root
       └── 古い進捗を破棄、新規変換を開始
```

---

## テスト計画

### ユニットテスト

#### 1. UBTConversionProgress のシリアライゼーション

```go
// triedb/pathdb/ubt_converter_test.go

func TestUBTConversionProgressSerialization(t *testing.T) {
    db := rawdb.NewMemoryDatabase()
    
    progress := &UBTConversionProgress{
        Version:         1,
        Stage:           UBTStageRunning,
        StateRoot:       common.HexToHash("0x1234..."),
        UbtRoot:         common.HexToHash("0x5678..."),
        NextAccountHash: common.HexToHash("0xabcd..."),
        AccountsDone:    1000,
        SlotsDone:       50000,
    }
    
    WriteUBTConversionStatus(db, progress)
    
    loaded := ReadUBTConversionStatus(db)
    assert.Equal(t, progress, loaded)
}
```

#### 2. アカウント変換

```go
func TestConvertSingleAccount(t *testing.T) {
    // Setup: メモリDBにflat stateを作成
    db := setupTestStateWithAccount(t)
    
    // Preimageを登録
    addr := common.HexToAddress("0x1234...")
    addrHash := crypto.Keccak256Hash(addr.Bytes())
    db.InsertPreimage(map[common.Hash][]byte{addrHash: addr.Bytes()})
    
    // 変換を実行
    converter := newUBTConverter(...)
    err := converter.convertAccount(addrHash, acc)
    
    assert.NoError(t, err)
    
    // BinaryTrieからアカウントを読み取り
    gotAcc, err := converter.bt.GetAccount(addr)
    assert.NoError(t, err)
    assert.Equal(t, acc.Nonce, gotAcc.Nonce)
    assert.Equal(t, acc.Balance, gotAcc.Balance)
}
```

#### 3. ストレージ変換

```go
func TestConvertAccountStorage(t *testing.T) {
    db := setupTestStateWithStorage(t)
    
    // 複数のストレージスロットを持つアカウント
    slots := map[common.Hash][]byte{
        common.HexToHash("0x01"): common.Hex2Bytes("value1"),
        common.HexToHash("0x02"): common.Hex2Bytes("value2"),
    }
    
    converter := newUBTConverter(...)
    err := converter.processStorage(accountHash, addr)
    
    assert.NoError(t, err)
    
    for slotHash, expectedValue := range slots {
        slotKey := db.Preimage(slotHash)
        got, err := converter.bt.GetStorage(addr, slotKey)
        assert.NoError(t, err)
        assert.Equal(t, expectedValue, got)
    }
}
```

#### 4. 進捗保存と再開

```go
func TestConversionResumeAfterInterrupt(t *testing.T) {
    db := setupLargeTestState(t, 1000) // 1000アカウント
    
    // 最初の変換を開始
    converter1 := newUBTConverter(...)
    go converter1.run()
    
    // 50%程度で停止
    time.Sleep(500 * time.Millisecond)
    converter1.stop()
    <-converter1.doneCh
    
    // 進捗を確認
    progress := ReadUBTConversionStatus(db)
    assert.Equal(t, UBTStageRunning, progress.Stage)
    assert.True(t, progress.AccountsDone > 0)
    assert.True(t, progress.AccountsDone < 1000)
    
    // 新しいコンバーターで再開
    converter2 := newUBTConverter(...) // 進捗を読み込む
    go converter2.run()
    <-converter2.doneCh
    
    // 完了を確認
    progress = ReadUBTConversionStatus(db)
    assert.Equal(t, UBTStageDone, progress.Stage)
    assert.Equal(t, uint64(1000), progress.AccountsDone)
}
```

#### 5. Preimage欠落エラー

```go
func TestConversionFailsOnMissingPreimage(t *testing.T) {
    db := setupTestStateWithAccount(t)
    // Preimageを登録しない
    
    converter := newUBTConverter(...)
    go converter.run()
    <-converter.doneCh
    
    progress := ReadUBTConversionStatus(db)
    assert.Equal(t, UBTStageFailed, progress.Stage)
    assert.Contains(t, progress.LastError, "missing preimage")
}
```

### 統合テスト

#### 1. Snap Sync完了後の自動変換

```go
// eth/downloader/downloader_test.go or core/blockchain_test.go

func TestSnapSyncTriggersUBTConversion(t *testing.T) {
    // UBTモードでブロックチェーンを初期化
    cfg := core.DefaultConfig()
    cfg.StateScheme = rawdb.PathScheme
    cfg.UseUBT = true
    cfg.Preimages = true
    
    bc, err := core.NewBlockChain(db, cfg, genesis, nil, engine, vmConfig, nil)
    require.NoError(t, err)
    
    // Snap syncをシミュレート
    // ... setup snap synced state ...
    
    // SnapSyncCommitHeadを呼び出し
    err = bc.SnapSyncCommitHead(pivotBlock.Hash())
    require.NoError(t, err)
    
    // 変換が開始されたことを確認
    status, err := bc.TrieDB().UBTConversionStatus()
    require.NoError(t, err)
    require.Equal(t, pathdb.UBTStageRunning, status.Stage)
    
    // 変換完了を待機 (テスト用に短いタイムアウト)
    timeout := time.After(30 * time.Second)
    for {
        select {
        case <-timeout:
            t.Fatal("UBT conversion timed out")
        default:
            status, _ := bc.TrieDB().UBTConversionStatus()
            if status.Stage == pathdb.UBTStageDone {
                return // Success
            }
            time.Sleep(100 * time.Millisecond)
        }
    }
}
```

#### 2. 変換中のブロック処理

```go
func TestBlockProcessingDuringConversion(t *testing.T) {
    bc := setupUBTBlockchainWithLargeState(t)
    
    // 変換を開始
    bc.TrieDB().StartUBTConversion(genesisRoot)
    
    // 変換中に新しいブロックを処理
    blocks := generateBlocks(t, 10)
    _, err := bc.InsertChain(blocks)
    require.NoError(t, err)
    
    // ブロック処理が成功することを確認
    head := bc.CurrentBlock()
    require.Equal(t, blocks[len(blocks)-1].Hash(), head.Hash())
}
```

#### 3. ノード再起動後の変換再開

```go
func TestConversionResumesAfterRestart(t *testing.T) {
    dir := t.TempDir()
    
    // 最初のインスタンス
    bc1 := createUBTBlockchain(t, dir)
    bc1.TrieDB().StartUBTConversion(root)
    
    // 途中で停止
    time.Sleep(500 * time.Millisecond)
    bc1.Stop()
    
    progress1 := rawdb.ReadUBTConversionStatus(bc1.db)
    require.True(t, progress1.AccountsDone > 0)
    
    // 新しいインスタンスを起動
    bc2 := createUBTBlockchain(t, dir)
    
    // 変換が再開されることを確認
    // ... wait for completion ...
    
    progress2 := rawdb.ReadUBTConversionStatus(bc2.db)
    require.Equal(t, UBTStageDone, progress2.Stage)
}
```

---

## 実装順序

### Phase 1: 基盤 (1-2日)

1. `rawdb` に変換進捗のread/write関数を追加
2. `UBTConversionProgress` 構造体を定義
3. 基本的なシリアライゼーションテスト

### Phase 2: 変換ワーカー (2-3日)

1. `ubtConverter` 構造体と基本ロジック
2. AccountIterator/StorageIterator の走査
3. Preimage lookup統合
4. BinaryTrie への挿入
5. バッチコミットと進捗保存

### Phase 3: API統合 (1日)

1. `triedb.Database` にAPI追加
2. `pathdb.Database` に内部メソッド追加
3. `blockchain.go` の `SnapSyncCommitHead` 統合

### Phase 4: OpenTrieガード (0.5日)

1. `OpenTrie` に変換ステータスチェックを追加
2. MPTフォールバック実装

### Phase 5: テストと修正 (2-3日)

1. ユニットテスト実装
2. 統合テスト実装
3. エッジケース修正
4. ログとメトリクス追加

---

## 設定フラグ

```go
// cmd/utils/flags.go

var (
    UBTConversionBatchSize = &cli.IntFlag{
        Name:  "ubt.batchsize",
        Usage: "Number of accounts to process per commit during UBT conversion",
        Value: 1000,
    }
    
    UBTConversionDisable = &cli.BoolFlag{
        Name:  "ubt.noconversion",
        Usage: "Disable automatic MPT to UBT conversion after snap sync",
    }
)
```

---

## メトリクス

```go
// triedb/pathdb/metrics.go

var (
    ubtConversionAccountsGauge = metrics.NewRegisteredGauge("ubt/conversion/accounts", nil)
    ubtConversionSlotsGauge    = metrics.NewRegisteredGauge("ubt/conversion/slots", nil)
    ubtConversionProgressGauge = metrics.NewRegisteredGaugeFloat64("ubt/conversion/progress", nil)
    ubtConversionDurationTimer = metrics.NewRegisteredTimer("ubt/conversion/duration", nil)
)
```

---

## 今後の拡張

1. **並列変換**: キー空間を分割して複数ワーカーで処理
2. **増分変換**: 新しいブロックの状態変更をリアルタイムでUBTに反映
3. **検証機能**: 変換後のUBTとMPTの一貫性検証
4. **プルーニング**: 変換完了後のMPTノード削除オプション
