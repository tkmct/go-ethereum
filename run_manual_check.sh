#!/usr/bin/env bash
set -euo pipefail

GETH_RPC="http://127.0.0.1:8545"
UBT_RPC="http://127.0.0.1:8560"
CHECK_ADDRESS=""
STORAGE_ADDRESS=""
STORAGE_SLOT=""
CHECK_STORAGE=0
CHECK_PROOF=1
ADDRESS_EXPLICIT=0
EXPECT_EXECUTION_ENABLED=0
CHECK_BLOCK_HEX="latest"

usage() {
  cat <<'USAGE'
Usage:
  ./run_manual_check.sh [options]

Options:
  --geth URL              geth HTTP RPC endpoint (default: http://127.0.0.1:8545)
  --ubt URL               ubtconv query RPC endpoint (default: http://127.0.0.1:8560)
  --address ADDR          address used for balance/proof/code checks
  --storage-address ADDR  contract address for storage equivalence check (optional)
  --storage-slot SLOT     storage slot key for storage equivalence check (optional)
  --no-proof              skip debug_getUBTProof check
  --execution-enabled     expect debug_callUBT/debug_executionWitnessUBT to succeed
  --execution-disabled    expect debug_callUBT/debug_executionWitnessUBT to return disabled error (default)
  -h, --help              show this help
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

warn() {
  printf '[WARN] %s\n' "$*" >&2
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || fail "missing required command: $1"
}

rpc_call() {
  local endpoint="$1"
  local method="$2"
  local params="$3"
  curl -sS -H 'Content-Type: application/json' \
    --data "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"${method}\",\"params\":${params}}" \
    "${endpoint}"
}

rpc_result() {
  local response="$1"
  local err
  err="$(jq -r '.error.message // empty' <<<"${response}")"
  if [[ -n "${err}" ]]; then
    return 1
  fi
  jq -c '.result' <<<"${response}"
}

rpc_error() {
  local response="$1"
  jq -r '.error.message // empty' <<<"${response}"
}

normalize_hex() {
  local v="${1,,}"
  v="${v#0x}"
  v="${v##0}"
  if [[ -z "${v}" ]]; then
    v="0"
  fi
  printf '%s' "${v}"
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

is_balance_too_large_for_ubt() {
  local v
  v="$(normalize_hex "$1")"
  if (( ${#v} > 32 )); then
    return 0
  fi
  return 1
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --geth)
      GETH_RPC="$2"
      shift 2
      ;;
    --ubt)
      UBT_RPC="$2"
      shift 2
      ;;
    --address)
      CHECK_ADDRESS="$2"
      ADDRESS_EXPLICIT=1
      shift 2
      ;;
    --storage-address)
      STORAGE_ADDRESS="$2"
      CHECK_STORAGE=1
      shift 2
      ;;
    --storage-slot)
      STORAGE_SLOT="$2"
      CHECK_STORAGE=1
      shift 2
      ;;
    --no-proof)
      CHECK_PROOF=0
      shift
      ;;
    --execution-enabled)
      EXPECT_EXECUTION_ENABLED=1
      shift
      ;;
    --execution-disabled)
      EXPECT_EXECUTION_ENABLED=0
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

log "Using geth RPC: ${GETH_RPC}"
log "Using ubt RPC:  ${UBT_RPC}"
if (( EXPECT_EXECUTION_ENABLED == 1 )); then
  log "Execution RPC expectation: enabled (success expected)"
else
  log "Execution RPC expectation: disabled (disabled error expected)"
fi

if [[ -z "${CHECK_ADDRESS}" ]]; then
  log "Resolving default check address from eth_accounts/eth_coinbase"
  accounts_resp="$(rpc_call "${GETH_RPC}" "eth_accounts" '[]')"
  accounts_err="$(rpc_error "${accounts_resp}")"
  if [[ -z "${accounts_err}" ]]; then
    CHECK_ADDRESS="$(jq -r '.result[0] // empty' <<<"${accounts_resp}")"
  fi
  if [[ -z "${CHECK_ADDRESS}" ]]; then
    coinbase_resp="$(rpc_call "${GETH_RPC}" "eth_coinbase" '[]')"
    coinbase_err="$(rpc_error "${coinbase_resp}")"
    if [[ -z "${coinbase_err}" ]]; then
      CHECK_ADDRESS="$(jq -r '.result // empty' <<<"${coinbase_resp}")"
    fi
  fi
