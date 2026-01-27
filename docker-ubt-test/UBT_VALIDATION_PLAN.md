# UBT State Validation Plan v3

UBT化したノードの状態を検証するための方針書。
即座に実装可能な検証フェーズに集中し、将来拡張は別途検討する。

## 設計原則

1. **既存RPCの最大活用**: 新規RPC追加は必要最小限
2. **リファレンスノード要件の明確化**: debug RPC対応が必須
3. **段階的実装**: 既存機能での検証 → 追加機能による拡張
4. **fail-fast**: 1件でも不一致なら即失敗

---

## 運用モード

本検証は **sidecar モードのみ**を対象とする。

1. **Sidecar モード（推奨）**
   - `--ubt.sidecar` を使用
   - header.Root は **MPT state root のまま**
   - UBT root は `debug_getUBTProof` で取得
   - `--syncmode=full --state.scheme=path --cache.preimages` 必須

---

## 設計決定事項

| 項目                    | 決定                                                            |
| ----------------------- | --------------------------------------------------------------- |
| Witness検証             | `debug_executionWitnessUBT` + `ExecuteStateless(usePathDB=true)` |
| 実装言語                | Go                                                              |
| 不一致対応              | 即座に失敗（fail-fast）                                         |
| サンプル数              | 数万件（パラメータ化）                                          |
| リファレンス            | **自前MPTノード必須**（Infura不可）                             |
| 対象ネットワーク        | hoodi → mainnet                                                 |
| ブロック指定            | finalized/safe（latestは使用禁止）                              |
| サンプル取得            | リファレンスノード側で実行                                      |
| CommitStateRootToHeader | **sidecar: false（常に）**                                      |
| 同期方式                | **full sync 前提**（sidecarは `--state.scheme=path` 必須）      |

---

## リファレンスノード要件（重要）

| 要件                      | 理由                                              | 代替         |
| ------------------------- | ------------------------------------------------- | ------------ |
| **debug RPC対応**         | accountRange, storageRangeAt, getModifiedAccounts | なし（必須） |
| **同一ネットワーク**      | 同じチェーンデータ                                | -            |
| **同期済み**              | 同じブロック高で比較                              | -            |
| **PathDB/HashDB問わない** | MPTとして動作すればOK                             | -            |

**推奨構成**:

```
┌────────────────────┐     ┌────────────────────┐
│   UBT Node         │     │   Reference Node   │
│   (検証対象)        │     │   (自前MPTノード)   │
│   --ubt.sidecar    │     │   --state.scheme=path │
│   --state.scheme=path │  │                      │
│   localhost:8545   │     │   localhost:8546   │
└────────────────────┘     └────────────────────┘
```

※ sidecar モードでは `--syncmode=full --cache.preimages` が必須。

**Infuraは使用不可**（debug RPC非対応のため）

---

## Sync + Conversion 直後の検証（Sidecar 必須）

**目的**: sync + conversion が正しく完了したことを **RPCで即検証**。

### 必須チェック

1. **UBT root が取得できること**
   - `debug_getUBTProof` をアンカーブロックで実行
   - `UbtRoot` が非ゼロで、複数アドレスでも **同一 root**

2. **アカウント値の一致**
   - `debug_accountRange` で N 件サンプリング
   - UBT ノードと reference ノードで balance/nonce/code を一致確認

3. **ストレージ一致**
   - サンプルアカウントに対し `debug_storageRangeAt` で K スロット取得
   - `eth_getStorageAt` で UBT ノードと一致確認

**失敗時**: conversion 失敗扱い。側車の再変換が必要。

---

## アーキテクチャ

```
┌─────────────────────────────────────────────────────────────────────┐
│                        検証システム構成                               │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│  ┌──────────────────────────────────────────────────────────────┐  │
│  │                    Validator (Go)                            │  │
│  │  ┌────────┐┌────────┐┌────────┐┌────────┐┌────────┐┌───────┐│  │
│  │  │Phase 0 ││Phase 1 ││Phase 2 ││Phase 3 ││Phase 4 ││Phase 5││  │
│  │  │前提    ││UBT状態 ││値検証  ││遷移    ││Witness ││RPC    ││  │
│  │  └───┬────┘└───┬────┘└───┬────┘└───┬────┘└───┬────┘└───┬───┘│  │
│  └──────┼────────┼────────┼────────┼────────┼────────┼───────┘  │
│         │        │        │        │        │        │           │
│         ▼        ▼        ▼        ▼        ▼        ▼           │
│  ┌──────────────────────────────────┐  ┌────────────────────┐   │
│  │       UBT Node (localhost)       │  │  Reference Node    │   │
│  │  - debug_executionWitness        │  │  - debug_accountRange│  │
│  │  - debug_executionWitnessUBT     │  │                     │  │
│  │  - debug_getUBTProof / debug_getUBTState │  │  - debug_storageRangeAt│ │
│  │  - eth_*                         │  │  - debug_getModified │  │
│  │  - core.ExecuteStateless (内部) │  │  - eth_*            │   │
│  └──────────────────────────────────┘  └────────────────────┘   │
└─────────────────────────────────────────────────────────────────────┘
```

---

## 検証フェーズ概要

| Phase | 目的                  | 新規RPC               |
| ----- | --------------------- | --------------------- |
| 0     | 前提チェック          | 不要                  |
| 1     | UBT状態確認           | 不要                  |
| 2     | サンプリング値検証    | 不要                  |
| 3     | State遷移検証         | 不要                  |
| 4     | Witness Stateless検証 | **debug_executionWitnessUBT** |
| 5     | RPC整合性検証         | **debug_getUBTProof / debug_getUBTState** |

---

## Phase 0: 前提チェック

### 目的

検証が成立する環境かを確認し、早期に問題を検出

### 確認項目と方法

| 項目                        | 確認方法                                                                                | 期待値                                 |
| --------------------------- | --------------------------------------------------------------------------------------- | -------------------------------------- |
| RPC接続                     | `web3_clientVersion`                                                                    | 応答あり                               |
| ブロック同期                | `eth_blockNumber`                                                                       | 両ノードで近い値（差100以内）          |
| アンカーブロック取得        | `eth_getBlockByNumber(finalized/safe)`                                                  | ブロック取得成功                       |
| 同期方式                    | 運用確認（full sync 前提）                                                              | snap sync を使用しない                 |
| debug RPC対応               | `debug_accountRange(anchorBlock, nocode=false, nostorage=true, incompletes=false)` 試行 | エラーでない                           |
| Preimage有効（best-effort） | `debug_preimage(addressHash)` 試行                                                      | エラーでない（警告のみ、オプショナル） |

**注意**: Preimage チェックは best-effort です。`debug_preimage(emptyCodeHash)`
は preimage が有効でも失敗する場合があります。より確実なテストは
`debug_accountRange` で取得した `AddressHash` に対して preimage
を確認することですが、必須ではありません。

### 実装

