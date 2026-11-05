# Cineye

A video surveillance and recording management system built with Go + Vue 3, supporting multi-camera access, live preview, playback, recording storage, alert push and user permission management.

> [简体中文版 README](README.md) | 🇬 English

Ready to use: download the release archive for your platform → install ffmpeg → run → complete the first-run setup in the browser.

---

## ✨ Features

### 🎥 Camera Access (Two Connection Modes)
- **ONVIF auto-discovery (recommended)**: enter IP/credentials → discover the device → fetch Profiles & RTSP stream URL → preview/record
- **Direct RTSP URL**: paste any playable full RTSP URL (with credentials), no ONVIF needed; connectivity is ffprobe-tested before saving
- The connection mode is fixed at creation time and locked during editing (prevents cross-contamination of the two connection field sets)
- ONVIF device discovery, LAN WS-Discovery probe, camera start/stop/restart, online status monitoring
- Live preview (low-latency HLS), PTZ control (ONVIF), manual snapshots
- Preview source selection: main stream (HD) / sub stream (low resolution, saves bandwidth/CPU, recommended for multi-view)

### 🎬 Recording & Playback
- Recording policies: continuous, motion-triggered (ONVIF cameras only), schedule-based
- Segment storage (default 3 min/segment, configurable), automatic indexing, fast seeking
- Historical playback (HLS), segment download, snapshot management (global browse, filter, one-click clear)

### 🔔 Alert Push
- **ONVIF event-driven motion detection**: camera hardware reports motion → alert generated → recording auto-triggered
  - 60 s recording, 60 s cooldown (`camera.motion_record` configurable)
- Tiered alerts (low/medium/high/critical), deduplication & cooldown
- Multi-channel push: Webhook (DingTalk/Feishu/WeCom/Gotify), Email (SMTP), SMS

### 💾 Storage Management
- **Local storage**: disk monitoring, automatic cleanup by retention days / capacity limit
- **WebDAV**: remote archiving with independent retention; "WebDAV-only" mode (delete local copy after upload; local is just a temporary buffer)
- **MinIO / S3 object storage**: remote archiving, independent retention, optional "MinIO-only" mode
- Playback auto-fallback: when the local file is missing, stream from WebDAV / MinIO (Range seeking supported)
- Storage usage statistics, manual cleanup

### 🛡 Security & Operations
- **First-run wizard**: fresh installs ship with no default account; the browser opens an environment check, then you create the admin account
- **Environment check**: ffmpeg/ffprobe presence, writable database/recording/log directories, disk space, WebDAV/MinIO connectivity — each failed item includes concrete fix commands; also available anytime under System Settings → Maintenance
- RBAC with three roles: admin / operator / read-only; JWT authentication
- Database backup/restore, log viewer/clear, self-update (pulls from GitHub Releases)

---

## 🏗 Architecture

```
┌──────────────────────────────────────────────────────────────┐
│                        Frontend (Vue 3)                      │
│   Vue 3 + Element Plus + Pinia + Vue Router + HLS.js + ECharts │
└──────────────────────────┬───────────────────────────────────┘
                           │ HTTP (REST) / WebSocket
┌──────────────────────────▼───────────────────────────────────┐
│                      API Server (Gin)                        │
│     JWT auth · static serving · SPA fallback · WebSocket     │
└──────────────────────────┬───────────────────────────────────┘
                           │
       ┌───────────────────┼──────────────────────┐
       ▼                   ▼                      ▼
┌──────────────┐  ┌────────────────┐  ┌──────────────────┐
│  Cameras     │  │    Storage     │  │  Alerts / Events │
│ ONVIF disc.  │  │ Local · WebDAV │  │ ONVIF events     │
│ RTSP direct  │  │ MinIO/S3       │  │ Webhook/Email/   │
│ FFmpeg       │  │ cleanup/fallback│  │ SMS push         │
│ PTZ control  │  │                │  │                  │
└──────────────┘  └────────────────  └──────────────────
       │                   │                      │
       └───────────────────┼──────────────────────┘
                           ▼
                  ┌──────────────────────┐
                  │  Infrastructure      │
                  │ SQLite + FFmpeg      │
                  └──────────────────────┘
```

### Tech Stack

| Layer | Tech |
|----|------|
| Backend | Go 1.22+ · Gin · GORM · SQLite (pure-Go driver, zero CGO) · FFmpeg · Viper · logrus |
| Frontend | Vue 3 · TypeScript · Element Plus · Pinia · Vue Router · HLS.js · ECharts |
| Protocols | ONVIF (discovery/PTZ/events) + RTSP (streaming) |