fi

[[ -n "${CHECK_ADDRESS}" ]] || fail "could not determine check address; pass --address explicitly"

if (( ADDRESS_EXPLICIT == 0 )); then
  candidate_bal_resp="$(rpc_call "${GETH_RPC}" "eth_getBalance" "[\"${CHECK_ADDRESS}\",\"latest\"]")"
  candidate_bal_hex="$(jq -r '.result // empty' <<<"${candidate_bal_resp}")"
  if [[ -n "${candidate_bal_hex}" ]] && is_balance_too_large_for_ubt "${candidate_bal_hex}"; then
    fallback_addr="0x0000000000000000000000000000000000000001"
    warn "auto-selected address ${CHECK_ADDRESS} has >128-bit balance (${candidate_bal_hex}); using ${fallback_addr} for UBT parity check"
    CHECK_ADDRESS="${fallback_addr}"
  fi
fi

log "Check address: ${CHECK_ADDRESS}"

# 1. ubt_latestSeq increments
seq1_resp="$(rpc_call "${GETH_RPC}" "ubt_latestSeq" '[]')"
seq1_hex="$(jq -r '.result // empty' <<<"${seq1_resp}")"
[[ -n "${seq1_hex}" ]] || fail "ubt_latestSeq initial call failed: $(rpc_error "${seq1_resp}")"
seq1="$(hex_to_dec "${seq1_hex}")"
sleep 3
seq2_resp="$(rpc_call "${GETH_RPC}" "ubt_latestSeq" '[]')"
seq2_hex="$(jq -r '.result // empty' <<<"${seq2_resp}")"
[[ -n "${seq2_hex}" ]] || fail "ubt_latestSeq second call failed: $(rpc_error "${seq2_resp}")"
seq2="$(hex_to_dec "${seq2_hex}")"
if (( seq2 < seq1 )); then
  fail "ubt_latestSeq moved backward: ${seq1} -> ${seq2}"
fi
if (( seq2 == seq1 )); then
  warn "ubt_latestSeq did not increase within 3s (${seq1_hex}); chain may be idle"
else
  pass "ubt_latestSeq increased: ${seq1_hex} -> ${seq2_hex}"
fi

# 2. ubt_status progresses
status1_resp="$(rpc_call "${UBT_RPC}" "ubt_status" '[]')"
status1="$(rpc_result "${status1_resp}")" || fail "ubt_status initial call failed: $(rpc_error "${status1_resp}")"
applied_seq_1="$(jq -r '.appliedSeq // 0' <<<"${status1}")"
applied_block_1="$(jq -r '.appliedBlock // 0' <<<"${status1}")"
sleep 3
status2_resp="$(rpc_call "${UBT_RPC}" "ubt_status" '[]')"
status2="$(rpc_result "${status2_resp}")" || fail "ubt_status second call failed: $(rpc_error "${status2_resp}")"
applied_seq_2="$(jq -r '.appliedSeq // 0' <<<"${status2}")"
applied_block_2="$(jq -r '.appliedBlock // 0' <<<"${status2}")"
if (( applied_seq_2 < applied_seq_1 )) || (( applied_block_2 < applied_block_1 )); then
  fail "ubt_status moved backward: seq ${applied_seq_1}->${applied_seq_2}, block ${applied_block_1}->${applied_block_2}"
fi
if (( applied_seq_2 == applied_seq_1 )) && (( applied_block_2 == applied_block_1 )); then
  warn "ubt_status did not advance within 3s (seq=${applied_seq_1}, block=${applied_block_1})"
else
  pass "ubt_status progressed: seq ${applied_seq_1}->${applied_seq_2}, block ${applied_block_1}->${applied_block_2}"
fi

if (( applied_block_2 > 0 )); then
  CHECK_BLOCK_HEX="$(dec_to_hex "${applied_block_2}")"
fi
log "Using block selector for parity checks: ${CHECK_BLOCK_HEX}"

