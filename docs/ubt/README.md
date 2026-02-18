# UBT ドキュメント集約（コミット推奨範囲）

このディレクトリは、このブランチで「コミット対象に残すドキュメント」を集約した場所です。

## 収録ドキュメント
1. `docs/ubt/outbox_to_ubt_apply_flow.md`
- geth outbox から ubtconv apply/commit/reorg/recovery までの詳細フロー。

2. `docs/ubt/operator_manual.md`
- 起動手順・運用確認・トラブルシュート。

3. `docs/ubt/manual_acceptance_checklist.md`
- 手動受け入れ確認の実行手順。

4. `docs/ubt/ubtconv_test_matrix.md`
- テスト実行証跡の台帳。

5. `docs/ubt/ubtconv_midpoint_recovery_and_bug_investigation.md`
- triedb 障害時の途中復旧機能の検討と、`Unexpected trie node` の不具合調査計画。

6. `docs/ubt/ubtconv_midpoint_recovery_v1_design.md`
- Midpoint Recovery v1 の詳細設計（データレイアウト、起動復旧分岐、compaction 連携、実装差分、テスト計画）。

## このブランチで docs だけコミットする場合
```bash
git add docs/ubt
```
