#!/bin/bash
# Build the UBT validation tool
#
# Usage:
#   ./build.sh           # Build binary
#   ./build.sh run       # Build and run

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

# Build from the go-ethereum root to resolve imports
GETH_ROOT="$(cd ../.. && pwd)"

echo "Building ubt-validate..."
cd "$GETH_ROOT"
go build -o docker-ubt-test/cmd/validate/ubt-validate ./docker-ubt-test/cmd/validate

echo "Built: docker-ubt-test/cmd/validate/ubt-validate"

if [ "$1" = "run" ]; then
    echo ""
    echo "Running validation..."
    echo ""
    ./docker-ubt-test/cmd/validate/ubt-validate "${@:2}"
fi
