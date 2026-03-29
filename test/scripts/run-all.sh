#!/usr/bin/env bash
# Run all integration tests sequentially.
# Usage: cd test && ./scripts/run-all.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
TEST_DIR="$(dirname "$SCRIPT_DIR")"

cd "$TEST_DIR"

if [[ ! -f .env ]]; then
    echo "ERROR: test/.env not found."
    echo "Copy .env.example to .env and add your JOTTA_TOKEN."
    exit 1
fi

TOTAL_PASS=0
TOTAL_FAIL=0
RESULTS=()

# Clean log files before each suite run.
LOG_FILE="$TEST_DIR/data/container.log"
mkdir -p "$(dirname "$LOG_FILE")"
> "$LOG_FILE"
> "$TEST_DIR/data/jottad/jottabackup.log" 2>/dev/null || true

for script in "$SCRIPT_DIR"/test-*.sh; do
    name="$(basename "$script" .sh)"
    echo ""
    echo "========================================"
    echo "Running: $name"
    echo "========================================"

    if bash "$script"; then
        RESULTS+=("PASS  $name")
        TOTAL_PASS=$((TOTAL_PASS + 1))
    else
        RESULTS+=("FAIL  $name")
        TOTAL_FAIL=$((TOTAL_FAIL + 1))
    fi
done

echo ""
echo "========================================"
echo "Integration Test Summary"
echo "========================================"
for r in "${RESULTS[@]}"; do
    echo "  $r"
done
echo ""
echo "Passed: $TOTAL_PASS  Failed: $TOTAL_FAIL"

if [[ $TOTAL_FAIL -gt 0 ]]; then
    exit 1
fi
