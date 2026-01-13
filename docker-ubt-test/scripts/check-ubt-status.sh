#!/bin/bash
# Check UBT conversion status from Geth logs and database
# Since there's no RPC endpoint yet, we parse logs

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DOCKER_DIR="$(dirname "$SCRIPT_DIR")"

cd "$DOCKER_DIR"

echo "=========================================="
echo "UBT Conversion Status Check"
echo "=========================================="
echo ""

# Check if container is running
if ! docker ps --format '{{.Names}}' | grep -q "geth-ubt-test"; then
    echo "ERROR: geth-ubt-test container is not running"
    exit 1
fi

# 1. Check snap sync status
echo "1. Snap Sync Status:"
SYNC_RESULT=$(curl -s -X POST http://localhost:8545 \
    -H "Content-Type: application/json" \
    -d '{"jsonrpc":"2.0","method":"eth_syncing","id":1}')

SYNCING=$(echo "$SYNC_RESULT" | jq -r '.result')

if [ "$SYNCING" = "false" ]; then
    echo "   Snap sync: COMPLETE"
else
    CURRENT=$(echo "$SYNCING" | jq -r '.currentBlock')
    HIGHEST=$(echo "$SYNCING" | jq -r '.highestBlock')
    SYNCED_ACCOUNTS=$(echo "$SYNCING" | jq -r '.syncedAccounts')
    echo "   Snap sync: IN PROGRESS"
    echo "   Blocks: $CURRENT / $HIGHEST"
    echo "   Synced accounts: $SYNCED_ACCOUNTS"
    echo ""
    echo "   UBT conversion will start after snap sync completes."
    echo ""
    exit 0
fi
echo ""

# 2. Check for UBT conversion start message
echo "2. UBT Conversion Status (from logs):"

# Look for conversion start
STARTED=$(docker logs geth-ubt-test 2>&1 | grep -i "Started background UBT conversion" | tail -1)
if [ -n "$STARTED" ]; then
    echo "   Started: YES"
    echo "   $STARTED"
else
    echo "   Started: NO (not found in logs)"
    echo ""
    echo "   Possible reasons:"
    echo "   - Snap sync just completed, conversion starting soon"
    echo "   - --ubt.noconversion flag was set"
    echo "   - Error during initialization"
    echo ""
    echo "   Check full logs: docker logs geth-ubt-test 2>&1 | grep -i ubt"
fi
echo ""

# 3. Check for completion
COMPLETED=$(docker logs geth-ubt-test 2>&1 | grep -i "UBT conversion completed" | tail -1)
if [ -n "$COMPLETED" ]; then
    echo "3. Conversion Result: COMPLETED"
    echo "   $COMPLETED"
    echo ""

    # Extract stats if available
    ACCOUNTS=$(echo "$COMPLETED" | grep -oP 'accounts=\K[0-9]+' || echo "?")
    SLOTS=$(echo "$COMPLETED" | grep -oP 'slots=\K[0-9]+' || echo "?")
    UBT_ROOT=$(echo "$COMPLETED" | grep -oP 'ubtRoot=0x[a-fA-F0-9]+' || echo "?")

    echo "   Accounts converted: $ACCOUNTS"
    echo "   Storage slots: $SLOTS"
    echo "   UBT Root: $UBT_ROOT"
else
    # Check for failure
    FAILED=$(docker logs geth-ubt-test 2>&1 | grep -i "UBT conversion failed" | tail -1)
    if [ -n "$FAILED" ]; then
        echo "3. Conversion Result: FAILED"
        echo "   $FAILED"
    else
        # Check for in-progress
        BATCH_COUNT=$(docker logs geth-ubt-test 2>&1 | grep -c "UBT conversion batch committed" || echo "0")
        if [ "$BATCH_COUNT" -gt 0 ]; then
            echo "3. Conversion Result: IN PROGRESS"
            echo "   Batches committed: $BATCH_COUNT"

            # Get latest batch info
            LATEST_BATCH=$(docker logs geth-ubt-test 2>&1 | grep "UBT conversion batch committed" | tail -1)
            echo "   Latest: $LATEST_BATCH"
        else
            echo "3. Conversion Result: NOT STARTED or WAITING"
        fi
    fi
fi
echo ""

# 4. Recent UBT-related logs
echo "4. Recent UBT Log Entries (last 10):"
docker logs geth-ubt-test 2>&1 | grep -iE "ubt|conversion" | tail -10 || echo "   No UBT-related log entries found"
echo ""

echo "=========================================="
echo "Tips:"
echo "  - Use verbosity 4 (--verbosity 4) to see batch commits"
echo "  - Follow live: ./scripts/check-ubt-logs.sh tail"
echo "=========================================="
