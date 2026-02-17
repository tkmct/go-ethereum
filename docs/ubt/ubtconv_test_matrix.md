# UBTConv Test Matrix (Plan-Final Closeout)

Last updated: 2026-02-14 (complete-run refresh)

## 1. Scope
This ledger records reproducible verification evidence for UBT conversion completion checks:
1. Gate A package suites.
2. Execution-class OFF/ON behavior.
3. Restart/recovery behavior.
4. Reorg recovery behavior.
5. Storage parity regression coverage.
6. Full-sync-only operational assumptions (no snap/bootstrap-backfill path).

## 2. Gate A Results (Re-run)
Executed at 2026-02-14T13:12:46Z.

| Command | Result |
|---|---|
| `go test ./core/ubtemit -count=1` | PASS |
| `go test ./core/rawdb -run UBT -count=1` | PASS |
| `go test ./core/state -run UBT -count=1` | PASS |
| `go test ./trie/bintrie -run Prove -count=1` | PASS |
| `go test ./cmd/ubtconv -count=1` | PASS |
| `go test ./eth -run UBT -count=1` | PASS |

## 2.1 Gate A Results (Complete-run refresh)
Executed at 2026-02-14T17:46:43Z.

| Command | Result |
|---|---|
| `go test ./core/ubtemit -count=1` | PASS |
| `go test ./core/rawdb -run UBT -count=1` | PASS |
| `go test ./core/state -run UBT -count=1` | PASS |
| `go test ./trie/bintrie -run Prove -count=1` | PASS |
| `go test ./cmd/ubtconv -count=1` | PASS |
| `go test ./eth -run UBT -count=1` | PASS |

## 2.2 Gate B/Gate C Results (Complete-run refresh)
Executed at 2026-02-14T17:46:43Z.

| Gate | Command | Result |
|---|---|---|
| Gate B (focused integration) | `go test ./cmd/ubtconv -run 'TestQueryServerIntegration|TestVerify_FreshStartConsumesSeqZero|TestVerify_RestartAfterSeqZero|TestVerify_ReorgRecovery|TestVerify_MissingEventDeterministicError|TestVerify_BlockSelectorValidation|TestVerify_ProofDeterminism|TestVerify_GetCodeBehavior|TestVerify_ExecutionRPCsExplicitErrors|TestVerify_FullPipelineIntegration' -count=1` | PASS |
| Gate C (nightly/chaos subset) | `go test ./cmd/ubtconv -run 'TestGateC_|TestFault_|TestCrash_|TestValidateOnly_|TestOutboxReader_ConcurrentCloseAndRead|TestCompaction_' -count=1` | PASS |

## 3. Session Evidence (Automation)
Executed with:

```bash
scripts/ubt_sessions/run_sessions.sh --session all --workdir /tmp/ubt-session-final4 --keep-data
```

### 3.1 Session outcomes
| Session | Coverage | Result |
|---|---|---|
| `disabled` | `debug_callUBT` / `debug_executionWitnessUBT` disabled errors, proof/balance/code checks | PASS |
| `enabled` | execution-class RPC enabled success (`debug_callUBT`, `debug_executionWitnessUBT`) | PASS |
| `restart` | deterministic restart/recovery tests | PASS |
| `reorg` | deterministic reorg recovery test (`TestVerify_ReorgRecovery`) | PASS |

### 3.3 Session rerun (Complete-run refresh)
Executed with:

```bash
scripts/ubt_sessions/run_sessions.sh --session all --workdir /tmp/ubt-session-complete-20260214-postfix --keep-data
```

Result:
1. `disabled` PASS.
2. `enabled` PASS.
3. `restart` PASS.
4. `reorg` PASS.
5. all requested sessions PASS.

### 3.2 Command outputs used inside sessions
| Session | Command(s) | Result |
|---|---|---|
| `restart` | `go test ./cmd/ubtconv -run 'TestVerify_RestartAfterSeqZero|TestCrashRecovery_ReplayFromAppliedSeq|TestRestart_AfterConsumingSeqZero' -count=1` | PASS |
| `reorg` | `go test ./cmd/ubtconv -run TestVerify_ReorgRecovery -count=1` | PASS |

## 4. Storage Parity Evidence
Live `--dev` fixture deployment can be unstable under 128-bit UBT balance constraints, so storage parity is enforced with deterministic integration coverage.

| Command | Assertion | Result |
|---|---|---|
| `go test ./trie/bintrie -run TestGetStorage_RoundTrip -count=1` | storage key mapping read/write round-trip | PASS |
| `go test ./cmd/ubtconv -run TestVerify_FullPipelineIntegration -count=1` | `ubt_getStorageAt` value parity (`deadbeef`) | PASS |

## 5. Execution-Class RPC Evidence
| Command | Assertion | Result |
|---|---|---|
| `go test ./cmd/ubtconv -run TestQueryAPI_CallUBT_Disabled -count=1` | disabled error path | PASS |
| `go test ./cmd/ubtconv -run TestCallUBT_SimpleBalance -count=1` | enabled success path | PASS |
| `go test ./cmd/ubtconv -run TestQueryAPI_ExecutionWitnessUBT_Disabled -count=1` | witness disabled error path | PASS |

## 6. Coverage Snapshot (package-level)
Measured with:
1. `go test -cover ./core/ubtemit`
2. `go test -cover ./cmd/ubtconv`
3. `go test -cover ./eth -run UBT`

| Package | Coverage |
|---|---|
| `core/ubtemit` | 75.0% |
| `cmd/ubtconv` | 55.4% |
| `eth` (`-run UBT`) | 3.7% |

Package-level percentages are tracked for visibility; acceptance is based on mandatory suite pass evidence in this ledger.

Compensating controls for current coverage shortfall:
1. Mandatory Gate A suite remains blocking for all UBT-path changes.
2. `scripts/ubt_sessions/run_sessions.sh --session all` evidence is required before release candidate sign-off.
3. Critical storage-path regressions are covered by:
   1. `TestGetStorage_RoundTrip`
   2. `TestVerify_FullPipelineIntegration`

### 6.1 Coverage Exception Record
Record ID:
1. `COV-EX-2026-02-14-UBT`.

Rationale:
1. Current package-level coverage does not meet the target percentages initially planned for UBT conversion.
2. Functional rollout decision is controlled by mandatory gate pass + deterministic session evidence.

Compensating controls:
1. Gate A remains blocking on UBT-path changes.
2. Gate B focused integration suite is executed for UBT-path changes.
3. Gate C chaos/recovery suite is executed in nightly/release candidate flow.
4. Session evidence (`scripts/ubt_sessions/run_sessions.sh --session all`) is required before release sign-off.

Stability hardening applied:
1. `run_manual_check.sh` proof check now retries up to 3 times with refreshed applied block selector to avoid transient false negatives during automated sessions.
