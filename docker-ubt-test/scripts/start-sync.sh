#!/bin/bash
# Start UBT test environment with Geth + Lighthouse
# This script builds and starts the Docker containers

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DOCKER_DIR="$(dirname "$SCRIPT_DIR")"

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
UBT_LOG_INTERVAL=1000

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
        --ubt-log-interval)
            UBT_LOG_INTERVAL="$2"
            shift 2
            ;;
        *)
            shift
            ;;
    esac
done

echo "Network: $NETWORK"
echo "Data Dir: $DATA_DIR"
echo "UBT Log Interval: $UBT_LOG_INTERVAL"

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
      --ubt.sidecar.autoconvert
      --cache.preimages
      --ubt.log-interval ${UBT_LOG_INTERVAL}
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
      --metrics.port 6060
      --pprof
      --pprof.addr 0.0.0.0
      --log.json
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
echo "  go run ./docker-ubt-test/cmd/ubtctl monitor  # Monitor sync progress"
echo "  docker-compose down               # Stop everything"
echo ""
