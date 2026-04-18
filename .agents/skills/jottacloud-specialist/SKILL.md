---
name: jottacloud-specialist
description: Expert on Jottacloud backup/sync/archive, CLI (jotta-cli/jottad), Docker containers, and help center FAQ. Use whenever working on this project's Dockerfile or Go entrypoint, debugging jottad startup, setting up volumes or environment variables, configuring backup/sync, or answering any Jottacloud question, even if not explicitly asked.
---

# Jottacloud Specialist

You are a Jottacloud expert. Use this knowledge to help with Jottacloud CLI, Docker containers, backup/sync configuration, and troubleshooting.

## Ad-hoc: Always Fetch Current State

When this skill is invoked, ALWAYS do these two things before answering:

1. **Fetch latest CLI release notes** to know the current version and recent changes:
   - WebFetch `https://docs.jottacloud.com/en/articles/1461561-release-notes-for-jottacloud-cli`

2. **Check open GitHub issues** for known bugs and workarounds (best-effort - skip silently if `gh` is unavailable or unauthenticated):
   ```bash
   gh issue list --repo jotta/jotta-cli-issues --state open --limit 20 2>/dev/null || true
   ```

Reference these when answering version-specific questions, debugging, or recommending workarounds.

## Reference Files

This skill includes supporting reference files. Load them on-demand when the question requires detailed information:

- **[references/docker-reference.md](references/docker-reference.md)**: Docker image env vars, volumes, Docker Compose, Docker secrets, Synology NAS setup, debugging, automated rebuilds. Load for any Docker or container question.
- **[references/cli-reference.md](references/cli-reference.md)**: Complete jotta-cli command reference, backup/sync/archive commands, config settings, installation per platform, permissions, webhooks, downloading, shell completion
- **[references/help-center-index.md](references/help-center-index.md)**: Full index of all 186 Jottacloud help center articles across 17 collections with URLs for deep-linking

---

## Core Concepts

### Storage Areas

**Backup** - One-way mirror from local to cloud
- Mirrors selected local folders, organized per-device
- Changes on local reflected in cloud (add, modify, delete)
- Deleted files go to Trash (30-day retention)
- Files are NOT synced back to other devices

**Sync** - Bidirectional sync across all devices
- Special "Synced" folder mirrors content to/from cloud
- Changes propagate instantly; supports shared folders
- Stores up to 5 versions per file (versions consume storage)
- Deleting from Sync removes from cloud AND all devices

**Archive** - Manual cloud-only storage
- Independent of devices; NOT automatically updated
- Ideal for freeing local disk space

**Trash** - 30-day retention, restorable via web interface

### Sharing
- **Public Link**: URL to a file/folder (anyone with link can view)
- **Shared Folder**: Invite collaborators on Sync folders; synced to all members

### Storage
- All files in Backup, Sync, Archive + file versions count toward quota
- Jottacloud uses GB (decimal, 1 GB = 1 billion bytes), not GiB

---

## CLI Architecture

Two components, both must be running:
- **jottad** - Background daemon handling file operations
- **jotta-cli** - CLI to control jottad

Daemon runs in a systemd user slice on Linux. Default: listens on `127.0.0.1:14443`.

### Key Commands (quick reference)

```bash
jotta-cli login                              # Login with Personal Login Token
jotta-cli status                             # Overall status
jotta-cli add "/path"                        # Add folder to backup
jotta-cli rem "/path"                        # Remove folder from backup
jotta-cli sync setup --root /path            # Setup sync folder
jotta-cli sync start|stop|trigger            # Manage sync
jotta-cli archive "/path" [--nogui]          # Upload to archive
jotta-cli download Remote/path ~/local       # Download from cloud
jotta-cli config                             # View all settings
jotta-cli config <key> <value>               # Change setting
jotta-cli observe [--sync|--downloads]       # Watch transfers
jotta-cli pause 5m | jotta-cli resume        # Pause/resume
jotta-cli ignores add --pattern "**.log"     # Ignore pattern
```

For the full command reference, installation guides, config settings table, and platform-specific details, see [references/cli-reference.md](references/cli-reference.md).

---

## Security & Privacy

- **Storage**: Own data centers near Stavanger, Norway; renewable energy
- **Encryption**: TLS/SSL in transit (256-bit, A+ SSL Labs), encrypted at rest
- **No private encryption keys** (Jottacloud manages keys)
- **GDPR compliant**; Norwegian privacy laws apply
- **Uptime**: 99.9%+

---

## Subscriptions

- **Personal**: Individual, unlimited storage (upload throttled above 5TB)
- **Home**: Up to 5 family members, shared storage pool (1/5/10/20 TB)
- **Business**: Organization accounts with admin user management
