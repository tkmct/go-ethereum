# MPT -> UBT Conversion Final Plan (Hardened v3)

## 1. Objective
Implement a production-grade MPT-to-UBT conversion system that:
1. Processes from genesis, block-by-block.
2. Uses canonical MPT execution updates as source of truth.
3. Applies equivalent updates to UBT asynchronously.
4. Keeps geth block import non-blocking under converter failure.
5. Supports restart recovery, reorg rollback/replay, and deterministic convergence.
6. Establishes a path to witness/proof integration after conversion hardening.

## 2. Architecture Options
### Option A: Durable Outbox + External `ubtconv` Daemon (Selected)
1. Geth process:
   1. Emits canonical conversion events into durable outbox.
   2. Does not run UBT trie apply loop.
2. External `ubtconv` daemon:
   1. Consumes outbox strictly by sequence via geth outbox-read RPC.
   2. Applies events to UBT trie DB.
   3. Handles checkpointing, replay, reorg recovery, and validation.
3. Optional queue relay:
   1. May accelerate consumption.
   2. Never becomes source of truth.

### Option B: RPC Stream + External Daemon
1. Geth exposes streaming/range RPC for conversion diffs.
2. Daemon persists and consumes its own queue.

### Option C: In-Process Adapter Only
1. Conversion and apply stay inside geth runtime.

Decision:
1. Use Option A.

## 3. Core Principles
1. Single-writer ownership:
   1. Geth is the only outbox writer.
   2. Daemon is the only UBT/checkpoint writer.
2. Outbox is authoritative; queue is optional transport.
3. Deterministic encoding and replay semantics are mandatory.
4. Conversion failures must not block canonical block import.
5. Reorg correctness has priority over throughput.
6. Recovery must provide both fast-path and slow-path fallback.
7. Deep replay must be feasibility-safe (archive replay requirement).
8. Cross-package boundaries must avoid exporting mutable internal state types.
9. Outbox DB file access must remain single-process safe (no multi-process direct-open).

## 4. Existing Code to Reuse
1. `core/blockchain.go`: `StateDB.CommitWithUpdate` integration point.
2. `core/state/stateupdate.go`: internal update model and raw-key aware state origins.
3. `core/tracing/hooks.go`: exported `tracing.StateUpdate` model.
4. `trie/bintrie/trie.go`: `UpdateAccount`, `UpdateStorage`, `UpdateContractCode`, `Commit`.
5. `trienode.NewWithNodeSet(...)` and `triedb.Update(...)` commit path.
6. `core/rawdb.ReadCode(...)` for code-length fallback.
7. `types.EmptyBinaryHash` for empty UBT root.

## 5. Module Boundaries
### 5.1 Geth-side module (`core/ubtemit`)
Responsibilities:
1. Hook block write path where `CommitWithUpdate` is available.
2. Accept already-converted outbox events from `core/blockchain.go` and append durably.
3. Persist events to outbox in strict sequence.
4. Emit health and metrics.
5. Emit reorg markers from canonical reorg hook path (`blockchain.reorg()`).

Non-responsibilities:
1. No UBT trie mutation.
2. No long-running replay/recovery loops.

### 5.2 External daemon (`cmd/ubtconv`)
Responsibilities:
1. Read outbox by sequence through geth outbox-read RPC transport.
2. Apply events to UBT trie and commit roots.
3. Maintain durable consumer state (`pendingSeq`, `appliedSeq`, `appliedRoot`).
4. Handle reorg rewind/replay and snapshot fallback.
5. Validate and expose operational health.

Non-responsibilities:
1. No outbox write or rewrite.
2. No ownership of canonical block processing.

## 6. Data Contract
### 6.1 Internal update conversion boundary
1. `core/state.stateUpdate` remains unexported.
2. Conversion from internal update to `QueuedDiffV1` is executed in `core/blockchain.go`.
3. Emitter API is payload-oriented and state-type-agnostic:
   1. `core/ubtemit.Service.EmitDiff(diff *QueuedDiffV1)`.
   2. `core/ubtemit.Service.EmitReorg(marker *ReorgMarkerV1)`.
4. `core/ubtemit` must not import internal `core/state` update types.

### 6.2 Durable outbox envelope
1. Common fields:
   1. `Seq`, `Version`, `Kind`, `BlockNumber`, `BlockHash`, `ParentHash`, `Timestamp`.
