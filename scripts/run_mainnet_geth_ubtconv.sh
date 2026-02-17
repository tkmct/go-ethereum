#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

GETH_BIN="${ROOT_DIR}/build/bin/geth"
UBTCONV_BIN="${ROOT_DIR}/build/bin/ubtconv"
LIGHTHOUSE_BIN="${LIGHTHOUSE_BIN:-lighthouse}"

ACTION="up" # up | down | status
BUILD=1
DETACH=0
ENABLE_EXECUTION_RPC=0

WORKDIR="${WORKDIR:-${HOME}/.ubt-mainnet}"
GETH_DATADIR="${GETH_DATADIR:-${WORKDIR}/geth}"
UBT_DATADIR="${UBT_DATADIR:-${WORKDIR}/ubtconv}"
LIGHTHOUSE_DATADIR="${LIGHTHOUSE_DATADIR:-${WORKDIR}/lighthouse}"
LOG_DIR="${LOG_DIR:-${WORKDIR}/logs}"
JWT_SECRET_FILE="${JWT_SECRET_FILE:-${WORKDIR}/jwtsecret.hex}"

GETH_HTTP_ADDR="${GETH_HTTP_ADDR:-0.0.0.0}"
GETH_HTTP_PORT="${GETH_HTTP_PORT:-8545}"
GETH_HTTP_CORSDOMAIN="${GETH_HTTP_CORSDOMAIN:-*}"
GETH_HTTP_VHOSTS="${GETH_HTTP_VHOSTS:-*}"
GETH_P2P_PORT="${GETH_P2P_PORT:-30303}"
GETH_AUTHRPC_ADDR="${GETH_AUTHRPC_ADDR:-0.0.0.0}"
GETH_AUTHRPC_PORT="${GETH_AUTHRPC_PORT:-8551}"
GETH_AUTHRPC_VHOSTS="${GETH_AUTHRPC_VHOSTS:-*}"
GETH_IPC_PATH="${GETH_IPC_PATH:-${GETH_DATADIR}/geth.ipc}"
UBT_OUTBOX_RPC_ENDPOINT="${UBT_OUTBOX_RPC_ENDPOINT:-${GETH_IPC_PATH}}"

UBT_HTTP_ADDR="${UBT_HTTP_ADDR:-0.0.0.0}"
UBT_HTTP_PORT="${UBT_HTTP_PORT:-8560}"
LIGHTHOUSE_HTTP_ADDR="${LIGHTHOUSE_HTTP_ADDR:-127.0.0.1}"
LIGHTHOUSE_HTTP_PORT="${LIGHTHOUSE_HTTP_PORT:-5052}"
LIGHTHOUSE_P2P_PORT="${LIGHTHOUSE_P2P_PORT:-9000}"
LIGHTHOUSE_QUIC_PORT="${LIGHTHOUSE_QUIC_PORT:-9001}"
CHECKPOINT_SYNC_URL="${CHECKPOINT_SYNC_URL:-https://mainnet.checkpoint.sigp.io}"
GETH_MAX_PEERS="${GETH_MAX_PEERS:-80}"
GETH_CACHE_MB="${GETH_CACHE_MB:-4096}"
GETH_CACHE_DATABASE_PERCENT="${GETH_CACHE_DATABASE_PERCENT:-60}"
GETH_CACHE_TRIE_PERCENT="${GETH_CACHE_TRIE_PERCENT:-20}"
GETH_CACHE_GC_PERCENT="${GETH_CACHE_GC_PERCENT:-15}"
GETH_CACHE_SNAPSHOT_PERCENT="${GETH_CACHE_SNAPSHOT_PERCENT:-5}"

APPLY_COMMIT_INTERVAL="${APPLY_COMMIT_INTERVAL:-1024}"
APPLY_COMMIT_MAX_LATENCY="${APPLY_COMMIT_MAX_LATENCY:-10s}"
TRIEDB_STATE_HISTORY="${TRIEDB_STATE_HISTORY:-90000}"
MAX_RECOVERABLE_REORG_DEPTH="${MAX_RECOVERABLE_REORG_DEPTH:-128}"
OUTBOX_READ_AHEAD="${OUTBOX_READ_AHEAD:-256}"
BACKPRESSURE_LAG_THRESHOLD="${BACKPRESSURE_LAG_THRESHOLD:-5000}"
VALIDATION_STRICT_CATCHUP_SAMPLE_RATE="${VALIDATION_STRICT_CATCHUP_SAMPLE_RATE:-0}"
VALIDATION_STRICT="${VALIDATION_STRICT:-false}"
BLOCK_ROOT_INDEX_STRIDE_HIGH_LAG="${BLOCK_ROOT_INDEX_STRIDE_HIGH_LAG:-64}"
OUTBOX_RETENTION_SEQ_WINDOW="${OUTBOX_RETENTION_SEQ_WINDOW:-0}"
SLOT_INDEX_ENABLED="${SLOT_INDEX_ENABLED:-false}"
PENDING_STATE_PERSIST_INTERVAL="${PENDING_STATE_PERSIST_INTERVAL:-2s}"
PPROF_ENABLED="${PPROF_ENABLED:-false}"
PPROF_LISTEN_ADDR="${PPROF_LISTEN_ADDR:-127.0.0.1:6061}"
MONITOR_PROGRESS_INTERVAL="${MONITOR_PROGRESS_INTERVAL:-30}"

