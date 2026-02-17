# Roadmap Validation Against `plan_final.md`

## Method
1. Map each numbered section in `plan_final.md` to one or more PRs in `implementation_roadmap.md`.
2. Validate consistency of ownership, recovery semantics, storage choices, and test obligations.
3. Confirm every critical capability has explicit test implementation tasks and pass verification.

## Coverage Matrix
| Plan Section | Expected Capability | Roadmap PR Coverage | Status |
|---|---|---|---|
| 1 Objective | Decoupled, deterministic, recoverable conversion | PR2, PR4, PR5, PR6, PR8, PR10a, PR10b | Covered |
| 2 Architecture Options | Option A baseline with authoritative outbox | PR0, PR1, PR2, PR3 | Covered |
| 3 Core Principles | Single-writer, non-blocking import, replay-safe model | PR0, PR2, PR4, PR6, PR7 | Covered |
| 4 Existing Code to Reuse | Commit/update/bintrie/rawdb reuse boundaries | PR2, PR4, PR5 | Covered |
| 5 Module Boundaries | `core/ubtemit` producer and `cmd/ubtconv` consumer split | PR0, PR2, PR3 | Covered |
| 6 Data Contract | Diff/reorg event contracts, envelope versioning | PR0, PR1, PR6 | Covered |
| 7 Raw Storage Key Handling | Raw-key-safe canonical conversion path | PR2 | Covered |
| 8 Deletion Semantics + Slot Index Policy | Correct deletion semantics and scalable index policy | PR9 | Covered |
| 9 Emitter Write Policy | Durable append + degraded mode behavior | PR2 | Covered |
| 10 Replay and Recovery Boundary | Archive-aware replay contract and preflight checks | PR3, PR5 | Covered |
| 11 Reorg Handling and Cleanup | Marker-driven reorg handling, ancestor recovery, cleanup | PR6, PR7 | Covered |
| 12 Consumer Transaction Semantics | `pendingSeq/appliedSeq/appliedRoot` crash consistency | PR1, PR4 | Covered |
| 13 Validation Modes | Strict/deep validation and proof-readiness checks | PR10b, PR11 | Covered |
| 14 Commit Policy | Interval/latency commit, finalize-after-commit, and finalized-block query visibility | PR4 | Covered |
| 15 Bootstrap and Genesis Strategy | `tail/backfill-direct` with daemon no-outbox-write invariant | PR5 | Covered |
| 16 Config and Flags | Geth/daemon config split, archive requirements, and daemon trie-history window config | PR3, PR5, PR7 | Covered |
| 17 Observability and SLOs | Required metrics and operational thresholds | PR10b | Covered |
| 18 Upgrade and Migration Path | Shadow->validate->apply migration flow | PR8 | Covered |
| 19 Backpressure and Capacity Policy | Budget-aware lag handling and protection rules | PR7 | Covered |
| 20 File-Level Plan | Required file additions and modifications | PR0..PR14 | Covered |
| 21 Delivery Phases | Phase-aligned rollout through proof integration and Phase 7 staged RPC scope | PR sequencing + checkpoints | Covered |
| 22 Acceptance Criteria | End-to-end completion, proof-readiness, and staged UBT debug RPC criteria | PR10a, PR10b, PR11, PR12, PR13, PR14 closure | Covered |
| 23 Testing and Coverage Requirements | Mainnet-scale, chaos, equivalence, coverage gates, and RPC-stage tests | PR10a, PR10b, PR11, PR12, PR13, PR14 + all PR verification | Covered |
| 24 Import Cycle Safety | Directional import policy and cycle guard enforcement | PR0 + CI checks | Covered |
| 25 UBT Debug RPC Endpoint Plan | Proxy model, selector rules, UBT-native proof schema, staged delivery, history-window-bounded historical queries | PR12, PR13, PR14 | Covered |