```go
func (v *Validator) Phase0_PreconditionCheck(ctx context.Context) error {
    // 1. UBTノード接続確認
    if _, err := v.ubt.ClientVersion(ctx); err != nil {
        return fmt.Errorf("UBT node not reachable: %w", err)
    }

    // 2. リファレンスノード接続確認
    if _, err := v.ref.ClientVersion(ctx); err != nil {
        return fmt.Errorf("Reference node not reachable: %w", err)
    }

    // 3. ブロック高の近さを確認
    ubtBlock, err := v.ubt.BlockNumber(ctx)
    if err != nil {
        return fmt.Errorf("failed to get UBT block number: %w", err)
    }
    refBlock, err := v.ref.BlockNumber(ctx)
    if err != nil {
        return fmt.Errorf("failed to get reference block number: %w", err)
    }
    if abs(int64(ubtBlock)-int64(refBlock)) > 100 {
        return fmt.Errorf("Block height mismatch: UBT=%d, Ref=%d", ubtBlock, refBlock)
    }

    // 4. アンカーブロックを取得（latestは使用しない）
    anchor, err := v.getAnchorBlock(ctx)
    if err != nil {
        return fmt.Errorf("failed to get anchor block: %w", err)
    }
    blockTag := rpc.BlockNumberOrHashWithHash(anchor.Hash, false)

    // 5. リファレンスノードのdebug RPC確認（アンカーブロックで）
    //    フラグ: nocode=false, nostorage=true, incompletes=false
    _, err = v.ref.AccountRange(ctx, blockTag, common.Hash{}, 1,
        false,  // nocode: false - コード取得（コントラクト検出に必要）
        true,   // nostorage: true - ストレージは別途取得
        false,  // incompletes: false - 完全なアカウントのみ
    )
    if err != nil {
        return fmt.Errorf("Reference node does not support debug_accountRange: %w", err)
    }

    // 6. Preimage確認（既知のコードハッシュで検証）
    //    注意: ストレージキー列挙は reference node の debug_storageRangeAt を使用するため、
    //    reference node にも preimage が必要
    //    アンカーブロックを使用（finalized がない環境でも動作するように）
    if err := v.checkPreimageSupport(ctx, v.ubt, "UBT", blockTag); err != nil {
        log.Warn("UBT node preimage check failed", "err", err)
    }
    if err := v.checkPreimageSupport(ctx, v.ref, "Reference", blockTag); err != nil {
        // reference node で preimage がないとストレージサンプリングが失敗する
        log.Warn("Reference node preimage check failed - storage sampling may not work", "err", err)
    }

    log.Info("Phase 0: Precondition check passed", "anchor", anchor.Number)
    return nil
}

// checkPreimageSupport - Preimageが有効かどうかを確認（best-effort）
// client: 確認対象のRPCクライアント
// name: ログ出力用の名前（"UBT" or "Reference"）
// blockTag: 使用するブロック（Phase 0 で計算したアンカーブロックを使用）
//
// 注意: このチェックは best-effort です。debug_preimage(emptyCodeHash) は
// preimage が有効でも失敗する場合があります（空のコードは preimage store に
// 保存されないことがある）。より確実なテストは debug_accountRange で取得した
// AddressHash に対して preimage を確認することです。
func (v *Validator) checkPreimageSupport(ctx context.Context, client *rpc.Client, name string, blockTag rpc.BlockNumberOrHash) error {
    // まず debug_accountRange でアドレスハッシュを取得
    // アンカーブロックを使用（finalized がない環境でも動作するように）
    var accounts state.Dump
    err := client.Call(&accounts, "debug_accountRange",
        blockTag, common.Hash{}, 1, false, true, false)
    if err != nil {
        // debug_accountRange が使えない場合はスキップ
        return fmt.Errorf("%s node: cannot get address hash for preimage test: %w", name, err)
    }

    // 取得したアカウントの AddressHash で preimage を確認
    for _, acc := range accounts.Accounts {
        if len(acc.AddressHash) > 0 {
            var preimage hexutil.Bytes
            hash := common.BytesToHash(acc.AddressHash)
            err := client.Call(&preimage, "debug_preimage", hash)
            if err != nil {
                // preimage 未対応の可能性
                return fmt.Errorf("%s node: debug_preimage failed for address hash: %w", name, err)
            }
            log.Info("Preimage support confirmed", "node", name)
            return nil
        }
    }

    // アドレスハッシュが取得できなかった場合は警告のみ
    log.Warn("Could not verify preimage support - no address hash available", "node", name)
    return nil
}
```

---

## Phase 1: UBT状態確認（full sync 前提）

### 目的

UBTノードが full sync 完了済みで、アンカーブロックの state が読めることを確認
sidecar モードでは **conversion 完了後** であることが前提。

### 確認項目

| 項目             | 方法                                                     | 判定                              |
| ---------------- | -------------------------------------------------------- | --------------------------------- |
| 同期完了         | `eth_syncing`                                            | false                             |
| ブロック値取得   | `eth_getBalance(knownAddr, anchorBlock)`                 | エラーなし                        |
| UBT root（必須） | `debug_getUBTProof` で `UbtRoot` が取得できるか           | 非ゼロかつ取得成功                |

```go
func (v *Validator) Phase1_UBTStatusCheck(ctx context.Context, anchor *BlockAnchor) error {
    // 1. 同期完了確認
    if syncing, err := v.ubt.SyncProgress(ctx); err == nil && syncing != nil {
        return fmt.Errorf("UBT node still syncing: current=%d highest=%d", syncing.CurrentBlock, syncing.HighestBlock)
    }

    // 2. アンカーブロックで状態が読めるか確認（latestは使用しない）
    blockTag := rpc.BlockNumberOrHashWithHash(anchor.Hash, false)
    _, err := v.ubt.BalanceAt(ctx, common.Address{}, blockTag)
    if err != nil {
        return fmt.Errorf("Cannot read state from UBT node at block %d: %w", anchor.Number, err)
    }

    // 3. UBT root を取得できるか確認（sidecar必須）
    // debug_getUBTProof を直接叩き、UbtRoot が非ゼロであることを確認
    var proof UBTProofResult
    if err := v.ubt.CallContext(ctx, &proof, "debug_getUBTProof",
        common.Address{}, []string{}, blockTag); err != nil {
        return fmt.Errorf("Cannot fetch UBT root at block %d: %w", anchor.Number, err)
    }
    if proof.UbtRoot == (common.Hash{}) {
        return fmt.Errorf("UBT root is zero at block %d", anchor.Number)
    }

    log.Info("Phase 1: UBT state readable (full sync mode)")
    return nil
}
```

---

## Phase 2: サンプリング値検証

### 目的

UBTノードが返す値がリファレンスノードと一致することを確認

### 設計ポイント

| 項目         | 設計                       | 理由                                  |
| ------------ | -------------------------- | ------------------------------------- |
| ブロック指定 | finalized/safe/(latest-32) | reorg対策                             |
| サンプル取得 | リファレンスノード         | UBTのdebug_accountRangeが壊れる可能性 |
| UBT側        | 値取得のみ（eth_*）        | 標準RPCは動作する前提                 |

### ブロック指定戦略

```go
type BlockAnchor struct {
    Number uint64
    Hash   common.Hash
}

func (v *Validator) getAnchorBlock(ctx context.Context) (*BlockAnchor, error) {
    // 優先順位: finalized > safe > (latest - 32)

    // 1. finalized を試行
    block, err := v.ref.BlockByNumber(ctx, rpc.FinalizedBlockNumber)
    if err == nil && block != nil {
        return &BlockAnchor{block.NumberU64(), block.Hash()}, nil
    }

    // 2. safe を試行
    block, err = v.ref.BlockByNumber(ctx, rpc.SafeBlockNumber)
    if err == nil && block != nil {
        return &BlockAnchor{block.NumberU64(), block.Hash()}, nil
    }

    // 3. latest - 32 (reorg対策)
    latest, _ := v.ref.BlockNumber(ctx)
    safeNum := latest - 32
    block, _ = v.ref.BlockByNumber(ctx, rpc.BlockNumber(safeNum))
    return &BlockAnchor{block.NumberU64(), block.Hash()}, nil
}
```

### サンプリングフロー

