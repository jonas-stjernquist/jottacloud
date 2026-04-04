---
name: jottacloud-testing
description: Guides running the jottacloud Docker integration tests. Use this skill whenever the user asks to run tests, check test results, debug test failures, or work with the integration test suite in this project — including when they say "run tests", "run the tests", "check the logs", or provide a PAT/token for testing.
user-invocable: true
---

# Jottacloud Testing

This skill covers everything needed to run and debug the jottacloud integration test suite.

## Running the tests

**Unit tests** (no token needed, fast):
```bash
go test -v -race -timeout 30s ./...
```

**Integration tests** (requires PAT, always run in background):
```bash
cd <repo-root>/test && ./scripts/run-all.sh
```
Always use the `bash` tool with `mode: "async"` — the suite takes several minutes.

## PAT (Personal Access Token) management

PATs are **single-use** — once a container logs in, that token is spent.

**The .env file** lives at `test/.env`:
```
JOTTA_TOKEN=<token>
```

When the user provides a new token, write it directly to `test/.env`. The PAT is from a dedicated test account so there are no security concerns.

### When a new PAT is needed

| Situation | Need new PAT? |
|-----------|--------------|
| `test/data` does not exist | Yes |
| Container logs show "Login failed" or "server did not recognize the provided credentials" | Yes |
| User asks for a clean run | Yes |
| Tests failed for non-login reasons (assertion failures, crashes) | No |
| `test/data/jottad/` exists with valid credentials | No — reuse session |

**Always ask the user for a new PAT before a fresh-login run.** Direct them to:
> https://www.jottacloud.com/web/secure → Security tab → Personal login token

### Fresh login sequence
1. Ask user to generate a new PAT
2. Write it to `test/.env`
3. Clean stale data: `rm -rf <repo-root>/test/data`
4. Run the test suite

## Persistent state

Credentials survive container restarts in `test/data/jottad/`. **Do not clean this directory** unless a fresh login is actually needed — cleaning it burns the PAT on re-login.

`run-all.sh` automatically truncates these at the start of each suite run:
- `test/data/container.log`
- `test/data/jottad/jottabackup.log`

## Diagnosing failures

Check these in order:

1. **Test summary** — look for PASS/FAIL lines in the task output
2. **Container log** — `test/data/container.log` — one timestamped block per test, written by `compose_down`
3. **jottad log** — `test/data/jottad/jottabackup.log` — jottad's own log
4. **Live logs** — `podman logs --since 5m jottacloud-test` (while container is running)

### Common failure patterns

| Error | Cause | Fix |
|-------|-------|-----|
| `Login failed: exit status 1` | PAT spent or invalid | New PAT + clean data |
| `server did not recognize the provided credentials` | Same as above | New PAT + clean data |
| `Startup timeout reached` | Unhandled jottad state | Check `container.log` for the error before the timeout |
| `Could not connect to jottad` | jottad crashed | Check `container.log` for a panic/stacktrace |
| `device does not exist remotely` | Device deleted from Jottacloud backend | Entrypoint handles automatically (logs out + re-login); needs valid PAT |
| Container logs growing to GB | Unanswered interactive prompt | `jotta-cli` is stuck waiting; check `container.log` for the prompt text and add a handler in `main.go` |
| `jottacloud-test already in use` | Previous container not cleaned up | `podman rm -f jottacloud-test` |

## Test architecture

- 6 integration tests run sequentially via `test/scripts/run-all.sh`
- Each test calls `compose_up` (which runs `compose_down` first to clean up) then `wait_for_startup`
- Startup is detected by the `"Monitoring active."` sentinel in container logs
- Credentials persist in `test/data/jottad/` across tests within a suite run and between runs
