#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
GETH_BIN="${ROOT_DIR}/build/bin/geth"
UBTCONV_BIN="${ROOT_DIR}/build/bin/ubtconv"
MANUAL_CHECK_BIN="${ROOT_DIR}/run_manual_check.sh"
GO_CACHE_DIR="${GO_CACHE_DIR:-/tmp/go-build}"
GO_MOD_CACHE_DIR="${GO_MOD_CACHE_DIR:-/tmp/go-mod}"

SESSION="all"
WORKDIR="/tmp/ubt-session-$(date +%Y%m%d-%H%M%S)"
SKIP_BUILD=0
KEEP_DATA=0
TEST_CHECK_ADDRESS="${TEST_CHECK_ADDRESS:-0x000000000000000000000000000000000000c0de}"
SESSION_STORAGE_ADDRESS=""
SESSION_STORAGE_SLOT="0x0000000000000000000000000000000000000000000000000000000000000000"
SESSION_STORAGE_BLOCK_DEC=0
ENABLE_STORAGE_FIXTURE="${ENABLE_STORAGE_FIXTURE:-0}"

GETH_HTTP_PORT=18545
UBT_HTTP_PORT=18560
GETH_P2P_PORT=30345
GETH_AUTHRPC_PORT=18551

GETH_PID=""
UBT_PID=""
CURRENT_SESSION=""
SESSION_CHECK_ADDRESS="${TEST_CHECK_ADDRESS}"

usage() {
  cat <<'USAGE'
Usage:
  scripts/ubt_sessions/run_sessions.sh [options]

Options:
  --session NAME      disabled | enabled | restart | reorg | all (default: all)
  --workdir DIR       working directory root (default: /tmp/ubt-session-<timestamp>)
  --skip-build        skip binary build step
  --keep-data         do not remove working directory on success
  -h, --help          show this help

Notes:
  - This script starts/stops geth and ubtconv automatically.
  - restart session uses deterministic restart/recovery tests.
  - reorg session uses deterministic Go integration test (TestVerify_ReorgRecovery).
USAGE
}

log() {
  printf '[INFO] %s\n' "$*"
}

pass() {
  printf '[PASS] %s\n' "$*"
}

fail() {
  printf '[FAIL] %s\n' "$*" >&2
  exit 1
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || fail "missing required command: $1"
}

hex_to_dec() {
  local h="${1,,}"
  h="${h#0x}"
  [[ -n "${h}" ]] || h="0"
  printf '%d' "$((16#${h}))"
}

rpc_call() {
  local endpoint="$1"
  local method="$2"
  local params="$3"
  curl -sS -H 'Content-Type: application/json' \
    --data "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"${method}\",\"params\":${params}}" \
    "${endpoint}"
}

wait_for_rpc() {
  local endpoint="$1"
  local method="$2"
  local timeout_sec="${3:-60}"
  local start now response err
  start="$(date +%s)"
  while true; do
    if response="$(rpc_call "${endpoint}" "${method}" '[]' 2>/dev/null)"; then
      err="$(jq -r '.error.message // empty' <<<"${response}" 2>/dev/null || true)"
      if [[ -z "${err}" ]]; then
        return 0
      fi
    fi
    now="$(date +%s)"
    if (( now - start >= timeout_sec )); then
      return 1
    fi
    sleep 1
  done
}

cleanup_processes() {
  set +e
  if [[ -n "${UBT_PID}" ]] && kill -0 "${UBT_PID}" 2>/dev/null; then
    kill "${UBT_PID}" 2>/dev/null
    wait "${UBT_PID}" 2>/dev/null
  fi
  if [[ -n "${GETH_PID}" ]] && kill -0 "${GETH_PID}" 2>/dev/null; then
    kill "${GETH_PID}" 2>/dev/null
    wait "${GETH_PID}" 2>/dev/null
  fi
  set -e
  UBT_PID=""
  GETH_PID=""
}

on_exit() {
  cleanup_processes
  if (( KEEP_DATA == 0 )); then
    rm -rf "${WORKDIR}" 2>/dev/null || true
  else
    log "Kept workdir: ${WORKDIR}"
  fi
}

build_binaries() {
  if (( SKIP_BUILD == 1 )); then
    log "Skipping binary build"
    return
  fi
  log "Building geth and ubtconv binaries"
  mkdir -p "${GO_CACHE_DIR}" "${GO_MOD_CACHE_DIR}"
  (cd "${ROOT_DIR}" && GOCACHE="${GO_CACHE_DIR}" GOMODCACHE="${GO_MOD_CACHE_DIR}" go build -o build/bin/geth ./cmd/geth)
  (cd "${ROOT_DIR}" && GOCACHE="${GO_CACHE_DIR}" GOMODCACHE="${GO_MOD_CACHE_DIR}" go build -o build/bin/ubtconv ./cmd/ubtconv)
}