```
┌─────────────────────────────────────────────────────────────────┐
│                    Phase 2: サンプリング検証                      │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  1. アンカーブロック決定                                          │
│     └─ finalized/safe/(latest-32) のいずれか                    │
│                                                                 │
│  2. リファレンスノードでサンプル取得                               │
│     ├─ debug_accountRange(blockHash, startHash, maxResults)    │
│     └─ debug_storageRangeAt(blockHash, 0, addr, startKey, max) │
│        ※ キー列挙専用 - 値は使用しない (key discovery only)     │
│                                                                 │
│  3. 両ノードで値取得（同一アンカーブロック指定）                    │
│     ├─ eth_getBalance(addr, blockHash)                         │
│     ├─ eth_getTransactionCount(addr, blockHash)                │
│     ├─ eth_getCode(addr, blockHash)                            │
│     └─ eth_getStorageAt(addr, key, blockHash)                  │
│        ※ ストレージ値は必ず eth_getStorageAt で取得             │
│                                                                 │
│  4. 比較（即座に失敗）                                           │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

### サンプリング設定

```go
type SamplingConfig struct {
    AccountCount             int   // 総サンプル数 (default: 30000)
    StorageSlotsPerContract  int   // コントラクトあたりスロット数 (default: 500)
    RandomSeed               int64 // 再現性用シード
    BatchSize                int   // 並列リクエスト数 (default: 100)
}
```

### 実装

```go
func (v *Validator) Phase2_ValueValidation(ctx context.Context, cfg SamplingConfig) error {
    // 1. アンカーブロック決定
    anchor, err := v.getAnchorBlock(ctx)
    if err != nil {
        return fmt.Errorf("failed to get anchor block: %w", err)
    }
    log.Info("Using anchor block", "number", anchor.Number, "hash", anchor.Hash)

    blockTag := rpc.BlockNumberOrHashWithHash(anchor.Hash, false)

    // 2. リファレンスノードでアカウントサンプリング
    rng := rand.New(rand.NewSource(cfg.RandomSeed))
    sampled := 0

    for sampled < cfg.AccountCount {
        startHash := randomHash(rng)

        // リファレンスノードで取得
        // フラグ: nocode=false（コード必要）, nostorage=true（ストレージは別途取得）, incompletes=false
        accounts, err := v.ref.AccountRange(ctx, blockTag, startHash, cfg.BatchSize,
            false,  // nocode: false - コード取得（コントラクト検出に必要）
            true,   // nostorage: true - ストレージは eth_getStorageAt で別途取得
            false,  // incompletes: false - 完全なアカウントのみ
        )
        if err != nil {
            return fmt.Errorf("debug_accountRange failed on reference: %w", err)
        }

        // 注意: accounts.Accounts は map[string]DumpAccount
        // キーは hex string なので common.Address にパースする必要がある
        for addrStr, acc := range accounts.Accounts {
            // アドレスをパース（acc.Address が nil の場合もあるため、キーからもパース）
            var addr common.Address
            if acc.Address != nil {
                addr = *acc.Address
            } else {
                addr = common.HexToAddress(addrStr)
            }

            // UBTノードで値取得・比較
            if err := v.compareAccount(ctx, blockTag, addr, acc); err != nil {
                return fmt.Errorf("account %s mismatch: %w", addr.Hex(), err)
            }

            // コントラクトならストレージも検証
            if acc.Code != nil && len(acc.Code) > 0 {
                if err := v.compareStorage(ctx, blockTag, addr, cfg.StorageSlotsPerContract); err != nil {
                    return fmt.Errorf("storage %s mismatch: %w", addr.Hex(), err)
                }
            }

            sampled++
            if sampled%1000 == 0 {
                log.Info("Progress", "sampled", sampled, "target", cfg.AccountCount)
            }

            if sampled >= cfg.AccountCount {
                break
            }
        }
    }

    log.Info("Phase 2: Value validation passed", "sampled", sampled)
    return nil
}

func (v *Validator) compareAccount(ctx context.Context, block rpc.BlockNumberOrHash, addr common.Address, refAcc state.DumpAccount) error {
    // 並列取得
    g, gctx := errgroup.WithContext(ctx)

    var ubtBalance *big.Int
    var ubtNonce uint64
    var ubtCode []byte

    g.Go(func() error {
        bal, err := v.ubt.BalanceAt(gctx, addr, block)
        ubtBalance = bal
        return err
    })

    g.Go(func() error {
        nonce, err := v.ubt.NonceAt(gctx, addr, block)
        ubtNonce = nonce
        return err
    })

    g.Go(func() error {
        code, err := v.ubt.CodeAt(gctx, addr, block)
        ubtCode = code
        return err
    })

    if err := g.Wait(); err != nil {
        return err
    }

    // refAcc.Balance は decimal string（"1000000000"形式）
    // big.Int に変換して比較
    refBalance, ok := new(big.Int).SetString(refAcc.Balance, 10)
    if !ok {
        return fmt.Errorf("failed to parse reference balance: %s", refAcc.Balance)
    }

    // 比較（即座に失敗）
    if ubtBalance.Cmp(refBalance) != 0 {
        return fmt.Errorf("balance: UBT=%s, Ref=%s", ubtBalance, refBalance)
    }
    if ubtNonce != refAcc.Nonce {
        return fmt.Errorf("nonce: UBT=%d, Ref=%d", ubtNonce, refAcc.Nonce)
    }
    if !bytes.Equal(ubtCode, refAcc.Code) {
        return fmt.Errorf("code mismatch")
    }

    return nil
}
```

### 注意事項

- **AccountRangeの上限**: `AccountRangeMaxResults = 256` で強制的に切られる
- **Preimage必須**: ストレージキーの参照に必要（`--cache.preimages`）
- **debug_accountRange フラグ**:
  - `nocode=false` - コード取得必須（コントラクト検出に使用）
  - `nostorage=true` - ストレージは別途取得するため不要
  - `incompletes=false` - 完全なアカウントのみ

### ヘルパー関数

```go
// randomHash - ランダムなハッシュを生成（サンプリング開始位置用）
func randomHash(rng *rand.Rand) common.Hash {
    var h common.Hash
    rng.Read(h[:])
    return h
}

// waitForBlock - 指定ブロックに到達するまで待機
func (v *Validator) waitForBlock(ctx context.Context, targetBlock uint64) error {
    for {
        current, err := v.ubt.BlockNumber(ctx)
        if err != nil {
            return err
        }
        if current >= targetBlock {
            return nil
        }
        select {
        case <-ctx.Done():
            return ctx.Err()
        case <-time.After(time.Second):
            // 1秒待機してリトライ
        }
    }
}

// compareAccountValues - 両ノードでアカウント値を取得・比較
func (v *Validator) compareAccountValues(ctx context.Context, blockTag rpc.BlockNumberOrHash, addr common.Address) error {
    g, gctx := errgroup.WithContext(ctx)

    var ubtBalance, refBalance *big.Int
    var ubtNonce, refNonce uint64

    g.Go(func() error {
        bal, err := v.ubt.BalanceAt(gctx, addr, blockTag)
        ubtBalance = bal
        return err
    })
    g.Go(func() error {
        bal, err := v.ref.BalanceAt(gctx, addr, blockTag)
        refBalance = bal
        return err
    })
    g.Go(func() error {
        nonce, err := v.ubt.NonceAt(gctx, addr, blockTag)
        ubtNonce = nonce
        return err
    })
    g.Go(func() error {
        nonce, err := v.ref.NonceAt(gctx, addr, blockTag)
        refNonce = nonce
        return err
    })

    if err := g.Wait(); err != nil {
        return err
    }

    if ubtBalance.Cmp(refBalance) != 0 {
        return fmt.Errorf("balance: UBT=%s, Ref=%s", ubtBalance, refBalance)
    }
    if ubtNonce != refNonce {
        return fmt.Errorf("nonce: UBT=%d, Ref=%d", ubtNonce, refNonce)
    }
    return nil
}

// compareBlocks - ブロック構造を比較
func compareBlocks(ubt, ref *types.Block) error {
    if ubt.Hash() != ref.Hash() {
        return fmt.Errorf("hash mismatch: UBT=%s, Ref=%s", ubt.Hash(), ref.Hash())
    }
    if ubt.NumberU64() != ref.NumberU64() {
        return fmt.Errorf("number mismatch: UBT=%d, Ref=%d", ubt.NumberU64(), ref.NumberU64())
    }
    // 注意: Root() は MPT state root なので両者で一致するはず
    if ubt.Root() != ref.Root() {
        return fmt.Errorf("root mismatch: UBT=%s, Ref=%s", ubt.Root(), ref.Root())
    }
    return nil
}

// compareBytes - バイト列を比較
func compareBytes(a, b []byte) error {
    if !bytes.Equal(a, b) {
        return fmt.Errorf("bytes mismatch: len(a)=%d, len(b)=%d", len(a), len(b))
    }
    return nil
}

