# UBTConv 途中復旧機能の検討と不具合調査計画

## 1. 目的
この文書は、次の2点を分けて検討するための1枚ドキュメントです。

1. 不正終了・ストレージ障害時でも、`triedb` 全入れ替えや genesis/floor 再開に依存せず、途中状態から復旧できる機能を定義する。
2. 今回発生した `Unexpected trie node` を、運用問題とソフトウェア不具合の両面で切り分ける調査計画を定義する。

## 2. 背景（今回の事象）
`2026-02-17` に以下が連続発生した。

1. `failed to open trie with expected root`
2. `Unexpected trie node`
3. `anchor restore failed (no anchor snapshots available)`
4. `fallback to genesis`
5. `bootstrapped to compacted outbox floor`

その後 `2026-02-17 18:40` 以降、`pendingSeq` で再試行ループに入り、`appliedSeq` が停止した。  
`triedb` を退避して再起動後は進捗が再開したが、これは root 連続性より可用性を優先した回復であり、期待する運用状態ではない。

## 3. 問題定義
現在の運用では、`triedb` が壊れたときに次のリスクがある。

1. anchor が無い場合、genesis/floor へフォールバックして root 連続性を失う。
2. outbox compaction と組み合わさると、厳密再構築経路が消える。
3. 失敗が継続してもプロセスが生存し続けるため、監視上「停止」に見えにくい。

## 4. 目標要件（途中復旧機能）
### 4.1 必須
1. `triedb` 障害時でも「途中状態（checkpoint）」から復旧できること。
2. root 連続性を壊す復旧（genesis/floor）を既定動作にしないこと。
3. 復旧不能時は fail-fast で停止し、明示的な運用判断なしに degraded 継続しないこと。
4. 復旧可否を起動時に機械判定できること。

### 4.2 非目標
1. 壊れた `triedb` の中身を比較して同期・修復すること。
2. 現行の outbox compaction 方針を無条件で拡張すること。

## 5. 提案: Midpoint Recovery v1
### 5.1 概要
`root pointer` だけの anchor ではなく、**独立した復旧可能スナップショット**を導入する。

1. `materialized anchor` を別領域に周期保存する（例: `datadir/recovery/anchors/<seq>/`）。
2. 各 anchor に `manifest` を持たせる（`seq, block, root, createdAt, checksum`）。
3. 起動時に live triedb が壊れていたら、最新の検証済み anchor を開く。
4. `anchor.seq + 1` から outbox replay で head へ追いつく。

### 5.2 起動時アルゴリズム（提案）
1. `expectedRoot` で live triedb を開く。
2. 失敗したら `materialized anchor` を新しい順で検証して open。
3. outbox coverage を確認する。`lowestSeq <= anchor.seq + 1` を満たすか検証。
4. 満たせば replay 実行し、復旧完了後に新 live triedb に切替。
5. 満たさなければ fail-fast で停止。既定では genesis fallback しない。

### 5.3 設定（提案）
1. `--recovery-strict=true`（既定）: 復旧不能なら停止。
2. `--recovery-allow-genesis-fallback=false`（既定）: 明示指定時のみ許可。
3. `--anchor-snapshot-interval` を運用必須化。
4. `--anchor-snapshot-retention` を運用必須化。
5. `--recovery-min-anchor-distance` を導入し、過疎 anchor を検知。

### 5.4 運用不変条件
1. outbox retention は「最新 anchor からの replay 長 + 安全余裕」を常に満たす。
2. anchor 作成失敗が連続した場合は alert を上げる。
3. `appliedSeq` 停滞かつ `pendingSeq` 固定を障害判定に使う。

## 6. 実装検討項目
1. anchor を「メタデータ root」から「復旧可能な実体スナップショット」に拡張する方式。
2. snapshot 作成中クラッシュに対する原子性（temp 作成→fsync→rename）。
3. replay 途中失敗時の再入可能性（idempotent resume）。
4. query/read path と apply/write path の競合安全性の再確認。
5. fail-fast 時の診断情報（失敗 seq, root, outbox coverage, anchor list）出力。

## 7. 不具合調査計画（別トラック）
途中復旧機能と独立して、ソフトウェア不具合有無を検証する。

### 7.1 調査目的
`Unexpected trie node` が、運用要因のみか、実装上の整合性バグを含むかを判定する。

### 7.2 仮説
1. ストレージ/異常終了に起因する純粋破損。
2. read/write 競合に起因する整合性崩壊。
3. apply/commit/reopen の順序または root 管理のバグ。
4. outbox payload 特定パターンでの apply 不整合。

### 7.3 収集すべき証跡
1. 失敗直前の `seq/block/root` と outbox event digest（payload hash）。
2. `pendingSeq` 固定期間の `ConsumeNext` エラー集計。
3. OS レベル証跡（OOM, I/O error, fs error, reboot, signal）。
4. anchor 作成状況と retention 実績。

### 7.4 実験計画
1. 強制 kill/再起動の fault injection で復旧成否を検証。
2. query 負荷あり/なしで再現性を比較。
3. compaction window を変えたときの復旧可能性を検証。
4. 同一 outbox を使った再実行で root 再現性を比較。

### 7.5 完了条件
1. 再現条件が1つ以上確定している。
2. 原因分類（運用/実装/両方）が明確である。
3. 修正方針または運用制約が文書化されている。

## 8. 直近アクション
1. `Midpoint Recovery v1` の詳細設計（データ構造・I/O手順・互換性）を作成する。
2. `anchor-snapshot-*` を運用既定値へ反映する。
3. 不具合調査用のログ拡張（失敗 seq/root/payload hash）を先に入れる。
