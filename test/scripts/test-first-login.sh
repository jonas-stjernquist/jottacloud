#!/usr/bin/env bash
# Test: Login flow.
# Verifies that the container starts up and reports a logged-in state.
# Reuses persisted credentials from /data/jottad — no clean_data.
set -euo pipefail
source "$(dirname "$0")/lib.sh"

echo "=== Test: First Login ==="

if [[ ! -f "$TEST_DIR/.env" ]]; then
    red "ERROR: test/.env not found. Copy .env.example and add your token."
    exit 1
fi

build_image
compose_up

if wait_for_startup 90; then
    pass "Container started successfully"

    # Check jotta-cli status shows logged-in state.
    status="$(container_exec jotta-cli status 2>&1)" || true
    assert_contains "$status" "on Jottacloud" "jotta-cli reports logged in"

    # Check device name matches.
    assert_contains "$status" "integration-test" "device name is set"
else
    fail "Container did not start"
fi

compose_down
summary
