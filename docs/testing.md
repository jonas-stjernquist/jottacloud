# Testing

This project uses two test layers: **Go unit tests** (deterministic, no network) and **container integration tests** (podman, requires a Jottacloud token).

## Go Unit Tests

Unit tests verify the PTY prompt-matching engine, terminal-query responder, startup state machine, command-failure handling, env file parsing, and helper functions using a fake-cli simulator plus in-process fakes — no real `jotta-cli` binary required.

### Running

```bash
go test -v -race -timeout 30s ./...
```

### What's tested

| Area | Tests | Purpose |
|------|-------|---------|
| `ptyRun` | Single/multiple/mutually-exclusive prompts, timeout, partial reads, exit codes | Core prompt-response engine |
| Terminal queries | Split ANSI query sequences across reads, deferred prompt replies | Covers fragile PTY/TTY negotiation behavior |
| `loginWithToken` | New device + existing device flows, prompt string regression | Full login flow with exact prompt strings |
| Startup loop | Status classification, revoked-session recovery, timeout diagnostics | Deterministic coverage for startup branches |
| Command failures | Ignore loading, scan-interval config, health-check failures | Ensures setup errors fail loudly instead of looking healthy |
| `loadEnvFile` | KEY=VALUE, quotes, export, comments, missing file | Env file parser |
| `envInt` | Set, default, invalid | Integer env helper |
| `forceSymlink` | New, replace | Symlink helper |

### How prompt regression detection works

The application defines the known jotta-cli prompt strings as constants in `main.go`, and the tests exercise those exact constants:

```go
const (
    promptLicense     = "accept license (yes/no): "
    promptToken       = "Personal login token: "
    promptDeviceName  = "Device name"
    promptReuseDevice = "Do you want to re-use this device? (yes/no):"
    promptLogout      = "Backup will stop. Continue?(y/n): "
)
```

The `TestLoginWithToken_PromptStringsMatch` test runs `loginWithToken()` against a fake-cli that expects **exactly** these prompt strings. If jotta-cli changes a prompt in a future release, this test fails — telling you precisely which string changed.

**To update after a jotta-cli prompt change:**
1. Update the prompt constant in `main.go`
2. Re-run `go test`

### The fake-cli simulator

`testdata/fake-cli/main.go` is a small Go program that simulates jotta-cli's interactive behavior. It reads a JSON scenario from the `FAKECLI_SCENARIO` env var:

```json
{
  "steps": [
    {"prompt": "Enter name: ", "expect": "alice"},
    {"prompt": "Token: ", "expect": "secret"}
  ],
  "finalOutput": "Done.\n",
  "exitCode": 0
}
```

Each step prints the prompt (no trailing newline, matching jotta-cli behavior), reads a line from stdin, and optionally validates the response. Features:

- `chunkSize` — split prompt into small chunks (tests partial PTY reads)
- `delayMs` — delay before printing (tests timing)
- `hangForever` — never exit (tests timeout handling)

The binary is built automatically in `TestMain` and cleaned up after tests.

## Container Integration Tests

These tests run the actual Docker image with podman and a real Jottacloud token. They verify the full startup flow, backup registration, sync setup, and configuration.

### Prerequisites

- `podman` and `podman-compose` (or `podman compose`) installed
- A valid Jottacloud personal login token

### Setup

```bash
cd test
cp .env.example .env
# Edit .env and add your JOTTA_TOKEN
```

### Running all tests

```bash
cd test
./scripts/run-all.sh
```

### Running individual tests

```bash
cd test
./scripts/test-first-login.sh
./scripts/test-backup-dirs.sh
./scripts/test-sync-setup.sh
./scripts/test-scan-interval.sh
```

### Test descriptions

| Script | What it tests |
|--------|---------------|
| `test-first-login.sh` | Clean-state login: removes data dir, starts container, verifies login and device name |
| `test-backup-dirs.sh` | Backup directory registration: verifies `/backup/documents` appears in `jotta-cli ls` |
| `test-sync-setup.sh` | Sync directory setup: verifies "Adding sync directory" appears in logs |
| `test-scan-interval.sh` | Scan interval: verifies "Setting scan interval to 1m" appears in logs |

### Test data

The `test/` directory contains:

```
test/
├── .env.example          # Token template
├── compose.yml           # Podman compose for tests
├── backup/documents/     # Test files for backup
├── sync/                 # Test file for sync
├── config/ignorefile     # Test ignore patterns
├── scripts/              # Test scripts
└── data/                 # (git-ignored) Persistent state during tests
```

### Environment

Override the compose command if needed:

```bash
COMPOSE_CMD="docker compose" ./scripts/run-all.sh
```

## CI

The GitHub Actions workflow runs Go unit tests on every push. Integration tests are manual-only since they require a real token.

```yaml
# .github/workflows/build.yml
test:
  runs-on: ubuntu-latest
  steps:
    - uses: actions/checkout@v4
    - uses: actions/setup-go@v5
      with:
        go-version-file: go.mod
    - run: go test -v -race -timeout 30s ./...
```
