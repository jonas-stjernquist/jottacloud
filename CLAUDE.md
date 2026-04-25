# jottacloud

Docker container wrapping the Jottacloud CLI backup client. Single Go entrypoint (`main.go`) that orchestrates jottad daemon startup via PTY-based interactive prompt handling.

## Build

```bash
docker build -t jottacloud:local .
```

Go binary is compiled in-container (`golang:trixie` builder stage, Go 1.26.2+) with `CGO_ENABLED=0` for a static binary.

## Run

Requires `JOTTA_TOKEN`. Mount `/data` for persistent state.

```bash
docker run -e JOTTA_TOKEN=<token> -v jottacloud_data:/data jottacloud:local
```

## Test

```bash
# Unit tests (no jotta-cli needed — uses fake-cli simulator)
go test -v -race -timeout 30s ./...

# Integration tests (requires JOTTA_TOKEN in test/.env)
cd test && ./scripts/run-all.sh
```

See `docs/testing.md` for details.

## Key facts

- Only external Go dep: `github.com/creack/pty` — needed because jotta-cli flushes prompts only to a TTY
- Unit tests use `testdata/fake-cli/` to simulate jotta-cli prompts via PTY
- Prompt string constants in `main_test.go` catch regressions when jotta-cli updates
- CI: Go tests run on every push; weekly image rebuilds (Debian patches) + daily jotta-cli version checks → auto-release