// getSampleAddresses - サンプルアドレスを取得（Phase 5 UBT Proof検証用）
func (v *Validator) getSampleAddresses(count int) []common.Address {
    // 既にPhase 2で使用したアドレスをキャッシュしておくか、
    // 新たにリファレンスノードから取得する
    var addrs []common.Address
    result, _ := v.ref.AccountRange(context.Background(),
        rpc.BlockNumberOrHashWithNumber(rpc.FinalizedBlockNumber),
        common.Hash{}, count, false, true, false)
    for addrStr := range result.Accounts {
        addrs = append(addrs, common.HexToAddress(addrStr))
    }
    return addrs
}
```

### ストレージサンプリングの重要な制約

**問題**: `debug_storageRangeAt(blockHash, txIndex=0, ...)`
は**ブロック先頭状態**（トランザクション実行前）を返すが、`eth_getStorageAt(..., blockHash)`
は**ブロック末尾状態**（全トランザクション実行後）を返す。

**解決策**: `debug_storageRangeAt`
は**キー列挙専用**として使用し、**値の取得・比較**は両ノードとも
`eth_getStorageAt` を使用する。

```go
func (v *Validator) compareStorage(ctx context.Context, blockTag rpc.BlockNumberOrHash, addr common.Address, maxSlots int) error {
    // 1. リファレンスノードでストレージキーを列挙（キーのみ取得）
    //    ※ txIndex=0 の値は使用しない
    storage, err := v.ref.StorageRangeAt(ctx, blockTag, 0, addr, common.Hash{}, maxSlots)
    if err != nil {
        return fmt.Errorf("storage key enumeration failed: %w", err)
    }

    // 2. 列挙されたキーに対して、両ノードで eth_getStorageAt を使用
    //    注意: entry.Key は preimage から取得した元のスロットキー
    //    eth_getStorageAt は元のスロットキーを要求するため、
    //    entry.Key が nil の場合は検証をスキップする（preimage 必須）
    skipped := 0
    validated := 0
    for _, entry := range storage.Storage {
        if entry.Key == nil {
            // preimage がない場合はスキップ
            // eth_getStorageAt はハッシュ化されたキーを受け付けない
            skipped++
            continue
        }
        slotKey := *entry.Key

        // 両ノードで同じ eth_getStorageAt を呼び出し（ブロック末尾状態）
        refValue, err := v.ref.StorageAt(ctx, addr, slotKey, blockTag)
        if err != nil {
            return fmt.Errorf("reference node eth_getStorageAt failed: %w", err)
        }

        ubtValue, err := v.ubt.StorageAt(ctx, addr, slotKey, blockTag)
        if err != nil {
            return fmt.Errorf("UBT node eth_getStorageAt failed: %w", err)
        }

        // 比較
        if !bytes.Equal(refValue, ubtValue) {
            return fmt.Errorf("storage mismatch: key=%s, ref=%x, ubt=%x",
                slotKey.Hex(), refValue, ubtValue)
        }
        validated++
    }

    if skipped > 0 {
        log.Warn("Some storage slots skipped due to missing preimages",
            "address", addr, "validated", validated, "skipped", skipped)
    }

    return nil
}
```

**注意**: `debug_storageRangeAt` の値を直接比較に使用してはならない。常に
`eth_getStorageAt` で取得した値を比較すること。

---

## Phase 3: State遷移検証

### 目的

新しいブロックでの状態更新が正しいことを確認

### 設計ポイント

| 項目                  | 設計                   | 理由                       |
| --------------------- | ---------------------- | -------------------------- |
| modified accounts取得 | リファレンスノードのみ | UBTではMPT差分計算が壊れる |
| フォールバック        | tx from/to抽出         | debug RPC失敗時            |

### 実装

```go
func (v *Validator) Phase3_TransitionValidation(ctx context.Context, blocks int) error {
    startBlock, _ := v.ubt.BlockNumber(ctx)

    for i := 0; i < blocks; i++ {
        targetBlock := startBlock + uint64(i) + 1

        // ブロック到達を待機
        if err := v.waitForBlock(ctx, targetBlock); err != nil {
            return err
        }

        // 変更アカウント取得（2つの方法）
        var modifiedAddrs []common.Address

        // 方法1: リファレンスノードで debug_getModifiedAccountsByNumber
        addrs, err := v.ref.GetModifiedAccountsByNumber(ctx, targetBlock)
        if err == nil {
            modifiedAddrs = addrs
        } else {
            // 方法2: フォールバック - ブロック内の変更アドレスを推定
            log.Warn("debug_getModifiedAccountsByNumber failed, using address extraction fallback", "err", err)
            modifiedAddrs, _ = v.extractBlockModifiedAddresses(ctx, targetBlock)
        }

        log.Info("Validating block transition", "block", targetBlock, "addresses", len(modifiedAddrs))

        // 各アドレスを検証
        blockTag := rpc.BlockNumberOrHashWithNumber(rpc.BlockNumber(targetBlock))
        for _, addr := range modifiedAddrs {
            if err := v.compareAccountValues(ctx, blockTag, addr); err != nil {
                return fmt.Errorf("block %d, account %s: %w", targetBlock, addr, err)
            }
        }
    }

    log.Info("Phase 3: Transition validation passed", "blocks", blocks)
    return nil
}