2. Event kinds:
   1. `diff` -> `QueuedDiffV1`.
   2. `reorg` -> `ReorgMarkerV1`.

### 6.3 `QueuedDiffV1` (kind=`diff`)
1. `OriginRoot`, `Root`.
2. `Accounts []AccountEntry` sorted by address bytes.
3. `Storage []StorageEntry` sorted by `(address, slotKeyRaw)`.
4. `Codes []CodeEntry` sorted by address bytes.

Encoding rules:
1. RLP v1 over sorted slices.
2. Never persist map structures directly.
3. Unknown major version must be rejected by consumers.

### 6.4 `ReorgMarkerV1` (kind=`reorg`)
1. `FromBlockNumber`, `FromBlockHash`.
2. `ToBlockNumber`, `ToBlockHash`.
3. `CommonAncestorNumber`, `CommonAncestorHash` (when known by emitter).

### 6.5 Outbox storage backend
1. Dedicated DB backend (default and baseline requirement): `<datadir>/ubt-outbox`.
2. Rationale:
   1. Avoid chainDB lock contention and compaction coupling.
   2. Isolate lifecycle and retention operations.
3. Append durability:
   1. Write batch per event.
   2. Durability mode configured with explicit fsync policy.
4. Access model:
   1. Geth is the only process that opens the outbox DB files.
   2. Daemon consumption uses RPC streaming/range APIs from geth.
   3. Retention prune/compaction is executed by geth-side compactor only.

## 7. Raw Storage Key Handling (Critical)
1. Current `stateUpdate` internally tracks origin slots by raw key and by hashed key.
2. Canonical diff conversion executes in `core/blockchain.go` on the `CommitWithUpdate` return value.
3. If raw key is unexpectedly unavailable, treat as invariant violation:
   1. Mark emitter degraded.
   2. Emit critical metric.
   3. Keep canonical import path unblocked.

## 8. Deletion Semantics + Slot Index Policy
1. Do not rely on `BinaryTrie.DeleteAccount` (current no-op).
2. Deletion helper:
   1. `UpdateAccount(addr, zeroAccount, 0)`.
   2. Iterate indexed slots and zero via `UpdateStorage`.
   3. Clear index entries.
3. `LegacySlotIndexMode`:
   1. `auto` default.
   2. `on` always maintain.
   3. `off` only when chain rules permit.
4. Cancun boundary in `auto`:
   1. Maintain index through last pre-Cancun block.
   2. Freeze at first Cancun block.
   3. Prune only after replay window no longer overlaps pre-Cancun range.
5. Scalability requirement:
   1. Track expected entry count, bytes per entry, and total disk budget.
   2. Enforce alert threshold and budget guard.

## 9. Emitter Write Policy
1. Immediate durable append to dedicated outbox DB.
2. On append failure:
   1. Mark degraded.
   2. Persist failure checkpoint.
   3. Continue canonical import.
3. Emission SLOs:
   1. Track append latency and failure rates.

## 10. Replay and Recovery Boundary
```go
type BlockReplayer interface {
    CurrentBlock() *types.Header
    GetBlockByNumber(number uint64) *types.Block
    GetCanonicalHash(number uint64) common.Hash
    ReplayBlock(block *types.Block) (*QueuedDiffV1, error)
}
```

Feasibility constraints:
1. Deep replay requires archive-capable RPC.
2. Daemon must fail fast if requested replay depth exceeds local history and archive replay is unavailable.
3. Local path-history window is an optimization, not a guarantee for deep lag.

## 11. Reorg Handling, Recovery, and Cleanup
Reorg signaling:
1. Emitter writes explicit `ReorgMarkerV1` events from `core/blockchain.reorg()` hook.
2. Marker emission must happen before first diff event of the new canonical branch is published.

Detection sources:
1. Reorg marker events.
2. Parent-hash mismatch.
3. Canonical hash mismatch at block number.

Common ancestor discovery:
1. Use canonical RPC (`GetCanonicalHash`) plus local applied block->ubtRoot mapping.
2. Walk back from divergent heads until hashes match.

Recovery:
1. Stop apply loop.
2. Resolve ancestor.
3. Restore ancestor UBT root checkpoint.
4. Reset consumer checkpoint to ancestor sequence.
5. Replay canonical blocks and resume tailing.

Retention invariants:
1. `OutboxRetentionSeqWindow > MaxRecoverableReorgDepth + safetyMargin`.
2. Keep block->ubtRoot mapping for at least the same effective window.

