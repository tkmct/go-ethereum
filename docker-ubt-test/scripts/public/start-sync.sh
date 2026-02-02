#!/bin/bash
# Start UBT test environment with Geth + Lighthouse
# This script builds and starts the Docker containers

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DOCKER_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"

cd "$DOCKER_DIR"

echo "=========================================="
echo "UBT Test Environment - Starting"
echo "=========================================="

# Check if JWT secret exists
if [ ! -f "geth/config/jwt.hex" ]; then
    echo "Generating JWT secret..."
    openssl rand -hex 32 > geth/config/jwt.hex
fi

# Parse arguments
NETWORK="hoodi"
BUILD_ONLY=false
DATA_DIR="/mnt/q/ubt-sync"
METRICS_PORT=6061
PPROF_PORT=6060
CACHE_MB=""
HISTORY_STATE=""
HISTORY_TX=""
HISTORY_LOGS_DISABLE=false
MAXPEERS=""
UBT_SANITY=false
UBT_AUTOCONVERT=true
UBT_COMMIT_INTERVAL=128

while [[ $# -gt 0 ]]; do
    case $1 in
        --mainnet)
            NETWORK="mainnet"
            shift
            ;;
        --hoodi)
            NETWORK="hoodi"
            shift
            ;;
        --build-only)
            BUILD_ONLY=true
            shift
            ;;
        --data-dir)
            DATA_DIR="$2"
            shift 2
            ;;
        --metrics-port)
            METRICS_PORT="$2"
            shift 2
            ;;
        --pprof-port)
            PPROF_PORT="$2"
            shift 2
            ;;
        --cache)
            CACHE_MB="$2"
            shift 2
            ;;
        --history-state)
            HISTORY_STATE="$2"
            shift 2
            ;;
        --history-transactions)
            HISTORY_TX="$2"
            shift 2
            ;;
        --history-logs-disable)
            HISTORY_LOGS_DISABLE=true
            shift
            ;;
        --maxpeers)
            MAXPEERS="$2"
            shift 2
            ;;
        --ubt-sanity)
            UBT_SANITY=true
            shift
            ;;
        --ubt-autoconvert)
            UBT_AUTOCONVERT=true
            shift
            ;;
        --ubt-no-autoconvert)
            UBT_AUTOCONVERT=false
            shift
            ;;
        --ubt-commit)
            UBT_COMMIT_INTERVAL="$2"
            shift 2
            ;;
        *)
            shift
            ;;
    esac
done

echo "Network: $NETWORK"
echo "Data Dir: $DATA_DIR"
echo "Metrics Port: $METRICS_PORT"
echo "Pprof Port: $PPROF_PORT"

CACHE_FLAG=""
if [ -n "$CACHE_MB" ]; then
    CACHE_FLAG="--cache ${CACHE_MB}"
fi

HISTORY_STATE_FLAG=""
if [ -n "$HISTORY_STATE" ]; then
    HISTORY_STATE_FLAG="--history.state ${HISTORY_STATE}"
fi

HISTORY_TX_FLAG=""
if [ -n "$HISTORY_TX" ]; then
    HISTORY_TX_FLAG="--history.transactions ${HISTORY_TX}"
fi

HISTORY_LOGS_DISABLE_FLAG=""
if [ "$HISTORY_LOGS_DISABLE" = true ]; then
    HISTORY_LOGS_DISABLE_FLAG="--history.logs.disable"
fi

MAXPEERS_FLAG=""
if [ -n "$MAXPEERS" ]; then
    MAXPEERS_FLAG="--maxpeers ${MAXPEERS}"
fi

UBT_SANITY_FLAG=""
if [ "$UBT_SANITY" = true ]; then
    UBT_SANITY_FLAG="--ubt.sanity"
fi

UBT_AUTOCONVERT_FLAG=""
if [ "$UBT_AUTOCONVERT" = true ]; then
    UBT_AUTOCONVERT_FLAG="--ubt.sidecar.autoconvert"
fi

UBT_COMMIT_FLAG=""
if [ -n "$UBT_COMMIT_INTERVAL" ]; then
    UBT_COMMIT_FLAG="--ubt.sidecar.commit ${UBT_COMMIT_INTERVAL}"