// extractBlockModifiedAddresses - ブロック内で変更された可能性のあるアドレスを抽出
// debug_getModifiedAccountsByNumber のフォールバック用
func (v *Validator) extractBlockModifiedAddresses(ctx context.Context, blockNum uint64) ([]common.Address, error) {
    block, err := v.ref.BlockByNumber(ctx, rpc.BlockNumber(blockNum))
    if err != nil {
        return nil, err
    }

    addrSet := make(map[common.Address]struct{})

    // 1. Coinbase（ブロック報酬受領者）
    addrSet[block.Coinbase()] = struct{}{}

    // 2. トランザクションの from/to
    for _, tx := range block.Transactions() {
        // From（送信者）
        from, err := types.Sender(types.LatestSignerForChainID(tx.ChainId()), tx)
        if err == nil {
            addrSet[from] = struct{}{}
        }

        // To（受信者）
        if tx.To() != nil {
            addrSet[*tx.To()] = struct{}{}
        }

        // Access List内のアドレス（EIP-2930）
        for _, accessTuple := range tx.AccessList() {
            addrSet[accessTuple.Address] = struct{}{}
        }
    }

    // 3. Withdrawals（EIP-4895、BeaconChainからの引き出し）
    for _, withdrawal := range block.Withdrawals() {
        addrSet[withdrawal.Address] = struct{}{}
    }

    // 4. システムコントラクト（ハードコーク依存）
    // EIP-4788: Beacon Root Contract
    beaconRootContract := common.HexToAddress("0x000F3df6D732807Ef1319fB7B8bB8522d0Beac02")
    addrSet[beaconRootContract] = struct{}{}

    // EIP-2935: History Storage Contract (Prague以降)
    historyStorageContract := common.HexToAddress("0x0F792be4B0c0cb4DAE440Ef133E90C0eCD48CCCC")
    addrSet[historyStorageContract] = struct{}{}

    // 注意: これは完全なリストではない
    // debug_getModifiedAccountsByNumber が使えない場合の近似値
    log.Warn("Using address extraction fallback - may miss internal transfers",
        "addresses", len(addrSet))

    addrs := make([]common.Address, 0, len(addrSet))
    for addr := range addrSet {
        addrs = append(addrs, addr)
    }
    return addrs, nil
}
```

---

## Phase 4: Witness Stateless検証

### 目的

生成されたWitnessでstateless実行が成功することを確認

### 重要な制約

**現状は UBT witness の検証が不可能:**

- `debug_executionWitness` は `ExtWitness` を返すが、**UBT の `StatePaths` を持たない**
- `ExecuteStateless` は `IsVerkle=false` のチェーンで **HashDB(MPT) 固定**

このため **UBT witness を PathDB で検証できない**。

### 最小パス（UBT witness が検証可能になるまで）

1. **新RPC追加: `debug_executionWitnessUBT`**
   - 既存 `debug_executionWitness` は変更しない（後方互換）
   - UBT 用の path 情報を返す専用RPCを追加

**Wire format（例）:**

```json
{
  "headers": [ /* same as ExtWitness */ ],
  "codes":   [ "0x6000..." ],
  "statePaths": [
    { "path": "0x", "node": "0xf8..." },        // root node (empty path)
    { "path": "0x01", "node": "0xf9..." },
    { "path": "0x8a7f", "node": "0xf8..." }
  ]
}
```

2. **UBT witness 変換関数の追加**
   - `stateless.NewWitnessFromUBTWitness(ext *ExtUBTWitness)` を追加
   - `StatePaths[path] = node` を保持しつつ、`State` にも blob を入れる（互換維持）

3. **ExecuteStateless を PathDB で動かせるようにする**
   - 既存APIを壊さない形で `usePathDB` を指定できるようにする
   - 例: `ExecuteStateless(..., usePathDB bool)` もしくは `ExecuteStatelessWithPathDB(...)`

4. **UBT root 取得の最小手段**
   - 既存計画の `debug_getUBTProof` を **root取得目的だけでも先に使用**
   - あるいは軽量版 `debug_ubtRootByNumber` を新設（Proof不要）

5. **Phase 4 実装を UBT 仕様に変更**
   - `debug_executionWitnessUBT` → `NewWitnessFromUBTWitness` → `ExecuteStateless(usePathDB=true)`
   - `computedRoot` と **UBT root** を比較

> 上記 1-5 が揃えば **UBT witness の検証が可能**。証明検証（proof）は Phase 5 で拡張。

**`ExecuteStateless` の現在の実装制限:**

```go
// core/stateless.go:55-64 (現行)
isVerkle := config.IsVerkle(block.Number(), block.Time())
if isVerkle {
    memdb = witness.MakePathDB()    // PathDB (UBT/Verkle)
    triedbConfig = triedb.VerkleDefaults
} else {
    memdb = witness.MakeHashDB()    // HashDB (MPT) ← mainnet/hoodiはこっち
    triedbConfig = triedb.HashDefaults
}
```

**問題**: `IsVerkle=false`（mainnet/hoodi）では、`ExecuteStateless` は常に MPT
witness を使用する。 UBT witness（`witness.StatePaths`）は無視される。

**解決策（Option B）**: `ExecuteStateless` に `usePathDB` フラグを追加し、
UBT witness を明示的に PathDB で検証できるようにする。

```go
// core/stateless.go (修正案)
func ExecuteStateless(config *params.ChainConfig, vmconfig vm.Config, block *types.Block, witness *stateless.Witness, usePathDB bool) (common.Hash, common.Hash, error) {
    var memdb ethdb.Database
    var triedbConfig *triedb.Config
    if usePathDB {
        memdb = witness.MakePathDB()
        triedbConfig = triedb.VerkleDefaults
    } else {
        memdb = witness.MakeHashDB()
        triedbConfig = triedb.HashDefaults
    }
    // ...以降は現行と同じ...
}
```

### 設計ポイント

| 項目     | 設計                                                | 理由                                   |
| -------- | --------------------------------------------------- | -------------------------------------- |
| 実行方法 | `core.ExecuteStateless(..., usePathDB=true)` で実行 | UBT witness を PathDB で検証するため   |
| root比較 | **UBT root 同士の比較**                             | UBT witness 検証の正しさを担保         |
| UBT検証  | **Phase 4 で実施**                                  | `ExecuteStateless` 修正後に実現可能    |

### Root比較の整合性

**UBT検証（Option B適用後）:**

- `block.Root()` = **MPT state root**（Ethereum consensus, 比較には使わない）
- `ExecuteStateless(..., usePathDB=true)` の返り値 = **UBT state root**（PathDB使用）
- **比較対象は `bt.Hash()` で計算した UBT root**

**重要な注意:**

- Phase 4 は **UBT witness の検証** として扱う
- `ExecuteStateless` の修正（usePathDB 追加）が必須

### 実装

```go
func (v *Validator) Phase4_WitnessValidation(ctx context.Context, blockNumbers []uint64) error {
    for _, blockNum := range blockNumbers {
        log.Info("Validating witness", "block", blockNum)

        // 1. Witness取得（UBT path 付き）
        var extWitness stateless.ExtUBTWitness
        err := v.ubt.Call(&extWitness, "debug_executionWitnessUBT", rpc.BlockNumber(blockNum))
        if err != nil {
            return fmt.Errorf("failed to get witness for block %d: %w", blockNum, err)
        }

        // 2. Block取得
        block, err := v.ubt.BlockByNumber(ctx, rpc.BlockNumber(blockNum))
        if err != nil {
            return fmt.Errorf("failed to get block %d: %w", blockNum, err)
        }

        // 3. Witness変換（UBT path を保持）
        //    NewWitnessFromUBTWitness を追加して StatePaths を復元する
        witness, err := stateless.NewWitnessFromUBTWitness(&extWitness)
        if err != nil {
            return fmt.Errorf("failed to convert witness: %w", err)
        }

        // 4. Stateless実行（UBT witness 検証のため PathDB を使用）
        stateRoot, receiptRoot, err := core.ExecuteStateless(
            v.chainConfig,
            vm.Config{},
            block,
            witness,
            true, // usePathDB: UBT witness を使用
        )
        if err != nil {
            return fmt.Errorf("stateless execution failed for block %d: %w", blockNum, err)
        }

        // 5. 結果検証
        // 注意: block.Root() は MPT state root。UBT root とは別物。
        //       比較対象は debug_getUBTProof で得られる UBT root を使用する
        //       （Phase 5 で Proof 検証を拡張）
        var proof UBTProofResult
        err = v.ubt.Call(&proof, "debug_getUBTProof", common.Address{}, []string{}, rpc.BlockNumber(blockNum))
        if err != nil {
            return fmt.Errorf("failed to get UBT root for block %d: %w", blockNum, err)
        }
        ubtRoot := proof.UbtRoot
        if stateRoot != ubtRoot {
            return fmt.Errorf("block %d: UBT root mismatch: computed=%s, ubt=%s",
                blockNum, stateRoot.Hex(), ubtRoot.Hex())
        }
        if receiptRoot != block.ReceiptHash() {
            return fmt.Errorf("block %d: receipt root mismatch: computed=%s, header=%s",
                blockNum, receiptRoot.Hex(), block.ReceiptHash().Hex())
        }

        log.Info("Witness validation passed", "block", blockNum)
    }

    log.Info("Phase 4: Witness validation complete", "blocks", len(blockNumbers))
    return nil
}
```

---

## Phase 5: RPC整合性検証

### 目的

標準Ethereum RPCが正しく動作することを確認

### 検証対象RPC

| RPC                         | 検証内容                 | 状態       |
| --------------------------- | ------------------------ | ---------- |
| `eth_getBalance`            | 残高一致                 | ✓          |
| `eth_getCode`               | コード一致               | ✓          |
| `eth_getTransactionCount`   | nonce一致                | ✓          |
| `eth_getStorageAt`          | ストレージ値一致         | ✓          |
| `eth_getBlockByNumber`      | ブロック構造             | ✓          |
| `eth_getBlockByHash`        | ブロック構造             | ✓          |
| `eth_getTransactionByHash`  | トランザクション         | ✓          |
| `eth_getTransactionReceipt` | レシート                 | ✓          |
| `eth_call`                  | コントラクト呼び出し結果 | ✓          |
| ~~`eth_getProof`~~          | MPT証明（除外）          | ✗          |
| **`debug_getUBTProof`**     | **UBT証明検証**          | **✓ 新規** |

### 実装

```go
func (v *Validator) Phase5_RPCConsistency(ctx context.Context) error {
    anchor, _ := v.getAnchorBlock(ctx)
    blockTag := rpc.BlockNumberOrHashWithHash(anchor.Hash, false)

    tests := []RPCTest{
        {
            Name: "eth_getBlockByNumber",
            Run: func() error {
                ubtBlock, _ := v.ubt.BlockByNumber(ctx, rpc.BlockNumber(anchor.Number))
                refBlock, _ := v.ref.BlockByNumber(ctx, rpc.BlockNumber(anchor.Number))
                return compareBlocks(ubtBlock, refBlock)
            },
        },
        {
            Name: "eth_call",
            Run: func() error {
                // 既知のコントラクトに対してcall
                msg := ethereum.CallMsg{To: &knownContract, Data: callData}
                ubtResult, _ := v.ubt.CallContract(ctx, msg, anchor.Number)
                refResult, _ := v.ref.CallContract(ctx, msg, anchor.Number)
                return compareBytes(ubtResult, refResult)
            },
        },
        // ... 他のテスト
    }

    for _, test := range tests {
        log.Info("Testing RPC", "name", test.Name)
        if err := test.Run(); err != nil {
            return fmt.Errorf("%s: %w", test.Name, err)
        }
    }

    // UBT Proof検証
    if err := v.validateUBTProofs(ctx, blockTag); err != nil {
        return fmt.Errorf("UBT proof validation failed: %w", err)
    }

    log.Info("Phase 5: RPC consistency validation passed")
    return nil
}
```

---

## 新規RPC: debug_getUBTProof

### 目的

UBT状態に対するMerkle証明を取得・検証する

### RPC仕様

```go
// debug_getUBTProof - UBT状態に対するMerkle証明を取得
func (api *DebugAPI) GetUBTProof(
    ctx context.Context,
    address common.Address,
    storageKeys []string,
    blockNrOrHash rpc.BlockNumberOrHash,
) (*UBTProofResult, error)
```

### 返り値の構造

```go
type UBTProofResult struct {
    Address      common.Address    `json:"address"`
    AccountProof []hexutil.Bytes   `json:"accountProof"`  // Binary Trie proof
    Balance      *hexutil.Big      `json:"balance"`
    CodeHash     common.Hash       `json:"codeHash"`
    Nonce        hexutil.Uint64    `json:"nonce"`
    StorageHash  common.Hash       `json:"storageHash"`
    StorageProof []UBTStorageProof `json:"storageProof"`
    StateRoot    common.Hash       `json:"stateRoot"`     // MPT root（header.Root）
    UbtRoot      common.Hash       `json:"ubtRoot"`       // sidecar UBT root
}