Slow-path fallback:
1. Restore latest anchor snapshot.
2. Replay `snapshot.blockNumber + 1` to head.
3. If no valid snapshot, replay from block 0.

Cleanup:
1. Prune outbox events older than retention behind applied sequence (geth-side compactor).
2. Prune stale block->ubtRoot entries outside recovery window.
3. Prune snapshots by configured retention.

## 12. Consumer Transaction Semantics (Crash Consistency)
Durable consumer state:
1. `pendingSeq`.
2. `appliedSeq`.
3. `appliedRoot`.

Per-seq protocol:
1. Persist `pendingSeq = N`.
2. Apply event `N` and commit trie DB changes.
3. Finalize `appliedSeq = N`, `appliedRoot = rootN`, clear `pendingSeq`.

Correctness requirements:
1. Apply operations must be idempotent for replay safety.
2. Startup must validate root consistency for last finalized seq.
3. On inconsistency, rewind to last known-good anchor and replay.

## 13. Validation Modes
1. Strict mode (default): validate touched-key post-state per applied event.
2. Deep mode (sampled): cross-check MPT and UBT for account/storage/code equivalence.
3. Proof-readiness mode (Phase 6): validate witness/proof inputs against committed UBT roots.

## 14. Commit Policy (Daemon)
1. Commit when either:
   1. `uncommittedBlocks >= ApplyCommitInterval`, or
   2. `timeSinceLastCommit >= ApplyCommitMaxLatency`.
2. Finalize `appliedSeq` only after commit durability succeeds.
3. Query visibility rules:
   1. Only finalized (`appliedSeq`) blocks are queryable by UBT debug RPC.
   2. Daemon must persist `blockNumber/blockHash -> postStateRoot` mapping for every finalized applied block.
   3. Requests for in-flight (not yet finalized) applied blocks return `state not yet available`.
4. Defaults:
   1. `ApplyCommitInterval = 128`.
   2. `ApplyCommitMaxLatency = 10s`.

## 15. Bootstrap and Genesis Strategy
1. Daemon never writes outbox events.
2. Daemon never opens outbox DB files directly.
3. Modes:
   1. `tail`: consume outbox via RPC from `appliedSeq + 1`.
   2. `backfill-direct`: replay canonical blocks directly into UBT/checkpoints when historical outbox range is absent.
4. Genesis:
   1. Fresh startup may begin in `backfill-direct` from block 0, then cut over to `tail`.
   2. Initialized DB resumes in `tail`.

## 16. Config and Flags
### 16.1 Geth config
1. `UBTConversionEnabled bool`
2. `UBTConfig`:
   1. `DecoupledMode bool`.
   2. `OutboxDBPath string`.
   3. `OutboxRetentionSeqWindow uint64`.
   4. `OutboxWriteTimeout time.Duration`.
   5. `ReorgMarkerEnabled bool` (default true).
   6. `ReplayRPCEnabled bool`.
   7. `ReplayRPCEndpoint string`.
   8. `OutboxReadRPCEnabled bool` (default true).
   9. `UBTDebugRPCProxyEnabled bool` (default false).
   10. `UBTDebugRPCEndpoint string` (ubtconv query API endpoint).
   11. `UBTDebugRPCTimeout time.Duration`.

### 16.2 Daemon config (`ubtconv`)
1. `ApplyCommitInterval uint64`.
2. `ApplyCommitMaxLatency time.Duration`.
3. `ValidationEnabled bool`.
4. `ValidationSampleRate float64`.
5. `LegacySlotIndexMode string` (`auto|on|off`).
6. `MaxRecoverableReorgDepth uint64`.
7. `BootstrapMode string` (`tail|backfill-direct`).
8. `AnchorSnapshotInterval uint64`.
9. `AnchorSnapshotRetention uint64`.
10. `RequireArchiveReplay bool` (default true).
11. `OutboxDiskBudgetBytes uint64`.
12. `OutboxAlertThresholdPct uint64`.
13. `OutboxRPCEndpoint string`.
14. `UBTQueryRPCEnabled bool` (default true).
15. `UBTQueryRPCListenAddr string`.
16. `UBTQueryRPCMaxBatch uint64`.
17. `UBTTrieDBScheme string` (`path`) (default `path` for historical query support).
18. `UBTTrieDBStateHistory uint64` (default `90000` blocks).

