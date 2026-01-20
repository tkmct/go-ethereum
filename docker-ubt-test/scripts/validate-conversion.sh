#!/bin/bash
# Validate UBT conversion completeness
# Runs a series of checks to verify the conversion was successful

set -e

GETH_RPC="${GETH_RPC:-http://localhost:8545}"

echo "=========================================="
echo "UBT Conversion Validation"
echo "=========================================="
echo ""

# Function to make RPC call
rpc_call() {
    local method="$1"
    local params="$2"
    curl -s -X POST "$GETH_RPC" \
        -H "Content-Type: application/json" \
        -d "{\"jsonrpc\":\"2.0\",\"method\":\"$method\",\"params\":$params,\"id\":1}"
}

# Check 1: Verify Geth is accessible
echo "1. Checking Geth connectivity..."
RESULT=$(rpc_call "web3_clientVersion" "[]")
VERSION=$(echo "$RESULT" | jq -r '.result // "error"')
if [ "$VERSION" = "error" ]; then
    echo "   FAILED: Cannot connect to Geth at $GETH_RPC"
    exit 1
fi
echo "   OK: Connected to $VERSION"
echo ""

# Check 2: Verify sync status
echo "2. Checking sync status..."
RESULT=$(rpc_call "eth_syncing" "[]")
SYNCING=$(echo "$RESULT" | jq -r '.result')
if [ "$SYNCING" != "false" ]; then
    echo "   WARNING: Node is still syncing"
    echo "   Current: $(echo "$SYNCING" | jq -r '.currentBlock // "?"')"
    echo "   Highest: $(echo "$SYNCING" | jq -r '.highestBlock // "?"')"
else
    echo "   OK: Node is fully synced"
fi
echo ""

# Check 3: Get latest block
echo "3. Getting latest block info..."
RESULT=$(rpc_call "eth_getBlockByNumber" "[\"latest\", false]")
BLOCK_NUM=$(echo "$RESULT" | jq -r '.result.number // "0x0"')
BLOCK_HASH=$(echo "$RESULT" | jq -r '.result.hash // "unknown"')
STATE_ROOT=$(echo "$RESULT" | jq -r '.result.stateRoot // "unknown"')
echo "   Block Number: $((BLOCK_NUM))"
echo "   Block Hash: $BLOCK_HASH"
echo "   State Root: $STATE_ROOT"
echo ""

# Check 4: Sample account reads
echo "4. Sampling account reads..."
# Test with well-known addresses (Sepolia faucet, etc.)
TEST_ADDRESSES=(
    "0x0000000000000000000000000000000000000000"  # Zero address
    "0xEeeeeEeeeEeEeeEeEeEeeEEEeeeeEeeeeeeeEEeE"  # ETH placeholder
)

for ADDR in "${TEST_ADDRESSES[@]}"; do
    RESULT=$(rpc_call "eth_getBalance" "[\"$ADDR\", \"latest\"]")
    BALANCE=$(echo "$RESULT" | jq -r '.result // "error"')
    ERROR=$(echo "$RESULT" | jq -r '.error.message // empty')

    if [ -n "$ERROR" ]; then
        echo "   $ADDR: ERROR - $ERROR"
    else
        echo "   $ADDR: Balance $BALANCE"
    fi
done
echo ""

# Check 5: Check storage read (for a contract)
echo "5. Testing storage reads..."
# Use a well-known contract address (can be adjusted based on network)
# For now, just verify the call works
RESULT=$(rpc_call "eth_getStorageAt" "[\"0x0000000000000000000000000000000000000000\", \"0x0\", \"latest\"]")
STORAGE=$(echo "$RESULT" | jq -r '.result // "error"')
ERROR=$(echo "$RESULT" | jq -r '.error.message // empty')
if [ -n "$ERROR" ]; then
    echo "   Storage read: ERROR - $ERROR"
else
    echo "   Storage read: OK (value: $STORAGE)"
fi
echo ""

# Check 6: Verify node info
echo "6. Checking node info..."
RESULT=$(rpc_call "admin_nodeInfo" "[]")
PROTOCOLS=$(echo "$RESULT" | jq -r '.result.protocols | keys | join(", ") // "error"')
echo "   Protocols: $PROTOCOLS"
echo ""

# Summary
echo "=========================================="
echo "Validation Summary"
echo "=========================================="
echo ""
echo "Basic validation complete. For full UBT validation:"
echo ""
echo "1. Use the monitoring tool for sync status:"
echo "   go run ./docker-ubt-test/cmd/ubtctl monitor"
echo ""
echo "2. Run the full validation suite:"
echo "   ./scripts/validate-ubt-complete.sh"
echo ""
