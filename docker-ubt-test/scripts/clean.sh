#!/bin/bash
# Clean all data and start fresh
# This removes Geth chain data, Lighthouse beacon data, and any generated files

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DOCKER_DIR="$(dirname "$SCRIPT_DIR")"
DATA_DIR="/mnt/q/ubt-sync"

cd "$DOCKER_DIR"

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --data-dir)
            DATA_DIR="$2"
            shift 2
            ;;
        *)
            shift
            ;;
    esac
done

echo "=========================================="
echo "UBT Test Environment - Clean"
echo "=========================================="
echo ""

# Check if containers are running
if docker ps --format '{{.Names}}' | grep -qE "geth-ubt-test|lighthouse-ubt-test"; then
    echo "Stopping running containers..."
    docker-compose down
fi

echo ""
echo "This will delete:"
echo "  - Geth chain data (${DATA_DIR}/geth-data/)"
echo "  - Lighthouse beacon data (${DATA_DIR}/lighthouse-data/)"
echo "  - Generated override file (docker-compose.override.yml)"
echo ""

read -p "Are you sure? (y/N): " confirm
if [[ "$confirm" != "y" && "$confirm" != "Y" ]]; then
    echo "Aborted."
    exit 0
fi

echo ""
echo "Cleaning data directories..."

rm -rf "${DATA_DIR}/geth-data"/*
rm -rf "${DATA_DIR}/lighthouse-data"/*
rm -f docker-compose.override.yml

echo "Done. Data cleared."
echo ""
echo "To start fresh:"
echo "  ./scripts/start-sync.sh --data-dir ${DATA_DIR}"
echo ""