start_geth() {
  local session_dir="$1"
  local geth_dir="${session_dir}/geth"
  local geth_log="${session_dir}/geth.log"

  mkdir -p "${geth_dir}"

  log "Starting geth for session '${CURRENT_SESSION}'"
  "${GETH_BIN}" \
    --dev --dev.period 1 \
    --datadir "${geth_dir}" \
    --port "${GETH_P2P_PORT}" \
    --authrpc.port "${GETH_AUTHRPC_PORT}" \
    --http --http.addr 127.0.0.1 --http.port "${GETH_HTTP_PORT}" \
    --http.api eth,net,web3,admin,debug,ubt,txpool \
    --ipcdisable \
    --nodiscover --maxpeers 0 \
    --miner.etherbase "${TEST_CHECK_ADDRESS}" \
    --ubt.conversion-enabled \
    --ubt.decoupled \
    --ubt.outbox-db-path "${geth_dir}/ubt-outbox" \
    --ubt.outbox-retention-seq-window 100000 \
    --ubt.reorg-marker-enabled \
    --ubt.outbox-read-rpc-enabled \
    --ubt.debug-rpc-proxy-enabled \
    --ubt.debug-endpoint "http://127.0.0.1:${UBT_HTTP_PORT}" \
    --ubt.debug-timeout 5s \
    >"${geth_log}" 2>&1 &
  GETH_PID="$!"

  if ! wait_for_rpc "http://127.0.0.1:${GETH_HTTP_PORT}" "rpc_modules" 90; then
    fail "geth RPC did not become ready (log: ${geth_log})"
  fi
  pass "geth started (pid=${GETH_PID})"
}

start_ubtconv() {
  local session_dir="$1"
  local execution_enabled="$2"
  local ubt_dir="${session_dir}/ubtconv"
  local ubt_log="${session_dir}/ubtconv.log"

  mkdir -p "${ubt_dir}"

  log "Starting ubtconv for session '${CURRENT_SESSION}' (execution_enabled=${execution_enabled})"
  local cmd=(
    "${UBTCONV_BIN}"
    --outbox-rpc-endpoint "http://127.0.0.1:${GETH_HTTP_PORT}"
    --datadir "${ubt_dir}"
    --apply-commit-interval 1
    --apply-commit-max-latency 1s
    --query-rpc-enabled
    --query-rpc-listen-addr "127.0.0.1:${UBT_HTTP_PORT}"
    --triedb-state-history 90000
    --validation-strict=false
  )
  if (( execution_enabled == 1 )); then
    cmd+=(--execution-class-rpc-enabled)
  fi

  "${cmd[@]}" >"${ubt_log}" 2>&1 &
  UBT_PID="$!"

  if ! wait_for_rpc "http://127.0.0.1:${UBT_HTTP_PORT}" "ubt_status" 90; then
    fail "ubtconv RPC did not become ready (log: ${ubt_log})"
  fi
  pass "ubtconv started (pid=${UBT_PID})"
}

stop_ubtconv() {
  if [[ -n "${UBT_PID}" ]] && kill -0 "${UBT_PID}" 2>/dev/null; then
    log "Stopping ubtconv (pid=${UBT_PID})"
    kill "${UBT_PID}" 2>/dev/null || true
    wait "${UBT_PID}" 2>/dev/null || true
  fi
  UBT_PID=""
}

wait_for_ubt_ready() {
  local timeout_sec="${1:-120}"
  local start now status_resp applied_block applied_seq
  start="$(date +%s)"
  while true; do
    status_resp="$(rpc_call "http://127.0.0.1:${UBT_HTTP_PORT}" "ubt_status" '[]' || true)"
    applied_seq="$(jq -r '.result.appliedSeq // 0' <<<"${status_resp}" 2>/dev/null || echo "0")"
    applied_block="$(jq -r '.result.appliedBlock // 0' <<<"${status_resp}" 2>/dev/null || echo "0")"
    if (( applied_block >= 1 )); then
      pass "UBT ready: appliedSeq=${applied_seq}, appliedBlock=${applied_block}"
      return 0
    fi

    now="$(date +%s)"
    if (( now - start > timeout_sec )); then
      fail "UBT ready timeout: appliedSeq=${applied_seq}, appliedBlock=${applied_block}"
    fi
    sleep 2
  done
}

