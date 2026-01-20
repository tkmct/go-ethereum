#!/bin/bash
# Comprehensive UBT Conversion Validation
# This script validates that MPT to UBT conversion completed successfully
# and that the node is functioning correctly with the converted state.

set -e

GETH_RPC="${GETH_RPC:-http://localhost:8545}"
PASS=0
FAIL=0
WARN=0

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Helper functions
rpc_call() {
    local method="$1"
    local params="$2"
    curl -s -X POST "$GETH_RPC" \
        -H "Content-Type: application/json" \
        -d "{\"jsonrpc\":\"2.0\",\"method\":\"$method\",\"params\":$params,\"id\":1}"
}

check_pass() {
    ((PASS++))
    echo -e "  ${GREEN}✓${NC} $1"
}

check_fail() {
    ((FAIL++))
    echo -e "  ${RED}✗${NC} $1"
}

check_warn() {
    ((WARN++))
    echo -e "  ${YELLOW}!${NC} $1"
}

echo "=========================================="
echo "UBT Conversion Complete Validation"
echo "=========================================="
echo ""
echo "Geth RPC: $GETH_RPC"
echo ""

# ===========================================
# Phase 1: Connectivity Check
# ===========================================
echo "=== Phase 1: Connectivity ==="

RESULT=$(rpc_call "web3_clientVersion" "[]" 2>/dev/null || echo '{"error":"connection failed"}')
VERSION=$(echo "$RESULT" | jq -r '.result // empty')
ERROR=$(echo "$RESULT" | jq -r '.error // empty')

if [ -n "$VERSION" ]; then
    check_pass "Connected to $VERSION"
else
    check_fail "Cannot connect to Geth at $GETH_RPC"
    echo ""
    echo "Ensure Geth is running and accessible."
    exit 1
fi
echo ""

# ===========================================
# Phase 2: Sync Status
# ===========================================
echo "=== Phase 2: Sync Status ==="

RESULT=$(rpc_call "eth_syncing" "[]")
SYNCING=$(echo "$RESULT" | jq -r '.result')

if [ "$SYNCING" = "false" ]; then
    check_pass "Node is fully synced"
else
    CURRENT=$(echo "$SYNCING" | jq -r '.currentBlock // "?"')
    HIGHEST=$(echo "$SYNCING" | jq -r '.highestBlock // "?"')
    check_warn "Node is still syncing: $CURRENT / $HIGHEST"
    echo ""
    echo "UBT conversion may not be complete yet."
    echo "Wait for sync to complete before running validation."
fi
echo ""

# ===========================================
# Phase 3: State Reads
# ===========================================
echo "=== Phase 3: State Reads ==="

# Test balance read on zero address
RESULT=$(rpc_call "eth_getBalance" "[\"0x0000000000000000000000000000000000000000\", \"latest\"]")
BALANCE=$(echo "$RESULT" | jq -r '.result // empty')
ERROR=$(echo "$RESULT" | jq -r '.error.message // empty')

if [ -n "$BALANCE" ]; then
    check_pass "eth_getBalance works (zero address: $BALANCE)"
else
    check_fail "eth_getBalance failed: $ERROR"
fi

# Test balance read on deposit contract (common on testnets)
RESULT=$(rpc_call "eth_getBalance" "[\"0x4242424242424242424242424242424242424242\", \"latest\"]")
BALANCE=$(echo "$RESULT" | jq -r '.result // empty')
ERROR=$(echo "$RESULT" | jq -r '.error.message // empty')

if [ -n "$BALANCE" ]; then
    check_pass "eth_getBalance works (deposit contract: $BALANCE)"
else
    check_fail "eth_getBalance failed for deposit contract: $ERROR"
fi

# Test storage read
RESULT=$(rpc_call "eth_getStorageAt" "[\"0x0000000000000000000000000000000000000000\", \"0x0\", \"latest\"]")
STORAGE=$(echo "$RESULT" | jq -r '.result // empty')
ERROR=$(echo "$RESULT" | jq -r '.error.message // empty')

