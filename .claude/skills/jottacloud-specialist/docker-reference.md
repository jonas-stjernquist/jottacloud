# Jottacloud Docker Reference

Image: `stjernquist/jottacloud` — Dockerized jotta-cli on `debian:trixie-slim`.
Source: https://github.com/jonas-stjernquist/jottacloud

Rebuilt automatically every Monday via GitHub Actions (picks up latest Debian patches + jotta-cli updates).

**Supported platforms:** `linux/amd64`, `linux/arm64`

---

## Quick Start

```bash
docker run \
  -e JOTTA_TOKEN=your-personal-login-token \
  -e JOTTA_DEVICE=my-docker-backup \
  -v /path/to/config:/data/jottad \
  -v /path/to/backup:/backup/data \
  stjernquist/jottacloud
```

---

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `JOTTA_TOKEN` | `**None**` | Personal login token from [Jottacloud Settings > Security](https://www.jottacloud.com/web/secure). Required for first login only — credentials are saved to the `/data/jottad` volume after that. |
| `JOTTA_DEVICE` | `**docker-jottacloud**` | Device name shown in Jottacloud. Identifies which machine the backup belongs to. |
| `JOTTA_CONFIG_<SETTING>` | `""` | Override a managed `jotta-cli config` setting such as `JOTTA_CONFIG_SCANINTERVAL=12h`. |
| `JOTTA_IGNORE_PATTERNS` | `""` | Extra ignore patterns added on top of `/data/jottad/ignorefile`. |
| `LOCALTIME` | `Europe/Stockholm` | Timezone for the container. |
| `STARTUP_TIMEOUT` | `30` | Seconds to wait for jottad to start before exiting with an error. |
| `JOTTAD_SYSTEMD` | `0` | Controls whether jottad attempts systemd integration (sd_notify, socket activation). Must be `0` inside Docker — containers don't run systemd. Set to `1` only when running jottad directly on a host with systemd. |

### Environment variable priority (highest last)

1. Defaults in Dockerfile
2. Values from `docker run -e` / `environment:` in Compose
3. Values from `/data/jottad/jottad.env` file (useful for secrets without restarting)
4. Docker secret `jotta_token`

---

## Volumes

| Path | Description |
|------|-------------|
| `/data/jottad` | Persistent config and state. **Always mount this** to preserve login and backup progress across restarts. |
| `/backup/` | Backup source. Each subdirectory under `/backup/` is registered via `jotta-cli add`. Mount multiple sources: `-v /home:/backup/home -v /var/data:/backup/data`. |
| `/sync` | Sync source. Mount a **single** directory here. Only one sync root is supported by jotta-cli. |
| `/data/jottad/jotta-config.env` | Auto-created commented template for managed `jotta-cli config` settings. |
| `/data/jottad/ignorefile` | Auto-created ignore file containing the Synology defaults. |

---

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

---

## Docker Secrets

Prefer secrets over plaintext env vars in production:

```yaml
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

The entrypoint reads `/run/secrets/jotta_token` automatically and sets `JOTTA_TOKEN` from it, taking priority over the env var.

---

## Synology NAS (Container Manager)

jottad normally expects systemd, which doesn't exist in a container. This image sets `JOTTAD_SYSTEMD=0` to work around that. No native Synology package exists; this Docker image is the recommended approach.

### Setup

1. Get a login token from [Jottacloud Settings → Security](https://www.jottacloud.com/web/secure).
2. Create a persistent config folder on the NAS, e.g. `/volume1/docker/jottacloud`.
3. In **Container Manager → Registry**, search `stjernquist/jottacloud` and download.
4. Configure volumes:

   | Host path (Synology) | Container path | Purpose |
   |----------------------|----------------|---------|
   | `/volume1/docker/jottacloud` | `/data/jottad` | Persistent config (required) |
   | `/volume1/homes` | `/backup/homes` | Backup |
   | `/volume1/documents` | `/backup/documents` | Backup |
   | `/volume1/photos` | `/sync` | Sync (only one allowed) |

5. Set env vars: `JOTTA_TOKEN`, `JOTTA_DEVICE`, `LOCALTIME`, and optional `JOTTA_CONFIG_<SETTING>` overrides.
6. `JOTTA_TOKEN` only needed on first start — credentials persist in `/data/jottad`.
7. On first start the container creates `/data/jottad/jotta-config.env` and `/data/jottad/ignorefile`.

---

## Debugging

```bash
# Shell into a running container
docker exec -it jottacloud bash

# Start a fresh shell (bypasses entrypoint startup logic)
docker run -it stjernquist/jottacloud bash

# Check backup status
docker exec jottacloud jotta-cli status

# Stream logs
docker exec jottacloud jotta-cli tail

# Watch transfers
docker exec jottacloud jotta-cli observe
```

---

## Go Entrypoint

The container uses a custom Go entrypoint (`main.go`) instead of a shell script. On startup it:

1. Loads env overrides from `/data/jottad/jottad.env` (if present)
2. Reads Docker secret `/run/secrets/jotta_token` (if present)
3. Sets timezone via `LOCALTIME`
4. Starts `jottad` via `/usr/bin/run_jottad`
5. Polls `jotta-cli status` until jottad is ready, handling these interactive prompts automatically:
   - **Not logged in** → runs `jotta-cli login` with token + device name
   - **Session revoked** → logs out and re-logs in
   - **Device name not set** → answers the device name prompt via `jotta-cli status`
   - **Matching device found** → confirms re-use
6. Registers all directories under `/backup/` as backup sources
7. Sets up `/sync` as sync root (if mounted and non-empty)
8. Creates `/data/jottad/jotta-config.env` and `/data/jottad/ignorefile` if missing
9. Loads `/data/jottad/ignorefile`
10. Applies managed settings from `/data/jottad/jotta-config.env` plus `JOTTA_CONFIG_<SETTING>` env overrides
11. Runs `jotta-cli tail` to stream logs, then health-checks every 15s

Passing `bash` as the container command (`docker run -it ... bash`) drops straight to a shell, bypassing all of the above.

---

## Automated Rebuilds & Versioning

- **Weekly rebuild**: Every Monday via GitHub Actions — pulls latest Debian patches + jotta-cli
- **Version tags**: Created automatically when a new jotta-cli version is detected in the APT repo (checked daily at 08:00 UTC)
- **Docker Hub tags**:
  - `:latest` — updated on every build
  - `:{version}` (e.g. `:3.14.2`) — pinned, created only on new jotta-cli releases
- **GitHub releases**: Tagged `v{jotta-cli-version}` (e.g. `v3.14.2`)

To set up automated rebuilds in a fork, add GitHub secrets: `DOCKERHUB_USERNAME`, `DOCKERHUB_TOKEN`.
