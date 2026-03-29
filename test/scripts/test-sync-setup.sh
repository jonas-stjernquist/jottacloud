#!/usr/bin/env bash
# Test: Sync directory setup.
# Verifies that /sync is registered as sync root when non-empty.
set -euo pipefail
source "$(dirname "$0")/lib.sh"

echo "=== Test: Sync Setup ==="

if [[ ! -f "$TEST_DIR/.env" ]]; then
    red "ERROR: test/.env not found."
    exit 1
fi

build_image
compose_up

if wait_for_startup 90; then
    pass "Container started"

    logs="$(container_logs 2>&1)"
    assert_contains "$logs" "Adding sync directory" "sync directory setup logged"
else
    fail "Container did not start"
fi

compose_down
summary