## 17. Observability and SLOs
Required metrics:
1. `ubt_emitter_outbox_append_latency`.
2. `ubt_emitter_degraded_total`.
3. `ubt_daemon_applied_seq`.
4. `ubt_daemon_head_seq`.
5. `ubt_daemon_reorg_recovery_total`.
6. `ubt_daemon_commit_duration`.
7. `ubt_daemon_replay_blocks_per_sec`.
8. `ubt_daemon_snapshot_restore_total`.
9. `ubt_outbox_disk_usage_bytes`.
10. `ubt_debug_rpc_proxy_latency` (method-labeled).
11. `ubt_debug_rpc_proxy_errors_total` (method/reason-labeled).
12. `ubt_debug_rpc_proxy_inflight`.

Operational SLO baselines:
1. Bounded lag under normal sync.
2. Recovery time objective for fast-path and slow-path.
3. Alerting on degraded emitter and disk budget breaches.
4. UBT debug RPC baseline latency SLO:
   1. `debug_getUBTBalance`, `debug_getUBTStorageAt`, `debug_getUBTCode` p99 < 500ms under normal load.

## 18. Upgrade and Migration Path
1. Support migration from existing sidecar deployment to outbox architecture.
2. Migration stages:
   1. Deploy emitter in shadow mode.
   2. Start daemon in read/validate mode.
   3. Switch daemon to apply mode once equivalence checks pass.
3. Rollback path:
   1. Stop daemon apply.
   2. Preserve outbox and checkpoints.
   3. Resume from last finalized sequence.

## 19. Backpressure and Capacity Policy
1. Outbox growth control:
   1. Retention pruning.
   2. Disk budget alerts.
   3. High-watermark protections.
2. If consumption lags persistently:
   1. Enter catch-up mode.
   2. Increase commit aggressiveness.
   3. Emit critical alerts if budget risk persists.
3. If outbox DB pressure grows, geth compactor and daemon catch-up mode coordinate via RPC lag signals.
4. RPC transport backpressure must expose:
   1. Queue depth / lag metrics.
   2. Rate-limit signals.
   3. Retry and resume semantics.

## 20. File-Level Plan
### 20.1 New geth-side package: `core/ubtemit/`
1. `types.go`
2. `service.go`
3. `outbox_store.go`
4. `encoder.go`
5. `reorg_marker.go`
6. `metrics.go`

### 20.2 New external daemon: `cmd/ubtconv/`
1. `main.go`
2. `runner.go`
3. `outbox_reader.go`
4. `consumer.go`
5. `applier.go`
6. `reorg_marker_handler.go`
7. `ancestor_finder.go`
8. `recovery.go`
9. `snapshot_manager.go`
10. `backpressure.go`
11. `validate.go`
12. `rpc_replay_client.go`
13. `metrics.go`
14. `rpc_query_api.go`
15. `rpc_query_server.go`

### 20.3 Modified files
1. `core/blockchain.go`
   1. Add `hasEmitter` in commit-path condition so emitter-enabled flow always uses `CommitWithUpdate`.
   2. Convert returned update to `QueuedDiffV1` in `core/blockchain.go`, then call emitter API with converted payload.
   3. Hook reorg path to emit `ReorgMarkerV1`.
2. `core/state/*` (adapter boundary for canonical diff conversion)
3. `core/rawdb/schema.go`
4. `core/rawdb/accessors_ubt_outbox.go` (new)
5. `eth/ethconfig/*`, `cmd/utils/flags.go`, `cmd/geth/main.go`
6. Outbox read RPC handlers (required for daemon transport)
7. Optional replay RPC handlers
8. `internal/ethapi/backend.go` (UBT debug proxy backend interface additions)
9. `eth/api_backend.go` (backend implementation for UBT debug proxy client)
10. `internal/ethapi/api.go` (debug namespace UBT RPC handlers)

## 21. Delivery Phases
1. Phase 1: outbox contracts, dedicated backend, emitter baseline, contract tests passing.
2. Phase 2: daemon apply + crash-consistency protocol, applier tests passing.
3. Phase 3: reorg markers + fast-path recovery + retention compaction, recovery tests passing.
4. Phase 4: snapshots + slow-path fallback + migration workflow, deep recovery tests passing.
5. Phase 5: strict/deep validation + observability + perf tuning, stress/perf suites passing, and `BinaryTrie.Prove()` implementation completed.
6. Phase 6: witness/proof integration and proof-readiness validation.
7. Phase 7 (future work): UBT-backed execution adapter and execution-class RPCs (`debug_callUBT`, `debug_executionWitnessUBT`).

