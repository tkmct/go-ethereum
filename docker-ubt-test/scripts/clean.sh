#!/bin/bash
# Clean all data and start fresh
# This removes Geth chain data, Lighthouse beacon data, and any generated files

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DOCKER_DIR="$(dirname "$SCRIPT_DIR")"

cd "$DOCKER_DIR"

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
echo "  - Geth chain data (data/geth-data/)"
echo "  - Lighthouse beacon data (data/lighthouse-data/)"
echo "  - Generated override file (docker-compose.override.yml)"
echo ""

read -p "Are you sure? (y/N): " confirm
if [[ "$confirm" != "y" && "$confirm" != "Y" ]]; then
    echo "Aborted."
    exit 0
fi

echo ""
echo "Cleaning data directories..."

rm -rf data/geth-data/*
rm -rf data/lighthouse-data/*
rm -f docker-compose.override.yml

echo "Done. Data cleared."
echo ""
echo "To start fresh:"
echo "  ./scripts/start-sync.sh"
echo ""
