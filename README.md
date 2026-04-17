# Jottacloud Docker

**NOTE: This software is in a experimental stage and should not be considered as a stable solution**

Dockerized [Jottacloud](https://www.jottacloud.com/) CLI backup client running on Debian. &nbsp;·&nbsp; [GitHub](https://github.com/jonas-stjernquist/jottacloud) &nbsp;·&nbsp; [Docker Hub](https://hub.docker.com/r/stjernquist/jottacloud)

Built on `debian:trixie-slim` with the official `jotta-cli` package. The image is automatically rebuilt weekly via GitHub Actions to pick up the latest OS security patches and Jottacloud CLI updates.

**Supported platforms:** `linux/amd64`, `linux/arm64`

## Quick Start

```bash
docker run \
  -e JOTTA_TOKEN=your-personal-login-token \
  -e JOTTA_DEVICE=my-docker-backup \
  -v /path/to/config:/data/jottad \
  -v /path/to/backup:/backup/data \
  stjernquist/jottacloud
```

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `JOTTA_TOKEN` | `**None**` | Personal login token from [Jottacloud Settings > Security](https://www.jottacloud.com/web/secure). Required for first login. Use a persistent volume on `/data/jottad` to preserve login state. |
| `JOTTA_DEVICE` | `**docker-jottacloud**` | Device name shown in Jottacloud. Identifies which machine the backup belongs to. |
| `JOTTA_IGNORE_FILE` | `/config/ignorefile` | Optional path to ignore pattern file. If present, patterns are merged with built-in Synology defaults. |
| `JOTTA_IGNORE_PATTERNS` | `""` | Optional extra ignore patterns as comma- or newline-separated values. |
| `JOTTA_CONFIG_FILE` | `/config/jotta-config.env` | Optional key/value file for `jotta-cli` config settings, e.g. `maxuploads=4`. |
| `JOTTA_CONFIG_<SETTING>` | `""` | Override any CLI setting from env, for example `JOTTA_CONFIG_MAXUPLOADS=4`. |
| `JOTTA_CONFIG_SCANINTERVAL` | `12h` | Example of a managed CLI setting passed as env (`JOTTA_CONFIG_<SETTING>`). |
| `JOTTA_MONITOR_INTERVAL_SECONDS` | `15` | Seconds between background `jotta-cli status` health probes. |
| `LOCALTIME` | `Europe/Stockholm` | Timezone for the container. |
| `STARTUP_TIMEOUT` | `15` | Seconds to wait for jottad to start before failing. |
| `JOTTAD_SYSTEMD` | `0` | Controls whether the `jottad` daemon attempts systemd integration (sd_notify, socket activation). Set to `0` in this image since Docker containers don't run systemd. Set to `1` only if running `jottad` directly on a host with systemd. |

### Environment variable priority (highest last)

1. Defaults in Dockerfile
2. Values from `docker run -e`
3. Values from `/data/jottad/jottad.env` file
4. Docker secret `jotta_token`

### Managed CLI config priority (highest last)

1. Values from `JOTTA_CONFIG_FILE` (default `/config/jotta-config.env`)
2. Values from `JOTTA_CONFIG_<SETTING>` environment variables

If the same key exists in both places, `JOTTA_CONFIG_<SETTING>` wins.

## Volumes

| Path | Description |
|------|-------------|
| `/data/jottad` | Persistent config and state. **Mount this to preserve login and backup progress across restarts.** |
| `/backup/` | Backup source. Each subdirectory is registered via `jotta-cli add`, e.g. `-v /home:/backup/home`. |
| `/sync` | Sync source. Mount a **single** directory here, e.g. `-v /photos:/sync`. Only one sync root is supported by `jotta-cli`. |
| `/config/ignorefile` | Optional gitignore-style file for excluding paths from backup. Merged with built-in Synology ignore defaults. |
| `/config/jotta-config.env` | Optional key/value file to manage `jotta-cli config` settings at startup/restart. |

### Backup vs. Sync

- **Backup** (`/backup/`): one-way upload, full version history, deleted files kept in trash for 30 days.
- **Sync** (`/sync`): bi-directional sync, up to 5 versions, deletions on device propagate to all synced locations. Only one sync root is supported by `jotta-cli`.

## Docker Secrets

The container supports Docker secrets for the login token:

```yaml
# docker-compose.yml
services:
  jottacloud:
    image: stjernquist/jottacloud
    environment:
      - JOTTA_DEVICE=my-backup
    secrets:
      - jotta_token
    volumes:
      - ./jottacloud-config:/data/jottad
      - /home:/backup/home

secrets:
  jotta_token:
    file: ./jotta_token.txt
```

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
      - LOCALTIME=Europe/Stockholm
    volumes:
      - ./jottacloud-config:/data/jottad
      - /home:/backup/home
    ports:
      - "14443:14443"
```

## Synology NAS (Container Manager)

Jottacloud does not offer a native Synology package in the Package Center, and installing `jotta-cli` directly on DSM requires SSH access to the NAS and manual package management — steps that are error-prone, undone by DSM upgrades, and unsupported by Jottacloud. This image solves that: pull `stjernquist/jottacloud` from Docker Hub in Container Manager and you get a fully self-contained, auto-updating Jottacloud backup client without ever opening a terminal.

The image also handles a common compatibility problem: `jottad` normally expects to run under systemd, which does not exist inside a container. This image sets `JOTTAD_SYSTEMD=0` so the daemon starts correctly in the Docker environment.

### Setup

1. **Get a login token** from [Jottacloud Settings → Security](https://www.jottacloud.com/web/secure).
2. **Create a persistent config folder** on the NAS, e.g. `/volume1/docker/jottacloud`.
3. In **Container Manager → Registry**, search for `stjernquist/jottacloud` and download the image.
4. **Configure volumes** when creating the container:

   | Host path (Synology) | Container path | Purpose |
   |----------------------|----------------|---------|
   | `/volume1/docker/jottacloud` | `/data/jottad` | Persistent config (required) |
   | `/volume1/homes` | `/backup/homes` | Backup |
   | `/volume1/documents` | `/backup/documents` | Backup |
   | `/volume1/photos` | `/sync` | Sync |

5. **Set environment variables**: `JOTTA_TOKEN`, `JOTTA_DEVICE`, `JOTTA_CONFIG_SCANINTERVAL`, `LOCALTIME`.
6. `JOTTA_TOKEN` is only required on the **first start**. Once logged in, credentials are saved to the `/data/jottad` volume and the token is no longer needed.

### Synology Ignore Defaults (Enabled Automatically)

Synology metadata/recycle folders are ignored by default:

```text
**/@eaDir
**/@eaDir/**
**/@tmp
**/@tmp/**
**/#recycle
**/#recycle/**
```

You can still add your own patterns with `/config/ignorefile` (`JOTTA_IGNORE_FILE`) or inline via `JOTTA_IGNORE_PATTERNS`.

### Managed CLI Configuration

At container startup, the entrypoint manages `jotta-cli config` values **only** from:
1. `/config/jotta-config.env` (or `JOTTA_CONFIG_FILE`)
2. `JOTTA_CONFIG_<SETTING>` environment variables

Priority: config file first, then env overrides (`JOTTA_CONFIG_<SETTING>` has higher priority).

Examples:

```bash
-e JOTTA_CONFIG_MAXUPLOADS=4
-e JOTTA_CONFIG_IGNOREHIDDENFILES=true
-e JOTTA_CONFIG_WEBHOOKSTATUSINTERVAL=1h
```

Or file-based:

```text
maxuploads=4
maxdownloads=4
ignorehiddenfiles=true
uploadrate=2m
```

On restart, changed values are re-applied. If a previously managed setting is removed from env/file, it is reset to the documented CLI default for supported keys.

## Debugging

```bash
# Shell into a running container
docker exec -it jottacloud bash

# Check status
docker exec jottacloud jotta-cli status
```

For production troubleshooting, prefer one-off commands like `docker exec jottacloud jotta-cli status` or `docker logs -f jottacloud`. Long-lived interactive shells are fine for inspection, but repeatedly running `jotta-cli` commands during heavy scans can add avoidable load while `jottad` is already busy.

## Automated Updates

This image is automatically rebuilt every Monday via GitHub Actions. Each rebuild pulls:
- Latest Debian security patches (`apt-get upgrade`)
- Latest Jottacloud CLI version from the official apt repository

### Version naming

- **GitHub releases** are tagged `v{jotta-cli-version}` (e.g. `v3.14.2`) and created automatically when a new CLI version is detected in the Jottacloud APT repo (checked daily at 08:00 UTC).
- **Docker Hub tags**:
  - `:latest` — updated on every build (weekly rebuilds + new CLI releases)
  - `:{version}` (e.g. `:3.14.2`) — pinned tag created only when a new jotta-cli version is released

To set up automated rebuilds in your own fork, add these GitHub repository secrets:
- `DOCKERHUB_USERNAME` — your Docker Hub username
- `DOCKERHUB_TOKEN` — a Docker Hub access token

## Security

- **Minimal base image:** `debian:trixie-slim` reduces attack surface
- **No recommended packages:** `--no-install-recommends` keeps dependencies minimal
- **Weekly rebuilds:** automated CI ensures OS and CLI stay patched
- **Docker secrets support:** avoid passing tokens via environment variables in production