wait_for_receipt() {
  local tx_hash="$1"
  local timeout_sec="${2:-120}"
  local start now receipt contract
  start="$(date +%s)"
  while true; do
    receipt="$(rpc_call "http://127.0.0.1:${GETH_HTTP_PORT}" "eth_getTransactionReceipt" "[\"${tx_hash}\"]" || true)"
    contract="$(jq -r '.result.contractAddress // empty' <<<"${receipt}" 2>/dev/null || true)"
    if [[ -n "${contract}" && "${contract}" != "null" ]]; then
      printf '%s' "${receipt}"
      return 0
    fi

    now="$(date +%s)"
    if (( now - start > timeout_sec )); then
      return 1
    fi
    sleep 1
  done
}

deploy_storage_fixture() {
  local session_dir="$1"
  local from_resp from_addr tx_resp tx_hash receipt block_hex

  SESSION_STORAGE_ADDRESS=""
  from_resp="$(rpc_call "http://127.0.0.1:${GETH_HTTP_PORT}" "eth_accounts" '[]')"
  from_addr="$(jq -r '.result[0] // empty' <<<"${from_resp}" 2>/dev/null || true)"
  [[ -n "${from_addr}" ]] || fail "could not resolve deployment account from eth_accounts"

  # Init code: SSTORE(slot0, 0x2a); RETURN(0, 0)
  tx_resp="$(rpc_call "http://127.0.0.1:${GETH_HTTP_PORT}" "eth_sendTransaction" "[{\"from\":\"${from_addr}\",\"data\":\"0x602a60005560006000f3\"}]")"
  tx_hash="$(jq -r '.result // empty' <<<"${tx_resp}" 2>/dev/null || true)"
  [[ -n "${tx_hash}" ]] || fail "failed to submit storage fixture tx: $(jq -r '.error.message // empty' <<<"${tx_resp}")"

  receipt="$(wait_for_receipt "${tx_hash}" 120)" || fail "timed out waiting for fixture receipt (tx=${tx_hash})"
  SESSION_STORAGE_ADDRESS="$(jq -r '.result.contractAddress // empty' <<<"${receipt}" 2>/dev/null || true)"
  block_hex="$(jq -r '.result.blockNumber // empty' <<<"${receipt}" 2>/dev/null || true)"
  [[ -n "${SESSION_STORAGE_ADDRESS}" ]] || fail "fixture receipt missing contractAddress (tx=${tx_hash})"
  [[ -n "${block_hex}" ]] || fail "fixture receipt missing blockNumber (tx=${tx_hash})"
  SESSION_STORAGE_BLOCK_DEC="$(hex_to_dec "${block_hex}")"

  printf '%s\n' "contract=${SESSION_STORAGE_ADDRESS}" > "${session_dir}/storage_fixture.env"
  printf '%s\n' "slot=${SESSION_STORAGE_SLOT}" >> "${session_dir}/storage_fixture.env"
  printf '%s\n' "tx=${tx_hash}" >> "${session_dir}/storage_fixture.env"
  printf '%s\n' "block=${block_hex}" >> "${session_dir}/storage_fixture.env"
  pass "Deployed storage fixture contract ${SESSION_STORAGE_ADDRESS} at block ${block_hex}"
}

wait_for_ubt_block() {
  local target_block="$1"
  local timeout_sec="${2:-180}"
  local start now status_resp applied_block
  start="$(date +%s)"
  while true; do
    status_resp="$(rpc_call "http://127.0.0.1:${UBT_HTTP_PORT}" "ubt_status" '[]' || true)"
    applied_block="$(jq -r '.result.appliedBlock // 0' <<<"${status_resp}" 2>/dev/null || echo "0")"
    if (( applied_block >= target_block )); then
      pass "UBT applied block reached ${applied_block} (target=${target_block})"
      return 0
    fi

    now="$(date +%s)"
    if (( now - start > timeout_sec )); then
      fail "timeout waiting ubt appliedBlock >= ${target_block} (current=${applied_block})"
    fi
    sleep 2
  done
}

run_manual_check() {
  local execution_enabled="$1"
  local args=(
    --geth "http://127.0.0.1:${GETH_HTTP_PORT}"
    --ubt "http://127.0.0.1:${UBT_HTTP_PORT}"
  )
  if [[ -n "${SESSION_CHECK_ADDRESS}" ]]; then
    args+=(--address "${SESSION_CHECK_ADDRESS}")
  fi
  if [[ -n "${SESSION_STORAGE_ADDRESS}" ]]; then
    args+=(--storage-address "${SESSION_STORAGE_ADDRESS}" --storage-slot "${SESSION_STORAGE_SLOT}")
  fi
  if (( execution_enabled == 1 )); then
    args+=(--execution-enabled)
  else
    args+=(--execution-disabled)
  fi

  log "Running run_manual_check.sh (${args[*]})"
  wait_for_ubt_ready 120
  (cd "${ROOT_DIR}" && "${MANUAL_CHECK_BIN}" "${args[@]}")
  pass "run_manual_check.sh completed"
}

