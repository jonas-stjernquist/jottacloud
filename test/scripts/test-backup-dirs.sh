#!/usr/bin/env bash
# Test: Backup directory registration.
# Verifies that /backup/* subdirectories are registered with jotta-cli.
set -euo pipefail
source "$(dirname "$0")/lib.sh"

echo "=== Test: Backup Directories ==="

if [[ ! -f "$TEST_DIR/.env" ]]; then
    red "ERROR: test/.env not found."
    exit 1
fi

build_image
compose_up

if wait_for_startup 90; then
    pass "Container started"

    # Verify backup path is registered via status.
    output="$(container_exec jotta-cli status 2>&1)" || true
    assert_contains "$output" "/backup/documents" "documents backup registered"
else
    fail "Container did not start"
fi

compose_down
summary
