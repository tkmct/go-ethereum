#!/bin/bash
# Monitor Geth and Lighthouse sync progress
# Polls both clients every 10 seconds and displays status

set -e

GETH_RPC="${GETH_RPC:-http://localhost:8545}"
LIGHTHOUSE_API="${LIGHTHOUSE_API:-http://localhost:5052}"
INTERVAL="${INTERVAL:-10}"

echo "=========================================="
echo "Sync Progress Monitor"
echo "=========================================="
echo "Geth RPC: $GETH_RPC"
echo "Lighthouse API: $LIGHTHOUSE_API"
echo "Poll interval: ${INTERVAL}s"
echo ""
echo "Press Ctrl+C to exit"
echo "=========================================="
echo ""

while true; do
    TIMESTAMP=$(date '+%Y-%m-%d %H:%M:%S')

    # Get Geth sync status
    GETH_SYNC=$(curl -s -X POST "$GETH_RPC" \
        -H "Content-Type: application/json" \
        -d '{"jsonrpc":"2.0","method":"eth_syncing","id":1}' 2>/dev/null || echo '{"error":"connection failed"}')

    # Get Geth block number
    GETH_BLOCK=$(curl -s -X POST "$GETH_RPC" \
        -H "Content-Type: application/json" \
        -d '{"jsonrpc":"2.0","method":"eth_blockNumber","id":1}' 2>/dev/null || echo '{"error":"connection failed"}')

    # Get Geth peer count
    GETH_PEERS=$(curl -s -X POST "$GETH_RPC" \
        -H "Content-Type: application/json" \
        -d '{"jsonrpc":"2.0","method":"net_peerCount","id":1}' 2>/dev/null || echo '{"error":"connection failed"}')

    # Get Lighthouse sync status
    LIGHTHOUSE_SYNC=$(curl -s "$LIGHTHOUSE_API/eth/v1/node/syncing" 2>/dev/null || echo '{"error":"connection failed"}')

    # Parse responses
    SYNCING=$(echo "$GETH_SYNC" | jq -r '.result // "error"')
    BLOCK_HEX=$(echo "$GETH_BLOCK" | jq -r '.result // "0x0"')
    PEERS_HEX=$(echo "$GETH_PEERS" | jq -r '.result // "0x0"')

    # Convert hex to decimal
    BLOCK_NUM=$((BLOCK_HEX))
    PEER_COUNT=$((PEERS_HEX))

    # Parse Lighthouse - handle boolean false properly
    LH_HAS_DATA=$(echo "$LIGHTHOUSE_SYNC" | jq -r 'has("data")')
    LH_IS_SYNCING=$(echo "$LIGHTHOUSE_SYNC" | jq -r '.data.is_syncing')
    LH_HEAD_SLOT=$(echo "$LIGHTHOUSE_SYNC" | jq -r '.data.head_slot // "0"')
    LH_SYNC_DISTANCE=$(echo "$LIGHTHOUSE_SYNC" | jq -r '.data.sync_distance // "0"')
    LH_IS_OPTIMISTIC=$(echo "$LIGHTHOUSE_SYNC" | jq -r '.data.is_optimistic')

    # Display status
    echo "[$TIMESTAMP]"
    echo "  Geth:"
    if [ "$SYNCING" = "false" ]; then
        echo "    Status: Synced"
    elif [ "$SYNCING" = "error" ]; then
        echo "    Status: Connection failed"
    else
        CURRENT=$(echo "$SYNCING" | jq -r '.currentBlock // "?"')
        HIGHEST=$(echo "$SYNCING" | jq -r '.highestBlock // "?"')
        echo "    Status: Syncing ($CURRENT / $HIGHEST)"
    fi
    echo "    Block: $BLOCK_NUM"
    echo "    Peers: $PEER_COUNT"

    echo "  Lighthouse:"
    if [ "$LH_HAS_DATA" != "true" ]; then
        echo "    Status: Connection failed"
    elif [ "$LH_IS_SYNCING" = "false" ]; then
        if [ "$LH_IS_OPTIMISTIC" = "true" ]; then
            echo "    Status: Synced (optimistic - waiting for EL)"
        else
            echo "    Status: Synced"
        fi
    else
        echo "    Status: Syncing (distance: $LH_SYNC_DISTANCE slots)"
    fi
    echo "    Head Slot: $LH_HEAD_SLOT"
    echo ""

    sleep "$INTERVAL"
done
