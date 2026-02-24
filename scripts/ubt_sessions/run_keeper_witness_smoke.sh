#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
GETH_BIN="${ROOT_DIR}/build/bin/geth"
UBTCONV_BIN="${ROOT_DIR}/build/bin/ubtconv"

WORKDIR="/tmp/ubt-keeper-smoke-$(date +%Y%m%d-%H%M%S)"
SKIP_BUILD=0
KEEP_DATA=0

GETH_HTTP_PORT=19545
UBT_HTTP_PORT=19560
GETH_P2P_PORT=31345
GETH_AUTHRPC_PORT=19551

GETH_PID=""
UBT_PID=""

usage() {
  cat <<'USAGE'
Usage:
  scripts/ubt_sessions/run_keeper_witness_smoke.sh [options]

Options:
  --workdir DIR     custom working dir
  --skip-build      skip building geth/ubtconv
  --keep-data       keep workdir after exit
  -h, --help        show help
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

dec_to_hex() {
  local d="$1"
  printf '0x%x' "${d}"
}

normalize_hex() {
  local v="${1,,}"
  v="${v#0x}"
  v="${v##0}"
  if [[ -z "${v}" ]]; then
    v="0"
  fi
  printf '0x%s' "${v}"
}

rpc_call() {
  local endpoint="$1"
  local method="$2"
  local params="$3"
  curl -sS -H 'Content-Type: application/json' \
    --data "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"${method}\",\"params\":${params}}" \
    "${endpoint}"
}

rpc_error() {
  local response="$1"
  jq -r '.error.message // empty' <<<"${response}"
}

rpc_result() {
  local response="$1"
  local err
  err="$(rpc_error "${response}")"
  if [[ -n "${err}" ]]; then
    return 1
  fi
  jq -c '.result' <<<"${response}"
}

wait_for_rpc() {
  local endpoint="$1"
  local method="$2"
  local timeout_sec="${3:-120}"
  local start now response
  start="$(date +%s)"
  while true; do
    if response="$(rpc_call "${endpoint}" "${method}" '[]' 2>/dev/null)"; then
      if [[ -z "$(rpc_error "${response}")" ]]; then
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

wait_for_ubt_min_block() {
  local target_block="$1"
  local timeout_sec="${2:-180}"
  local start now status applied
  start="$(date +%s)"
  while true; do
    status="$(rpc_call "http://127.0.0.1:${UBT_HTTP_PORT}" "ubt_status" '[]' || true)"
    applied="$(jq -r '.result.appliedBlock // 0' <<<"${status}" 2>/dev/null || echo "0")"
    if (( applied >= target_block )); then
      pass "UBT applied block reached ${applied} (target=${target_block})"
      return 0
    fi
    now="$(date +%s)"
    if (( now - start >= timeout_sec )); then
      fail "timeout waiting ubt appliedBlock >= ${target_block} (current=${applied})"
    fi
    sleep 1
  done
}

cleanup() {
  set +e
  if [[ -n "${UBT_PID}" ]] && kill -0 "${UBT_PID}" 2>/dev/null; then
    kill "${UBT_PID}" 2>/dev/null
    wait "${UBT_PID}" 2>/dev/null
  fi
  if [[ -n "${GETH_PID}" ]] && kill -0 "${GETH_PID}" 2>/dev/null; then
    kill "${GETH_PID}" 2>/dev/null
    wait "${GETH_PID}" 2>/dev/null
  fi
  if (( KEEP_DATA == 0 )); then
    rm -rf "${WORKDIR}" 2>/dev/null || true
  else
    log "Kept workdir: ${WORKDIR}"
  fi
}

build_binaries() {
  if (( SKIP_BUILD == 1 )); then
    log "Skipping build"
    return
  fi
  log "Building geth and ubtconv"
  (cd "${ROOT_DIR}" && go build -o build/bin/geth ./cmd/geth)
  (cd "${ROOT_DIR}" && go build -o build/bin/ubtconv ./cmd/ubtconv)
}

start_geth() {
  local geth_dir="${WORKDIR}/geth"
  local geth_log="${WORKDIR}/geth.log"
  mkdir -p "${geth_dir}"

  "${GETH_BIN}" \
    --dev --dev.period 1 \
    --syncmode full \
    --datadir "${geth_dir}" \
    --port "${GETH_P2P_PORT}" \
    --authrpc.port "${GETH_AUTHRPC_PORT}" \
    --http --http.addr 127.0.0.1 --http.port "${GETH_HTTP_PORT}" \
    --http.api eth,net,web3,admin,debug,ubt,txpool \
    --ipcdisable \
    --nodiscover --maxpeers 0 \
    --miner.etherbase "0x000000000000000000000000000000000000c0de" \
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

  if ! wait_for_rpc "http://127.0.0.1:${GETH_HTTP_PORT}" "rpc_modules" 120; then
    tail -n 120 "${geth_log}" || true
    fail "geth RPC did not become ready"
  fi
  pass "geth started (pid=${GETH_PID})"
}

start_ubtconv() {
  local ubt_dir="${WORKDIR}/ubtconv"
  local ubt_log="${WORKDIR}/ubtconv.log"
  mkdir -p "${ubt_dir}"

  "${UBTCONV_BIN}" \
    --outbox-rpc-endpoint "http://127.0.0.1:${GETH_HTTP_PORT}" \
    --datadir "${ubt_dir}" \
    --apply-commit-interval 1 \
    --apply-commit-max-latency 1s \
    --query-rpc-enabled \
    --query-rpc-listen-addr "127.0.0.1:${UBT_HTTP_PORT}" \
    --triedb-state-history 90000 \
    --validation-strict=false \
    --execution-class-rpc-enabled \
    >"${ubt_log}" 2>&1 &
  UBT_PID="$!"

  if ! wait_for_rpc "http://127.0.0.1:${UBT_HTTP_PORT}" "ubt_status" 120; then
    tail -n 160 "${ubt_log}" || true
    fail "ubtconv RPC did not become ready"
  fi
  pass "ubtconv started (pid=${UBT_PID})"
}

verify_witness_proofs() {
  local witness_json="$1"
  local state_root accounts_len storage_len

  state_root="$(jq -r '.stateRoot' <<<"${witness_json}")"
  accounts_len="$(jq -r '.accounts | length' <<<"${witness_json}")"
  storage_len="$(jq -r '.storage | length' <<<"${witness_json}")"

  for ((i=0; i<accounts_len; i++)); do
    local key proof alive verify_resp verify_result valid present
    key="$(jq -r ".accounts[${i}].key" <<<"${witness_json}")"
    proof="$(jq -c ".accounts[${i}].proofNodes" <<<"${witness_json}")"
    alive="$(jq -r ".accounts[${i}].alive" <<<"${witness_json}")"

    verify_resp="$(rpc_call "http://127.0.0.1:${UBT_HTTP_PORT}" "ubt_verifyProof" "[\"${state_root}\",\"${key}\",${proof}]")"
    verify_result="$(rpc_result "${verify_resp}")" || fail "ubt_verifyProof(account[${i}]) failed: $(rpc_error "${verify_resp}")"
    valid="$(jq -r '.valid // false' <<<"${verify_result}")"
    present="$(jq -r '.present // false' <<<"${verify_result}")"
    [[ "${valid}" == "true" ]] || fail "account proof invalid at index ${i}"
    if [[ "${alive}" == "true" ]]; then
      [[ "${present}" == "true" ]] || fail "alive account proof not present at index ${i}"
    fi
  done
  pass "validated ${accounts_len} account proofs"

  for ((i=0; i<storage_len; i++)); do
    local key proof verify_resp verify_result valid
    key="$(jq -r ".storage[${i}].key" <<<"${witness_json}")"
    proof="$(jq -c ".storage[${i}].proofNodes" <<<"${witness_json}")"

    verify_resp="$(rpc_call "http://127.0.0.1:${UBT_HTTP_PORT}" "ubt_verifyProof" "[\"${state_root}\",\"${key}\",${proof}]")"
    verify_result="$(rpc_result "${verify_resp}")" || fail "ubt_verifyProof(storage[${i}]) failed: $(rpc_error "${verify_resp}")"
    valid="$(jq -r '.valid // false' <<<"${verify_result}")"
    [[ "${valid}" == "true" ]] || fail "storage proof invalid at index ${i}"
  done
  pass "validated ${storage_len} storage proofs"
}

run_keeper_execution_check() {
  local block_hex="$1"
  local target_addr="$2"
  local witness_resp witness_json status witness_type

  witness_resp="$(rpc_call "http://127.0.0.1:${GETH_HTTP_PORT}" "debug_executionWitnessUBT" "[\"${block_hex}\"]")"
  witness_json="$(rpc_result "${witness_resp}")" || fail "debug_executionWitnessUBT failed: $(rpc_error "${witness_resp}")"

  status="$(jq -r '.status // empty' <<<"${witness_json}")"
  witness_type="$(jq -r '.witnessType // empty' <<<"${witness_json}")"
  [[ "${status}" == "complete" ]] || fail "unexpected witness status: ${status}"
  [[ "${witness_type}" == "proof_pack" ]] || fail "unexpected witness type: ${witness_type}"
  pass "fetched complete proof_pack witness at ${block_hex}"

  verify_witness_proofs "${witness_json}"

  local eth_call_resp ubt_call_resp eth_call ubt_call
  eth_call_resp="$(rpc_call "http://127.0.0.1:${GETH_HTTP_PORT}" "eth_call" "[{\"to\":\"${target_addr}\",\"data\":\"0x\"},\"${block_hex}\"]")"
  ubt_call_resp="$(rpc_call "http://127.0.0.1:${GETH_HTTP_PORT}" "debug_callUBT" "[{\"to\":\"${target_addr}\",\"data\":\"0x\"},\"${block_hex}\",null,null]")"

  eth_call="$(rpc_result "${eth_call_resp}")" || fail "eth_call failed: $(rpc_error "${eth_call_resp}")"
  ubt_call="$(rpc_result "${ubt_call_resp}")" || fail "debug_callUBT failed: $(rpc_error "${ubt_call_resp}")"

  if [[ "$(normalize_hex "${eth_call}")" != "$(normalize_hex "${ubt_call}")" ]]; then
    fail "keeper execution mismatch at ${target_addr}: eth_call=${eth_call} debug_callUBT=${ubt_call}"
  fi
  pass "keeper execution parity passed at ${target_addr} (eth_call == debug_callUBT == ${eth_call})"
}

main() {
  while [[ $# -gt 0 ]]; do
    case "$1" in
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

  trap cleanup EXIT
  mkdir -p "${WORKDIR}"

  build_binaries
  [[ -x "${GETH_BIN}" ]] || fail "geth binary missing: ${GETH_BIN}"
  [[ -x "${UBTCONV_BIN}" ]] || fail "ubtconv binary missing: ${UBTCONV_BIN}"

  log "Workdir: ${WORKDIR}"

  start_geth
  start_ubtconv
  wait_for_ubt_min_block 2 240

  status_resp="$(rpc_call "http://127.0.0.1:${UBT_HTTP_PORT}" "ubt_status" '[]')"
  applied_block_dec="$(jq -r '.result.appliedBlock // 0' <<<"${status_resp}")"
  [[ "${applied_block_dec}" != "0" ]] || fail "invalid appliedBlock from ubt_status"
  target_block_hex="$(dec_to_hex "${applied_block_dec}")"

  witness_resp="$(rpc_call "http://127.0.0.1:${GETH_HTTP_PORT}" "debug_executionWitnessUBT" "[\"${target_block_hex}\"]")"
  witness_json="$(rpc_result "${witness_resp}")" || fail "debug_executionWitnessUBT failed: $(rpc_error "${witness_resp}")"
  target_addr=""
  while IFS= read -r candidate; do
    [[ -n "${candidate}" ]] || continue
    call_probe="$(rpc_call "http://127.0.0.1:${GETH_HTTP_PORT}" "eth_call" "[{\"to\":\"${candidate}\",\"data\":\"0x\"},\"${target_block_hex}\"]")"
    if [[ -z "$(rpc_error "${call_probe}")" ]]; then
      target_addr="${candidate}"
      break
    fi
  done < <(printf '%s\n' "0x000000000000000000000000000000000000c0de"; jq -r '.accounts[].address' <<<"${witness_json}")
  [[ -n "${target_addr}" ]] || fail "failed to find a non-reverting execution target"
  pass "selected keeper execution target ${target_addr} at block ${target_block_hex}"

  run_keeper_execution_check "${target_block_hex}" "${target_addr}"

  pass "keeper witness smoke completed"
}

main "$@"
