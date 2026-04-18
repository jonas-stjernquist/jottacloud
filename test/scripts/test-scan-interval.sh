#!/usr/bin/env bash
# Test: Scan interval configuration.
# Verifies that JOTTA_CONFIG_SCANINTERVAL is applied.
set -euo pipefail
source "$(dirname "$0")/lib.sh"

echo "=== Test: Scan Interval ==="

if [[ ! -f "$TEST_DIR/.env" ]]; then
    red "ERROR: test/.env not found."
    exit 1
fi

build_image
compose_up

if wait_for_startup 90; then
    pass "Container started"

    logs="$(container_logs 2>&1)"
    assert_contains "$logs" "Setting scan interval to 1m" "scan interval configured"
else
    fail "Container did not start"
fi

compose_down
summary