GETH_PID_FILE="${WORKDIR}/geth.pid"
UBT_PID_FILE="${WORKDIR}/ubtconv.pid"
LIGHTHOUSE_PID_FILE="${WORKDIR}/lighthouse.pid"
GETH_LOG_FILE="${LOG_DIR}/geth.log"
UBT_LOG_FILE="${LOG_DIR}/ubtconv.log"
LIGHTHOUSE_LOG_FILE="${LOG_DIR}/lighthouse.log"

log() {
  printf '[INFO] %s\n' "$*"
}

warn() {
  printf '[WARN] %s\n' "$*" >&2
}

fail() {
  printf '[FAIL] %s\n' "$*" >&2
  exit 1
}

spawn_process() {
  local log_file="$1"
  shift
  if command -v setsid >/dev/null 2>&1; then
    nohup setsid "$@" >"${log_file}" 2>&1 < /dev/null &
  else
    nohup "$@" >"${log_file}" 2>&1 < /dev/null &
  fi
  echo "$!"
}

usage() {
  cat <<'USAGE'
Usage:
  scripts/run_mainnet_geth_ubtconv.sh [options]

Options:
  --action up|down|status   Action to run (default: up)
  --workdir DIR             Base directory for data/log/pid files
  --geth-bin PATH           Path to geth binary
  --ubtconv-bin PATH        Path to ubtconv binary
  --lighthouse-bin PATH     Path to lighthouse binary (default: lighthouse in PATH)
  --geth-http-corsdomain D  geth --http.corsdomain value (comma-separated)
  --geth-http-vhosts V      geth --http.vhosts value (comma-separated)
  --geth-authrpc-vhosts V   geth --authrpc.vhosts value (comma-separated)
  --checkpoint-sync-url URL Lighthouse checkpoint sync URL (default: https://mainnet.checkpoint.sigp.io)
  --skip-build              Skip go build step
  --detach                  Start processes and exit immediately
  --enable-execution-rpc    Start ubtconv with --execution-class-rpc-enabled
  --help                    Show this help

Environment overrides:
  GETH_HTTP_ADDR, GETH_HTTP_PORT, GETH_HTTP_CORSDOMAIN, GETH_HTTP_VHOSTS
  GETH_P2P_PORT, GETH_AUTHRPC_ADDR, GETH_AUTHRPC_PORT, GETH_AUTHRPC_VHOSTS
  GETH_IPC_PATH, UBT_OUTBOX_RPC_ENDPOINT
  GETH_MAX_PEERS, GETH_CACHE_MB
  GETH_CACHE_DATABASE_PERCENT, GETH_CACHE_TRIE_PERCENT
  GETH_CACHE_GC_PERCENT, GETH_CACHE_SNAPSHOT_PERCENT
  UBT_HTTP_ADDR, UBT_HTTP_PORT
  LIGHTHOUSE_HTTP_ADDR, LIGHTHOUSE_HTTP_PORT, LIGHTHOUSE_P2P_PORT, LIGHTHOUSE_QUIC_PORT
  CHECKPOINT_SYNC_URL (default: https://mainnet.checkpoint.sigp.io)
  APPLY_COMMIT_INTERVAL, APPLY_COMMIT_MAX_LATENCY
  OUTBOX_READ_AHEAD, BACKPRESSURE_LAG_THRESHOLD
  VALIDATION_STRICT, VALIDATION_STRICT_CATCHUP_SAMPLE_RATE, BLOCK_ROOT_INDEX_STRIDE_HIGH_LAG
  TRIEDB_STATE_HISTORY, MAX_RECOVERABLE_REORG_DEPTH, OUTBOX_RETENTION_SEQ_WINDOW
  SLOT_INDEX_ENABLED, PENDING_STATE_PERSIST_INTERVAL
  PPROF_ENABLED, PPROF_LISTEN_ADDR
  MONITOR_PROGRESS_INTERVAL

Notes:
  - This script forces geth sync mode to full-sync: --syncmode full.
  - It starts geth + lighthouse beacon node + ubtconv.
  - It enables UBT outbox/debug plumbing in geth and starts ubtconv against it.
USAGE
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || fail "missing required command: $1"
}

require_opt_value() {
  local opt="$1"
  local val="${2:-}"
  if [[ -z "${val}" || "${val}" == --* ]]; then
    fail "option ${opt} requires a value"
  fi
  printf '%s' "${val}"
}

pid_running() {
  local pid="$1"
  kill -0 "${pid}" 2>/dev/null
}

read_pid() {
  local pid_file="$1"
  if [[ -f "${pid_file}" ]]; then
    cat "${pid_file}"
  fi
}

rpc_call() {
  local endpoint="$1"
  local method="$2"
  local params="$3"
  curl -sS -H 'Content-Type: application/json' \
    --data "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"${method}\",\"params\":${params}}" \
    "${endpoint}"
}

rpc_result_raw() {
  local endpoint="$1"
  local method="$2"
  local params="$3"
  local response
  if ! response="$(rpc_call "${endpoint}" "${method}" "${params}" 2>/dev/null)"; then
    return 1
  fi
  local err
  err="$(jq -r '.error.message // empty' <<<"${response}" 2>/dev/null || true)"
  if [[ -n "${err}" ]]; then
    return 1
  fi
  jq -rc '.result' <<<"${response}" 2>/dev/null
}

hex_to_dec_or_na() {
  local hex="${1:-}"
  if [[ -z "${hex}" || "${hex}" == "null" ]]; then
    printf 'n/a'
    return
  fi
  if [[ "${hex}" =~ ^0x[0-9a-fA-F]+$ ]]; then
    printf '%d' "$((16#${hex#0x}))"
    return
  fi
  printf '%s' "${hex}"
}

wait_for_rpc() {
  local endpoint="$1"
  local method="$2"
  local timeout_sec="${3:-120}"
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

build_binaries() {
  if (( BUILD == 0 )); then
    return
  fi
  log "Building geth and ubtconv binaries"
  (cd "${ROOT_DIR}" && go build -o build/bin/geth ./cmd/geth)
  (cd "${ROOT_DIR}" && go build -o build/bin/ubtconv ./cmd/ubtconv)
}

ensure_dirs() {
  mkdir -p "${WORKDIR}" "${GETH_DATADIR}" "${UBT_DATADIR}" "${LIGHTHOUSE_DATADIR}" "${LOG_DIR}"
}

ensure_jwt_secret() {
  if [[ -f "${JWT_SECRET_FILE}" ]]; then
    local existing
    existing="$(tr -d '\n\r' <"${JWT_SECRET_FILE}")"
    if [[ "${existing}" =~ ^[0-9a-fA-F]{64}$ ]]; then
      return
    fi
    warn "Existing JWT secret is invalid, regenerating: ${JWT_SECRET_FILE}"
  fi

  local secret
  secret="$(head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n')"
  printf '%s\n' "${secret}" >"${JWT_SECRET_FILE}"
  chmod 600 "${JWT_SECRET_FILE}"
  log "Generated JWT secret: ${JWT_SECRET_FILE}"
}

start_geth() {
  local existing_pid
  existing_pid="$(read_pid "${GETH_PID_FILE}" || true)"
  if [[ -n "${existing_pid}" ]] && pid_running "${existing_pid}"; then
    fail "geth already running (pid=${existing_pid})"
  fi

  log "Starting geth (mainnet full-sync)"
  local cmd=(
    env -u GETH_CACHE_MB "${GETH_BIN}"
    --mainnet
    --syncmode full
    --cache "${GETH_CACHE_MB}"
    --cache.database "${GETH_CACHE_DATABASE_PERCENT}"
    --cache.trie "${GETH_CACHE_TRIE_PERCENT}"
    --cache.gc "${GETH_CACHE_GC_PERCENT}"
    --cache.snapshot "${GETH_CACHE_SNAPSHOT_PERCENT}"
    --datadir "${GETH_DATADIR}"
    --port "${GETH_P2P_PORT}"
    --maxpeers "${GETH_MAX_PEERS}"
    --ipcpath "${GETH_IPC_PATH}"
    --authrpc.addr "${GETH_AUTHRPC_ADDR}"
    --authrpc.port "${GETH_AUTHRPC_PORT}"
    --authrpc.vhosts "${GETH_AUTHRPC_VHOSTS}"
    --authrpc.jwtsecret "${JWT_SECRET_FILE}"
    --http --http.addr "${GETH_HTTP_ADDR}" --http.port "${GETH_HTTP_PORT}"
    --http.vhosts "${GETH_HTTP_VHOSTS}"
    --http.api eth,net,web3,debug,ubt
    --ubt.conversion-enabled
    --ubt.decoupled
    --ubt.outbox-db-path "${GETH_DATADIR}/ubt-outbox"
    --ubt.outbox-retention-seq-window "${OUTBOX_RETENTION_SEQ_WINDOW}"
    --ubt.reorg-marker-enabled
    --ubt.outbox-read-rpc-enabled
    --ubt.debug-rpc-proxy-enabled
    --ubt.debug-endpoint "http://${UBT_HTTP_ADDR}:${UBT_HTTP_PORT}"
    --ubt.debug-timeout 5s
  )
  if [[ -n "${GETH_HTTP_CORSDOMAIN}" ]]; then
    cmd+=(--http.corsdomain "${GETH_HTTP_CORSDOMAIN}")
  fi
  spawn_process "${GETH_LOG_FILE}" "${cmd[@]}" >"${GETH_PID_FILE}"

  if ! wait_for_rpc "http://${GETH_HTTP_ADDR}:${GETH_HTTP_PORT}" "rpc_modules" 180; then
    fail "geth RPC did not become ready (log: ${GETH_LOG_FILE})"
  fi
  log "geth started (pid=$(cat "${GETH_PID_FILE}"))"
}

wait_for_http_get() {
  local url="$1"
  local timeout_sec="${2:-180}"
  local pid="${3:-}"
  local start now

  start="$(date +%s)"
  while true; do
    if curl --max-time 2 -fsS "${url}" >/dev/null 2>&1; then
      return 0
    fi
    if [[ -n "${pid}" ]] && ! pid_running "${pid}"; then
      return 2
    fi
    now="$(date +%s)"
    if (( now - start >= timeout_sec )); then
      return 1
    fi
    sleep 1
  done
}

start_lighthouse() {
  local existing_pid
  existing_pid="$(read_pid "${LIGHTHOUSE_PID_FILE}" || true)"
  if [[ -n "${existing_pid}" ]] && pid_running "${existing_pid}"; then
    fail "lighthouse already running (pid=${existing_pid})"
  fi

  log "Starting lighthouse beacon node (mainnet)"
  local cmd=(
    "${LIGHTHOUSE_BIN}"
    bn
    --network mainnet
    --datadir "${LIGHTHOUSE_DATADIR}"
    --execution-endpoint "http://${GETH_AUTHRPC_ADDR}:${GETH_AUTHRPC_PORT}"
    --execution-jwt "${JWT_SECRET_FILE}"
    --http
    --http-address "${LIGHTHOUSE_HTTP_ADDR}"
    --http-port "${LIGHTHOUSE_HTTP_PORT}"
    --port "${LIGHTHOUSE_P2P_PORT}"
    --quic-port "${LIGHTHOUSE_QUIC_PORT}"
  )
  if [[ -n "${CHECKPOINT_SYNC_URL}" ]]; then
    cmd+=(--checkpoint-sync-url "${CHECKPOINT_SYNC_URL}")
  fi

  spawn_process "${LIGHTHOUSE_LOG_FILE}" "${cmd[@]}" >"${LIGHTHOUSE_PID_FILE}"
  local lighthouse_pid
  lighthouse_pid="$(cat "${LIGHTHOUSE_PID_FILE}")"

  # Consider lighthouse "started" once process is alive and startup banner is written.
  local start now
  start="$(date +%s)"
  while true; do
    if ! pid_running "${lighthouse_pid}"; then
      fail "lighthouse exited during startup (log: ${LIGHTHOUSE_LOG_FILE})"
    fi
    if grep -q "Lighthouse started" "${LIGHTHOUSE_LOG_FILE}" 2>/dev/null; then
      break
    fi
    now="$(date +%s)"
    if (( now - start >= 60 )); then
      fail "lighthouse startup banner not observed within timeout (log: ${LIGHTHOUSE_LOG_FILE})"
    fi
    sleep 1
  done
  log "lighthouse started (pid=${lighthouse_pid})"

  # HTTP API readiness can lag behind startup; warn but continue when process is healthy.
  if ! wait_for_http_get "http://${LIGHTHOUSE_HTTP_ADDR}:${LIGHTHOUSE_HTTP_PORT}/eth/v1/node/version" 30 "${lighthouse_pid}"; then
    if ! pid_running "${lighthouse_pid}"; then
      fail "lighthouse exited before HTTP API became ready (log: ${LIGHTHOUSE_LOG_FILE})"
    fi
    warn "lighthouse HTTP API not ready yet; continuing while process is healthy"
  fi
}

start_ubtconv() {
  local existing_pid
  existing_pid="$(read_pid "${UBT_PID_FILE}" || true)"
  if [[ -n "${existing_pid}" ]] && pid_running "${existing_pid}"; then
    fail "ubtconv already running (pid=${existing_pid})"
  fi

  log "Starting ubtconv"
  local cmd=(
    "${UBTCONV_BIN}"
    --outbox-rpc-endpoint "${UBT_OUTBOX_RPC_ENDPOINT}"
    --outbox-read-ahead "${OUTBOX_READ_AHEAD}"
    --datadir "${UBT_DATADIR}"
    --apply-commit-interval "${APPLY_COMMIT_INTERVAL}"
    --apply-commit-max-latency "${APPLY_COMMIT_MAX_LATENCY}"
    --pending-state-persist-interval "${PENDING_STATE_PERSIST_INTERVAL}"
    --backpressure-lag-threshold "${BACKPRESSURE_LAG_THRESHOLD}"
    --block-root-index-stride-high-lag "${BLOCK_ROOT_INDEX_STRIDE_HIGH_LAG}"
    --query-rpc-enabled
    --query-rpc-listen-addr "${UBT_HTTP_ADDR}:${UBT_HTTP_PORT}"
    --triedb-scheme path
    --triedb-state-history "${TRIEDB_STATE_HISTORY}"
    --max-recoverable-reorg-depth "${MAX_RECOVERABLE_REORG_DEPTH}"
    --validation-strict="${VALIDATION_STRICT}"
    --validation-strict-catchup-sample-rate "${VALIDATION_STRICT_CATCHUP_SAMPLE_RATE}"
    --slot-index-enabled="${SLOT_INDEX_ENABLED}"
    --require-archive-replay=true
  )
  if [[ "${PPROF_ENABLED}" == "true" ]]; then
    cmd+=(--pprof-enabled --pprof-listen-addr "${PPROF_LISTEN_ADDR}")
  fi
  if (( ENABLE_EXECUTION_RPC == 1 )); then
    cmd+=(--execution-class-rpc-enabled)
  fi

  spawn_process "${UBT_LOG_FILE}" "${cmd[@]}" >"${UBT_PID_FILE}"

  if ! wait_for_rpc "http://${UBT_HTTP_ADDR}:${UBT_HTTP_PORT}" "ubt_status" 180; then
    fail "ubtconv RPC did not become ready (log: ${UBT_LOG_FILE})"
  fi
  log "ubtconv started (pid=$(cat "${UBT_PID_FILE}"))"
}

stop_pid_file() {
  local name="$1"
  local pid_file="$2"
  if [[ ! -f "${pid_file}" ]]; then
    log "${name}: pid file not found"
    return
  fi

  local pid
  pid="$(cat "${pid_file}")"
  if [[ -z "${pid}" ]]; then
    rm -f "${pid_file}"
    return
  fi

  if pid_running "${pid}"; then
    log "Stopping ${name} (pid=${pid})"
    kill "${pid}" 2>/dev/null || true
    for _ in $(seq 1 20); do
      if ! pid_running "${pid}"; then
        break
      fi
      sleep 0.5
    done
    if pid_running "${pid}"; then
      warn "${name} did not stop gracefully, sending SIGKILL"
      kill -9 "${pid}" 2>/dev/null || true
    fi
  else
    log "${name}: process not running (pid=${pid})"
  fi
  rm -f "${pid_file}"
}

show_status() {
  local geth_pid ubt_pid lighthouse_pid
  geth_pid="$(read_pid "${GETH_PID_FILE}" || true)"
  ubt_pid="$(read_pid "${UBT_PID_FILE}" || true)"
  lighthouse_pid="$(read_pid "${LIGHTHOUSE_PID_FILE}" || true)"

  printf 'workdir: %s\n' "${WORKDIR}"
  printf 'geth pid: %s\n' "${geth_pid:-<none>}"
  if [[ -n "${geth_pid}" ]] && pid_running "${geth_pid}"; then
    printf 'geth running: yes\n'
  else
    printf 'geth running: no\n'
  fi
  printf 'ubtconv pid: %s\n' "${ubt_pid:-<none>}"
  if [[ -n "${ubt_pid}" ]] && pid_running "${ubt_pid}"; then
    printf 'ubtconv running: yes\n'
  else
    printf 'ubtconv running: no\n'
  fi
  printf 'lighthouse pid: %s\n' "${lighthouse_pid:-<none>}"
  if [[ -n "${lighthouse_pid}" ]] && pid_running "${lighthouse_pid}"; then
    printf 'lighthouse running: yes\n'
  else
    printf 'lighthouse running: no\n'
  fi
  printf 'geth rpc: http://%s:%s\n' "${GETH_HTTP_ADDR}" "${GETH_HTTP_PORT}"
  printf 'geth http corsdomain: %s\n' "${GETH_HTTP_CORSDOMAIN:-<empty>}"
  printf 'geth http vhosts: %s\n' "${GETH_HTTP_VHOSTS}"
  printf 'geth ipc: %s\n' "${GETH_IPC_PATH}"
  printf 'ubt outbox endpoint: %s\n' "${UBT_OUTBOX_RPC_ENDPOINT}"
  printf 'geth authrpc: http://%s:%s\n' "${GETH_AUTHRPC_ADDR}" "${GETH_AUTHRPC_PORT}"
  printf 'geth authrpc vhosts: %s\n' "${GETH_AUTHRPC_VHOSTS}"
  printf 'lighthouse http: http://%s:%s\n' "${LIGHTHOUSE_HTTP_ADDR}" "${LIGHTHOUSE_HTTP_PORT}"
  printf 'ubt rpc:  http://%s:%s\n' "${UBT_HTTP_ADDR}" "${UBT_HTTP_PORT}"
  printf 'logs: %s , %s , %s\n' "${GETH_LOG_FILE}" "${LIGHTHOUSE_LOG_FILE}" "${UBT_LOG_FILE}"

  local eth_block_hex ubt_seq_hex ubt_status_json geth_ubt_status_json
  eth_block_hex="$(rpc_result_raw "http://${GETH_HTTP_ADDR}:${GETH_HTTP_PORT}" "eth_blockNumber" '[]' || true)"
  ubt_seq_hex="$(rpc_result_raw "http://${GETH_HTTP_ADDR}:${GETH_HTTP_PORT}" "ubt_latestSeq" '[]' || true)"
  ubt_status_json="$(rpc_result_raw "http://${UBT_HTTP_ADDR}:${UBT_HTTP_PORT}" "ubt_status" '[]' || true)"
  geth_ubt_status_json="$(rpc_result_raw "http://${GETH_HTTP_ADDR}:${GETH_HTTP_PORT}" "ubt_status" '[]' || true)"

  printf 'eth_blockNumber: %s (%s)\n' \
    "${eth_block_hex:-n/a}" "$(hex_to_dec_or_na "${eth_block_hex:-}")"
  printf 'ubt_latestSeq:  %s (%s)\n' \
    "${ubt_seq_hex:-n/a}" "$(hex_to_dec_or_na "${ubt_seq_hex:-}")"
  if [[ -n "${ubt_status_json}" ]]; then
    printf 'ubt_status:     appliedSeq=%s appliedBlock=%s pendingSeq=%s outboxLag=%s\n' \
      "$(jq -r '.appliedSeq // "n/a"' <<<"${ubt_status_json}" 2>/dev/null || echo n/a)" \
      "$(jq -r '.appliedBlock // "n/a"' <<<"${ubt_status_json}" 2>/dev/null || echo n/a)" \
      "$(jq -r '.pendingSeq // "n/a"' <<<"${ubt_status_json}" 2>/dev/null || echo n/a)" \
      "$(jq -r '.outboxLag // "n/a"' <<<"${ubt_status_json}" 2>/dev/null || echo n/a)"
  else
    printf 'ubt_status:     n/a (RPC not ready)\n'
  fi

  if [[ -n "${geth_ubt_status_json}" ]]; then
    printf 'geth ubt:       enabled=%s degraded=%s latestSeq=%s\n' \
      "$(jq -r '.enabled // "n/a"' <<<"${geth_ubt_status_json}" 2>/dev/null || echo n/a)" \
      "$(jq -r '.degraded // "n/a"' <<<"${geth_ubt_status_json}" 2>/dev/null || echo n/a)" \
      "$(jq -r '.latestSeq // "n/a"' <<<"${geth_ubt_status_json}" 2>/dev/null || echo n/a)"
    local degraded_reason_code degraded_block
    degraded_reason_code="$(jq -r '.degradedReasonCode // empty' <<<"${geth_ubt_status_json}" 2>/dev/null || true)"
    degraded_block="$(jq -r '.degradedBlock // empty' <<<"${geth_ubt_status_json}" 2>/dev/null || true)"
    if [[ -n "${degraded_reason_code}" ]]; then
      printf 'geth ubt:       degradedReasonCode=%s degradedBlock=%s\n' \
        "${degraded_reason_code}" "${degraded_block:-n/a}"
    fi
  fi

  local beacon_line
  beacon_line="$(grep "Syncing beacon headers" "${GETH_LOG_FILE}" 2>/dev/null | tail -n 1 || true)"
  if [[ -n "${beacon_line}" ]]; then
    printf 'geth beacon sync: %s\n' "${beacon_line}"
  fi
}

monitor_loop() {
  local geth_pid ubt_pid lighthouse_pid
  geth_pid="$(cat "${GETH_PID_FILE}")"
  ubt_pid="$(cat "${UBT_PID_FILE}")"
  lighthouse_pid="$(cat "${LIGHTHOUSE_PID_FILE}")"
  local ticks=0
  local prev_block=""
  local prev_seq=""
  local stagnant_intervals=0

  while true; do
    if ! pid_running "${geth_pid}"; then
      fail "geth exited unexpectedly (see ${GETH_LOG_FILE})"
    fi
    if ! pid_running "${ubt_pid}"; then
      fail "ubtconv exited unexpectedly (see ${UBT_LOG_FILE})"
    fi
    if ! pid_running "${lighthouse_pid}"; then
      fail "lighthouse exited unexpectedly (see ${LIGHTHOUSE_LOG_FILE})"
    fi
    ticks=$((ticks + 5))
    if (( ticks >= MONITOR_PROGRESS_INTERVAL )); then
      ticks=0
      local block_hex seq_hex ubt_status_json geth_ubt_status_json applied_seq applied_block beacon_line
      block_hex="$(rpc_result_raw "http://${GETH_HTTP_ADDR}:${GETH_HTTP_PORT}" "eth_blockNumber" '[]' || true)"
      seq_hex="$(rpc_result_raw "http://${GETH_HTTP_ADDR}:${GETH_HTTP_PORT}" "ubt_latestSeq" '[]' || true)"
      ubt_status_json="$(rpc_result_raw "http://${UBT_HTTP_ADDR}:${UBT_HTTP_PORT}" "ubt_status" '[]' || true)"
      geth_ubt_status_json="$(rpc_result_raw "http://${GETH_HTTP_ADDR}:${GETH_HTTP_PORT}" "ubt_status" '[]' || true)"
      applied_seq="$(jq -r '.appliedSeq // "n/a"' <<<"${ubt_status_json:-null}" 2>/dev/null || echo n/a)"
      applied_block="$(jq -r '.appliedBlock // "n/a"' <<<"${ubt_status_json:-null}" 2>/dev/null || echo n/a)"

      log "progress: eth_blockNumber=${block_hex:-n/a}($(hex_to_dec_or_na "${block_hex:-}")) ubt_latestSeq=${seq_hex:-n/a}($(hex_to_dec_or_na "${seq_hex:-}")) appliedSeq=${applied_seq} appliedBlock=${applied_block}"

      local degraded_reason_code degraded_block
      degraded_reason_code="$(jq -r '.degradedReasonCode // empty' <<<"${geth_ubt_status_json:-null}" 2>/dev/null || true)"
      degraded_block="$(jq -r '.degradedBlock // empty' <<<"${geth_ubt_status_json:-null}" 2>/dev/null || true)"
      if [[ "${seq_hex:-}" == "0x0" && "${block_hex:-}" != "0x0" && -n "${degraded_reason_code}" ]]; then
        warn "UBT emitter degraded (${degraded_reason_code}) at block ${degraded_block:-n/a}; outbox seq will stay 0 until this constraint clears"
      fi

      if [[ -n "${block_hex}" && -n "${seq_hex}" && "${block_hex}" == "${prev_block}" && "${seq_hex}" == "${prev_seq}" ]]; then
        stagnant_intervals=$((stagnant_intervals + 1))
      else
        stagnant_intervals=0
      fi
      prev_block="${block_hex:-}"
      prev_seq="${seq_hex:-}"

      if (( stagnant_intervals >= 2 )); then
        if [[ -n "${degraded_reason_code}" ]]; then
          warn "geth UBT emitter is degraded (${degraded_reason_code}) at block ${degraded_block:-n/a}; latestSeq may stay 0 until constraint is resolved"
        fi
        beacon_line="$(grep "Syncing beacon headers" "${GETH_LOG_FILE}" 2>/dev/null | tail -n 1 || true)"
        if [[ -n "${beacon_line}" ]]; then
          warn "no EL/UBT counter movement yet; geth still in beacon-header sync: ${beacon_line}"
        else
          warn "no EL/UBT counter movement yet; waiting for full-sync pipeline to advance"
        fi
      fi
    fi
    sleep 5
  done
}

cleanup_on_signal() {
  log "Signal received, stopping processes"
  stop_pid_file "ubtconv" "${UBT_PID_FILE}"
  stop_pid_file "lighthouse" "${LIGHTHOUSE_PID_FILE}"
  stop_pid_file "geth" "${GETH_PID_FILE}"
  exit 0
}

parse_args() {
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --action)
        ACTION="$(require_opt_value "$1" "${2:-}")"
        shift 2
        ;;
      --workdir)
        WORKDIR="$(require_opt_value "$1" "${2:-}")"
        GETH_DATADIR="${WORKDIR}/geth"
        UBT_DATADIR="${WORKDIR}/ubtconv"
        LIGHTHOUSE_DATADIR="${WORKDIR}/lighthouse"
        LOG_DIR="${WORKDIR}/logs"
        JWT_SECRET_FILE="${WORKDIR}/jwtsecret.hex"
        GETH_PID_FILE="${WORKDIR}/geth.pid"
        UBT_PID_FILE="${WORKDIR}/ubtconv.pid"
        LIGHTHOUSE_PID_FILE="${WORKDIR}/lighthouse.pid"
        GETH_LOG_FILE="${LOG_DIR}/geth.log"
        UBT_LOG_FILE="${LOG_DIR}/ubtconv.log"
        LIGHTHOUSE_LOG_FILE="${LOG_DIR}/lighthouse.log"
        shift 2
        ;;
      --geth-bin)
        GETH_BIN="$(require_opt_value "$1" "${2:-}")"
        shift 2
        ;;
      --ubtconv-bin)
        UBTCONV_BIN="$(require_opt_value "$1" "${2:-}")"
        shift 2
        ;;
      --lighthouse-bin)
        LIGHTHOUSE_BIN="$(require_opt_value "$1" "${2:-}")"
        shift 2
        ;;
      --geth-http-corsdomain)
        GETH_HTTP_CORSDOMAIN="$(require_opt_value "$1" "${2:-}")"
        shift 2
        ;;
      --geth-http-vhosts)
        GETH_HTTP_VHOSTS="$(require_opt_value "$1" "${2:-}")"
        shift 2
        ;;
      --geth-authrpc-vhosts)
        GETH_AUTHRPC_VHOSTS="$(require_opt_value "$1" "${2:-}")"
        shift 2
        ;;
      --checkpoint-sync-url)
        CHECKPOINT_SYNC_URL="$(require_opt_value "$1" "${2:-}")"
        shift 2
        ;;
      --skip-build)
        BUILD=0
        shift
        ;;
      --detach)
        DETACH=1
        shift
        ;;
      --enable-execution-rpc)
        ENABLE_EXECUTION_RPC=1
        shift
        ;;
      -h|--help)
        usage
        exit 0
        ;;
      *)
        fail "unknown option: $1 (use --help)"
        ;;
    esac
  done
}

main() {
  parse_args "$@"

  case "${ACTION}" in
    up|down|status) ;;
    *) fail "invalid --action: ${ACTION} (expected up|down|status)" ;;
  esac

  require_cmd curl
  require_cmd jq

  case "${ACTION}" in
    status)
      show_status
      exit 0
      ;;
    down)
      stop_pid_file "ubtconv" "${UBT_PID_FILE}"
      stop_pid_file "lighthouse" "${LIGHTHOUSE_PID_FILE}"
      stop_pid_file "geth" "${GETH_PID_FILE}"
      show_status
      exit 0
      ;;
  esac

  ensure_dirs
  ensure_jwt_secret
  build_binaries

  [[ -x "${GETH_BIN}" ]] || fail "geth binary not found or not executable: ${GETH_BIN}"
  [[ -x "${UBTCONV_BIN}" ]] || fail "ubtconv binary not found or not executable: ${UBTCONV_BIN}"
  require_cmd "${LIGHTHOUSE_BIN}"

  start_geth
  start_lighthouse
  start_ubtconv
  show_status

  if (( DETACH == 1 )); then
    log "Started in detached mode"
    exit 0
  fi

  trap cleanup_on_signal INT TERM
  monitor_loop
}

main "$@"