type UBTStorageProof struct {
    Key   common.Hash     `json:"key"`
    Value hexutil.Bytes   `json:"value"`
    Proof []hexutil.Bytes `json:"proof"`
}
```

### Key Encoding（EIP-7864準拠）

UBTでは、アドレスとキーから32バイトのバイナリトライキーを生成する。

#### 定数定義（trie/bintrie/key_encoding.go）

```go
const (
    BasicDataLeafKey        = 0   // balance, nonce, code size用
    CodeHashLeafKey         = 1   // code hash用
    BasicDataCodeSizeOffset = 5
    BasicDataNonceOffset    = 8
    BasicDataBalanceOffset  = 16
)

var (
    headerStorageOffset = uint256.NewInt(64)   // ヘッダー領域のストレージオフセット
    codeOffset          = uint256.NewInt(128)  // コードチャンクの開始オフセット
)
```

#### 基本キー生成

```go
// trie/bintrie/key_encoding.go より
func GetBinaryTreeKey(addr common.Address, key []byte) []byte {
    hasher := sha256.New()
    hasher.Write(zeroHash[:12])    // 12 zero bytes
    hasher.Write(addr[:])           // 20 bytes address
    hasher.Write(key[:31])          // 31 bytes key
    k := hasher.Sum(nil)            // 32 bytes hash
    k[31] = key[31]                 // Last byte is suffix
    return k
}
```

#### アカウント基本データキー

```go
// BasicData (balance, nonce, code size) のキー
func GetBinaryTreeKeyBasicData(addr common.Address) []byte {
    var k [32]byte
    k[31] = BasicDataLeafKey  // = 0
    return GetBinaryTreeKey(addr, k[:])
}

// CodeHash のキー
func GetBinaryTreeKeyCodeHash(addr common.Address) []byte {
    var k [32]byte
    k[31] = CodeHashLeafKey  // = 1
    return GetBinaryTreeKey(addr, k[:])
}
```

#### ストレージスロットキー（重要）

```go
// ストレージスロットのキー
func GetBinaryTreeKeyStorageSlot(address common.Address, key []byte) []byte {
    var k [32]byte

    // ケース1: ヘッダー領域（key[31] < 64 かつ key[:31] がゼロ）
    if bytes.Equal(key[:31], zeroHash[:31]) && key[31] < 64 {
        k[31] = 64 + key[31]  // オフセット64を加算
        return GetBinaryTreeKey(address, k[:])
    }

    // ケース2: メインストレージ領域
    k[0] = 1  // 1 << 248 をセット
    copy(k[1:], key[:31])
    k[31] = key[31]

    return GetBinaryTreeKey(address, k[:])
}
```

**注意**: ストレージプルーフを生成する際は、元のストレージキー（Ethereum形式）を
`GetBinaryTreeKeyStorageSlot` で変換してからバイナリトライを検索する必要がある。

#### キー導出早見表

| 対象                                  | 関数                                     | サフィックス |
| ------------------------------------- | ---------------------------------------- | ------------ |
| BasicData (balance, nonce, code size) | `GetBinaryTreeKeyBasicData(addr)`        | 0            |
| CodeHash                              | `GetBinaryTreeKeyCodeHash(addr)`         | 1            |
| ストレージスロット                    | `GetBinaryTreeKeyStorageSlot(addr, key)` | 可変         |

### 実装方針

**重要な制約**:

1. `StateDB` からは直接 `BinaryTrie` を取得できない
2. `triedb.Database.OpenBinaryTrie()` は存在しない
3. プルーフ生成には `bintrie.NewBinaryTrie(root, trieDB)` で直接オープンする

```go
import "github.com/ethereum/go-ethereum/trie/bintrie"

func (api *DebugAPI) GetUBTProof(ctx context.Context, address common.Address, storageKeys []string, blockNrOrHash rpc.BlockNumberOrHash) (*UBTProofResult, error) {
    // 1. StateDB と header を取得
    statedb, header, err := api.eth.APIBackend.StateAndHeaderByNumberOrHash(ctx, blockNrOrHash)
    if err != nil {
        return nil, err
    }

    // 2. アカウント情報取得（StateDB経由）
    balance := statedb.GetBalance(address)
    nonce := statedb.GetNonce(address)
    codeHash := statedb.GetCodeHash(address)

    // 3. UBT root を取得して BinaryTrie を直接オープン
    //    重要: header.Root は MPT root。UBT root は sidecar から取得する
    ubtRoot := getUBTRootForBlock(header.Hash()) // debug_getUBTProof などで取得
    trieDB := api.eth.BlockChain().TrieDB()
    bt, err := bintrie.NewBinaryTrie(ubtRoot, trieDB)
    if err != nil {
        return nil, fmt.Errorf("failed to open binary trie: %w", err)
    }

    // 4. bt.Hash() は ubtRoot と一致するはず
    _ = bt.Hash()

    // 5. アカウントプルーフ生成
    //    BasicDataLeafKey でアカウント基本データのプルーフを取得
    accountKey := bintrie.GetBinaryTreeKeyBasicData(address)
    accountProof, err := generateProofFromBinaryTrie(bt, accountKey)
    if err != nil {
        return nil, fmt.Errorf("failed to generate account proof: %w", err)
    }

    // 6. ストレージプルーフ生成
    storageProofs := make([]UBTStorageProof, len(storageKeys))
    for i, keyHex := range storageKeys {
        key := common.HexToHash(keyHex)
        value := statedb.GetState(address, key)

        // ストレージキーを UBT キーに変換（重要）
        ubtStorageKey := bintrie.GetBinaryTreeKeyStorageSlot(address, key.Bytes())

        proof, err := generateProofFromBinaryTrie(bt, ubtStorageKey)
        if err != nil {
            return nil, fmt.Errorf("failed to generate storage proof for %s: %w", keyHex, err)
        }
        storageProofs[i] = UBTStorageProof{
            Key:   key,
            Value: value.Bytes(),
            Proof: proof,
        }
    }

    return &UBTProofResult{
        Address:      address,
        AccountProof: accountProof,
        Balance:      (*hexutil.Big)(balance.ToBig()),
        CodeHash:     codeHash,
        Nonce:        hexutil.Uint64(nonce),
        StorageHash:  common.Hash{}, // UBTでは個別ストレージrootは存在しない
        StorageProof: storageProofs,
        StateRoot:    header.Root,   // 状態識別子（MPT互換）
        UbtRoot:      ubtRoot,       // 実際のUBT root（bt.Hash()で計算）
    }, nil
}

