# Jottacloud CLI Complete Reference

## Installation

### Debian/Ubuntu
```bash
sudo apt-get install curl apt-transport-https ca-certificates
sudo curl -fsSL https://repo.jotta.cloud/jotta.gpg -o /usr/share/keyrings/jotta.gpg
echo "deb [signed-by=/usr/share/keyrings/jotta.gpg] https://repo.jotta.cloud/debian debian main" | sudo tee /etc/apt/sources.list.d/jotta-cli.list
sudo apt-get update && sudo apt-get install jotta-cli
run_jottad
```

Update expired GPG key:
```bash
sudo curl -fsSL https://repo.jotta.cloud/jotta.gpg -o /usr/share/keyrings/jotta.gpg
echo "deb [signed-by=/usr/share/keyrings/jotta.gpg] https://repo.jotta.cloud/debian debian main" | sudo tee /etc/apt/sources.list.d/jotta-cli.list
```

### RPM (RHEL/CentOS/Fedora)
Save to `/etc/yum.repos.d/JottaCLI.repo`:
```ini
[jotta-cli]
name=Jottacloud CLI
baseurl=https://repo.jotta.cloud/redhat
gpgcheck=1
gpgkey=https://repo.jotta.cloud/public.gpg
```
```bash
sudo yum install jotta-cli
run_jottad
```

Update GPG key if expired:
```bash
rpm -q gpg-pubkey --qf "%{NAME}-%{VERSION}-%{RELEASE}\t%{SUMMARY}\n" | grep "Jottacloud" | awk '{ print $1}' | xargs -r rpm -e && sudo rpm --import https://repo.jotta.cloud/public.asc
```

### AUR (Arch Linux)
```bash
yay -S jotta-cli
```

### macOS (Homebrew)
```bash
brew tap jotta/cli && brew install jotta-cli
brew services start jotta-cli
# Update:
brew update && brew upgrade jotta-cli && brew services restart jotta-cli
```

### Windows
Download MSI from: https://repo.jotta.cloud/archives/windows/amd64/

### FreeBSD
Download from: https://repo.jotta.cloud/archives/
Files install to `/usr/bin`, `/usr/share/jottad`, `/etc/jottad`.

### Starting at Boot (Linux)
```bash
loginctl enable-linger USERNAME   # creates user session at boot for systemd user slice
```

---

## Supported Platforms
- Linux: amd64, i386, arm64, armhf (kernel 2.6.23+)
- macOS: Intel + Apple Silicon (0.11+), requires macOS 10.12+ (from 0.13)
- Windows: 10, 8, 7 (64-bit only)
- FreeBSD: x86_64

## Supported File Systems
Any POSIX-compatible (ext2/3/4, XFS, Btrfs, ZFS, NTFS, APFS, HFS+, FAT32, exFAT).

Remote/overlay filesystems (NFS, SMB/CIFS) trigger a warning: if unmounted, jotta-cli may interpret missing files as deletions. Best practice: add the mount point itself.

---

## Login & Setup

```bash
jotta-cli login   # Accept terms -> Personal Login Token (from https://www.jottacloud.com/web/secure) -> name device
jotta-cli status
jotta-cli help [command]
```

Global flags: `--host` (default 127.0.0.1), `--port` (default 14443)

---

## Full Command List

```
add, archive, completion, config, download, dump, help, ignores, list, logfile,
login, logout, ls, observe, pause, rem, resume, scan, share, status, sync, tail,
trash, version, web, webhook
```

---

## Backup Management

```bash
jotta-cli add "/path/to/folder"              # Add to backup
jotta-cli rem "/path/to/folder"              # Remove (does NOT delete cloud copies)
jotta-cli scan                                # Trigger immediate scan
jotta-cli ls Backup/DeviceName/FolderName    # List backed-up contents
jotta-cli status                              # Check backup status
jotta-cli status -v                           # Verbose status with errors
```

### Ignore Patterns

```bash
jotta-cli ignores add --pattern "node_modules" --backup mybackup
jotta-cli ignores add --pattern "**.log"      # All backups
jotta-cli ignores rem --pattern "**.log" --backup mybackup
jotta-cli ignores list --backup mybackup
jotta-cli ignores test --pattern "**.png" --path "photos/image.png"
```

Glob: `*` (not `/`), `**` (everything), `**/` (zero+ dirs). Hardcoded: `**.DS_Store`, `**/.Thumbs.db`, `**/.desktop.ini`.

### Scan Interval
```bash
jotta-cli config set scaninterval 30m     # 30 minutes
jotta-cli config set scaninterval 0       # realtime (filesystem triggers)
```

---

## Sync Folder

```bash
jotta-cli sync setup --root /path/to/sync    # Setup (disclaimer, reporting mode, path)
jotta-cli sync start                          # Continuous sync
jotta-cli sync stop                           # Stop sync
jotta-cli sync trigger                        # One-time sync
jotta-cli sync move /new/path                 # Move (NEVER move manually!)
jotta-cli sync reset                          # Reset config (files NOT deleted)
jotta-cli config set syncpaused true|false    # Pause/unpause
jotta-cli sync log -n20                       # Last 20 entries
jotta-cli sync log --watch                    # Stream entries
jotta-cli observe --sync                      # Watch transfers
```

