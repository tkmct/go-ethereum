# Implementation Roadmap (MPT -> UBT, Hardened v3)

## Scope
This roadmap executes `plan_final.md` with strict boundaries and test gates:
1. Geth is only outbox writer.
2. Daemon is only UBT/checkpoint writer.
3. Dedicated outbox DB is baseline backend and is opened only by geth process.
4. Archive replay is required for deep recovery.
5. Every PR must implement and verify its mapped tests.
6. Daemon consumes outbox via geth RPC transport (no direct outbox DB open).
7. UBT debug RPC delivery is staged: read-class (Stage A), proof (Stage B), execution-class (Stage C, Phase 7).

## Delivery Model
1. Keep PRs reviewable and independently testable.
2. Freeze contracts and storage semantics before runtime complexity.
3. Preserve canonical import isolation from converter failures.
4. Enforce CI pass gates and coverage gates throughout.

## Testing and Verification Policy
1. Mandatory suite groups:
   1. `TC-CONTRACT-*`
   2. `TC-OUTBOX-*`
   3. `TC-EMIT-*`
   4. `TC-DAEMON-*`
   5. `TC-APPLY-*`
   6. `TC-REPLAY-*`
   7. `TC-REORG-*`
   8. `TC-SNAP-*`
   9. `TC-DEL-*`
   10. `TC-VAL-*`
   11. `TC-MAINNET-*`
   12. `TC-CHAOS-*`
   13. `TC-E2E-*`
   14. `TC-PROOF-*`
   15. `TC-RPC-READ-*`
   16. `TC-RPC-PROOF-*`
   17. `TC-RPC-EXEC-*`
2. Coverage gates:
   1. 80% minimum on touched feature packages.
   2. 90% minimum on critical new logic.
3. Pass gates:
   1. No PR merges with failing mandatory suites.
   2. Phase 1-6 rollout requires full mandatory matrix pass excluding `TC-RPC-EXEC-*`.
   3. Phase 7 rollout requires `TC-RPC-EXEC-*` pass evidence in addition to prior suites.

## PR0: Contract and Interface Freeze
Goal:
1. Freeze outbox envelope/types and replay interface decisions.

Changes:
1. Define event envelope with `Kind`.
2. Define `QueuedDiffV1` and `ReorgMarkerV1` contracts.
3. Freeze replay interface including canonical hash access.
4. Add import-boundary policy and CI import-cycle check rules.

Test implementation tasks:
1. `TC-CONTRACT-001..006`: deterministic encoding and version compatibility.
2. `TC-CONTRACT-010..012`: interface compile and compatibility tests.
3. `TC-BOUNDARY-001..003`: import-boundary and cycle-guard checks.

Verification:
1. Run contract package tests.
2. Verify all `TC-CONTRACT-*` pass.
3. Verify `TC-BOUNDARY-*` cycle checks pass.

Exit criteria:
1. Contracts are frozen.
2. `TC-CONTRACT-*` passes.

## PR1: Dedicated Outbox DB and Accessors
Goal:
1. Implement dedicated outbox backend and durable accessor layer.

Changes:
1. Add outbox schema and accessors in rawdb adapter package.
2. Implement dedicated DB open/close lifecycle.
3. Add consumer state records (`pendingSeq`, `appliedSeq`, `appliedRoot`).

Test implementation tasks:
1. `TC-OUTBOX-001..006`: append/order/range correctness.
2. `TC-OUTBOX-010..013`: dedicated DB lifecycle and reopen behavior.
3. `TC-STATE-001..006`: durable consumer state transitions.

Verification:
1. Run outbox/state suites.
2. Verify `TC-OUTBOX-*` and `TC-STATE-*` pass.

Exit criteria:
1. Dedicated backend works with deterministic ordering.
2. State accessors are crash-safe.

## PR2: Geth Emitter and Raw-Key-Safe Adapter
Goal:
1. Emit canonical diff events with raw-key-safe conversion path.