// generateProofFromBinaryTrie - BinaryTrieからプルーフを生成
//
// 注意: 現在の BinaryTrie.NodeIterator(startKey) は startKey を無視する実装になっている
// (trie/bintrie/trie.go:344 で常に nil を渡している)
// そのため、目的のキーまで手動でイテレートする必要がある
func generateProofFromBinaryTrie(bt *bintrie.BinaryTrie, targetKey []byte) ([]hexutil.Bytes, error) {
    // 1. イテレータを作成（startKey は無視される）
    it, err := bt.NodeIterator(nil)
    if err != nil {
        return nil, err
    }

    // 2. 目的のキーまでイテレート
    //    注意: これは O(n) の操作であり、トライが大きい場合は非効率
    found := false
    for it.Next(true) {
        if it.Leaf() && bytes.Equal(it.LeafKey(), targetKey) {
            found = true
            break
        }
    }
    if it.Error() != nil {
        return nil, it.Error()
    }
    if !found {
        return nil, fmt.Errorf("key not found in trie: %x", targetKey)
    }

    // 3. LeafProof() を呼び出し
    //    trie/bintrie/iterator.go の binaryNodeIterator.LeafProof() を参照
    proofBytes := it.LeafProof()

    // 4. [][]byte を []hexutil.Bytes に変換
    result := make([]hexutil.Bytes, len(proofBytes))
    for i, p := range proofBytes {
        result[i] = hexutil.Bytes(p)
    }
    return result, nil
}
```

### UBT Proof検証

```go
func (v *Validator) validateUBTProofs(ctx context.Context, blockTag rpc.BlockNumberOrHash) error {
    // サンプルアドレスに対してUBT Proofを検証
    sampleAddrs := v.getSampleAddresses(10) // 10件サンプル

    for _, addr := range sampleAddrs {
        // 1. UBT Proof取得
        var proof UBTProofResult
        err := v.ubt.Call(&proof, "debug_getUBTProof", addr, []string{}, blockTag)
        if err != nil {
            return fmt.Errorf("failed to get UBT proof for %s: %w", addr, err)
        }

        // 2. 値の一致確認（リファレンスノードと比較）
        refBalance, _ := v.ref.BalanceAt(ctx, addr, blockTag)
        if proof.Balance.ToInt().Cmp(refBalance) != 0 {
            return fmt.Errorf("balance mismatch in proof for %s", addr)
        }

        // 3. Proof構造の検証
        if len(proof.AccountProof) == 0 {
            return fmt.Errorf("account proof is empty for %s", addr)
        }

        // 4. Proof検証（Merkle検証）
        // verifyUBTProof(proof.UbtRoot, proof.AccountProof, addr, ...)

        log.Info("UBT proof validation passed", "address", addr)
    }

    return nil
}
```

---

## 実装上のハードブロッカーと解決策

### 1. UBT Proof生成 (`debug_getUBTProof`)

**Problem**: `StateDB` は `BinaryTrie` を公開しない。`OpenTrie` も UBT
モードでは返さない。

**Fix**: `triedb/pathdb` から `BinaryTrie` を明示的にオープンする。

**Plan**:

1. `header.Root` は **MPT root**。UBT root は sidecar から取得する
2. `bintrie.NewBinaryTrie(ubtRoot, trieDB)` で BinaryTrie をオープン
3. `bt.Hash()` が `ubtRoot` と一致することを確認
4. `trie/bintrie/iterator.go` の `binaryNodeIterator` と `LeafProof()` を使用
5. **推奨**: `*bintrie.BinaryTrie` を直接返すヘルパー関数を追加

**実装コード**:

```go
// eth/api_debug.go に追加推奨
func (api *DebugAPI) openBinaryTrie(root common.Hash) (*bintrie.BinaryTrie, error) {
    trieDB := api.eth.BlockChain().TrieDB()
    return bintrie.NewBinaryTrie(root, trieDB)
}
```

**注意**: 現在の `BinaryTrie.NodeIterator(startKey)` は startKey
を無視する（`trie/bintrie/trie.go:344`）。 プルーフ生成は O(n)
となるが、検証目的では許容可能。本番用途では修正が必要。

---

### 2. UBT Proofキー導出

**Problem**: `GetBinaryTreeKey`
だけでは不十分。ストレージキーとアカウントリーフキーには特定のエンコーディングが必要。

**Fix**: `trie/bintrie/key_encoding.go` の正しいヘルパーを使用する。

**Plan**:

| 対象                          | 関数                                            |
| ----------------------------- | ----------------------------------------------- |
| Account proof key (BasicData) | `GetBinaryTreeKeyBasicData(addr)`               |
| Code hash leaf                | `GetBinaryTreeKeyCodeHash(addr)`                |
| Storage proof key             | `GetBinaryTreeKeyStorageSlot(addr, storageKey)` |

---

### 3. ストレージスナップショットの不整合

**Problem**: `debug_storageRangeAt(blockHash, 0, ...)`
はブロック先頭状態（pre-block）だが、`eth_getStorageAt(blockHash)`
はブロック末尾状態（post-block）。

**Fix**: `debug_storageRangeAt`
は**キー列挙専用**として使用し、値の比較は常に同一アンカーブロックで
`eth_getStorageAt` を使用する。

**Plan**:

1. **Step A (リファレンスノード)**: `debug_storageRangeAt` でキーのみ取得
2. **Step B (両ノード)**: そのキーを使って `eth_getStorageAt(..., blockTag)`
   で値を比較
3. **禁止**: `debug_storageRangeAt` の返り値を直接比較しない

**Doc change**: Phase 2 フローに「storageRangeAt is used for key discovery
only」を明記。

---

### 4. Balance比較の型不一致

**Problem**: `DumpAccount.Balance` は `string` 型（decimal形式）。

**Fix**: 比較前に `big.Int` にパースする。

**Plan**:

```go
refBalance, ok := new(big.Int).SetString(refAcc.Balance, 10)
if !ok {
    return fmt.Errorf("failed to parse reference balance: %s", refAcc.Balance)
}
```

---

### 5. Phase 0/1 での `latest` 使用禁止

**Problem**: `latest` と `nil` ブロックタグを使用していた（reorgリスク）。

**Fix**: 同一のアンカーブロック戦略を使用する。

**Plan**:

- Phase 0: `finalized`/`safe` ブロックが利用可能ならそれを使用して debug RPC
  サポートを確認
- Phase 1: `latest` の代わりに `eth_getBalance(knownAddr, blockTag)` を使用

---

### 6. UBT Root と header.Root の違い

**Problem**: `header.Root` は MPT root であり、UBT root ではない。

**Fix**: sidecar から UBT root を取得し、`bintrie.NewBinaryTrie(ubtRoot, trieDB)`
でトライをオープンする。

**Plan**:

1. `debug_getUBTProof` などで対象ブロックの `UbtRoot` を取得
2. `bintrie.NewBinaryTrie(ubtRoot, trieDB)` で UBT トライを開く
3. `UBTProofResult` には両方を含める（`StateRoot`=MPT root, `UbtRoot`=sidecar root）

**Precondition**: sidecar モードでは `CommitStateRootToHeader=false` が前提。

---

### 7. AccountRange の返り値の型

**Problem**: `debug_accountRange` の返り値 `state.Dump.Accounts` は
`map[string]DumpAccount` であり、キーは hex string。

**Fix**: キーを `common.HexToAddress()` でパースする。

**Plan**:

```go
for addrStr, acc := range accounts.Accounts {
    var addr common.Address
    if acc.Address != nil {
        addr = *acc.Address
    } else {
        addr = common.HexToAddress(addrStr)
    }
    // ...
}
```

---

### 8. ExtWitness → Witness 変換

**Problem**: `ExtWitness.ToWitness()`
メソッドは存在しない。`Witness.fromExtWitness()` は private。

**Fix**: `core/stateless` パッケージに公開コンストラクタを追加する。

**Plan**: 以下のコードを `core/stateless/encoding.go` に追加:

```go
// NewWitnessFromExtWitness creates a Witness from ExtWitness format.
func NewWitnessFromExtWitness(ext *ExtWitness) (*Witness, error) {
    w := &Witness{}
    if err := w.fromExtWitness(ext); err != nil {
        return nil, err
    }
    return w, nil
}
```

---

### 8a. ExtUBTWitness → Witness 変換（UBT path 付き）

**Problem**: `ExtWitness` には `StatePaths` が無いため **UBT witness を復元できない**。

**Fix**: UBT 用に path 情報を含む wire format を追加し、専用変換を実装。

**Plan**:

```go
// ExtUBTWitness is a path-aware witness format for UBT.
type ExtUBTWitness struct {
    Headers    []*types.Header `json:"headers"`
    Codes      []hexutil.Bytes `json:"codes"`
    StatePaths []PathNode      `json:"statePaths"`
}

type PathNode struct {
    Path hexutil.Bytes `json:"path"`
    Node hexutil.Bytes `json:"node"`
}

