#!/usr/bin/env bash
# Test: Adding an extra backup directory.
# Verifies that an extra /backup subdirectory is registered with jotta-cli.
# Reuses persisted credentials from /data/jottad — no clean_data.
set -euo pipefail
source "$(dirname "$0")/lib.sh"

echo "=== Test: Add Extra Backup Directory ==="

if [[ ! -f "$TEST_DIR/.env" ]]; then
    red "ERROR: test/.env not found."
    exit 1
fi

mkdir -p "$TEST_DIR/backup/photos"
trap 'compose_down; rm -rf "$TEST_DIR/backup/photos"' EXIT

build_image
compose_up

if wait_for_startup 90; then
    pass "Container started"

    output="$(container_exec jotta-cli status 2>&1)" || true
    assert_contains "$output" "/backup/photos" "extra backup dir registered"
else
    fail "Container did not start"
fi

summary