Changes:
1. Implement emitter worker and outbox append pipeline.
2. Wire `core/blockchain.go` lifecycle and hook point.
3. Update `core/blockchain.go` commit condition to include `hasEmitter` so emitter-enabled path always runs `CommitWithUpdate`.
4. Convert `CommitWithUpdate` return payload to `QueuedDiffV1` in `core/blockchain.go` and pass converted payload to `ubtemit.Service.EmitDiff`.
5. Add geth CLI/config wiring:
   1. `--ubt.conversion-enabled`, `--ubt.decoupled`.
   2. `--ubt.outbox-db-path`, `--ubt.outbox-write-timeout`.
   3. `--ubt.reorg-marker-enabled`, `--ubt.replay-rpc-enabled`, `--ubt.replay-rpc-endpoint`.

Test implementation tasks:
1. `TC-EMIT-001..006`: commit-to-outbox path and sequence correctness.
2. `TC-EMIT-010..014`: degraded mode non-blocking behavior.
3. `TC-EMIT-020..024`: raw-key invariant checks.
4. `TC-EMIT-030..032`: `hasEmitter` commit-path selection behavior.

Verification:
1. Run emitter unit/integration suites.
2. Verify all `TC-EMIT-*` pass.

Exit criteria:
1. Emitter appends ordered diffs.
2. Canonical import remains non-blocking.

## PR3: Daemon Skeleton, Config Split, and Archive Preconditions
Goal:
1. Bring up daemon runtime with strict config boundaries.

Changes:
1. Implement startup/shutdown/checkpoint resume flow.
2. Enforce geth-vs-daemon config ownership split.
3. Add `RequireArchiveReplay` preflight checks.
4. Implement geth-side outbox-read RPC server handlers for outbox range/stream APIs.
5. Implement daemon-side outbox-read RPC client transport and prohibit daemon direct outbox DB open.
6. Add daemon trie-history config wiring (`UBTTrieDBScheme=path`, `UBTTrieDBStateHistory`) and startup validation.
7. Add CLI/config wiring:
   1. Geth flags: `--ubt.outbox-read-rpc-enabled`.
   2. Daemon flags: `--ubt.outbox-rpc-endpoint`, `--ubt.require-archive-replay`, `--ubt.triedb-scheme`, `--ubt.triedb-state-history`.

Test implementation tasks:
1. `TC-DAEMON-001..006`: startup/shutdown/resume.
2. `TC-CONFIG-001..004`: config split enforcement.
3. `TC-REPLAY-001..003`: archive precondition fail-fast.
4. `TC-DAEMON-020..022`: daemon direct DB-open rejection and RPC-only consumption path.
5. `TC-CONFIG-005..007`: daemon trie-history scheme/window config validation.

Verification:
1. Run daemon control-plane suites.
2. Verify `TC-DAEMON-*`, `TC-CONFIG-*`, and preflight replay tests pass.

Exit criteria:
1. Daemon stable runtime with explicit preflight guards.
2. Trie-history scheme/window config is validated and enforced at startup.

## PR4: Applier and Crash-Consistent Consumer Protocol
Goal:
1. Implement deterministic apply + finalize semantics.

Changes:
1. Implement account/storage/code apply.
2. Implement `pendingSeq -> apply -> commit -> appliedSeq` protocol.
3. Add root consistency check at startup.
4. Persist finalized block-to-root index for every `appliedSeq` to support historical selector resolution.
5. Add daemon CLI/config wiring:
   1. `--ubt.apply-commit-interval`, `--ubt.apply-commit-max-latency`.

Test implementation tasks:
1. `TC-APPLY-001..008`: apply correctness.
2. `TC-APPLY-010..016`: interrupted execution and restart safety.
3. `TC-COMMIT-001..004`: interval/latency commit logic.
4. `TC-COMMIT-005..008`: finalized-only query visibility and per-block root index correctness.

Verification:
1. Run applier/commit suites.
2. Verify `TC-APPLY-*` and `TC-COMMIT-*` pass.

Exit criteria:
1. No seq loss/skip.
2. Stable deterministic committed roots.

