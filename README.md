# Jottacloud Docker

Dockerized [Jottacloud CLI](https://www.jottacloud.com/) backup and sync container for Debian-based hosts and NAS setups.

This image wraps the official `jotta-cli` package with a Go entrypoint that:

- starts `jottad` in a container-friendly way
- logs in with a one-time `JOTTA_TOKEN` on first boot
- registers every mounted `/backup/*` directory as backup sources
- manages one canonical sync root at `/sync`
- applies managed `jotta-cli config` settings from the persisted data volume
- keeps both Docker logs and persisted on-disk logs

Supported platforms: `linux/amd64`, `linux/arm64`

## Quick Start

```bash
docker run \
  --name jottacloud \
  --restart unless-stopped \
  -e JOTTA_TOKEN=your-personal-login-token \
  -e JOTTA_DEVICE=my-docker-backup \
  -v /path/to/jottacloud-data:/data/jottad \
  -v /path/to/backup:/backup/data \
  stjernquist/jottacloud
```

To enable sync, mount a directory at `/sync`:

```bash
docker run \
  --name jottacloud \
  --restart unless-stopped \
  -e JOTTA_TOKEN=your-personal-login-token \
  -e JOTTA_DEVICE=my-docker-backup \
  -v /path/to/jottacloud-data:/data/jottad \
  -v /path/to/backup:/backup/data \
  -v /path/to/sync:/sync \
  stjernquist/jottacloud
```

`JOTTA_TOKEN` is only needed for the first successful login. After that, credentials persist inside `/data/jottad`.

## Runtime Model

The container uses `/data/jottad` as its persisted control directory.

It stores:

- login/session state
- backup and sync metadata
- managed state tracking for config and ignore reconciliation
- `jotta-config.env`
- `ignorefile`
- `container.log`
- `jottabackup.log`

On startup the container:

1. loads `/data/jottad/jottad.env` if present
2. loads Docker secret `jotta_token` if present
3. applies `LOCALTIME`
4. starts `jottad`
5. waits for `jotta-cli status` to become healthy
6. handles first-login and revoked-session prompts automatically
7. adds every directory under `/backup/*` to backup
8. manages sync at `/sync` if that mount exists
9. applies managed ignore patterns
10. applies managed config via `jotta-cli config <setting> <value>`
11. starts `jotta-cli tail`
12. keeps monitoring the daemon in the background

## Configuration

On first start the container creates two editable files in `/data/jottad`:

- `jotta-config.env`
- `ignorefile`

### `jotta-config.env`

`/data/jottad/jotta-config.env` is a commented template for managed `jotta-cli config` settings.

- It is created automatically if missing.
- Every line is commented out by default.
- Uncomment only the settings you want the container to manage.
- `#` is the comment marker.

Example generated content:

```ini
# downloadrate=0
# uploadrate=0
# checksumreadrate=52m
# checksumthreads=2
# maxuploads=12
# maxdownloads=12
# scaninterval=1h0m0s
# webhookstatusinterval=6h0m0s
# ignorehiddenfiles=false
# logscanignores=false
# slowmomode=0
# logtransfers=false
# screenshotscapture=false
# sharecapturedscreenshots=false
# syncpaused=false
# timeformat=RFC3339
# usesiunits=false
```

Managed settings are applied in this order:

1. values from `/data/jottad/jotta-config.env`
2. values from `JOTTA_CONFIG_<SETTING>` environment variables

If the same setting exists in both places, the environment variable wins.

Example:

```bash
-e JOTTA_CONFIG_MAXUPLOADS=4
-e JOTTA_CONFIG_SCANINTERVAL=12h
```

The entrypoint applies managed settings with the modern CLI syntax:

```bash
jotta-cli config <setting> <value>
```

If a previously managed setting is removed from both the file and the environment, the container resets it to the known CLI default for the settings it tracks.

### `ignorefile`

`/data/jottad/ignorefile` is the base ignore list for backup scanning.

- It is created automatically if missing.
- It starts with the built-in Synology metadata/recycle patterns as active lines.
- Edit the file directly to keep, remove, or add ignore patterns.
- `#` is the comment marker.

Default contents:

```text
**/@eaDir
**/@eaDir/**
**/@tmp
**/@tmp/**
**/#recycle
**/#recycle/**
```

Managed ignore patterns are applied in this order:

1. patterns from `/data/jottad/ignorefile`
2. extra patterns from `JOTTA_IGNORE_PATTERNS`

`JOTTA_IGNORE_PATTERNS` is additive. It does not replace `ignorefile`.

## Sync Contract

The container is intentionally opinionated about sync:

- `/sync` is the only supported local sync root
- mounting `/sync` enables sync
- not mounting `/sync` disables sync
- an empty `/sync` directory is valid and will still be configured as the sync root

### What happens when `/sync` exists

If `/sync` exists and is a directory, the container ensures sync is configured for `/sync` and starts continuous sync.

This is true even if `/sync` is empty.

### What happens when `/sync` is missing

If `/sync` is missing and there is no persisted sync state, the container skips sync entirely.

If `/sync` is missing but persisted sync state already exists, startup fails with a clear error. This is intentional: a previously configured sync root should not silently disappear because of a broken or forgotten mount.

### What happens when persisted sync state points somewhere else

If the persisted sync metadata points to a different local root than `/sync`, the container treats `/sync` as authoritative.

It will:

1. log that the sync root changed
2. run `jotta-cli sync reset` to clear the old local sync setup
3. run fresh sync setup for `/sync`
4. start continuous sync

This keeps the container contract simple and predictable: sync always belongs at `/sync` when sync is enabled.

### Selective sync and error reporting

The entrypoint keeps sync setup intentionally simple:

- sync error reporting mode is set to `off`
- selective sync is disabled by default during setup

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `JOTTA_TOKEN` | unset | Personal login token from [Jottacloud Settings > Security](https://www.jottacloud.com/web/secure). Required for first login only. |
| `JOTTA_DEVICE` | `**docker-jottacloud**` | Device name shown in Jottacloud. |
| `JOTTA_CONFIG_<SETTING>` | `""` | Override a managed `jotta-cli config` setting, for example `JOTTA_CONFIG_MAXUPLOADS=4`. |
| `JOTTA_IGNORE_PATTERNS` | `""` | Extra ignore patterns as comma- or newline-separated values. Added on top of `ignorefile`. |
| `JOTTA_MONITOR_INTERVAL_SECONDS` | `15` | Seconds between background `jotta-cli status` health probes. |
| `LOCALTIME` | `Europe/Stockholm` | Timezone for the container. |
| `STARTUP_TIMEOUT` | `60` | Seconds to wait for `jottad` startup before failing. |
| `BOOTSTRAP_TIMEOUT` | `60` | Seconds to wait for `jottad` responsiveness during each post-startup bootstrap phase. |
| `JOTTAD_SYSTEMD` | `0` | Must stay `0` inside Docker since the container does not run systemd. |

Container environment is loaded in this order:

1. defaults baked into the image
2. values from `docker run -e` or Compose `environment:`
3. values from `/data/jottad/jottad.env`
4. Docker secret `jotta_token`

This order applies to regular container variables such as `JOTTA_TOKEN`, `LOCALTIME`, or `JOTTA_DEVICE`.

## Volumes

| Path | Description |
|------|-------------|
| `/data/jottad` | Required persisted data and config volume. Mount this. |
| `/backup/` | Backup source parent. Each subdirectory under `/backup/` is added via `jotta-cli add`. |
| `/sync` | Optional sync root. Mount a directory here to enable sync. |

## Logging

The container keeps three useful logging surfaces.

### `docker logs`

`docker logs -f jottacloud` is the live aggregated output stream for the container.

It includes:

- entrypoint messages
- `run_jottad` output
- `jotta-cli tail` output

### `/data/jottad/container.log`

This is the container-managed persisted operator log.

- it contains the same aggregated output the entrypoint writes to stdout/stderr
- it survives container restarts because it lives in `/data/jottad`
- it rotates automatically at `10 MiB`
- retained files are:
  - `container.log`
  - `container.log.1`
  - `container.log.2`
  - `container.log.3`
  - `container.log.4`

Use this when you want restart-safe container history without depending only on the Docker log driver.

### `/data/jottad/jottabackup.log`

This is the upstream `jottad` daemon log.

- it is not managed or rotated by this project
- it is useful for low-level backup and sync diagnostics
- integration tests already inspect it directly for sync activity

Use this when you need detailed daemon-level events such as scans, uploads, sync transitions, and watcher activity.

## Health Checks

The image defines a Docker `HEALTHCHECK` that calls:

```bash
/src/entrypoint healthcheck
```

The healthcheck performs a short `jotta-cli status` probe.

The container becomes unhealthy if:

- `jotta-cli status` fails or times out
- the status output indicates a fatal state such as:
  - not logged in
  - revoked session
  - missing device name
  - remote device missing

The healthcheck does not start or repair anything. It only reports health.

## Docker Compose

```yaml
services:
  jottacloud:
    image: stjernquist/jottacloud
    container_name: jottacloud
    restart: unless-stopped
    environment:
      - JOTTA_TOKEN=your-token-here
      - JOTTA_DEVICE=my-docker-backup
      - JOTTA_CONFIG_SCANINTERVAL=12h
      - JOTTA_IGNORE_PATTERNS=**/*.tmp
      - LOCALTIME=Europe/Stockholm
    volumes:
      - ./jottacloud-data:/data/jottad
      - /home:/backup/home
      - ./sync:/sync
```

If you do not want sync, omit the `/sync` volume entirely.

## Synology NAS (Container Manager)

This image is intended to avoid the usual Synology friction around manually installing and maintaining `jotta-cli` on DSM. The container also disables systemd integration (`JOTTAD_SYSTEMD=0`), which is required inside Docker.

### Setup

1. Get a login token from [Jottacloud Settings → Security](https://www.jottacloud.com/web/secure).
2. Create a persistent folder on the NAS, for example `/volume1/docker/jottacloud`.
3. In Container Manager, download `stjernquist/jottacloud`.
4. Configure volumes:

   | Host path (Synology) | Container path | Purpose |
   |----------------------|----------------|---------|
   | `/volume1/docker/jottacloud` | `/data/jottad` | Persistent Jottacloud state, config, and logs |
   | `/volume1/homes` | `/backup/homes` | Backup |
   | `/volume1/documents` | `/backup/documents` | Backup |
   | `/volume1/photos` | `/sync` | Optional sync root |

5. Set env vars such as `JOTTA_TOKEN`, `JOTTA_DEVICE`, `LOCALTIME`, and any `JOTTA_CONFIG_<SETTING>` overrides you want.
6. Start the container once. It will create:

   - `/volume1/docker/jottacloud/jotta-config.env`
   - `/volume1/docker/jottacloud/ignorefile`
   - `/volume1/docker/jottacloud/container.log`

7. Edit those files if needed and restart the container.

## Debugging

```bash
docker exec -it jottacloud bash
docker exec jottacloud jotta-cli status
docker exec jottacloud jotta-cli tail
docker logs -f jottacloud
```

Useful persisted files:

```bash
tail -f /path/to/jottacloud-data/container.log
tail -f /path/to/jottacloud-data/jottabackup.log
cat /path/to/jottacloud-data/jotta-config.env
cat /path/to/jottacloud-data/ignorefile
```

## Troubleshooting

### The container is unhealthy

Start with:

```bash
docker logs --tail=200 jottacloud
docker exec jottacloud jotta-cli status
```

If you need persisted history across restarts, inspect `/data/jottad/container.log`.

### Sync is not starting

Check whether `/sync` is mounted as a directory:

```bash
docker exec jottacloud ls -ld /sync
```

Remember:

- mounting `/sync` enables sync
- omitting `/sync` disables sync
- an empty `/sync` directory is valid and still enables sync

### Startup fails because sync state exists but `/sync` is missing

That means the container has persisted sync metadata and now considers the missing mount a configuration error.

Fix it by either:

- restoring the `/sync` mount
- or intentionally clearing local sync state with `jotta-cli sync reset`

### Sync gets reconfigured to `/sync`

If the persisted sync root came from an older setup or a different local path, this container resets the old local sync config and recreates sync at `/sync`.

Check:

```bash
docker logs --tail=200 jottacloud
tail -n 200 /path/to/jottacloud-data/container.log
```

### Which log should I read first?

Use this order:

1. `docker logs`
2. `/data/jottad/container.log`
3. `/data/jottad/jottabackup.log`

`container.log` is usually the best persisted first stop. `jottabackup.log` is better when you need detailed daemon behavior.

## Automated Updates

This image is rebuilt regularly. Each rebuild pulls:

- latest Debian security patches
- latest Jottacloud CLI version available in the official apt repository

GitHub releases are tagged `v{jotta-cli-version}` and Docker Hub publishes `latest` plus versioned tags when a new CLI release is detected.

CI also smoke-tests that the built image contains a runnable `jotta-cli` binary before publishing.

## Security

- `debian:trixie-slim` base image
- `--no-install-recommends` package installs
- Docker secret support for `jotta_token`
- persistent credentials kept in the mounted `/data/jottad` volume
