#!/usr/bin/env bash
# Test: Sync file update.
# Stamps sync-test.txt with a timestamp, verifies jottad uploads the change,
# then reverts to the original content and verifies that revert is also synced.
# Leaves sync-test.txt unchanged after the test.
set -euo pipefail
source "$(dirname "$0")/lib.sh"

echo "=== Test: Sync File Update ==="

if [[ ! -f "$TEST_DIR/.env" ]]; then
    red "ERROR: test/.env not found."
    exit 1
fi

SYNC_FILE="$TEST_DIR/sync/sync-test.txt"
JOTTAD_LOG="$TEST_DIR/data/jottad/jottabackup.log"
TIMESTAMP="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
original_content="$(cat "$SYNC_FILE")"

trap 'compose_down; printf "%s" "$original_content" > "$SYNC_FILE"' EXIT

# Note current log size so we only inspect new entries from this test run.
log_offset="$(wc -l < "$JOTTAD_LOG" 2>/dev/null || echo 0)"

# Stamp the file with a unique timestamp so jottad detects a change.
printf 'Test file for Jottacloud sync integration testing.\n\nLast updated: %s\n' "$TIMESTAMP" > "$SYNC_FILE"

build_image
compose_up

if ! wait_for_startup 90; then
    fail "Container did not start"
    summary
fi

pass "Container started"

# The initial full-check runs during startup and should detect the changed file.
echo "Waiting for modified sync-test.txt to be uploaded (up to 30s)..."
elapsed=0; uploaded=false
while [ $elapsed -lt 30 ]; do
    tail -n +"$((log_offset + 1))" "$JOTTAD_LOG" 2>/dev/null \
        | grep -qF "1 changed" && { uploaded=true; break; }
    sleep 2; elapsed=$((elapsed + 2))
done

if $uploaded; then
    pass "modified sync-test.txt was uploaded"
else
    fail "modified sync-test.txt not uploaded within 30s"
    echo "  jottad log (new entries):"
    tail -n +"$((log_offset + 1))" "$JOTTAD_LOG" 2>/dev/null \
        | grep -E "sync|changed|upload" | tail -20 || true
fi

# Revert to original content; the filesystem watcher triggers a new sync.
log_offset="$(wc -l < "$JOTTAD_LOG" 2>/dev/null || echo 0)"
printf '%s' "$original_content" > "$SYNC_FILE"

echo "Waiting for reverted sync-test.txt to be uploaded (up to 60s)..."
elapsed=0; reverted=false
while [ $elapsed -lt 60 ]; do
    # fsw-triggered uploads log "Finished upload" rather than "1 changed".
    tail -n +"$((log_offset + 1))" "$JOTTAD_LOG" 2>/dev/null \
        | grep -qF "Finished upload" && { reverted=true; break; }
    sleep 2; elapsed=$((elapsed + 2))
done

if $reverted; then
    pass "reverted sync-test.txt was uploaded"
else
    fail "reverted sync-test.txt not uploaded within 60s"
fi

summary