---

## 🚀 Quick Start

### Requirements

| Dependency | Requirement | Notes |
|------|------|------|
| FFmpeg | `ffmpeg` + `ffprobe` in PATH | Used for streaming/preview/recording/probing — **the only required dependency** |
| Others | none | SQLite is a pure-Go built-in driver; no CGO, no database service |

Install FFmpeg:

```bash
# Debian / Ubuntu / Armbian
sudo apt update && sudo apt install -y ffmpeg

# RHEL / CentOS / Fedora
sudo dnf install -y ffmpeg
```

> If FFmpeg is missing or directories are not writable, the server still starts — the first-run setup page and "Maintenance → Environment Check" will list the missing items with exact fix commands.

### Option 1: systemd One-Click Install (recommended for Linux servers)

**One-liner install** (no clone needed; the script first asks for the language, then for the ports on first install):

```bash
curl -fsSL https://raw.githubusercontent.com/Assen998/cineye/main/deployments/deploy.sh | sudo bash
```

Or clone the repository and run it (same effect, convenient for inspecting the script):

```bash
git clone https://github.com/Assen998/cineye.git
cd cineye
sudo bash deployments/deploy.sh        # auto-downloads the latest release asset for your platform
# Install a local binary instead:
sudo bash deployments/deploy.sh /path/to/cineye
# Uninstall (data is kept; remove /opt/cineye manually to wipe it):
sudo bash deployments/deploy.sh --uninstall
```

- Auto-detects the architecture (amd64 / arm64 / armv7) and downloads the matching latest-release asset
- **Custom ports on first install**: the script prompts for the HTTP port (default `8080`) and WebSocket port (default `8081`), validates them (range + not already in use) and writes them into the generated `config.yaml`; with an existing config it asks nothing and changes nothing
- Installs to `/opt/cineye`; generates `config.yaml` on first install — **an existing config is always preserved, never overwritten**
- Creates the `cineye` systemd service: starts on boot, auto-restarts on crash
- If GitHub is not reachable directly, download via a proxy: `sudo env http_proxy=http://<proxy-host>:<port> https_proxy=http://<proxy-host>:<port> bash deployments/deploy.sh` (for the one-liner, prefix `curl` with the same `http_proxy=...`)
- Non-interactive (piped/CI) runs auto-detect the system locale; force a language with `DEPLOY_LANG=zh|en`; ports can be set with `SURVEILLANCE_HTTP_PORT` / `SURVEILLANCE_WS_PORT`

### Option 2: Download a Release (no build needed)