## Test Implementation Coverage Matrix
| Test Suite IDs | Capability Verified | Roadmap PR Owner | Verification Method | Status |
|---|---|---|---|---|
| `TC-CONTRACT-*` | Event contract determinism and compatibility | PR0 | Contract suite in CI | Covered |
| `TC-BOUNDARY-*` | Import boundary and cycle-guard policy | PR0 | CI import graph checks | Covered |
| `TC-OUTBOX-*`, `TC-STATE-*` | Outbox durability and consumer-state safety | PR1 | Outbox/state suites | Covered |
| `TC-EMIT-*` | Emitter correctness and degraded non-blocking behavior | PR2 | Emitter unit/integration suites | Covered |
| `TC-DAEMON-*`, `TC-CONFIG-*` | Daemon runtime, config ownership split, and trie-history config validation | PR3 | Daemon control-plane suites | Covered |
| `TC-APPLY-*`, `TC-COMMIT-*` | Apply correctness, commit protocol safety, and finalized-only query visibility | PR4 | Applier/commit suites | Covered |
| `TC-BOOT-*` | Bootstrap modes and no daemon outbox writes | PR5 | Bootstrap/replay suites | Covered |
| `TC-REORG-*` | Reorg marker handling and ancestor recovery | PR6 | Reorg suites | Covered |
| `TC-RET-*`, `TC-BP-*` | Retention safety and backpressure policies | PR7 | Retention/backpressure suites | Covered |
| `TC-SNAP-*`, `TC-MIG-*` | Slow-path recovery and migration workflow | PR8 | Snapshot/migration suites | Covered |
| `TC-DEL-*` | Deletion and slot-index correctness/scalability | PR9 | Deletion/index suites | Covered |
| `TC-VAL-*`, `TC-METRIC-*` | Validation correctness and observability | PR10b | Validation/metrics suites | Covered |
| `TC-MAINNET-*` | Mainnet-scale throughput/disk profile behavior | PR10b | Benchmark/perf suites | Covered |
| `TC-CHAOS-*` | Crash/corruption/network-fault resilience | PR10b | Chaos/fault-injection suites | Covered |
| `TC-E2E-*` | MPT/UBT equivalence at sampled heights | PR10b | End-to-end equivalence suite | Covered |
| `TC-PROOF-PRE-*` | `BinaryTrie.Prove` prerequisite correctness before Phase 6 | PR10a | Proof primitive test suite | Covered |
| `TC-PROOF-*` | Witness/proof integration and root consistency | PR11 | Proof integration suites | Covered |
| `TC-RPC-READ-*` | Stage A debug RPC compatibility, selector semantics, and history-window behavior | PR12 | Debug RPC Stage A suite | Covered |
| `TC-RPC-PROOF-*` | Stage B UBT proof schema and verifier conformance | PR13 | Debug RPC Stage B suite | Covered |
| `TC-RPC-EXEC-*` | Stage C execution-class RPC correctness and gating | PR14 | Debug RPC Stage C suite | Covered (Phase 7 delivered; flag-gated) |

## Test Case Pass Verification Rules
1. Each PR must implement its mapped test cases before PR completion.
2. Each PR must include CI evidence of mapped suite pass results.
3. Merge is blocked if any mandatory mapped suite fails.
4. Coverage gates (80% touched, 90% critical new logic) are mandatory.
5. Final rollout is blocked until full mandatory matrix rerun passes reproducibly.
6. Phase 7 rollout evidence is recorded in `docs/testing/ubtconv_test_matrix.md`.
7. Coverage shortfall is allowed only with explicit exception record + compensating controls in `docs/testing/ubtconv_test_matrix.md`.

## Conflict Check
1. No roadmap PR assigns outbox writes to daemon.
2. Dedicated outbox backend is represented as single-process DB with RPC read transport for daemon.
3. Replay feasibility constraints and archive requirement are consistently represented.
4. Reorg marker requirement and `blockchain.reorg()` emission timing are consistent across plan and roadmap.
5. Testing obligations include mainnet-scale, chaos, equivalence, and proof prerequisites.
6. UBT debug RPC architecture remains proxy-only (no geth direct UBT DB open).
7. `debug_getUBTProof` is consistently modeled as UBT-native schema, not `AccountResult`.
8. Stage C execution-class RPCs are consistently gated to Phase 7.
9. Historical UBT debug queries are consistently bounded by daemon trie-history retention with explicit pruned-history errors.

## Gaps Found
1. No blocking architecture or test-ownership gap found.
2. Test pass ledger is maintained in `docs/testing/ubtconv_test_matrix.md`.
3. Coverage shortfall handling is explicitly documented with compensating controls in `docs/testing/ubtconv_test_matrix.md`.

## Validation Result
Roadmap coverage is complete for `plan_final.md` Sections 1-25, and all critical architecture, staged UBT debug RPC delivery, and testing obligations are represented with explicit implementation and verification ownership.