## PR5: Replay Pipeline and Bootstrap Modes
Goal:
1. Implement `tail` and `backfill-direct` without daemon outbox writes.

Changes:
1. Implement replay adapter and bootstrap mode selection.
2. Add cutover from backfill-direct to tail.
3. Enforce daemon no-outbox-write invariant.
4. Add daemon CLI/config wiring:
   1. `--ubt.bootstrap-mode`.

Test implementation tasks:
1. `TC-BOOT-001..006`: bootstrap mode correctness.
2. `TC-BOOT-010..014`: cutover semantics.
3. `TC-BOOT-020`: no daemon outbox writes.

Verification:
1. Run bootstrap/replay suites.
2. Verify all `TC-BOOT-*` pass.

Exit criteria:
1. Safe startup for fresh and initialized deployments.

## PR6: Reorg Marker Contract and Ancestor Recovery
Goal:
1. Add explicit reorg signaling and deterministic rewind.

Changes:
1. Emit `ReorgMarkerV1` from geth `blockchain.reorg()` hook.
2. Handle marker in daemon before dependent diffs.
3. Implement common ancestor finder using canonical RPC + local root map.
4. Enforce marker ordering so marker is published before first new-branch diff.
5. Add daemon CLI/config wiring:
   1. `--ubt.max-recoverable-reorg-depth`.

Test implementation tasks:
1. `TC-REORG-001..008`: marker-driven reorg handling.
2. `TC-REORG-010..014`: ancestor search correctness.
3. `TC-REORG-020..024`: no bad-root persistence across reorg.
4. `TC-REORG-030..032`: marker-before-diff ordering guarantees.

Verification:
1. Run reorg suites.
2. Verify all `TC-REORG-*` pass.

Exit criteria:
1. Reorg converges deterministically with explicit markers.

## PR7: Retention, Compaction, and Backpressure
Goal:
1. Bound growth and maintain recoverability under lag.

Changes:
1. Implement retention invariant enforcement.
2. Add geth-side outbox compaction safe window checks.
3. Implement disk budget and high-watermark backpressure logic.
4. Implement RPC transport backpressure and retry/resume semantics.
5. Add CLI/config wiring:
   1. Geth flags: `--ubt.outbox-retention-seq-window`.
   2. Daemon flags: `--ubt.outbox-disk-budget-bytes`, `--ubt.outbox-alert-threshold-pct`.

Test implementation tasks:
1. `TC-RET-001..008`: compaction safety and retention boundaries.
2. `TC-BP-001..005`: disk budget alerts and catch-up transitions.
3. `TC-BP-010..013`: RPC backpressure and resume semantics.

Verification:
1. Run retention/backpressure suites.
2. Verify `TC-RET-*` and `TC-BP-*` pass.

Exit criteria:
1. Storage growth bounded with explicit operational behavior.

## PR8: Snapshot Slow-Path and Migration Workflow
Goal:
1. Ensure deep recovery and rollout migration safety.

Changes:
1. Implement anchor snapshot create/restore and pruning.
2. Implement slow-path replay from snapshot.
3. Implement migration runbook paths (shadow -> validate -> apply).
4. Add daemon CLI/config wiring:
   1. `--ubt.anchor-snapshot-interval`, `--ubt.anchor-snapshot-retention`.

Test implementation tasks:
1. `TC-SNAP-001..008`: snapshot creation and restore.
2. `TC-SNAP-010..014`: deep recovery fallback.
3. `TC-MIG-001..004`: migration mode transitions.

Verification:
1. Run snapshot/migration suites.
2. Verify `TC-SNAP-*` and `TC-MIG-*` pass.

Exit criteria:
1. Slow-path recovery and migration workflow validated.

## PR9: Deletion Semantics and Slot Index Scalability
Goal:
1. Finalize deletion correctness and index policy with scale constraints.