status_exec_enabled="$(jq -r '.executionClassRPCEnabled // false' <<<"${status2}")"
if (( EXPECT_EXECUTION_ENABLED == 1 )); then
  [[ "${status_exec_enabled}" == "true" ]] || fail "ubt_status.executionClassRPCEnabled=false; start ubtconv with --execution-class-rpc-enabled"
  pass "ubt_status.executionClassRPCEnabled is true"
else
  if [[ "${status_exec_enabled}" == "true" ]]; then
    warn "ubt_status.executionClassRPCEnabled=true while disabled mode checks were requested"
  fi
fi

# 3. Balance equivalence
eth_bal_resp="$(rpc_call "${GETH_RPC}" "eth_getBalance" "[\"${CHECK_ADDRESS}\",\"${CHECK_BLOCK_HEX}\"]")"
eth_bal="$(rpc_result "${eth_bal_resp}")" || fail "eth_getBalance failed: $(rpc_error "${eth_bal_resp}")"
ubt_bal_resp="$(rpc_call "${GETH_RPC}" "debug_getUBTBalance" "[\"${CHECK_ADDRESS}\",\"${CHECK_BLOCK_HEX}\"]")"
ubt_bal="$(rpc_result "${ubt_bal_resp}")" || fail "debug_getUBTBalance failed: $(rpc_error "${ubt_bal_resp}")"
if [[ "$(normalize_hex "${eth_bal}")" != "$(normalize_hex "${ubt_bal}")" ]]; then
  fail "balance mismatch for ${CHECK_ADDRESS}: eth=${eth_bal} ubt=${ubt_bal}"
fi
pass "balance match for ${CHECK_ADDRESS}: ${eth_bal}"

# 4. Code behavior on selected address (typically EOA in --dev)
code_resp="$(rpc_call "${GETH_RPC}" "debug_getUBTCode" "[\"${CHECK_ADDRESS}\",\"${CHECK_BLOCK_HEX}\"]")"
code_err="$(rpc_error "${code_resp}")"
if [[ -n "${code_err}" ]]; then
  warn "debug_getUBTCode returned error for ${CHECK_ADDRESS}: ${code_err}"
else
  code_val="$(jq -r '.result' <<<"${code_resp}")"
  if [[ "${code_val}" == "0x" ]]; then
    pass "debug_getUBTCode returned empty code for ${CHECK_ADDRESS}"
  else
    warn "debug_getUBTCode returned non-empty code for ${CHECK_ADDRESS}: ${code_val}"
  fi
fi

# 5. Optional storage equivalence check
if (( CHECK_STORAGE == 1 )); then
  [[ -n "${STORAGE_ADDRESS}" && -n "${STORAGE_SLOT}" ]] || fail "--storage-address and --storage-slot must be set together"
  eth_st_resp="$(rpc_call "${GETH_RPC}" "eth_getStorageAt" "[\"${STORAGE_ADDRESS}\",\"${STORAGE_SLOT}\",\"${CHECK_BLOCK_HEX}\"]")"
  eth_st="$(rpc_result "${eth_st_resp}")" || fail "eth_getStorageAt failed: $(rpc_error "${eth_st_resp}")"
  ubt_st_resp="$(rpc_call "${GETH_RPC}" "debug_getUBTStorageAt" "[\"${STORAGE_ADDRESS}\",\"${STORAGE_SLOT}\",\"${CHECK_BLOCK_HEX}\"]")"
  ubt_st="$(rpc_result "${ubt_st_resp}")" || fail "debug_getUBTStorageAt failed: $(rpc_error "${ubt_st_resp}")"
  if [[ "${eth_st,,}" != "${ubt_st,,}" ]]; then
    fail "storage mismatch at ${STORAGE_ADDRESS} slot ${STORAGE_SLOT}: eth=${eth_st} ubt=${ubt_st}"
  fi
  pass "storage match at ${STORAGE_ADDRESS} slot ${STORAGE_SLOT}"
else
  warn "storage check skipped (pass --storage-address and --storage-slot to enable)"
fi