session_disabled() {
  local session_dir="$1"
  start_geth "${session_dir}"
  if (( ENABLE_STORAGE_FIXTURE == 1 )); then
    deploy_storage_fixture "${session_dir}"
  fi
  start_ubtconv "${session_dir}" 0
  if (( SESSION_STORAGE_BLOCK_DEC > 0 )); then
    wait_for_ubt_block "${SESSION_STORAGE_BLOCK_DEC}" 180
  fi
  run_manual_check 0
}

session_enabled() {
  local session_dir="$1"
  start_geth "${session_dir}"
  if (( ENABLE_STORAGE_FIXTURE == 1 )); then
    deploy_storage_fixture "${session_dir}"
  fi
  start_ubtconv "${session_dir}" 1
  if (( SESSION_STORAGE_BLOCK_DEC > 0 )); then
    wait_for_ubt_block "${SESSION_STORAGE_BLOCK_DEC}" 180
  fi
  run_manual_check 1
}

session_restart() {
  log "Running deterministic restart/recovery validation tests"
  mkdir -p "${GO_CACHE_DIR}" "${GO_MOD_CACHE_DIR}"
  (cd "${ROOT_DIR}" && GOCACHE="${GO_CACHE_DIR}" GOMODCACHE="${GO_MOD_CACHE_DIR}" go test ./cmd/ubtconv -run 'TestVerify_RestartAfterSeqZero|TestCrashRecovery_ReplayFromAppliedSeq|TestRestart_AfterConsumingSeqZero' -count=1)
  pass "Restart recovery integration tests passed"
}

session_reorg() {
  log "Running deterministic reorg recovery validation via Go integration test"
  mkdir -p "${GO_CACHE_DIR}" "${GO_MOD_CACHE_DIR}"
  (cd "${ROOT_DIR}" && GOCACHE="${GO_CACHE_DIR}" GOMODCACHE="${GO_MOD_CACHE_DIR}" go test ./cmd/ubtconv -run TestVerify_ReorgRecovery -count=1)
  pass "Reorg recovery integration test passed"
}

run_one_session() {
  local name="$1"
  CURRENT_SESSION="${name}"
  local session_dir="${WORKDIR}/${name}"
  mkdir -p "${session_dir}"

  cleanup_processes

  case "${name}" in
    disabled)
      session_disabled "${session_dir}"
      ;;
    enabled)
      session_enabled "${session_dir}"
      ;;
    restart)
      session_restart "${session_dir}"
      ;;
    reorg)
      session_reorg
      ;;
    *)
      fail "unknown session: ${name}"
      ;;
  esac

  cleanup_processes
  pass "Session '${name}' completed"
}

main() {
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --session)
        SESSION="$2"
        shift 2
        ;;
      --workdir)
        WORKDIR="$2"
        shift 2
        ;;
      --skip-build)
        SKIP_BUILD=1
        shift
        ;;
      --keep-data)
        KEEP_DATA=1
        shift
        ;;
      -h|--help)
        usage
        exit 0
        ;;
      *)
        fail "unknown argument: $1"
        ;;
    esac
  done

  require_cmd curl
  require_cmd jq
  require_cmd go

  mkdir -p "${WORKDIR}"
  trap on_exit EXIT

  build_binaries

  local needs_runtime_binaries=0
  if [[ "${SESSION}" == "all" || "${SESSION}" == "disabled" || "${SESSION}" == "enabled" || "${SESSION}" == "restart" ]]; then
    needs_runtime_binaries=1
  fi

  if (( needs_runtime_binaries == 1 )); then
    if [[ ! -x "${GETH_BIN}" ]]; then
      fail "geth binary not found: ${GETH_BIN}"
    fi
    if [[ ! -x "${UBTCONV_BIN}" ]]; then
      fail "ubtconv binary not found: ${UBTCONV_BIN}"
    fi
    if [[ ! -x "${MANUAL_CHECK_BIN}" ]]; then
      fail "manual check script not executable: ${MANUAL_CHECK_BIN}"
    fi
  fi

  log "Workdir: ${WORKDIR}"
  case "${SESSION}" in
    disabled|enabled|restart|reorg)
      run_one_session "${SESSION}"
      ;;
    all)
      run_one_session disabled
      run_one_session enabled
      run_one_session restart
      run_one_session reorg
      ;;
    *)
      fail "invalid --session value: ${SESSION}"
      ;;
  esac

  pass "All requested sessions completed"
}

main "$@"
