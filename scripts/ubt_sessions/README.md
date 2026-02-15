# UBT Session Runner

This directory contains automation scripts for end-to-end session execution without manual process startup.

## Script
- `run_sessions.sh`: orchestrates `geth` + `ubtconv` lifecycle and validation sessions.

## Sessions
1. `disabled`
- Starts `geth` and `ubtconv` (execution RPC disabled).
- Runs `run_manual_check.sh --execution-disabled`.

2. `enabled`
- Starts `geth` and `ubtconv --execution-class-rpc-enabled`.
- Runs `run_manual_check.sh --execution-enabled`.

3. `restart`
- Runs deterministic restart/recovery tests:
  - `TestVerify_RestartAfterSeqZero`
  - `TestCrashRecovery_ReplayFromAppliedSeq`
  - `TestRestart_AfterConsumingSeqZero`

4. `reorg`
- Runs deterministic integration test:
  - `go test ./cmd/ubtconv -run TestVerify_ReorgRecovery -count=1`
- This avoids flaky multi-node live reorg orchestration.

Storage equivalence:
- `run_sessions.sh` defaults to stable `--dev` execution without storage fixture deployment.
- Deterministic storage parity is validated via:
  - `go test ./cmd/ubtconv -run TestVerify_FullPipelineIntegration -count=1`
- Optional live fixture deployment can be enabled with:
  - `ENABLE_STORAGE_FIXTURE=1 scripts/ubt_sessions/run_sessions.sh --session disabled`
  - Note: live fixture mode may fail on environments where 128-bit UBT balance constraints conflict with dev prefunded accounts.

## Usage
```bash
# Run all sessions
scripts/ubt_sessions/run_sessions.sh --session all

# Run only execution-enabled session
scripts/ubt_sessions/run_sessions.sh --session enabled

# Keep logs/data for debugging
scripts/ubt_sessions/run_sessions.sh --session restart --keep-data
```

## Options
- `--session`: `disabled | enabled | restart | reorg | all`
- `--workdir DIR`: custom working directory root
- `--skip-build`: skip `go build` for `geth`/`ubtconv`
- `--keep-data`: do not delete workdir on exit

## Environment Variables
- `TEST_CHECK_ADDRESS`: check address used by `run_manual_check.sh` and `--miner.etherbase` (default: `0x000000000000000000000000000000000000c0de`)
- `ENABLE_STORAGE_FIXTURE`: set to `1` to deploy a live storage fixture contract and enable live storage parity check in session runner
