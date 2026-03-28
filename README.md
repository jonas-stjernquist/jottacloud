# Jottacloud Docker

Dockerized [Jottacloud](https://www.jottacloud.com/) CLI backup client running on Debian.

Built on `debian:bookworm-slim` with the official `jotta-cli` package. The image is automatically rebuilt weekly via GitHub Actions to pick up the latest OS security patches and Jottacloud CLI updates.

Based on [bluet/docker-jottacloud](https://github.com/bluet/docker-jottacloud/).

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
| `JOTTA_SCANINTERVAL` | `12h` | How often to scan for changes. Examples: `1h`, `30m`, `0` (realtime). |
| `LOCALTIME` | `Europe/Stockholm` | Timezone for the container. |
| `STARTUP_TIMEOUT` | `15` | Seconds to wait for jottad to start before failing. |

### Environment variable priority (highest last)

1. Defaults in Dockerfile
2. Values from `docker run -e`
3. Values from `/data/jottad/jottad.env` file
4. Docker secret `jotta_token`

## Volumes

| Path | Description |
|------|-------------|
| `/data/jottad` | Persistent config and state. **Mount this to preserve login and backup progress across restarts.** |
| `/backup/` | Backup source. Each subdirectory is registered via `jotta-cli add`, e.g. `-v /home:/backup/home`. |
| `/sync/` | Sync source. Each subdirectory is registered via `jotta-cli sync setup`, e.g. `-v /photos:/sync/photos`. |
| `/config/ignorefile` | Optional gitignore-style file for excluding paths from backup. |

### Backup vs. Sync

- **Backup** (`/backup/`): one-way upload, full version history, deleted files kept in trash for 30 days.
- **Sync** (`/sync/`): bi-directional sync, up to 5 versions, deletions on device propagate to all synced locations.

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
      - JOTTA_SCANINTERVAL=12h
      - LOCALTIME=Europe/Stockholm
    volumes:
      - ./jottacloud-config:/data/jottad
      - /home:/backup/home
    ports:
      - "14443:14443"
```

## Synology NAS (Container Manager)

1. **Get a login token** from [Jottacloud Settings → Security](https://www.jottacloud.com/web/secure).
2. **Create a persistent config folder** on the NAS, e.g. `/volume1/docker/jottacloud`.
3. **Configure volumes** in Container Manager:

   | Host path (Synology) | Container path | Purpose |
   |----------------------|----------------|---------|
   | `/volume1/docker/jottacloud` | `/data/jottad` | Persistent config (required) |
   | `/volume1/homes` | `/backup/homes` | Backup |
   | `/volume1/documents` | `/backup/documents` | Backup |
   | `/volume1/photos` | `/sync/photos` | Sync |

4. **Set environment variables**: `JOTTA_TOKEN`, `JOTTA_DEVICE`, `JOTTA_SCANINTERVAL`, `LOCALTIME`.
5. `JOTTAD_SYSTEMD` is already `0` by default — no action needed in Container Manager.
6. `JOTTA_TOKEN` is only required on the **first start**. Once logged in, credentials are saved to the `/data/jottad` volume and the token is no longer needed.

## Debugging

```bash
# Start a shell inside the container
docker run -it stjernquist/jottacloud bash

# Shell into a running container
docker exec -it jottacloud bash

# Check status
docker exec jottacloud jotta-cli status
```

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

- **Minimal base image:** `debian:bookworm-slim` reduces attack surface
- **No recommended packages:** `--no-install-recommends` keeps dependencies minimal
- **Weekly rebuilds:** automated CI ensures OS and CLI stay patched
- **Docker secrets support:** avoid passing tokens via environment variables in production

## Debian Version Note

This image uses Debian 12 (bookworm) because Debian 13 (trixie) switched to Sequoia PGP for apt signature verification, which is currently incompatible with the Jottacloud apt repository's GPG signature ([jotta-cli-issues#208](https://github.com/jotta/jotta-cli-issues/issues/208)). The image will be upgraded to Debian 13 once this is resolved upstream.

## Credits

Based on [bluet/docker-jottacloud](https://github.com/bluet/docker-jottacloud/) by [BlueT - Matthew Lien](https://github.com/bluet).
