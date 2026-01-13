#!/bin/bash
# Check Geth logs for UBT conversion messages
# Filters logs for UBT-related entries

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DOCKER_DIR="$(dirname "$SCRIPT_DIR")"

cd "$DOCKER_DIR"

echo "=========================================="
echo "UBT Conversion Log Monitor"
echo "=========================================="
echo ""

# Check if container is running
if ! docker ps --format '{{.Names}}' | grep -q "geth-ubt-test"; then
    echo "ERROR: geth-ubt-test container is not running"
    echo "Start the environment with: ./scripts/start-sync.sh"
    exit 1
fi

MODE="${1:-tail}"

case "$MODE" in
    tail)
        echo "Following UBT-related logs (Ctrl+C to exit)..."
        echo ""
        docker logs -f geth-ubt-test 2>&1 | grep --line-buffered -iE "ubt|binary.?trie|conversion"
        ;;
    all)
        echo "All UBT-related log entries:"
        echo ""
        docker logs geth-ubt-test 2>&1 | grep -iE "ubt|binary.?trie|conversion"
        ;;
    errors)
        echo "UBT error entries:"
        echo ""
        docker logs geth-ubt-test 2>&1 | grep -iE "(ubt|binary.?trie|conversion).*(error|fail|panic)"
        ;;
    progress)
        echo "UBT progress entries:"
        echo ""
        docker logs geth-ubt-test 2>&1 | grep -iE "ubt.*(progress|account|slot|batch|commit)"
        ;;
    *)
        echo "Usage: $0 [tail|all|errors|progress]"
        echo ""
        echo "Modes:"
        echo "  tail     - Follow logs in real-time (default)"
        echo "  all      - Show all UBT-related entries"
        echo "  errors   - Show only error entries"
        echo "  progress - Show progress-related entries"
        exit 1
        ;;
esac