**WARNING**: Never move sync folder with `mv`. Use `jotta-cli sync move` only.

---

## Archive

```bash
jotta-cli archive "/path/to/file"
jotta-cli archive "/path" --remote=folder/subfolder/name
jotta-cli archive "/path" --share --clipboard   # Share + copy link
jotta-cli archive "/path" --nogui               # For scripts
echo "data" | jotta-cli archive -I --remote="file.txt"   # From stdin
jotta-cli list uploads                          # Monitor folder uploads
jotta-cli observe --uploadid=ID
```

Files stored under `Archive/<device-name>/` by default.

---

## Configuration

```bash
jotta-cli config                    # View all
jotta-cli config set <key> <value>  # Set value
```

| Setting | Default | Description |
|---------|---------|-------------|
| `downloadrate` | unlimited | Max download (e.g., `1m`=1MB/s, `512k`, `0`=unlimited) |
| `uploadrate` | unlimited | Max upload bandwidth |
| `checksumreadrate` | ~52MB/s | Disk read rate for checksumming |
| `ignorehiddenfiles` | false | Skip dotfiles (linux/mac) or hidden attribute (Windows) |
| `maxuploads` | 6 | Simultaneous uploads (1-6) |
| `maxdownloads` | 6 | Simultaneous downloads (1-6) |
| `scaninterval` | 1h0m0s | Backup scan frequency. `0`=realtime |
| `webhookstatusinterval` | 6h0m0s | Webhook status POST frequency |
| `logscanignores` | false | Log reasons for ignored files |
| `slowmomode` | 0 | Reduce scan CPU/disk (0-50) |
| `logtransfers` | false | Log all HTTP transfer requests |
| `screenshotscapture` | false | Auto-upload screenshots (macOS/Linux) |
| `syncpaused` | false | Pause sync |

### jottad Config File (INI)

```ini
[settings]
Datadir=/custom/data/dir
ListenAddr=0.0.0.0        # Listen on all interfaces
LogfileDir=/var/log
```

Locations: Linux/FreeBSD: `/etc/jottad/config.ini` | macOS: `~/.config/jottad.ini` | Windows: `installdir\config.ini`

---

## Downloading

```bash
jotta-cli download Archive/folder ~/Downloads
jotta-cli download Backup/Device/Folder ~/Downloads
jotta-cli download Sync .
jotta-cli observe --downloads                           # Watch progress
jotta-cli list downloads                                # List active
jotta-cli download --abort=ID                           # Cancel
jotta-cli download --retry=ID                           # Retry failed
jotta-cli download Archive/f ~/local --merge            # Update (only new/changed)
jotta-cli download Archive/f ~/local --merge --mergemode=metadata  # Lighter check
jotta-cli download -O Archive/file.txt > local.txt      # To stdout
jotta-cli list downloadinformation --downloadid=ID [--json]  # Error details
```

Note: If jottad and jotta-cli run on different hosts, destination must be valid on jottad host.

---

## Remote Management / Multiple Instances

```bash
jotta-cli --host 192.168.10.10 status    # Connect to remote jottad
```

Default: jottad listens on 127.0.0.1. Set `ListenAddr=0.0.0.0` in config file for remote access.

---

## Webhooks

```bash
jotta-cli webhook add <url>       # Add (sends test message immediately)
jotta-cli webhook rem <url>       # Remove
jotta-cli config set webhookstatusinterval 1h
```

Events: start, stop, periodic status. JSON payload in Slack format.

---

## Pausing

```bash
jotta-cli pause 5m                    # Pause with duration
jotta-cli pause 6h30m
jotta-cli resume                      # Resume manually
jotta-cli pause --backup mybackup     # Pause individual backup
```

Cron: `0 1 * * * jottad /usr/bin/jotta-cli pause 6h`

---

## Shell Completion

```bash
# Bash
. <(jotta-cli completion bash)
# Zsh
echo "autoload -U compinit; compinit" >> ~/.zshrc
jotta-cli completion zsh > "${fpath[1]}/_jotta-cli"
# Fish
jotta-cli completion fish > ~/.config/fish/completions/jotta-cli.fish
```

macOS: may need bash v4+ and `brew install bash-completion`.

---

## Permissions on Linux

jottad runs as user/group `jottad` by default on DEB/RPM installs.

**Option 1: jottad group**
```bash
usermod -a -G jottad <username>
chgrp jottad /home/<user>/folder && chmod g+r -R /home/<user>/folder
find /home/<user>/folder -type d -exec chmod g+x {} \;
```

**Option 2: Your user group** (systemd override)
```bash
systemctl edit jottad
# [Service]
# Group=<your-group>
# UMask=0002
systemctl daemon-reload && systemctl restart jottad
```

All parent directories need read+execute for jottad's user/group.

---

## Status & Logs

```bash
jotta-cli status [-v]                  # Status (verbose with errors)
jotta-cli observe [--sync|--downloads|--uploadid=ID]
jotta-cli list uploads|downloads
jotta-cli logfile                      # Print log file path
jotta-cli tail                         # Stream log output
```
