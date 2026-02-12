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
- Out of scope (for now): full execution-class UBT RPC (`call`, `executionWitness`) functional implementation.
- Success criteria:
1. All automated verification suites pass.
2. Manual acceptance checklist passes on a fresh environment.
3. No unresolved High severity findings remain.
4. Known deferred behavior is explicit and validated (expected errors, not silent incorrectness).

## Public APIs / Interfaces / Types to Verify
- `debug_*` proxy endpoints exposed by geth and routed to daemon:
1. `debug_getUBTBalance`
2. `debug_getUBTStorageAt`
3. `debug_getUBTCode`
4. `debug_getUBTProof`
5. `debug_callUBT` (expected Phase 7 error currently)
6. `debug_executionWitnessUBT` (expected Phase 7 error currently)
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
1. Bootstrap mode: `tail`, `backfill-direct`.
2. State: fresh start vs restart with persisted state.
3. Chain behavior: linear import vs reorg.
4. RPC target: daemon direct (`ubt_*`) vs geth proxy (`debug_getUBT*`).
- Required scenarios:
1. Fresh start consumes `seq=0` correctly.
2. Tail bootstrap skips historical backlog.
3. Restart after consuming `seq=0` starts at `seq=1`.
4. Reorg marker causes rollback to ancestor root and forward recovery.
5. Missing outbox event returns deterministic error, no corruption.
6. Block selector accepted and current behavior explicitly asserted (currently latest-only if still deferred).
7. Proof generation returns deterministic result for same root/key.
8. `getCode` behavior for no-code account and code account (current expected error if reconstruction not implemented).
9. Phase 7 RPCs return explicit not-available errors.

### 3. Manual Acceptance Checklist (Pre-release)
- Environment:
1. Start geth with UBT conversion + outbox enabled.
2. Start ubtconv with query RPC enabled.
- Checks:
1. `ubt_latestSeq` increments while blocks import.
2. `ubt_status` shows progressing `appliedSeq/appliedBlock`.
3. `debug_getUBTBalance` returns non-zero for funded address and matches expected state.
4. `debug_getUBTStorageAt` returns correct 32-byte value for known slot.
5. `debug_getUBTProof` returns non-empty proof for existing key.
6. `debug_getUBTCode` returns expected result/error based on implementation status.
7. `debug_callUBT` and `debug_executionWitnessUBT` return explicit Phase 7 errors (until implemented).
8. Kill/restart ubtconv and confirm it resumes without replay corruption.
9. Trigger controlled short reorg and confirm daemon recovers and continues.

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
3. Run manual acceptance checklist on release candidate.
4. Run Gate C nightly and track regressions.

## Exit Criteria for "Working Fine"
- "Working fine" is declared only when:
1. All CI gates pass.
2. Manual checklist is green.
3. Deferred behaviors are documented and verified by expected-error tests.
4. No High-severity finding is open.

## Assumptions and Defaults
1. `debug_callUBT` and `debug_executionWitnessUBT` remain deferred (Phase 7), verified via expected errors.
2. `getCode` may still be partial/deferred; verification expects explicit error, not silent empty success for code-bearing accounts.
3. Block selector full historical semantics may be staged; until complete, tests must assert and document latest-only behavior clearly.
4. Verification process is enforced in CI and not run ad hoc only.