// NewWitnessFromUBTWitness converts UBT witness into internal Witness.
func NewWitnessFromUBTWitness(ext *ExtUBTWitness) (*Witness, error) {
    w := &Witness{
        Headers:    ext.Headers,
        Codes:      make(map[string]struct{}, len(ext.Codes)),
        State:      make(map[string]struct{}),
        StatePaths: make(map[string][]byte, len(ext.StatePaths)),
    }
    for _, code := range ext.Codes {
        w.Codes[string(code)] = struct{}{}
    }
    for _, pn := range ext.StatePaths {
        w.StatePaths[string(pn.Path)] = pn.Node
        // Backward compatibility: also populate State
        w.State[string(pn.Node)] = struct{}{}
    }
    return w, nil
}
```

**Note**: ルートノードは `path=0x`（空パス）で必ず含める。

---

## コマンドラインインターフェース

```go
func main() {
    app := &cli.App{
        Name:  "ubt-validator",
        Usage: "Validate UBT state against reference node",
        Flags: []cli.Flag{
            &cli.StringFlag{
                Name:     "ubt-rpc",
                Usage:    "UBT node RPC endpoint",
                Value:    "http://localhost:8545",
                EnvVars:  []string{"UBT_RPC"},
            },
            &cli.StringFlag{
                Name:     "reference-rpc",
                Usage:    "Reference node RPC endpoint (must support debug APIs)",
                Required: true,
                EnvVars:  []string{"REFERENCE_RPC"},
            },
            &cli.IntFlag{
                Name:    "account-samples",
                Usage:   "Number of accounts to sample",
                Value:   30000,
            },
            &cli.IntFlag{
                Name:    "storage-samples",
                Usage:   "Storage slots per contract",
                Value:   500,
            },
            &cli.Int64Flag{
                Name:    "seed",
                Usage:   "Random seed for reproducibility",
                Value:   time.Now().UnixNano(),
            },
            &cli.IntFlag{
                Name:    "transition-blocks",
                Usage:   "Number of blocks to validate for state transition",
                Value:   5,
            },
            &cli.IntFlag{
                Name:    "witness-blocks",
                Usage:   "Number of blocks to validate witnesses",
                Value:   5,
            },
            &cli.StringSliceFlag{
                Name:    "phases",
                Usage:   "Phases to run (0,1,2,3,4,5 or all)",
                Value:   cli.NewStringSlice("all"),
            },
        },
        Action: runValidator,
    }

    if err := app.Run(os.Args); err != nil {
        log.Crit("Validation failed", "error", err)
    }
}
```

### 使用例

```bash
# フル検証
./ubt-validator \
  --ubt-rpc http://localhost:8545 \
  --reference-rpc http://localhost:8546 \
  --account-samples 30000 \
  --storage-samples 500 \
  --transition-blocks 5 \
  --witness-blocks 5 \
  --seed 12345

# 特定フェーズのみ
./ubt-validator \
  --reference-rpc http://localhost:8546 \
  --phases 0,1,2

# CI用（環境変数使用）
UBT_RPC=http://geth:8545 \
REFERENCE_RPC=http://geth-ref:8546 \
./ubt-validator --account-samples 10000
```

---

## 出力形式

### 成功時

```
2024-01-15T10:00:00Z INFO  Starting UBT validation
2024-01-15T10:00:00Z INFO  Phase 0: Checking preconditions
2024-01-15T10:00:01Z INFO  Phase 0: Precondition check passed
2024-01-15T10:00:01Z INFO  Phase 1: Checking UBT status
2024-01-15T10:00:02Z INFO  Phase 1: UBT state readable (full sync mode)
2024-01-15T10:00:02Z INFO  Phase 2: Validating sampled accounts
2024-01-15T10:00:02Z INFO  Using anchor block number=1234567 hash=0xdef...
2024-01-15T10:05:00Z INFO  Phase 2: Value validation passed sampled=30000
2024-01-15T10:05:00Z INFO  Phase 3: Validating state transitions
2024-01-15T10:06:00Z INFO  Phase 3: Transition validation passed blocks=5
2024-01-15T10:06:00Z INFO  Phase 4: Validating witnesses
2024-01-15T10:07:00Z INFO  Phase 4: Witness validation complete blocks=5
2024-01-15T10:07:00Z INFO  Phase 5: Validating RPC consistency
2024-01-15T10:07:30Z INFO  Phase 5: RPC consistency validation passed
2024-01-15T10:07:30Z INFO  ========================================
2024-01-15T10:07:30Z INFO  VALIDATION PASSED
2024-01-15T10:07:30Z INFO  ========================================
```

### 失敗時（即座に終了）

```
2024-01-15T10:00:00Z INFO  Starting UBT validation
2024-01-15T10:00:00Z INFO  Phase 0: Checking preconditions
2024-01-15T10:00:01Z INFO  Phase 0: Precondition check passed
2024-01-15T10:00:01Z INFO  Phase 1: Checking UBT status
2024-01-15T10:00:02Z INFO  Phase 1: UBT state readable (full sync mode)
2024-01-15T10:00:02Z INFO  Phase 2: Validating sampled accounts
2024-01-15T10:02:15Z INFO  Progress sampled=15234 target=30000
2024-01-15T10:02:15Z CRIT  ========================================
2024-01-15T10:02:15Z CRIT  VALIDATION FAILED
2024-01-15T10:02:15Z CRIT  Phase: 2
2024-01-15T10:02:15Z CRIT  Account: 0x1234567890abcdef...
2024-01-15T10:02:15Z CRIT  Error: balance mismatch: UBT=1000000000, Ref=2000000000
2024-01-15T10:02:15Z CRIT  ========================================
exit code 1
```

---

## ディレクトリ構成

```
docker-ubt-test/
├── cmd/
│   └── validate/
│       ├── main.go                # エントリポイント
│       ├── validator.go           # Validator構造体
│       ├── phase0_precondition.go # Phase 0
│       ├── phase1_ubt_status.go   # Phase 1
│       ├── phase2_values.go       # Phase 2
│       ├── phase3_transition.go   # Phase 3
│       ├── phase4_witness.go      # Phase 4
│       ├── phase5_rpc.go          # Phase 5
│       ├── sampler.go             # サンプリング
│       ├── comparator.go          # 値比較
│       └── anchor.go              # ブロックアンカー
├── scripts/
│   ├── run-validation.sh          # 実行スクリプト
│   └── setup-reference-node.sh    # リファレンスノード起動
├── UBT_VALIDATION_PLAN.md         # この計画書
└── Makefile
```

---

## 実装ロードマップ

### Phase A: 検証ツール基盤（新規RPC不要）

1. Phase 0-4 の実装
2. Phase 5 の基本RPC検証（eth_* のみ）

### Phase B: UBT Proof対応（新規RPC追加）

1. `debug_getUBTProof` の実装（eth/api_debug.go）
2. Phase 5 に UBT Proof検証を追加
3. Proof検証ロジックの実装

---

## 注意事項

### MPTとUBTのState Root

- MPTとUBTはstate rootが**異なるハッシュ体系**を使用
  - MPT: Keccak256
  - UBT: SHA256
- **ノード間の直接root比較は不可**（Phase 2, 3, 5 では値ベースで比較）
- **同一ノード内のself-validation**（Phase 4）では root が一致する

### リファレンスノードの要件

- **debug RPC対応必須**（Infura等の商用サービスは使用不可）
- UBTノードと同じブロック高で同期済みであること
- 推奨: 同一マシン上で別ポートで起動

### 再現性

- `--seed` オプションで乱数シードを固定可能
- 同じシードを使えば同じアカウントがサンプリングされる
- CI/CDでの再現性確保に有用

### Preimage要件

- ストレージサンプリングには `--cache.preimages` が必須
- start-sync.sh では既に有効化済み
- リファレンスノードにも必要

---

## 関連ドキュメント

- [README.md](./README.md) - Docker環境のセットアップ
- [scripts/](./scripts/) - 既存の検証スクリプト群
- [cmd/validate/](./cmd/validate/) - 検証ツール実装（予定）
- [trie/bintrie/WITNESS.md](../trie/bintrie/WITNESS.md) - Witness形式仕様

---

## 更新履歴

| バージョン | 日付       | 概要                                                 |
| ---------- | ---------- | ---------------------------------------------------- |
| v3.0       | 2026-01-19 | 将来拡張部分を削除し即時検証に集中、更新履歴を簡略化 |
| v2.x       | 2026-01-19 | 複数回のレビュー対応（v2.1〜v2.6）                   |
| v2.0       | 2026-01-19 | 初版（リファレンスノード比較方式）                   |
