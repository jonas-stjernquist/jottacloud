#!/usr/bin/env bash
# Shared helpers for integration tests.
set -euo pipefail

COMPOSE_CMD="${COMPOSE_CMD:-podman compose}"
IMAGE_NAME="jottacloud:test"
CONTAINER_NAME="jottacloud-test"
TEST_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
LOG_FILE="$TEST_DIR/data/container.log"
CONTAINER_START=""
PASS=0
FAIL=0

red()   { printf '\033[1;31m%s\033[0m\n' "$*"; }
green() { printf '\033[1;32m%s\033[0m\n' "$*"; }
yellow(){ printf '\033[1;33m%s\033[0m\n' "$*"; }

pass() { green "  PASS: $1"; PASS=$((PASS + 1)); }
fail() { red   "  FAIL: $1"; FAIL=$((FAIL + 1)); }

assert_contains() {
    local output="$1" expected="$2" label="${3:-}"
    if echo "$output" | grep -qF "$expected"; then
        pass "${label:-contains '$expected'}"
    else
        fail "${label:-expected '$expected' in output}"
        echo "  got: ${output:0:200}"
    fi
}

assert_not_contains() {
    local output="$1" unexpected="$2" label="${3:-}"
    if echo "$output" | grep -qF "$unexpected"; then
        fail "${label:-unexpected '$unexpected' in output}"
    else
        pass "${label:-does not contain '$unexpected'}"
    fi
}

require_env() {
    if [[ -z "${!1:-}" ]]; then
        red "ERROR: $1 is not set. Copy test/.env.example to test/.env and fill in your token."
        exit 1
    fi
}

build_image() {
    echo "Building image..."
    podman build -t "$IMAGE_NAME" "$TEST_DIR/.." 2>&1
}

compose_up() {
    echo "Starting container..."
    cd "$TEST_DIR"
    $COMPOSE_CMD down 2>/dev/null || true
    CONTAINER_START="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    $COMPOSE_CMD up -d 2>&1
}

compose_down() {
    echo "Stopping container..."
    cd "$TEST_DIR"
    # Save logs from this container run before stopping.
    if [[ -n "$CONTAINER_START" ]]; then
        mkdir -p "$(dirname "$LOG_FILE")"
        echo "=== $(date -u +%Y-%m-%dT%H:%M:%SZ) ===" >> "$LOG_FILE"
        podman logs --since "$CONTAINER_START" "$CONTAINER_NAME" >> "$LOG_FILE" 2>&1 || true
        echo "" >> "$LOG_FILE"
        CONTAINER_START=""
    fi
    $COMPOSE_CMD down 2>/dev/null || true
}

container_logs() {
    podman logs "$CONTAINER_NAME" 2>&1
}

container_exec() {
    podman exec "$CONTAINER_NAME" "$@" 2>&1
}

wait_for_startup() {
    local timeout="${1:-90}"
    # Record the time before we start so we only scan logs from this run.
    local since
    since="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    echo "Waiting for jottad startup (max ${timeout}s)..."
    local elapsed=0
    # Brief pause so compose_up -d has time to register the container.
    sleep 2
    elapsed=2
    while [ $elapsed -lt "$timeout" ]; do
        # Check if container is still running.
        if ! podman inspect --format '{{.State.Running}}' "$CONTAINER_NAME" 2>/dev/null | grep -q true; then
            red "  Container exited unexpectedly after ${elapsed}s."
            echo "  Logs:"
            container_logs | tail -20
            return 1
        fi

        # Pipe through grep without capturing into a variable — avoids OOM
        # when jotta-cli tail replays a large jottabackup.log to stdout.
        # grep -q exits on first match so podman logs gets SIGPIPE and stops early.
        local match
        match="$(podman logs --since "$since" "$CONTAINER_NAME" 2>&1 \
            | grep -F -e "Monitoring active." \
                      -e "Startup timeout reached" \
                      -e "Login failed" \
            | head -1)" || true

        if [[ "$match" == *"Monitoring active."* ]]; then
            echo "  Container started after ${elapsed}s."
            return 0
        fi
        if [[ "$match" == *"Startup timeout reached"* ]]; then
            red "  Container hit startup timeout after ${elapsed}s."
            echo "  Logs:"
            container_logs | tail -20
            return 1
        fi
        if [[ "$match" == *"Login failed"* ]]; then
            red "  Login failed after ${elapsed}s."
            echo "  Logs:"
            container_logs | tail -20
            return 1
        fi
        sleep 2
        elapsed=$((elapsed + 2))
    done
    red "  Timed out after ${timeout}s waiting for startup."
    echo "  Logs:"
    container_logs | tail -20
    return 1
}

clean_data() {
    echo "Cleaning persistent data..."
    rm -rf "$TEST_DIR/data"
}

summary() {
    echo ""
    echo "========================="
    green "Passed: $PASS"
    if [ "$FAIL" -gt 0 ]; then
        red "Failed: $FAIL"
        return 1
    else
        green "All tests passed."
    fi
}