Changes:
1. Implement deletion helper semantics.
2. Implement `LegacySlotIndexMode` with Cancun boundary behavior.
3. Add index sizing counters and budget guards.
4. Add daemon CLI/config wiring:
   1. `--ubt.legacy-slot-index-mode`.

Test implementation tasks:
1. `TC-DEL-001..008`: deletion behavior across fork regimes.
2. `TC-DEL-010..013`: auto freeze/prune triggers.
3. `TC-DEL-020..022`: index budget and scaling behavior.

Verification:
1. Run deletion/index suites.
2. Verify all `TC-DEL-*` pass.

Exit criteria:
1. Deletion semantics and index policy are deterministic and budget-aware.

## PR10a: BinaryTrie.Prove Prerequisite (Critical Path)
Goal:
1. Complete proof primitive implementation early to unblock proof-facing work.

Changes:
1. Implement `BinaryTrie.Prove` for inclusion and absence proofs with deterministic output encoding.
2. Add proof-fixture vectors and verifier-compatibility utilities for primitive-level correctness.
3. Remove/guard panic paths so proof generation fails with explicit errors instead of panics.

Test implementation tasks:
1. `TC-PROOF-PRE-001..004`: `BinaryTrie.Prove` inclusion/absence/correctness tests.

Verification:
1. Run proof-primitive suite.
2. Verify all `TC-PROOF-PRE-*` pass.

Exit criteria:
1. `BinaryTrie.Prove` prerequisite is complete and stable.
2. PR13 can proceed without waiting for non-proof hardening tasks.

## PR10b: Validation, Observability, and Mainnet-Scale Hardening
Goal:
1. Close correctness and operational hardening for Phase 5.

Changes:
1. Implement strict/deep validation modes.
2. Add required metrics and lag/recovery dashboards.
3. Add mainnet-scale benchmark harness and chaos test harness.
4. Add end-to-end equivalence harness for sampled-height MPT/UBT comparison.
5. Add CLI/config wiring:
   1. Daemon flags: `--ubt.validation-enabled`, `--ubt.validation-sample-rate`.

Test implementation tasks:
1. `TC-VAL-001..008`: strict/deep validation correctness.
2. `TC-METRIC-001..006`: metric emission and alert thresholds.
3. `TC-MAINNET-001..006`: scale throughput and disk profile tests.
4. `TC-CHAOS-001..006`: crash/corruption/partition scenarios.
5. `TC-E2E-001..010`: MPT/UBT equivalence at sampled heights.

Verification:
1. Run hardening suite for validation/observability/perf/chaos/equivalence.
2. Verify `TC-VAL-*`, `TC-METRIC-*`, `TC-MAINNET-*`, `TC-CHAOS-*`, and `TC-E2E-*` pass.

Exit criteria:
1. Phase 5 non-proof hardening criteria are satisfied with pass evidence.

## PR11: Witness/Proof Integration (Phase 6)
Goal:
1. Integrate proof-readiness path on top of stable conversion pipeline.

Changes:
1. Implement/bridge witness/proof generation interfaces for UBT pipeline (using completed `BinaryTrie.Prove` base).
2. Add proof input validation against committed UBT roots.
3. Expose proof/witness integration API contract.

Test implementation tasks:
1. `TC-PROOF-001..006`: witness/proof interface and root consistency tests.
2. `TC-PROOF-010..012`: failure-path and compatibility tests.

Verification:
1. Run proof integration suite.
2. Verify all `TC-PROOF-*` pass.

Exit criteria:
1. Phase 6 proof-readiness acceptance criteria met.

## PR12: UBT Debug RPC Stage A (Read-Class Proxy)
Goal:
1. Deliver `debug_getUBTStorageAt`, `debug_getUBTBalance`, and `debug_getUBTCode` via geth proxy to daemon query API.

Changes:
1. Geth-side changes:
   1. Extend debug backend interface/client (`internal/ethapi/backend.go`, `eth/api_backend.go`) with UBT query methods.
   2. Add debug RPC handlers in `internal/ethapi/api.go` for Stage A methods, mirroring MPT method signatures where possible.
   3. Add proxy failure handling and request timeout semantics with explicit RPC error mapping.
   4. Add debug RPC latency/availability metrics for proxy path.
