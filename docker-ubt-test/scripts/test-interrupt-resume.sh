#!/bin/bash
# Test UBT conversion interrupt and resume behavior
# Simulates node restart during conversion and verifies resume

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DOCKER_DIR="$(dirname "$SCRIPT_DIR")"

cd "$DOCKER_DIR"

echo "=========================================="
echo "UBT Interrupt/Resume Test"
echo "=========================================="
echo ""

# Function to get sync progress
get_sync_progress() {
    curl -s -X POST "http://localhost:8545" \
        -H "Content-Type: application/json" \
        -d '{"jsonrpc":"2.0","method":"eth_syncing","id":1}' | jq -r '.result'
}

# Function to get block number
get_block_number() {
    RESULT=$(curl -s -X POST "http://localhost:8545" \
        -H "Content-Type: application/json" \
        -d '{"jsonrpc":"2.0","method":"eth_blockNumber","id":1}')
    BLOCK_HEX=$(echo "$RESULT" | jq -r '.result // "0x0"')
    echo $((BLOCK_HEX))
}

# Check if containers are running
if ! docker ps --format '{{.Names}}' | grep -q "geth-ubt-test"; then
    echo "ERROR: Containers not running. Start with: ./scripts/start-sync.sh"
    exit 1
fi

echo "Phase 1: Recording initial state..."
echo ""

BLOCK_BEFORE=$(get_block_number)
echo "Current block: $BLOCK_BEFORE"

# Get UBT-related log count before
UBT_LOGS_BEFORE=$(docker logs geth-ubt-test 2>&1 | grep -ciE "ubt|conversion" || echo "0")
echo "UBT log entries: $UBT_LOGS_BEFORE"
echo ""

echo "Phase 2: Stopping Geth gracefully..."
echo ""

docker-compose stop geth
sleep 5

echo "Phase 3: Restarting Geth..."
echo ""

docker-compose start geth

# Wait for Geth to be ready
echo "Waiting for Geth to be ready..."
for i in {1..30}; do
    if curl -s -X POST "http://localhost:8545" \
        -H "Content-Type: application/json" \
        -d '{"jsonrpc":"2.0","method":"web3_clientVersion","id":1}' | grep -q "result"; then
        echo "Geth is ready after ${i} seconds"
        break
    fi
    sleep 1
done
echo ""

echo "Phase 4: Verifying resume..."
echo ""

BLOCK_AFTER=$(get_block_number)
echo "Current block: $BLOCK_AFTER"

UBT_LOGS_AFTER=$(docker logs geth-ubt-test 2>&1 | grep -ciE "ubt|conversion" || echo "0")
echo "UBT log entries: $UBT_LOGS_AFTER"

echo ""
echo "=========================================="
echo "Test Results"
echo "=========================================="
echo ""

if [ "$BLOCK_AFTER" -ge "$BLOCK_BEFORE" ]; then
    echo "PASS: Block number maintained or increased ($BLOCK_BEFORE -> $BLOCK_AFTER)"
else
    echo "FAIL: Block number decreased ($BLOCK_BEFORE -> $BLOCK_AFTER)"
fi

if [ "$UBT_LOGS_AFTER" -ge "$UBT_LOGS_BEFORE" ]; then
    echo "PASS: UBT processing continued ($UBT_LOGS_BEFORE -> $UBT_LOGS_AFTER entries)"
else
    echo "INFO: UBT log count: $UBT_LOGS_BEFORE -> $UBT_LOGS_AFTER"
fi

# Check for resume-related log messages
echo ""
echo "Checking for resume-related logs..."
docker logs geth-ubt-test 2>&1 | tail -50 | grep -iE "resum|continu|progress|started" || echo "No explicit resume messages found"

echo ""
echo "Test complete. Check logs for detailed verification:"
echo "  docker logs geth-ubt-test 2>&1 | tail -100"
echo ""