fi

# Determine checkpoint sync URL based on network
case "$NETWORK" in
    mainnet)
        CHECKPOINT_URL="https://beaconstate.ethstaker.cc"
        ;;
    hoodi)
        CHECKPOINT_URL="https://checkpoint-sync.hoodi.ethpandaops.io"
        ;;
    *)
        CHECKPOINT_URL="https://checkpoint-sync.hoodi.ethpandaops.io"
        ;;
esac

echo "Checkpoint Sync URL: $CHECKPOINT_URL"

# Ensure data directories exist
mkdir -p "${DATA_DIR}/geth-data" "${DATA_DIR}/lighthouse-data"

# Create docker-compose override for network
cat > docker-compose.override.yml << EOF
version: '3.8'
services:
  geth:
    volumes:
      - ${DATA_DIR}/geth-data:/root/.ethereum
      - ./geth/config:/config
    command: >
      --${NETWORK}
      --syncmode full
      --state.scheme path
      --ubt.sidecar
      ${UBT_AUTOCONVERT_FLAG}
      ${UBT_COMMIT_FLAG}
      ${UBT_SANITY_FLAG}
      --cache.preimages
      ${CACHE_FLAG}
      ${HISTORY_STATE_FLAG}
      ${HISTORY_TX_FLAG}
      ${HISTORY_LOGS_DISABLE_FLAG}
      ${MAXPEERS_FLAG}
      --datadir /root/.ethereum
      --authrpc.addr 0.0.0.0
      --authrpc.port 8551
      --authrpc.vhosts "*"
      --authrpc.jwtsecret /config/jwt.hex
      --ipcpath /tmp/geth.ipc
      --http
      --http.addr 0.0.0.0
      --http.port 8545
      --http.vhosts "*"
      --http.corsdomain "*"
      --http.api eth,net,web3,debug,admin,txpool
      --ws
      --ws.addr 0.0.0.0
      --ws.port 8546
      --ws.origins "*"
      --ws.api eth,net,web3,debug
      --metrics
      --metrics.addr 0.0.0.0
      --metrics.port ${METRICS_PORT}
      --pprof
      --pprof.addr 0.0.0.0
      --pprof.port ${PPROF_PORT}
      --log.format json
      --verbosity 4
  lighthouse:
    volumes:
      - ${DATA_DIR}/lighthouse-data:/root/.lighthouse
      - ./geth/config:/config
    command: >
      lighthouse bn
      --network ${NETWORK}
      --execution-endpoint http://geth:8551
      --execution-jwt /config/jwt.hex
      --checkpoint-sync-url ${CHECKPOINT_URL}
      --http
      --http-address 0.0.0.0
      --http-port 5052
      --metrics
      --metrics-address 0.0.0.0
      --metrics-port 5054
      --datadir /root/.lighthouse
EOF

echo ""
echo "Building Geth from source (this may take a few minutes)..."
docker-compose build geth

if [ "$BUILD_ONLY" = true ]; then
    echo "Build complete. Exiting (--build-only specified)."
    exit 0
fi

echo "Starting containers..."
docker-compose up -d

echo ""
echo "=========================================="
echo "UBT Test Environment Started"
echo "=========================================="
echo ""
echo "Network: $NETWORK"
echo "UBT Sidecar Enabled: Yes (--ubt.sidecar)"
echo ""
echo "Services:"
echo "  Geth RPC:      http://localhost:8545"
echo "  Geth WS:       ws://localhost:8546"
echo "  Geth Engine:   http://localhost:8551"
echo "  Geth Metrics:  http://localhost:6060/debug/metrics"
echo "  Lighthouse:    http://localhost:5052"
echo ""
echo "Useful commands:"
echo "  docker-compose logs -f geth       # Follow Geth logs"
echo "  docker-compose logs -f lighthouse # Follow Lighthouse logs"
echo "  go run ./cmd/ubtctl monitor  # Monitor sync progress"
echo "  docker-compose down               # Stop everything"
echo ""