# 6. Proof check (address-based API)
if (( CHECK_PROOF == 1 )); then
  proof_ok=0
  proof_attempts=3
  for attempt in $(seq 1 "${proof_attempts}"); do
    proof_resp="$(rpc_call "${GETH_RPC}" "debug_getUBTProof" "[\"${CHECK_ADDRESS}\",[],\"${CHECK_BLOCK_HEX}\"]")"
    proof_err="$(rpc_error "${proof_resp}")"
    if [[ -z "${proof_err}" ]]; then
      acct_proof_len="$(jq -r '.result.accountProof | length' <<<"${proof_resp}")"
      if [[ "${acct_proof_len}" != "0" ]] && [[ "${acct_proof_len}" != "null" ]]; then
        pass "debug_getUBTProof returned accountProof with ${acct_proof_len} nodes"
        proof_ok=1
        break
      fi
      proof_err="empty accountProof"
    fi

    if (( attempt < proof_attempts )); then
      warn "debug_getUBTProof attempt ${attempt}/${proof_attempts} failed (${proof_err}); retrying"
      status_retry_resp="$(rpc_call "${UBT_RPC}" "ubt_status" '[]')"
      status_retry="$(rpc_result "${status_retry_resp}" || true)"
      if [[ -n "${status_retry}" ]]; then
        applied_block_retry="$(jq -r '.appliedBlock // 0' <<<"${status_retry}")"
        if (( applied_block_retry > 0 )); then
          CHECK_BLOCK_HEX="$(dec_to_hex "${applied_block_retry}")"
          log "Retrying proof check at block selector ${CHECK_BLOCK_HEX}"
        fi
      fi
      sleep 1
    fi
  done
  if (( proof_ok == 0 )); then
    fail "debug_getUBTProof failed after ${proof_attempts} attempts"
  fi
fi

# 7. Execution-class RPC checks
if (( EXPECT_EXECUTION_ENABLED == 1 )); then
  call_resp="$(rpc_call "${GETH_RPC}" "debug_callUBT" "[{\"to\":\"${CHECK_ADDRESS}\",\"data\":\"0x\"},\"${CHECK_BLOCK_HEX}\",null,null]")"
  call_result="$(rpc_result "${call_resp}")" || fail "debug_callUBT failed: $(rpc_error "${call_resp}")"
  pass "debug_callUBT succeeded: ${call_result}"

  witness_resp="$(rpc_call "${GETH_RPC}" "debug_executionWitnessUBT" "[\"${CHECK_BLOCK_HEX}\"]")"
  witness_result="$(rpc_result "${witness_resp}")" || fail "debug_executionWitnessUBT failed: $(rpc_error "${witness_resp}")"
  witness_status="$(jq -r '.status // empty' <<<"${witness_result}")"
  [[ -n "${witness_status}" ]] || fail "debug_executionWitnessUBT returned no status field: ${witness_result}"
  pass "debug_executionWitnessUBT succeeded with status=${witness_status}"
else
  call_resp="$(rpc_call "${GETH_RPC}" "debug_callUBT" "[{\"to\":\"${CHECK_ADDRESS}\",\"data\":\"0x\"},\"${CHECK_BLOCK_HEX}\",null,null]")"
  call_err="$(rpc_error "${call_resp}")"
  if [[ -z "${call_err}" ]]; then
    fail "debug_callUBT unexpectedly succeeded; expected disabled error"
  elif [[ "${call_err}" == *"execution-class RPC disabled"* ]]; then
    pass "debug_callUBT returned expected disabled error"
  else
    fail "debug_callUBT returned unexpected error: ${call_err}"
  fi

  witness_resp="$(rpc_call "${GETH_RPC}" "debug_executionWitnessUBT" "[\"${CHECK_BLOCK_HEX}\"]")"
  witness_err="$(rpc_error "${witness_resp}")"
  if [[ -z "${witness_err}" ]]; then
    fail "debug_executionWitnessUBT unexpectedly succeeded; expected disabled error"
  elif [[ "${witness_err}" == *"execution-class RPC disabled"* ]]; then
    pass "debug_executionWitnessUBT returned expected disabled error"
  else
    fail "debug_executionWitnessUBT returned unexpected error: ${witness_err}"
  fi
fi

log "Automated manual-acceptance subset completed."
log "For deterministic restart/reorg coverage, run: scripts/ubt_sessions/run_sessions.sh --session all"