Grab the archive for your platform from [GitHub Releases](https://github.com/Assen998/cineye/releases) (e.g. `cineye-1.5.0-linux-arm64.tar.gz`). It extracts to a **version-less stable directory** (e.g. `cineye-linux-arm64/`):

```
cineye         # single binary (frontend embedded)
config.yaml                 # default configuration
README.md
```

```bash
cd cineye-linux-arm64
./cineye        # auto-loads config.yaml in the same directory
```

### Option 3: Build from Source

The frontend build is embedded into the backend binary via Go's `go:embed`, producing a **single executable**:

```bash
cd cineye

# 1) Build the frontend first (generates web/dist for go:embed)
cd web && npm install && npm run build && cd ..

# 2) Build the backend (embeds the frontend → single binary)
go build -o cineye ./cmd/server
```

Cross-compilation (zero CGO, fully static; linux/windows/darwin × amd64/arm64, etc.):

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o cineye ./cmd/server
```

### Uninstall

Pick the commands matching your installation method. **Data (config / recordings) is kept by default — use the final `rm -rf` step only if you want a complete removal**, and make sure you have a backup first.

**Option 1 (systemd one-click install):**

```bash
# Stop and remove the service (data is kept in /opt/cineye)
sudo bash deployments/deploy.sh --uninstall
# Equivalent commands without the script:
sudo systemctl stop cineye && sudo systemctl disable cineye
sudo rm /etc/systemd/system/cineye.service && sudo systemctl daemon-reload
# Complete removal (including data / recordings / config):
sudo rm -rf /opt/cineye
```

**Option 2 / Option 3 (running the binary directly):**

```bash
# Stop the process (Ctrl+C if running in the foreground)
pkill -x cineye
# Remove the install directory (data/ and recordings/ inside are removed too)
rm -rf /path/to/cineye-linux-arm64    # replace with your actual install dir
```

**Docker deployment:**

```bash
cd deployments
docker compose down          # stop and remove containers (incl. MinIO/Nginx if enabled); named volumes are kept
docker compose down -v       # also delete the named volumes (config / recordings / logs wiped)
rm -rf <project-dir>         # optional: remove the deployment files
```

### First Run (important)

```bash
./cineye
```

Open `http://<server-ip>:8080` in the browser:

1. **Environment check**: the page automatically checks ffmpeg/ffprobe, directory permissions, disk space, and connectivity of enabled storage services;
   failed items show exact fix commands (e.g. `sudo apt install -y ffmpeg`). **You cannot continue while a critical check fails** — fix it and click "Re-check"
2. **Create the admin account**: set a username/password (min 6 characters), then click "Finish setup and enter the system"
3. After setup you land on the dashboard; **the setup page is no longer accessible** (the backend endpoints are disabled at the same time)

> Fresh installs have no default account at all; upgrading an existing database skips the setup and logs in as usual.

- Web UI: `http://localhost:8080`
- Health check: `GET http://localhost:8080/health`

### Upgrading

The directory name carries no version, so upgrading is just **extract the new archive over the same directory**:

```bash
cd /root    # assuming deployment is /root/cineye-linux-arm64
cp cineye-linux-arm64/config.yaml /tmp/config.yaml.bak   # ① back up your config
tar xzf ~/cineye-1.5.0-linux-arm64.tar.gz                # ② extract over it
cp /tmp/config.yaml.bak cineye-linux-arm64/config.yaml   # ③ restore config
systemctl restart cineye   # ④ restart (or relaunch ./cineye)
```

- `data/` (database) and `recordings/` (recordings) are **not in the archive** — extracting over keeps them automatically
- `config.yaml` is in the archive and gets reset to defaults on extraction → restore per ①③ (or re-save settings in the web UI after upgrading)
- No changes needed for systemd units or autostart references
- You can also use **self-update** in "System Settings → Maintenance" (pulls from GitHub Releases)

---

## 📖 Configuration

Config file: `configs/config.yaml` (source) / `config.yaml` in the release directory. Most settings can also be hot-updated and persisted from the web UI (System Settings). Key options:

```yaml
server:
  host: 0.0.0.0
  http_port: 8080          # Web/API port
  ws_port: 8081            # WebSocket port
  mode: release

database:
  type: sqlite
  sqlite:
    path: ./data/surveillance.db

storage:
  local:
    enabled: true
    root_path: ./recordings   # recording directory
    segment_duration: 180     # segment duration (seconds)
    max_days: 7               # retention days (0 = no auto-delete)
    max_storage_gb: 10        # capacity limit (GB, 0 = unlimited)
    cleanup_interval: 3600    # cleanup check interval (seconds)
  webdav:
    enabled: true
    url: http://127.0.0.1:5244/dav
    username: ""
    password: ""
    base_path: surveillance
    max_days: 30
    max_storage_gb: 150
    only: false               # true = WebDAV-only (delete local copy after upload)
  minio:
    enabled: false            # optional object storage
    endpoint: localhost:9000
    access_key: minioadmin
    secret_key: minioadmin
    bucket: surveillance
    use_ssl: false
    base_path: surveillance
    max_days: 30
    max_storage_gb: 0
    only: false               # true = MinIO-only (delete local copy after upload)

camera:
  preview_stream: main        # preview source: main | sub (low-res, saves resources)
  onvif_event:
    enabled: true             # ONVIF event subscription (required for motion detection)
    poll_interval: 10
    subscription_timeout: 60
  motion_record:
    duration: 60              # motion recording length (seconds)
    pre_record: 0
    cooldown: 60              # cooldown (seconds)

alert:
  enabled: true
  channels:
    webhook:                  # DingTalk/Feishu/WeCom/Gotify
      enabled: true
      url: ""
    email:
      enabled: false          # SMTP
    sms:
      enabled: false          # Aliyun SMS

logging:
  level: info
  output: ./logs/surveillance.log

update:
  proxy: ""                   # HTTP proxy for update checks/downloads (if GitHub is unreachable)
```

> `redis`, `postgres`, and `gb28181` in the config are reserved placeholders; the current release runs on SQLite by default.

---

## 📡 Camera Access Guide

### ONVIF Auto-Discovery (recommended)

For ONVIF-capable cameras (Hikvision, Dahua, TP-Link and other mainstream brands):

1. Cameras → Add Camera → choose "ONVIF Auto-Discovery"
2. Enter IP/username/password, click "Auto-detect & fill"
3. The system fetches device info, Profiles (main/sub stream) and the RTSP URL; pick a Profile and save
4. Streaming starts automatically; the ONVIF event subscription is established too (motion alerts become available)

### Direct RTSP URL

For: cameras without ONVIF, difficult ONVIF setups, or a known-good RTSP URL (NVR re-streams, forwards from other devices, etc.):

1. Cameras → Add Camera → choose "RTSP Stream URL"
2. Enter a name and paste a **playable full RTSP URL** (with credentials), e.g. Hikvision main stream:
   ```
   rtsp://admin:admin@192.168.1.64:554/Streaming/Channels/101?transportmode=unicast
   ```
   Use `102` instead of `101` for the sub stream; for streams without credentials just use `rtsp://IP:port/path`
3. On save the backend runs an ffprobe connectivity test — a failing stream is rejected with a clear error and never stored
4. Note: RTSP-direct cameras have **no ONVIF event reporting**, so only "continuous"/"schedule" recording types are offered; PTZ is also unavailable (requires ONVIF)

### Editing Cameras

- The connection mode is fixed at creation and locked when editing (to change it, delete and re-add)
- ONVIF cameras can be re-probed to refresh Profiles; RTSP cameras can change their stream URL (password is never shown — leave it unchanged to keep the original credentials)

---

## 🎬 Recording Types

| Type | Description | Applies to |
|------|------|------|
| Continuous | 24/7 resident recording stream, cut into segments | all cameras |
| Motion-triggered | no resident stream; records 60 s only when an ONVIF event fires (60 s cooldown) | **ONVIF cameras only** |
| Schedule | records within hour ranges (e.g. `9-18,22-6`) | all cameras |

---

## 💾 Storage Modes

| Mode | Local | Remote (WebDAV/MinIO) | Use case |
|------|------|------|----------|
| Local only | store + auto-cleanup | — | single-node deployment |
| Local + remote archive | store + auto-cleanup | archived with independent cleanup | fast local + long-term remote |
| Remote only (`only: true`) | temporary buffer before upload, deleted after successful upload | primary storage | small local disk, remote-first |

- In "remote-only" mode, old recordings auto-play from the remote store (Range seeking supported)
- WebDAV and MinIO can be enabled simultaneously without conflict (the same recording is archived to both)

---

## 📡 API

Base prefix `/api/v1`. Everything except login and first-run setup requires `Authorization: Bearer <token>`.

### First-Run Setup (public; disabled automatically once setup is complete)
| Method | Path | Description |
|-----|------|------|
| GET | `/setup/status` | whether setup is needed + environment check report |
| POST | `/setup` | create admin `{username, password}`, returns a token |

### Auth
| Method | Path | Description |
|-----|------|------|
| POST | `/auth/login` | login |
| POST | `/auth/logout` | logout |
| GET | `/auth/me` | current user |
| PUT | `/auth/password` | change password |

### Cameras
| Method | Path | Description |
|-----|------|------|
| GET | `/cameras` | list |
| POST | `/cameras` | create (RTSP protocol is ffprobe-tested automatically) |
| GET | `/cameras/:id` | detail |
| PUT | `/cameras/:id` | update (empty password = keep existing) |
| DELETE | `/cameras/:id` | delete |
| GET | `/cameras/:id/status` | runtime status |
| POST | `/cameras/:id/start` / `stop` / `restart` | start/stop/restart |
| POST | `/cameras/:id/snapshot` | manual snapshot |
| POST | `/cameras/:id/ptz` | PTZ control (ONVIF) |
| GET | `/cameras/:id/snapshots` | camera snapshots |
| GET | `/cameras/discover` | ONVIF auto-discovery |
| POST | `/cameras/discover/lan` | LAN WS-Discovery probe |
| GET | `/cameras/probe` | ONVIF probe (pre-check before adding) |

### Recordings
| Method | Path | Description |
|-----|------|------|
| GET | `/recordings` | list (pagination/filters) |
| GET | `/recordings/:id` | detail |
| DELETE | `/recordings/:id` | delete |
| GET | `/recordings/camera/:cameraId` | recordings of a camera |
| GET | `/recordings/camera/:cameraId/segments` | segments in a time range |

### Snapshots
| Method | Path | Description |
|-----|------|------|
| GET | `/snapshots` | global snapshot list |
| DELETE | `/snapshots` | clear all |
| DELETE | `/snapshots/:id` | delete one |

### Alerts
| Method | Path | Description |
|-----|------|------|
| GET | `/analytics/alerts` | alert list |
| DELETE | `/analytics/alerts` | clear alerts |
| GET | `/analytics/alerts/:id` | alert detail |
| PUT | `/analytics/alerts/:id/ack` / `resolve` | acknowledge / resolve |
| DELETE | `/analytics/alerts/:id` | delete |
| GET / PUT | `/alerts/config` | alert config |
| POST | `/alerts/test` | send a test alert |

### Storage & Settings
| Method | Path | Description |
|-----|------|------|
| GET | `/storage/stats` | storage statistics |
| POST | `/storage/cleanup` | trigger cleanup |
| GET / PUT | `/settings/storage` | storage settings (local/WebDAV/MinIO, hot-updatable) |
| POST | `/settings/webdav/test` | WebDAV connectivity test |
| POST | `/settings/minio/test` | MinIO connectivity test |
| GET / PUT | `/settings/camera` | camera defaults (snapshots, etc.) |
| GET / PUT | `/settings/camera-defaults` | camera defaults |

### Streaming / Playback
| Method | Path | Description |
|-----|------|------|
| GET | `/stream/camera/:id/hls` | live HLS playlist |
| GET | `/stream/camera/:id/hls/*file` | HLS segments |
| GET | `/stream/camera/:id/mp4` | latest MP4 segment |
| GET | `/stream/camera/:id/snapshot` | latest snapshot |
| GET | `/stream/camera/:id/recordings/:recordingId/hls` | historical HLS |
| GET | `/recordings/:id/file` | recording file (auto-fallback local → WebDAV → MinIO) |
| GET | `/recordings/:id/download` | download recording |
| GET | `/webdav/list` / `/webdav/file` | WebDAV file list/stream |
| GET | `/minio/list` / `/media/minio/file` | MinIO file list/stream (Range) |

### System
| Method | Path | Description |
|-----|------|------|
| GET | `/system/info` | system info |
| POST | `/system/restart` | restart |
| GET | `/system/env` | environment check (same logic as the first-run page) |
| GET | `/system/logs` / `/system/logs/files` | log viewer |
| POST | `/system/logs/clear` | clear logs |
| POST | `/system/backup` | database backup |
| GET / DELETE | `/system/backups` | backup list/delete |
| GET | `/system/update/check` | check for updates |
| POST | `/system/update` | perform self-update |

### WebSocket
| Path | Description |
|------|------|
| `/ws` | global real-time events (alerts/status/recordings) |
| `/api/v1/ws/camera/:cameraId` | per-camera real-time events |

---

## 🐳 Docker Deployment

Deployment files live in `deployments/`. Besides the main app, MinIO (object storage) and Nginx (HTTPS reverse proxy) are optional, enabled via compose profiles:

```bash
cd deployments
docker compose up -d                          # main app only
docker compose --profile minio up -d          # + MinIO (9000 API / 9001 console)
docker compose --profile nginx up -d          # + Nginx (prepare deployments/ssl/ certs first)
```

Data is persisted in the named volumes `cineye-data` / `cineye-recordings` / `cineye-logs`, mounted at `/app/data`, `/app/recordings`, `/app/logs` inside the container.

> The system runs out of the box without Redis / PostgreSQL (pure-Go SQLite driver); MinIO is only needed when object storage is configured.

---

## 🔧 Development Guide

### Directory Layout

```
cineye/
├── cmd/server/             # entrypoint
├── internal/
│   ├── api/                # HTTP API handlers, routing, environment checks
│   ├── camera/             # camera management, recording triggers, ONVIF connection
│   ├── storage/            # storage management (local / WebDAV / MinIO, runtime settings)
│   ├── alert/              # alert push (Webhook/Email/SMS)
│   ├── onvifevent/         # ONVIF event subscription (motion reporting)
│   ├── config/             # config loading & persistence
│   ├── database/           # database init & first-run detection
│   └── models/             # data models
├── pkg/
│   ├── ffmpeg/             # FFmpeg wrapper (streaming / HLS / segments / probing)
│   ├── onvif/              # ONVIF client
│   ├── webdav/             # WebDAV client
│   ├── minio/              # MinIO/S3 client
│   └── gb28181/            # GB28181 protocol (reserved)
├── web/                    # Vue 3 frontend
│   └── src/
│       ├── api/            # API request wrapper
│       ├── views/          # page views (incl. Setup.vue first-run wizard)
│       ├── stores/         # Pinia state
│       ├── router/         # routes
│       └── components/     # shared components (EnvCheckTable, etc.)
├── configs/config.yaml     # configuration
└── deployments/            # deployment (systemd one-click installer + Docker)
```

### Key Flows

- **Motion-triggered recording**: camera reports motion via ONVIF events → `onvifevent` creates an alert → `TriggerMotionRecording` records per `camera.motion_record` and enters cooldown
- **Recording archiving**: segment finished → local write → parallel upload to WebDAV/MinIO (3 retries) → `only` mode deletes the local copy → `storage_path` written back
- **Playback fallback**: file request → serve from local if present → else WebDAV → else MinIO (all support Range)
- **First-run setup**: at startup, detect whether the DB file was just created (`FirstRun`) → skip the default admin seed → frontend `/setup` page runs the environment check + creates the admin → endpoints disabled after `POST /setup`

### Adding a New Module

1. Create a module directory under `internal/`
2. Implement `Start()` / `Stop()` lifecycle methods
3. Register and start it in `cmd/server/main.go`
4. Add routes in `internal/api/server.go`
5. Add the corresponding frontend page and API calls

---

## 🔍 FAQ

### First-run setup reports environment check failures
- Every failed check carries a concrete fix command; common ones:
  - missing `ffmpeg`/`ffprobe`: `sudo apt update && sudo apt install -y ffmpeg` (Debian/Armbian) or `sudo dnf install -y ffmpeg`
  - directory not writable: `sudo chown -R <service-user> <dir>`
  - low disk space: check with `df -h`, clean up or expand
- Click "Re-check" after fixing; "System Settings → Maintenance → Environment Check" is available anytime

### Camera shows offline / connection failed
1. Check network connectivity: `ping <camera_ip>`, ensure the RTSP port (usually 554) is reachable
2. Verify the RTSP URL plays in VLC
3. Ensure the camera encoding is H.264 / H.265
4. Check the server log (Maintenance → Logs) for the exact error

### Video won't play / stuttering
1. Ensure FFmpeg is installed: `ffmpeg -version`
2. For multi-view / low-bandwidth: set `camera.preview_stream` to `sub` (sub stream; recommended on ARM devices)
3. Tune `storage.local.segment_duration` and the HLS segment length
4. Check whether disk write speed is the bottleneck

### Motion-triggered recording never fires
- Motion detection is **only available for ONVIF cameras** (it relies on ONVIF event reporting); RTSP-direct cameras don't offer this option
- Verify:
  1. the camera is connected via ONVIF and supports ONVIF events (Analytics / MotionDetection)
  2. `camera.onvif_event.enabled` is `true`
  3. the recording type is set to "Motion-triggered"

### Low disk space
1. **Enable "WebDAV-only / MinIO-only" mode** (System Settings → Storage): the local copy is deleted after a successful upload; local keeps only the pre-upload buffer; old recordings auto-play from the remote store
2. Reduce `max_days` or increase the cleanup frequency
3. Set a `max_storage_gb` capacity limit
4. Archive to WebDAV / MinIO to offload
5. Trigger cleanup manually: `POST /api/v1/storage/cleanup`

### Forgot the admin password
- There is no online reset. If you have a database backup, restore it; otherwise delete `data/surveillance.db` and re-initialize (**this loses all camera/recording index settings**), then go through first-run setup again.

---

## 🤝 Contributing

1. Fork the project
2. Create a feature branch: `git checkout -b feat/amazing-feature`
3. Commit your changes: `git commit -m 'feat: add amazing feature'`
4. Push the branch: `git push origin feat/amazing-feature`
5. Open a Pull Request

---

## 🙏 Acknowledgements

- [FFmpeg](https://ffmpeg.org/) — multimedia core
- [ONVIF](https://www.onvif.org/) — open network video interface standard
- [Gin](https://github.com/gin-gonic/gin) — Go web framework
- [Vue.js](https://vuejs.org/) — progressive frontend framework
- [Element Plus](https://element-plus.org/) — Vue 3 component library
- [HLS.js](https://github.com/video-dev/hls.js/) — HTTP Live Streaming player
- [GORM](https://gorm.io/) — Go ORM
- [MinIO](https://min.io/) — high-performance object storage
