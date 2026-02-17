# UBT Verification Process (Integration + Manual)

## Summary
Establish a repeatable verification pipeline that proves:
1. Core UBT conversion correctness.
2. RPC/proxy behavior correctness.
3. Reorg/recovery safety.
4. Operational readiness (latency, failure handling, restart safety).

This process uses layered automated integration tests plus a fixed manual acceptance checklist before merge/release.

## Scope and Success Criteria
- Scope: `geth` UBT emitter/outbox, `ubtconv` consumer/query server, debug proxy RPC methods, proof path, bootstrap/reorg behavior.
- Scope assumes full-sync operation; snap/bootstrap-backfill paths are out of scope.
- Mainnet verification should use `geth + lighthouse + ubtconv` startup (see `scripts/run_mainnet_geth_ubtconv.sh`).
- Pre-Cancun raw storage key persistence is implemented; continuous diff emission before Cancun is expected in full-sync-from-genesis operation.
- Execution-class RPC (`debug_callUBT`, `debug_executionWitnessUBT`) は実装済み。既定値は無効で、`ubtconv --execution-class-rpc-enabled` 有効時のみ成功する。
- Success criteria:
1. All automated verification suites pass.
2. Manual acceptance checklist passes on a fresh environment.
3. No unresolved High severity findings remain.
4. Execution-class RPC を OFF/ON 両モードで検証し、期待通りのエラー/成功を確認する。

## Public APIs / Interfaces / Types to Verify
- `debug_*` proxy endpoints exposed by geth and routed to daemon:
1. `debug_getUBTBalance`
2. `debug_getUBTStorageAt`
3. `debug_getUBTCode`
4. `debug_getUBTProof`
5. `debug_callUBT` (proxied to daemon `ubt_callUBT`)
6. `debug_executionWitnessUBT` (proxied to daemon `ubt_executionWitnessUBT`)
- `ubt_*` daemon endpoints:
1. `ubt_getBalance`
2. `ubt_getStorageAt`
3. `ubt_getCode`
4. `ubt_getProof`
5. `ubt_status`
- Outbox transport endpoints:
1. `ubt_getEvent`
2. `ubt_getEvents`
3. `ubt_latestSeq`
4. `ubt_status` (emitter side)

## Verification Layers

### 1. CI Automated Test Gates
- Gate A: package/unit tests
1. `go test ./core/ubtemit`
2. `go test ./core/rawdb -run UBT`
3. `go test ./core/state -run UBT`
4. `go test ./trie/bintrie -run Prove`
5. `go test ./cmd/ubtconv`
6. `go test ./eth -run UBT`
- Gate B: focused integration tests (must run every PR touching UBT paths)
1. Query server integration.
2. Outbox read + consumer apply.
3. Debug proxy wiring.
4. Reorg marker handling.
- Gate C: nightly extended scenarios
1. Long-run replay.
2. Restart/crash recovery.
3. Retention window behavior.
4. Backoff/reconnect behavior.

### 2. Integration Test Matrix
- Matrix dimensions:
1. State: fresh start vs restart with persisted state.
2. Chain behavior: linear import vs reorg.
3. RPC target: daemon direct (`ubt_*`) vs geth proxy (`debug_getUBT*`).
- Required scenarios:
1. Fresh start consumes `seq=0` correctly.
2. Restart after consuming `seq=0` starts at `seq=1`.
3. Reorg marker causes rollback to ancestor root and forward recovery.
4. Missing outbox event returns deterministic error, no corruption.
5. Block selector behavior is explicitly asserted (`latest/number/hash` supported, `pending/safe/finalized` rejected).
6. Proof generation returns deterministic result for same root/key.
7. `getCode` behavior for no-code account and code account is explicitly asserted.
8. `callUBT` と `executionWitnessUBT` は `--execution-class-rpc-enabled=false` で disabled エラー、`true` で成功する。

### 3. Manual Acceptance Checklist (Pre-release)
- Environment:
1. Start geth with UBT conversion + outbox enabled.
2. Start consensus client (lighthouse) against geth authrpc.
3. Start ubtconv with query RPC enabled.
- Checks:
1. `ubt_latestSeq` increments while blocks import.
2. `ubt_status` shows progressing `appliedSeq/appliedBlock`.
3. `debug_getUBTBalance` matches expected state at a fixed selector (`ubt_status.appliedBlock` recommended for deterministic parity).
4. Storage parity is verified by deterministic integration (`TestVerify_FullPipelineIntegration`) and, when environment permits, optional live check with known contract/slot.
5. `debug_getUBTProof` returns non-empty proof for existing key.
6. `debug_getUBTCode` returns expected result/error based on implementation status.
7. `debug_callUBT` と `debug_executionWitnessUBT` について、flag OFF で disabled エラー、flag ON で成功を確認すること。
8. `debug_executionWitnessUBT` / `ubt_executionWitnessUBT` comparison is performed on the same explicit block selector.
9. Restart/resume and reorg recovery are validated by deterministic test sessions (`scripts/ubt_sessions/run_sessions.sh --session restart|reorg`) and optional live checks.

### 4. Fault and Recovery Verification
- Required injected failures:
1. Daemon crash during apply (before commit).
2. Daemon crash after commit.
3. Temporary RPC disconnect between daemon and geth.
4. Outbox retention pruning with lagging consumer.
- Pass criteria:
1. No stuck state.
2. Deterministic resume behavior.
3. Explicit errors instead of silent data divergence.

### 5. Observability Verification
- Ensure metrics/log checks are part of verification:
1. Outbox append success/error trend.
2. Consumer lag (`headSeq - appliedSeq`).
3. Reorg recovery count.
4. Commit latency distribution.
- Manual gate: alert thresholds tested in staging by synthetic lag/failure.

## Execution Order
1. Run Gate A on every branch push.
2. Run Gate B before merge.
3. Run `scripts/ubt_sessions/run_sessions.sh --session all` on release candidate.
4. Run Gate C nightly and track regressions.

## Exit Criteria for "Working Fine"
- "Working fine" is declared only when:
1. All CI gates pass.
2. Session matrix in `docs/testing/ubtconv_test_matrix.md` is green.
3. Execution-class RPC の OFF/ON 動作が文書化され、期待通りに検証されている。
4. No High-severity finding is open.

## Assumptions and Defaults
1. `debug_callUBT` と `debug_executionWitnessUBT` は既定で無効（`execution-class RPC disabled`）で、`--execution-class-rpc-enabled` 指定時のみ有効。
2. `getCode` は no-code/code-account の双方で期待挙動を明示検証する。
3. Block selector は `latest/number/hash` を受け付け、`pending/safe/finalized` は明示エラーを返す。
4. Verification process is enforced in CI and not run ad hoc only.
