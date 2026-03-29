#!/usr/bin/env bash
# Test: Sync add and delete file.
# Creates a new file in /sync, verifies jottad uploads it, then deletes it
# and verifies the deletion is also synced.
set -euo pipefail
source "$(dirname "$0")/lib.sh"

echo "=== Test: Sync Add and Delete File ==="

if [[ ! -f "$TEST_DIR/.env" ]]; then
    red "ERROR: test/.env not found."
    exit 1
fi

NEW_FILE="$TEST_DIR/sync/sync-test-newfile.txt"
JOTTAD_LOG="$TEST_DIR/data/jottad/jottabackup.log"

# Clean up any leftover file from a previous failed run.
rm -f "$NEW_FILE"
trap 'compose_down; rm -f "$NEW_FILE"' EXIT

log_offset="$(wc -l < "$JOTTAD_LOG" 2>/dev/null || echo 0)"

build_image
compose_up

if ! wait_for_startup 90; then
    fail "Container did not start"
    summary
fi

pass "Container started"

# Create the new file — the filesystem watcher triggers a sync immediately.
printf 'New file for sync add/delete integration test.\n' > "$NEW_FILE"

echo "Waiting for new file to be synced (up to 60s)..."
elapsed=0; added=false
while [ $elapsed -lt 60 ]; do
    # fsw-triggered uploads log "Finished upload" rather than "1 added".
    tail -n +"$((log_offset + 1))" "$JOTTAD_LOG" 2>/dev/null \
        | grep -qF "Finished upload" && { added=true; break; }
    sleep 2; elapsed=$((elapsed + 2))
done

if $added; then
    pass "new file was synced to Jottacloud"
else
    fail "new file was not synced within 60s"
    echo "  jottad log (new entries):"
    tail -n +"$((log_offset + 1))" "$JOTTAD_LOG" 2>/dev/null \
        | grep -E "sync|added|upload" | tail -20 || true
fi

# Delete the file and verify the deletion is synced.
# Deletions are not picked up by fsw — force an immediate rescan via sync start.
log_offset="$(wc -l < "$JOTTAD_LOG" 2>/dev/null || echo 0)"
rm -f "$NEW_FILE"
container_exec jotta-cli sync start 2>/dev/null || true

echo "Waiting for file deletion to be synced (up to 60s)..."
elapsed=0; deleted=false
while [ $elapsed -lt 60 ]; do
    tail -n +"$((log_offset + 1))" "$JOTTAD_LOG" 2>/dev/null \
        | grep -qF "Starting delete-remote-file" && { deleted=true; break; }
    sleep 2; elapsed=$((elapsed + 2))
done

if $deleted; then
    pass "file deletion was synced to Jottacloud"
else
    fail "file deletion was not synced within 90s"
    echo "  jottad log (new entries):"
    tail -n +"$((log_offset + 1))" "$JOTTAD_LOG" 2>/dev/null \
        | grep -E "sync|deleted|delete" | tail -20 || true
fi

summary