2. Daemon-side changes:
   1. Implement selector-resolution rules for Stage A (`latest` by daemon-applied head; unsupported tags rejected).
   2. Enforce history-window semantics (`UBTTrieDBStateHistory`) and return explicit `state not available` when pruned.
   3. Expose read-only query RPC methods for storage/balance/code lookups at selected blocks.
3. Add CLI/config wiring:
   1. Geth flags: `--ubt.debug-rpc-proxy-enabled`, `--ubt.debug-rpc-endpoint`, `--ubt.debug-rpc-timeout`.
   2. Daemon flags: `--ubt.query-rpc-enabled`, `--ubt.query-rpc-listen-addr`, `--ubt.query-rpc-max-batch`.

Test implementation tasks:
1. `TC-RPC-READ-001..006`: response compatibility with `eth_getStorageAt`, `eth_getBalance`, `eth_getCode`.
2. `TC-RPC-READ-010..013`: selector-resolution behavior and ahead-of-applied-head errors.
3. `TC-RPC-READ-014..018`: history-window behavior (in-window success, pruned-history errors, finalized-only visibility).
4. `TC-RPC-READ-020..024`: proxy failure/timeout/partial-response handling.

Verification:
1. Run Stage A debug RPC suite.
2. Verify all `TC-RPC-READ-*` pass.

Exit criteria:
1. Stage A read RPCs are stable, deterministic, and policy-compliant.
2. History-window error semantics are deterministic and documented.

## PR13: UBT Debug RPC Stage B (Proof Endpoint)
Goal:
1. Deliver `debug_getUBTProof` with UBT-native proof schema and verifier compatibility.

Changes:
1. Add `UBTProofResult` API schema and serialization contract in debug API layer.
2. Wire proof query path to daemon proof engine backed by `BinaryTrie.Prove`.
3. Enforce prerequisite gating: endpoint remains disabled/unavailable until prove capability is present.
4. Add conformance fixtures for inclusion/absence proofs and root binding.

Test implementation tasks:
1. `TC-RPC-PROOF-001..006`: schema validation and proof correctness against committed UBT roots.
2. `TC-RPC-PROOF-010..013`: prerequisite-unmet behavior and error-code compatibility.
3. `TC-RPC-PROOF-020..022`: daemon lag and selector boundary behavior for proof reads.

Verification:
1. Run Stage B proof RPC suite.
2. Verify all `TC-RPC-PROOF-*` pass.

Exit criteria:
1. `debug_getUBTProof` is production-ready with UBT-native schema contract.

## PR14: UBT Debug RPC Stage C (Execution-Class, Phase 7)
Goal:
1. Deliver `debug_callUBT` and `debug_executionWitnessUBT` on top of UBT-backed execution adapter.

Changes:
1. Implement UBT-backed execution adapter for EVM read/execution paths.
2. Add Phase 7 debug handlers for execution-class methods with compatibility-focused signatures.
3. Add feature gating so execution-class RPCs stay disabled before adapter readiness.
4. Define operational SLOs and resource limits for execution-class proxy workloads.

Test implementation tasks:
1. `TC-RPC-EXEC-001..006`: functional correctness against canonical MPT execution outputs on sampled blocks.
2. `TC-RPC-EXEC-010..013`: witness generation compatibility and failure-path behavior.
3. `TC-RPC-EXEC-020..022`: feature-gate behavior before readiness.

Verification:
1. Run Stage C execution-class RPC suite.
2. Verify all `TC-RPC-EXEC-*` pass.

Exit criteria:
1. Phase 7 execution-class UBT debug RPCs are validated.
2. This PR is not required for Phase 1-6 completion.

