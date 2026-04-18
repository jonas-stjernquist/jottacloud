# Jottacloud Docker

**NOTE:** This software is still experimental.

Dockerized [Jottacloud](https://www.jottacloud.com/) CLI backup client running on Debian. It uses the official `jotta-cli` package and is rebuilt regularly to pick up Debian security updates and newer Jottacloud CLI releases.

Supported platforms: `linux/amd64`, `linux/arm64`

## Quick Start

```bash
docker run \
  -e JOTTA_TOKEN=your-personal-login-token \
  -e JOTTA_DEVICE=my-docker-backup \
  -v /path/to/jottacloud-data:/data/jottad \
  -v /path/to/backup:/backup/data \
  stjernquist/jottacloud
```

On first start the container creates two editable files inside `/data/jottad`:

- `jotta-config.env`
- `ignorefile`

## How Configuration Works

The container uses `/data/jottad` as the persistent control directory.

It stores:

- login/session state
- backup and sync state
- managed state tracking for config and ignore reconciliation
- `jotta-config.env`
- `ignorefile`

### `jotta-config.env`

`/data/jottad/jotta-config.env` is a commented template for `jotta-cli config` settings.

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

## Precedence

### Settings

Managed settings are applied in this order:

1. Values from `/data/jottad/jotta-config.env`
2. Values from `JOTTA_CONFIG_<SETTING>` environment variables

If the same setting exists in both places, the environment variable wins.

Example:

```bash
-e JOTTA_CONFIG_MAXUPLOADS=4
-e JOTTA_CONFIG_SCANINTERVAL=12h
```

### Ignore Rules

Managed ignore patterns are applied in this order:

1. Patterns from `/data/jottad/ignorefile`
2. Extra patterns from `JOTTA_IGNORE_PATTERNS`

`JOTTA_IGNORE_PATTERNS` is additive. It does not replace `ignorefile`.

Example:

```bash
-e JOTTA_IGNORE_PATTERNS="**/*.tmp,**/node_modules"
```

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `JOTTA_TOKEN` | `**None**` | Personal login token from [Jottacloud Settings > Security](https://www.jottacloud.com/web/secure). Required for first login only. |
| `JOTTA_DEVICE` | `**docker-jottacloud**` | Device name shown in Jottacloud. |
| `JOTTA_CONFIG_<SETTING>` | `""` | Override a `jotta-cli config` setting, for example `JOTTA_CONFIG_MAXUPLOADS=4`. |
| `JOTTA_IGNORE_PATTERNS` | `""` | Extra ignore patterns as comma- or newline-separated values. Added on top of `ignorefile`. |
| `JOTTA_MONITOR_INTERVAL_SECONDS` | `15` | Seconds between background `jotta-cli status` health probes. |
| `LOCALTIME` | `Europe/Stockholm` | Timezone for the container. |
| `STARTUP_TIMEOUT` | `30` | Seconds to wait for `jottad` startup before failing. |
| `JOTTAD_SYSTEMD` | `0` | Must stay `0` inside Docker since the container does not run systemd. |

### Other Environment Sources

Container env is loaded in this order:

1. Defaults baked into the image
2. Values from `docker run -e` or Compose `environment:`
3. Values from `/data/jottad/jottad.env`
4. Docker secret `jotta_token`

This order applies to normal container environment variables such as `JOTTA_TOKEN` or `LOCALTIME`.

## Volumes

| Path | Description |
|------|-------------|
| `/data/jottad` | Persistent Jottacloud state and editable configuration files. Mount this. |
| `/backup/` | Backup sources. Each directory under `/backup/` is added with `jotta-cli add`. |
| `/sync` | Optional sync source. Mount a single directory here. |

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
```

## Synology NAS (Container Manager)

This image is intended to avoid the usual Synology friction around manually installing and maintaining `jotta-cli` on DSM. The container also disables systemd integration (`JOTTAD_SYSTEMD=0`), which is required inside Docker.

### Setup

1. Get a login token from [Jottacloud Settings → Security](https://www.jottacloud.com/web/secure).
2. Create a persistent folder on the NAS, for example `/volume1/docker/jottacloud`.
3. In Container Manager, download `stjernquist/jottacloud`.
4. Configure volumes:

   | Host path (Synology) | Container path | Purpose |
   |----------------------|----------------|---------|
   | `/volume1/docker/jottacloud` | `/data/jottad` | Persistent Jottacloud state and config files |
   | `/volume1/homes` | `/backup/homes` | Backup |
   | `/volume1/documents` | `/backup/documents` | Backup |
   | `/volume1/photos` | `/sync` | Optional sync |

5. Set env vars such as `JOTTA_TOKEN`, `JOTTA_DEVICE`, `LOCALTIME`, and any `JOTTA_CONFIG_<SETTING>` overrides you want.
6. Start the container once. It will create:

   - `/volume1/docker/jottacloud/jotta-config.env`
   - `/volume1/docker/jottacloud/ignorefile`

7. Edit those files if needed and restart the container.

`JOTTA_TOKEN` is only needed on the first successful login. Credentials persist in `/data/jottad`.

## Managed Settings Behavior

At startup the container:

1. reads `/data/jottad/jotta-config.env`
2. applies `JOTTA_CONFIG_<SETTING>` overrides
3. runs `jotta-cli config set <setting> <value>` for the managed settings it finds

If a previously managed setting is removed from both the file and env, the container resets it to the known CLI default for the settings it tracks internally.

## Debugging

```bash
docker exec -it jottacloud bash
docker exec jottacloud jotta-cli status
docker exec jottacloud jotta-cli tail
docker logs -f jottacloud
```

## Automated Updates

This image is rebuilt regularly. Each rebuild pulls:

- latest Debian security patches
- latest Jottacloud CLI version available in the official apt repository

GitHub releases are tagged `v{jotta-cli-version}` and Docker Hub publishes `latest` plus versioned tags when a new CLI release is detected.

## Security

- `debian:trixie-slim` base image
- `--no-install-recommends` package installs
- Docker secret support for `jotta_token`
- persistent credentials kept in the mounted `/data/jottad` volume