## 22. Acceptance Criteria
1. Canonical block import remains unaffected by converter lag/failure.
2. Outbox is durable, ordered, replayable, and single-writer owned by geth.
3. Daemon never appends outbox events.
4. Crash/restart preserves seq correctness (`pendingSeq/appliedSeq`).
5. Reorg rewind/replay converges within fast-path window.
6. Slow-path snapshot restore + replay converges when fast-path window is exceeded.
7. Archive replay requirement is enforced for deep recovery.
8. Backpressure policy prevents unbounded operational failure under sustained lag.
9. Strict and deep validation pass on target dev/test chains.
10. Phase 6 witness/proof interface path is implemented and validated.
11. Mandatory test matrix passes in CI before merge.
12. `BinaryTrie.Prove()` is implemented (no panic path) with passing proof correctness tests before Phase 6 wiring begins.
13. UBT debug RPC proxy endpoints return schema-compatible results for `getStorageAt`, `getBalance`, and `getCode`.
14. `debug_getUBTProof` returns a documented UBT-native proof schema and passes verifier conformance tests.
15. Block selector resolution rules for UBT debug RPCs are deterministic and tested (`latest` by daemon applied head, unsupported tags return explicit errors).
16. `debug_callUBT` and `debug_executionWitnessUBT` are explicitly tracked as Phase 7 scope and are not required for Phase 1-6 completion.
17. Historical UBT debug RPC queries are bounded by daemon `UBTTrieDBStateHistory`, and out-of-window queries return explicit `state not available`.

## 23. Testing Strategy and Coverage Requirements
Test layers:
1. Contract/serialization unit tests.
2. Emitter integration tests.
3. Consumer/applier crash-consistency tests.
4. Replay/reorg/retention/snapshot tests.
5. Semantic correctness tests (raw keys, deletions, slot-index policy).
6. Validation and observability tests.
7. Mainnet-scale and performance tests.
8. Chaos/fault-injection tests.
9. End-to-end MPT/UBT equivalence tests.

Mandatory test scenarios:
1. Mainnet-scale benchmarks:
   1. Large account/slot datasets.
   2. Peak block record processing.
   3. Outbox disk-usage profile over retention windows.
2. Chaos:
   1. Daemon crash at each protocol step.
   2. Outbox corruption detection and recovery.
   3. RPC partition and resume behavior.
3. Equivalence:
   1. At selected heights compare account/storage/code values from MPT and UBT bit-for-bit.
4. Proof prerequisites:
   1. `BinaryTrie.Prove()` proof generation correctness tests.
   2. Absence proof and inclusion proof conformance tests.
5. UBT debug RPC compatibility:
   1. `debug_getUBTStorageAt` response compatibility with `eth_getStorageAt`.
   2. `debug_getUBTBalance` response compatibility with `eth_getBalance`.
   3. `debug_getUBTCode` response compatibility with `eth_getCode`.
   4. `debug_getUBTProof` UBT-native schema and verifier compatibility tests.
   5. `debug_callUBT` and `debug_executionWitnessUBT` gating tests (disabled/unavailable before Phase 7 readiness).
   6. state-history window tests for within-window success and out-of-window explicit errors.

Coverage targets:
1. Minimum 80% statement coverage for touched feature packages.
2. Minimum 90% statement coverage for critical new logic in emitter/outbox/applier/recovery/reorg/validate/snapshot.
3. Every bug fix must include a regression test that fails before fix and passes after fix.

Verification gates:
1. Each PR must include implemented test cases and pass evidence.
2. CI merge gate requires all mandatory suites passing.
3. Final rollout gate requires full matrix rerun with reproducible results.

## 24. Import Cycle Safety
1. `core/ubtemit -> core/state` direct type coupling is prohibited.
2. `core/ubtemit -> core/blockchain` imports are prohibited.
3. `cmd/ubtconv -> trie/bintrie` and `cmd/ubtconv -> core/rawdb` are allowed.
4. `core/blockchain.go` is the orchestration boundary between `core/state` update conversion and `core/ubtemit` payload emission.
5. CI must enforce import graph checks to prevent reverse-cycle regressions.

## 25. UBT Debug RPC Endpoint Plan
Goal:
1. Add UBT-backed debug RPC endpoints that mirror existing MPT RPC signatures as closely as possible.
2. Keep geth as RPC ingress only; data is returned through proxy calls to `ubtconv` query API.
3. Separate read-class RPCs (current scope) from execution-class RPCs (future scope).