## PR Sequence and Dependency Graph
1. PR0 -> PR1 -> PR2.
2. PR3 depends on PR1.
3. PR4 depends on PR3.
4. PR5 depends on PR3 and PR4.
5. PR6 depends on PR5.
6. PR7 depends on PR6.
7. PR8 depends on PR7.
8. PR9 depends on PR4.
9. PR10a depends on PR4.
10. PR10b depends on PR7, PR8, and PR9.
11. PR11 depends on PR10a and PR10b.
12. PR12 depends on PR3 and PR4.
13. PR13 depends on PR10a and PR12.
14. PR14 depends on PR11 and PR13.

## Phase-to-PR Mapping
1. Phase 1: PR0, PR1, PR2.
2. Phase 2: PR3, PR4.
3. Phase 3: PR5, PR6, PR7.
4. Phase 4: PR8.
5. Phase 5: PR9, PR10a, PR10b.
6. Phase 6: PR11.
7. RPC Stage A (post-Phase 2): PR12.
8. RPC Stage B (post-Phase 5): PR13.
9. Phase 7 / RPC Stage C: PR14.

## CLI Flag Attribution by PR
1. PR2:
   1. `--ubt.conversion-enabled`, `--ubt.decoupled`.
   2. `--ubt.outbox-db-path`, `--ubt.outbox-write-timeout`.
   3. `--ubt.reorg-marker-enabled`, `--ubt.replay-rpc-enabled`, `--ubt.replay-rpc-endpoint`.
2. PR3:
   1. `--ubt.outbox-read-rpc-enabled`.
   2. `--ubt.outbox-rpc-endpoint`, `--ubt.require-archive-replay`.
   3. `--ubt.triedb-scheme`, `--ubt.triedb-state-history`.
3. PR4:
   1. `--ubt.apply-commit-interval`, `--ubt.apply-commit-max-latency`.
4. PR5:
   1. `--ubt.bootstrap-mode`.
5. PR6:
   1. `--ubt.max-recoverable-reorg-depth`.
6. PR7:
   1. `--ubt.outbox-retention-seq-window`.
   2. `--ubt.outbox-disk-budget-bytes`, `--ubt.outbox-alert-threshold-pct`.
7. PR8:
   1. `--ubt.anchor-snapshot-interval`, `--ubt.anchor-snapshot-retention`.
8. PR9:
   1. `--ubt.legacy-slot-index-mode`.
9. PR10b:
   1. `--ubt.validation-enabled`, `--ubt.validation-sample-rate`.
10. PR12:
   1. `--ubt.debug-rpc-proxy-enabled`, `--ubt.debug-rpc-endpoint`, `--ubt.debug-rpc-timeout`.
   2. `--ubt.query-rpc-enabled`, `--ubt.query-rpc-listen-addr`, `--ubt.query-rpc-max-batch`.

## Go/No-Go Checkpoints
1. After PR2: Phase 1 gate, emitter correctness and non-blocking guarantees (`TC-EMIT-*` pass).
2. After PR4: Phase 2 gate, crash-consistency and seq safety (`TC-APPLY-*`/`TC-COMMIT-*` pass).
3. After PR7: Phase 3 gate, replay/reorg/retention convergence (`TC-BOOT-*`/`TC-REORG-*`/`TC-RET-*`/`TC-BP-*` pass).
4. After PR8: Phase 4 gate, deep recovery and migration readiness (`TC-SNAP-*`/`TC-MIG-*` pass).
5. After PR10a: proof critical-path gate (`TC-PROOF-PRE-*` pass).
6. After PR10b: Phase 5 gate, hardening matrix pass (`TC-VAL-*`/`TC-MAINNET-*`/`TC-CHAOS-*`/`TC-E2E-*` pass).
7. After PR11: Phase 6 gate, proof-readiness matrix pass (`TC-PROOF-*`).
8. After PR12: RPC Stage A gate, debug RPC compatibility and selector policy pass (`TC-RPC-READ-*`).
9. After PR13: RPC Stage B gate, proof RPC schema and conformance pass (`TC-RPC-PROOF-*`).
10. After PR14: Phase 7 / RPC Stage C gate, execution-class RPC matrix pass (`TC-RPC-EXEC-*`).