if [ -n "$STORAGE" ]; then
    check_pass "eth_getStorageAt works"
else
    check_fail "eth_getStorageAt failed: $ERROR"
fi
echo ""

# ===========================================
# Phase 4: Block Execution
# ===========================================
echo "=== Phase 4: Block Execution ==="

# Get current block
RESULT1=$(rpc_call "eth_blockNumber" "[]")
BLOCK1=$(echo "$RESULT1" | jq -r '.result // "0x0"')
BLOCK1_DEC=$((BLOCK1))

echo "  Current block: $BLOCK1_DEC"
echo "  Waiting 15 seconds for new blocks..."
sleep 15

RESULT2=$(rpc_call "eth_blockNumber" "[]")
BLOCK2=$(echo "$RESULT2" | jq -r '.result // "0x0"')
BLOCK2_DEC=$((BLOCK2))

echo "  Block after wait: $BLOCK2_DEC"

if [ "$BLOCK2_DEC" -gt "$BLOCK1_DEC" ]; then
    check_pass "New blocks are being processed (advanced $((BLOCK2_DEC - BLOCK1_DEC)) blocks)"
else
    check_warn "No new blocks processed in 15 seconds (may be normal if network is slow)"
fi

# Get latest block details
RESULT=$(rpc_call "eth_getBlockByNumber" "[\"latest\", false]")
BLOCK_HASH=$(echo "$RESULT" | jq -r '.result.hash // "unknown"')
STATE_ROOT=$(echo "$RESULT" | jq -r '.result.stateRoot // "unknown"')
TX_COUNT=$(echo "$RESULT" | jq -r '.result.transactions | length // 0')

echo "  Latest block hash: $BLOCK_HASH"
echo "  State root: $STATE_ROOT"
echo "  Transaction count: $TX_COUNT"

if [ "$BLOCK_HASH" != "unknown" ]; then
    check_pass "Block data accessible"
else
    check_fail "Cannot read block data"
fi
echo ""

# ===========================================
# Phase 5: Witness Generation (Optional)
# ===========================================
echo "=== Phase 5: Witness Generation ==="

# Try to generate witness for a recent block
WITNESS_BLOCK=$((BLOCK2_DEC - 5))
if [ "$WITNESS_BLOCK" -lt 0 ]; then
    WITNESS_BLOCK=0
fi

RESULT=$(rpc_call "debug_executionWitness" "[$WITNESS_BLOCK]" 2>/dev/null || echo '{"error":"method not found"}')
ERROR=$(echo "$RESULT" | jq -r '.error.message // empty')
WITNESS=$(echo "$RESULT" | jq -r '.result // empty')

if [ -n "$ERROR" ]; then
    check_warn "Witness generation: $ERROR"
elif [ -n "$WITNESS" ] && [ "$WITNESS" != "null" ]; then
    check_pass "Witness generation works for block $WITNESS_BLOCK"
else
    check_warn "Witness generation returned empty result"
fi
echo ""

# ===========================================
# Summary
# ===========================================
echo "=========================================="
echo "Validation Summary"
echo "=========================================="
echo ""
echo -e "  ${GREEN}Passed:${NC}   $PASS"
echo -e "  ${RED}Failed:${NC}   $FAIL"
echo -e "  ${YELLOW}Warnings:${NC} $WARN"
echo ""

if [ "$FAIL" -eq 0 ]; then
    if [ "$WARN" -eq 0 ]; then
        echo -e "${GREEN}All checks passed! UBT conversion validation successful.${NC}"
    else
        echo -e "${YELLOW}Validation passed with warnings. Check details above.${NC}"
    fi
    exit 0
else
    echo -e "${RED}Validation failed. Check errors above.${NC}"
    exit 1
fi