Transport and ownership:
1. Geth debug RPC handlers do not open UBT DB directly.
2. Geth proxies UBT queries to `ubtconv` (`UBTDebugRPCEndpoint`) and normalizes results.
3. `ubtconv` query API is read-only and serves data from UBT/checkpointed state.

Planned RPC methods (debug namespace):
1. `debug_getUBTStorageAt(address, hexKey, blockNumberOrHash) -> DATA32`
   1. Signature mirrors `eth_getStorageAt`.
2. `debug_getUBTBalance(address, blockNumberOrHash) -> QUANTITY`
   1. Signature mirrors `eth_getBalance`.
3. `debug_getUBTCode(address, blockNumberOrHash) -> DATA`
   1. Signature mirrors `eth_getCode`.
4. `debug_getUBTProof(address, storageKeys[], blockNumberOrHash) -> UBTProofResult`
   1. Input signature mirrors `eth_getProof` as far as possible.
   2. Output is UBT-native (not `AccountResult` compatible), because MPT and UBT proof encodings and storage-root model differ.
   3. Enabled after `BinaryTrie.Prove()` prerequisite is complete.
5. `debug_executionWitnessUBT(blockNumberOrHash) -> ExtWitness-compatible object` (Phase 7)
   1. Requires UBT-backed execution adapter and EVM re-execution on UBT state.
6. `debug_callUBT(callObject, blockNumberOrHash, stateOverrides?, blockOverrides?) -> DATA` (Phase 7)
   1. Input/return shape mirrors `eth_call`.
   2. Requires UBT-backed execution adapter.

`UBTProofResult` schema (initial):
1. `address`, `balance`, `nonce`, `codeHash`.
2. `stateRoot` (UBT root used for proof).
3. `accountProof` (UBT-native path/sibling encoding).
4. `storageProof[]` entries each containing:
   1. `key`, `value`.
   2. `proof` (UBT-native encoding).
5. No MPT-style `storageHash` field requirement.

Compatibility requirements:
1. Block selector resolution is daemon-applied-state based:
   1. `latest` resolves to daemon `appliedSeq` head block.
   2. `pending`, `safe`, `finalized` return explicit unsupported-tag errors in initial implementation.
   3. Requested block newer than daemon-applied head returns explicit `state not yet available`.
   4. Historical block queries are supported only within daemon `UBTTrieDBStateHistory` window from applied head.
   5. Requests older than retained window return explicit `state not available` (history pruned).
2. Block/hash canonicality checks are performed daemon-side against consumed canonical lineage.
3. Error model follows existing RPC conventions:
   1. Invalid params.
   2. State unavailable.
   3. Feature disabled/prerequisite unmet.
4. Read-class result schema remains JSON-compatible with existing scalar RPC consumers.
5. Proof result is UBT-native and requires UBT-aware verifier.

Delivery staging:
1. Stage A:
   1. `debug_getUBTStorageAt`, `debug_getUBTBalance`, `debug_getUBTCode`.
2. Stage B:
   1. `debug_getUBTProof` after `BinaryTrie.Prove()` implementation.
3. Stage C:
   1. `debug_callUBT`, `debug_executionWitnessUBT` after UBT execution adapter validation (Phase 7).

Phase dependency mapping:
1. Stage A starts after Phase 2 (daemon apply baseline available).
2. Stage B starts after Phase 5 (`BinaryTrie.Prove()` complete).
3. Stage C starts after Phase 6 and is delivered in Phase 7.

Test requirements for this section:
1. Golden tests comparing UBT debug RPC outputs against MPT outputs on the same canonical blocks.
2. Proxy failure tests:
   1. `ubtconv` unavailable.
   2. timeout.
   3. partial batch failure.
3. Prerequisite gating tests:
   1. `debug_getUBTProof` unavailable before prove implementation.
   2. `debug_callUBT` and `debug_executionWitnessUBT` unavailable before execution adapter readiness.
4. Selector-resolution tests:
   1. `latest` maps to daemon-applied head.
   2. unsupported tags return expected errors.
   3. ahead-of-applied-head requests return `state not yet available`.
5. History-window tests:
   1. queries within retained `UBTTrieDBStateHistory` succeed.
   2. queries older than retained window return `state not available`.
   3. in-flight (not yet finalized) blocks are not exposed.
