# Cineye · 监控录像系统

一个基于 Go + Vue 3 的视频监控与录像管理系统，支持多路摄像头接入、实时预览、历史回放、录像存储、报警推送与用户权限管理。

> [English README](README.en.md) | 界面与文档均为中文。开箱即用：下载对应平台压缩包 → 装 ffmpeg → 运行 → 浏览器完成首次设置。

---

## ✨ 功能特性

### 🎥 摄像头接入（两种连接方式）
- **ONVIF 自动发现（推荐）**：填 IP/账号/密码 → 自动发现设备 → 获取 Profile 与 RTSP 流地址 → 拉流预览/录像
- **RTSP 流地址直连**：直接粘贴可正常播放的完整 RTSP URL（含账号密码），无需 ONVIF；保存前自动实测连通性
- 连接方式在添加时确定，编辑时锁定（避免两套连接字段互相污染）
- ONVIF 设备自动发现、局域网 WS-Discovery 探测、摄像头启停/重启、在线状态监控
- 实时预览（HLS 低延迟）、PTZ 云台控制（ONVIF）、手动抓拍
- 预览源选择：主码流（高清）/ 子码流（省带宽/CPU，多画面推荐）

### 🎬 录像与回放
- 录像策略：连续录像、移动侦测录像（仅 ONVIF 摄像头）、定时录像
- 分段存储（默认 3 分钟/段，可配置），自动索引、快速定位
- 历史回放（HLS）、片段下载、抓拍图片管理（全局浏览、筛选、一键清空）

### 🔔 报警推送
- **ONVIF 事件驱动的移动侦测**：摄像头硬件上报移动事件 → 生成报警 → 自动触发录像
  - 录像时长 60 秒、冷却 60 秒（`camera.motion_record` 可配置）
- 分级报警（低/中/高/严重），去重与冷却
- 多渠道推送：Webhook（钉钉/飞书/企业微信/Gotify）、Email（SMTP）、SMS

### 💾 存储管理
- **本地存储**：磁盘空间监控、按保留天数/容量上限自动清理
- **WebDAV 存储**：远程归档、按保留天数/容量上限清理；支持「仅存 WebDAV」模式（上传即删本地，本地仅作临时缓冲）
- **MinIO / S3 对象存储**：远程归档、独立保留策略、可选「仅存 MinIO」模式
- 旧录像回放自动回退：本地缺失时走 WebDAV / MinIO 流式播放（支持 Range 拖动）
- 存储占用统计、手动清理

### 🛡 安全与运维
- **首次运行初始化**：全新安装不预设默认账号，浏览器打开后先做运行环境检测，再创建管理员账户
- **运行环境检测**：ffmpeg/ffprobe、数据库/录像/日志目录可写、磁盘空间、WebDAV/MinIO 连通性，逐项给出修复建议；集成在「系统设置 → 系统维护」可随时复查
- RBAC 三级角色：管理员/操作员/只读用户，JWT 认证
- 数据库备份/恢复、日志查看/清空、程序自更新（GitHub Releases 拉取）

---

## 🏗 技术架构

```
┌──────────────────────────────────────────────────────────────┐
│                        前端 (Vue 3)                          │
│   Vue 3 + Element Plus + Pinia + Vue Router + HLS.js + ECharts │
└──────────────────────────┬───────────────────────────────────┘
                           │ HTTP (REST) / WebSocket
┌──────────────────────────▼───────────────────────────────────┐
│                      API 服务 (Gin)                          │
│        JWT 认证 · 静态资源服务 · SPA 回退 · WebSocket         │
└──────────────────────────┬───────────────────────────────────┘
                           │
       ┌───────────────────┼──────────────────────┐
       ▼                   ▼                      ▼
┌──────────────┐  ┌────────────────┐  ┌──────────────────┐
│  摄像头管理   │  │    存储管理     │  │   报警 / 事件    │
│ ONVIF 发现   │  │ 本地 · WebDAV  │  │ ONVIF 事件订阅   │
│ RTSP 直连    │  │ MinIO/S3       │  │ Webhook/Email/   │
│ FFmpeg 拉流  │  │ 自动清理/回退  │  │ SMS 推送         │
│ PTZ 控制     │  │                │  │                  │
└──────────────┘  └────────────────┘  └──────────────────┘
       │                   │                      │
       └───────────────────┼──────────────────────┘
                           ▼
                  ┌──────────────────────┐
                  │  基础设施             │
                  │ SQLite + FFmpeg      │
                  └──────────────────────┘
```

### 技术栈

| 端 | 技术 |
|----|------|
| 后端 | Go 1.22+ · Gin · GORM · SQLite（纯 Go 驱动，零 CGO）· FFmpeg · Viper · logrus |
| 前端 | Vue 3 · TypeScript · Element Plus · Pinia · Vue Router · HLS.js · ECharts |
| 协议 | ONVIF（发现/PTZ/事件）+ RTSP（拉流） |

---

## 🚀 快速开始

### 环境要求

| 依赖 | 要求 | 说明 |
|------|------|------|
| FFmpeg | `ffmpeg` + `ffprobe` 在 PATH 中 | 拉流/预览/录像/流探测全部依赖，**唯一必装依赖** |
| 其他 | 无 | SQLite 为纯 Go 内置驱动，无需 CGO、无需数据库服务 |

安装 FFmpeg：

```bash
# Debian / Ubuntu / Armbian
sudo apt update && sudo apt install -y ffmpeg

# RHEL / CentOS / Fedora
sudo dnf install -y ffmpeg
```

> 如果 FFmpeg 缺失或目录不可写，服务仍可启动，但浏览器首次设置页与「系统维护 → 运行环境检测」会明确列出缺失项与修复命令。

### 方式一：systemd 一键安装（Linux 服务器推荐）

**一行式安装**（无需克隆仓库；启动前会先询问选择语言，首次安装再询问端口）：

```bash
curl -fsSL https://raw.githubusercontent.com/Assen998/cineye/main/deployments/deploy.sh | sudo bash
```

或克隆仓库后运行（效果相同，便于查看脚本）：

```bash
git clone https://github.com/Assen998/cineye.git
cd cineye
sudo bash deployments/deploy.sh        # 自动下载当前平台的最新 Release 资产
# 本地二进制安装：
sudo bash deployments/deploy.sh /path/to/cineye
# 卸载（数据保留，彻底删除需手动 rm -rf /opt/cineye）：
sudo bash deployments/deploy.sh --uninstall
```

- 自动识别架构（amd64 / arm64 / armv7），下载最新 Release 中对应的资产包
- **首次安装时可自定义端口**：脚本依次询问 HTTP 端口（默认 `8080`）与 WebSocket 端口（默认 `8081`），带合法性与占用检查，写入生成的 `config.yaml`；已有配置时不询问、不改动
- 安装到 `/opt/cineye`；首次安装自动生成 `config.yaml`，**已有配置始终保留、不覆盖**
- 自动创建 `cineye` systemd 服务：开机自启、崩溃自动重启
- 无法直连 GitHub 时通过代理下载：`sudo env http_proxy=http://<代理地址>:<端口> https_proxy=http://<代理地址>:<端口> bash deployments/deploy.sh`（一行式同理，`curl` 前加同样的 `http_proxy=...` 即可）
- 非交互（管道/CI）运行自动按系统语言选择；可用 `DEPLOY_LANG=zh|en` 强制指定；端口可用 `SURVEILLANCE_HTTP_PORT` / `SURVEILLANCE_WS_PORT` 指定

### 方式二：下载发行版（无需编译）

到 [GitHub Releases](https://github.com/Assen998/cineye/releases) 下载对应平台压缩包（如 `cineye-1.5.0-linux-arm64.tar.gz`）。解压后得到一个**不带版本号的稳定目录**（如 `cineye-linux-arm64/`）：

```
cineye                  # 单二进制（前端已内嵌）
config.yaml                 # 默认配置
README.md
```

```bash
cd cineye-linux-arm64
./cineye        # 自动读取同目录 config.yaml
```

### 方式三：源码构建

前端构建产物通过 Go 的 `go:embed` 直接嵌入后端二进制，最终产出**单个可执行文件**：

```bash
cd cineye

# 1) 先构建前端（生成 web/dist，供 go:embed 嵌入）
cd web && npm install && npm run build && cd ..

# 2) 再构建后端（自动嵌入前端 → 单二进制）
go build -o cineye ./cmd/server
```

交叉编译（零 CGO，纯静态，支持 linux/windows/darwin × amd64/arm64 等）：

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o cineye ./cmd/server
```

### 卸载

按你的安装方式选择对应命令。**默认保留数据（配置/录像），如需彻底删除请最后一步手动 `rm -rf`**——执行前请确认已备份。

**方式一（systemd 一键安装）：**

```bash
# 停止并移除服务（数据保留在 /opt/cineye）
sudo bash deployments/deploy.sh --uninstall
# 没有脚本时的等效命令：
sudo systemctl stop cineye && sudo systemctl disable cineye
sudo rm /etc/systemd/system/cineye.service && sudo systemctl daemon-reload
# 彻底删除（含数据/录像/配置）：
sudo rm -rf /opt/cineye
```

**方式二 / 方式三（二进制直接运行）：**

```bash
# 停止进程（前台运行直接 Ctrl+C）
pkill -x cineye
# 删除安装目录（目录内 data/、recordings/ 一并删除）
rm -rf /path/to/cineye-linux-arm64    # 换成你的实际安装目录
```

**Docker 部署：**

```bash
cd deployments
docker compose down          # 停止并移除容器（含 MinIO/Nginx，如启用），数据卷保留
docker compose down -v       # 连数据卷一起删除（配置/录像/日志全部清除）
rm -rf <项目目录>             # 可选：删除部署文件
```

### 首次运行（重要）

```bash
./cineye
```

启动后浏览器打开 `http://<服务器IP>:8080`：

1. **运行环境检测**：页面自动检测 ffmpeg/ffprobe、目录权限、磁盘空间、可选存储服务连通性；
   未通过项会直接给出修复命令（如 `sudo apt install -y ffmpeg`），**关键项未通过时无法继续**，修复后点「重新检测」
2. **创建管理员账户**：设置用户名/密码（至少 6 位），点击「完成设置并进入系统」
3. 完成设置后进入仪表盘；**此设置页之后不再可访问**（后端接口同步失效）

> 全新安装不预设任何默认账号；已有数据库升级则跳过设置直接登录。

- Web 界面：`http://localhost:8080`
- 健康检查：`GET http://localhost:8080/health`

### 升级

目录名不带版本号，升级时**直接在新版压缩包上解压覆盖同名目录**即可：

```bash
cd /root    # 假设部署在 /root/cineye-linux-arm64
cp cineye-linux-arm64/config.yaml /tmp/config.yaml.bak   # ① 备份你的配置
tar xzf ~/cineye-1.5.0-linux-arm64.tar.gz                # ② 覆盖解压
cp /tmp/config.yaml.bak cineye-linux-arm64/config.yaml   # ③ 恢复配置
systemctl restart cineye   # ④ 重启（前台运行则重新 ./cineye）
```

- `data/`（数据库）、`recordings/`（录像）**不在包内，覆盖解压自动保留**
- `config.yaml` 在包内，覆盖解压会被重置为默认值 → 按 ①③ 备份恢复（或升级后在网页「系统设置」里重新保存各项配置）
- systemd 服务、开机自启等对目录的引用**无需任何改动**
- 也可在网页「系统设置 → 系统维护」里用**程序自更新**一键升级（GitHub Releases 拉取）

---

## 📖 配置说明

配置文件位于 `configs/config.yaml`（源码）/ 产物目录内 `config.yaml`（发行版）。大多数设置也可在网页「系统设置」中热更新并持久化。关键配置项：

```yaml
server:
  host: 0.0.0.0
  http_port: 8080          # Web/API 端口
  ws_port: 8081            # WebSocket 端口
  mode: release

database:
  type: sqlite
  sqlite:
    path: ./data/surveillance.db

storage:
  local:
    enabled: true
    root_path: ./recordings   # 录像目录
    segment_duration: 180     # 分段时长（秒）
    max_days: 7               # 保留天数（0=不自动删）
    max_storage_gb: 10        # 容量上限（GB，0=不限制）
    cleanup_interval: 3600    # 清理检查间隔（秒）
  webdav:
    enabled: true
    url: http://127.0.0.1:5244/dav
    username: ""
    password: ""
    base_path: surveillance
    max_days: 30
    max_storage_gb: 150
    only: false               # true=仅存 WebDAV（上传即删本地，本地仅作临时缓冲）
  minio:
    enabled: false            # 可选对象存储
    endpoint: localhost:9000
    access_key: minioadmin
    secret_key: minioadmin
    bucket: surveillance
    use_ssl: false
    base_path: surveillance
    max_days: 30
    max_storage_gb: 0
    only: false               # true=仅存 MinIO（上传即删本地）

camera:
  preview_stream: main        # 预览源：main=主码流 | sub=子码流（低清省资源）
  onvif_event:
    enabled: true             # ONVIF 事件订阅（移动侦测依赖此项）
    poll_interval: 10
    subscription_timeout: 60
  motion_record:
    duration: 60              # 移动侦测录像时长（秒）
    pre_record: 0
    cooldown: 60              # 冷却（秒）

alert:
  enabled: true
  channels:
    webhook:                  # 钉钉/飞书/企业微信/Gotify
      enabled: true
      url: ""
    email:
      enabled: false          # SMTP
    sms:
      enabled: false          # 阿里云短信

logging:
  level: info
  output: ./logs/surveillance.log

update:
  proxy: ""                   # 自更新检查/下载的 HTTP 代理（直连 GitHub 不稳时配置）
```

> 配置中的 `redis`、`postgres`、`gb28181` 为预留项，当前版本默认使用 SQLite，可按需扩展。

---

## 📡 摄像头接入指南

### ONVIF 自动发现（推荐）

适用于支持 ONVIF 的摄像头（海康、大华、TP-Link 等主流品牌）：

1. 摄像头管理 → 添加摄像头 → 选择「ONVIF 自动发现」
2. 填写 IP/用户名/密码，点击「自动探测并填充配置」
3. 系统自动获取设备信息、Profile（主/子码流）、RTSP 流地址，选择 Profile 后保存
4. 保存后自动拉流；ONVIF 事件订阅同步建立（移动侦测报警可用）

### RTSP 流地址直连

适用于：无 ONVIF、ONVIF 配置困难、或已有确定可用的 RTSP 地址（NVR 转推、其他设备转发等）：

1. 摄像头管理 → 添加摄像头 → 选择「RTSP 流地址」
2. 填写名称，粘贴**可正常播放的完整 RTSP 地址**（含账号密码），例如海康威视主码流：
   ```
   rtsp://admin:admin@192.168.1.64:554/Streaming/Channels/101?transportmode=unicast
   ```
   子码流将 `101` 改为 `102`；无账号密码的流直接填 `rtsp://IP:端口/路径`
3. 保存时后端自动 ffprobe 实测连通性，不通会明确报错，不会存入连不上的摄像头
4. 注意：RTSP 直连**没有 ONVIF 事件上报**，因此录像类型只有「连续录像/定时录像」；PTZ 也不可用（需 ONVIF）

### 编辑摄像头

- 连接方式在添加时确定，编辑时锁定不可更换（如需更换请删除后重新添加）
- ONVIF 摄像头可重新探测刷新 Profile；RTSP 摄像头可修改流地址（密码不回显，不修改则沿用原账号密码）

---

## 🎬 录像类型说明

| 类型 | 说明 | 适用 |
|------|------|------|
| 连续录像 | 7×24h 常驻录像流，按分段时长切段 | 全部摄像头 |
| 移动侦测录像 | 无常驻录像流，仅 ONVIF 事件触发时录 60 秒（冷却 60 秒） | **仅 ONVIF 摄像头** |
| 定时录像 | 按小时区间录像（如 `9-18,22-6`） | 全部摄像头 |

---

## 💾 存储模式说明

| 模式 | 本地 | 远程（WebDAV/MinIO） | 适用场景 |
|------|------|------|----------|
| 仅本地 | 存储并自动清理 | — | 单机部署 |
| 本地 + 远程归档 | 存储并自动清理 | 上传归档并独立清理 | 本地快存 + 远程长期保存 |
| 仅存远程（`only: true`） | 仅作上传前临时缓冲，上传成功即删 | 主存储 | 本地磁盘小、以远程为主存储 |

- 「仅存远程」模式下旧录像回放自动走远程流式播放（支持 Range 拖动）
- WebDAV 与 MinIO 可同时启用，互不冲突（同一录像分别归档）

---

## 📡 API 接口

统一前缀 `/api/v1`，除登录与首次设置外均需 `Authorization: Bearer <token>`。

### 首次设置（公开，设置完成后自动失效）
| 方法 | 路径 | 说明 |
|-----|------|------|
| GET | `/setup/status` | 是否需首次设置 + 环境检测报告 |
| POST | `/setup` | 创建管理员 `{username, password}`，返回 token |

### 认证
| 方法 | 路径 | 说明 |
|-----|------|------|
| POST | `/auth/login` | 登录 |
| POST | `/auth/logout` | 登出 |
| GET | `/auth/me` | 当前用户 |
| PUT | `/auth/password` | 修改密码 |

### 摄像头管理
| 方法 | 路径 | 说明 |
|-----|------|------|
| GET | `/cameras` | 列表 |
| POST | `/cameras` | 创建（RTSP 协议自动 ffprobe 实测） |
| GET | `/cameras/:id` | 详情 |
| PUT | `/cameras/:id` | 更新（密码留空=不修改） |
| DELETE | `/cameras/:id` | 删除 |
| GET | `/cameras/:id/status` | 运行状态 |
| POST | `/cameras/:id/start` / `stop` / `restart` | 启停/重启 |
| POST | `/cameras/:id/snapshot` | 手动抓拍 |
| POST | `/cameras/:id/ptz` | PTZ 控制（ONVIF） |
| GET | `/cameras/:id/snapshots` | 摄像头抓拍列表 |
| GET | `/cameras/discover` | ONVIF 自动发现 |
| POST | `/cameras/discover/lan` | 局域网 WS-Discovery 探测 |
| GET | `/cameras/probe` | ONVIF 探测（添加前预检） |

### 录像管理
| 方法 | 路径 | 说明 |
|-----|------|------|
| GET | `/recordings` | 列表（分页/筛选） |
| GET | `/recordings/:id` | 详情 |
| DELETE | `/recordings/:id` | 删除 |
| GET | `/recordings/camera/:cameraId` | 指定摄像头录像 |
| GET | `/recordings/camera/:cameraId/segments` | 时间段片段 |

### 抓拍图片
| 方法 | 路径 | 说明 |
|-----|------|------|
| GET | `/snapshots` | 全局抓拍列表 |
| DELETE | `/snapshots` | 一键清空 |
| DELETE | `/snapshots/:id` | 删除单张 |

### 报警
| 方法 | 路径 | 说明 |
|-----|------|------|
| GET | `/analytics/alerts` | 报警列表 |
| DELETE | `/analytics/alerts` | 清空报警 |
| GET | `/analytics/alerts/:id` | 报警详情 |
| PUT | `/analytics/alerts/:id/ack` / `resolve` | 确认/解决 |
| DELETE | `/analytics/alerts/:id` | 删除 |
| GET / PUT | `/alerts/config` | 报警配置 |
| POST | `/alerts/test` | 发送测试报警 |

### 存储与设置
| 方法 | 路径 | 说明 |
|-----|------|------|
| GET | `/storage/stats` | 存储统计 |
| POST | `/storage/cleanup` | 触发清理 |
| GET / PUT | `/settings/storage` | 存储设置（本地/WebDAV/MinIO，热更新） |
| POST | `/settings/webdav/test` | WebDAV 连接测试 |
| POST | `/settings/minio/test` | MinIO 连接测试 |
| GET / PUT | `/settings/camera` | 摄像头默认配置（抓拍等） |
| GET / PUT | `/settings/camera-defaults` | 摄像头默认配置 |

### 流媒体 / 回放
| 方法 | 路径 | 说明 |
|-----|------|------|
| GET | `/stream/camera/:id/hls` | 实时 HLS 播放列表 |
| GET | `/stream/camera/:id/hls/*file` | HLS 切片 |
| GET | `/stream/camera/:id/mp4` | 最新 MP4 片段 |
| GET | `/stream/camera/:id/snapshot` | 最新抓拍 |
| GET | `/stream/camera/:id/recordings/:recordingId/hls` | 历史录像 HLS |
| GET | `/recordings/:id/file` | 录像文件（本地→WebDAV→MinIO 自动回退） |
| GET | `/recordings/:id/download` | 下载录像 |
| GET | `/webdav/list` / `/webdav/file` | WebDAV 文件列表/流 |
| GET | `/minio/list` / `/media/minio/file` | MinIO 文件列表/流（Range） |

### 系统
| 方法 | 路径 | 说明 |
|-----|------|------|
| GET | `/system/info` | 系统信息 |
| POST | `/system/restart` | 重启系统 |
| GET | `/system/env` | 运行环境检测（与首次设置页同一逻辑） |
| GET | `/system/logs` / `/system/logs/files` | 日志查看 |
| POST | `/system/logs/clear` | 清空日志 |
| POST | `/system/backup` | 数据库备份 |
| GET / DELETE | `/system/backups` | 备份列表/删除 |
| GET | `/system/update/check` | 检查更新 |
| POST | `/system/update` | 执行自更新 |

### WebSocket
| 路径 | 说明 |
|------|------|
| `/ws` | 全局实时事件（报警/状态/录像） |
| `/api/v1/ws/camera/:cameraId` | 摄像头实时事件 |

---

## 🐳 Docker 部署

部署文件位于 `deployments/`。除主应用外，MinIO（对象存储）与 Nginx（HTTPS 反代）通过 compose profile 可选启用：

```bash
cd deployments
docker compose up -d                          # 仅主应用
docker compose --profile minio up -d          # + MinIO（9000 API / 9001 控制台）
docker compose --profile nginx up -d          # + Nginx（需先准备 deployments/ssl/ 证书）
```

数据持久化在 `cineye-data` / `cineye-recordings` / `cineye-logs` 三个命名卷中，容器内路径 `/app/data`、`/app/recordings`、`/app/logs`。

> 系统默认无需 Redis / PostgreSQL 即可运行（SQLite 纯 Go 驱动）；MinIO 仅在配置了对象存储时才需要。

---

## 🔧 开发指南

### 目录结构

```
cineye/
├── cmd/server/             # 主程序入口
├── internal/
│   ├── api/                # HTTP API 处理、路由注册、环境检测
│   ├── camera/             # 摄像头管理、录像触发、ONVIF 连接
│   ├── storage/            # 存储管理（本地 / WebDAV / MinIO、运行时设置）
│   ├── alert/              # 报警推送（Webhook/Email/SMS）
│   ├── onvifevent/         # ONVIF 事件订阅（移动侦测上报）
│   ├── config/             # 配置加载与持久化
│   ├── database/           # 数据库初始化与首启检测
│   └── models/             # 数据模型
├── pkg/
│   ├── ffmpeg/             # FFmpeg 封装（拉流 / HLS / 分段 / 探测）
│   ├── onvif/              # ONVIF 客户端
│   ├── webdav/             # WebDAV 客户端
│   ├── minio/              # MinIO/S3 客户端
│   └── gb28181/            # GB28181 协议（预留）
├── web/                    # Vue 3 前端
│   └── src/
│       ├── api/            # API 请求封装
│       ├── views/          # 页面视图（含 Setup.vue 首次设置）
│       ├── stores/         # Pinia 状态
│       ├── router/         # 路由配置
│       └── components/     # 通用组件（EnvCheckTable 等）
├── configs/config.yaml     # 配置文件
└── deployments/            # 部署（systemd 一键安装 + Docker）
```

### 关键流程

- **移动侦测录像**：摄像头通过 ONVIF 事件上报移动 → `onvifevent` 生成报警 → `TriggerMotionRecording` 按 `camera.motion_record` 配置录制并进入冷却
- **录像归档**：分段完成 → 本地落盘 → 并行上传 WebDAV/MinIO（3 次重试）→ `only` 模式删本地 → 回写 `storage_path`
- **回放回退**：请求录像文件 → 本地存在直接流式 → 否则 WebDAV → 否则 MinIO（均支持 Range）
- **首次设置**：启动时检测数据库文件是否新建（`FirstRun`）→ 跳过默认 admin 种子 → 前端 `/setup` 页环境检测 + 创建管理员 → `POST /setup` 后接口失效

### 添加新功能模块

1. 在 `internal/` 下创建模块目录
2. 实现 `Start()` / `Stop()` 生命周期方法
3. 在 `cmd/server/main.go` 中注册并启动
4. 在 `internal/api/server.go` 中添加路由
5. 前端添加对应页面和 API 调用

---

## 🔍 常见问题

### 首次设置页提示环境检测未通过
- 每项检测都带具体修复命令，常见：
  - `ffmpeg`/`ffprobe` 缺失：`sudo apt update && sudo apt install -y ffmpeg`（Debian/Armbian）或 `sudo dnf install -y ffmpeg`
  - 目录不可写：`sudo chown -R <服务运行用户> <目录>`
  - 磁盘空间不足：`df -h` 查看，清理或扩容
- 修复后点「重新检测」；「系统设置 → 系统维护 → 运行环境检测」可随时复查

### 摄像头显示离线 / 连接失败
1. 检查网络连通性：`ping <camera_ip>`，确认 RTSP 端口（通常 554）可达
2. 用 VLC 验证 RTSP 地址能否播放
3. 确认摄像头编码为 H.264 / H.265
4. 查看服务日志（系统维护 → 日志）定位错误信息

### 视频无法播放 / 卡顿
1. 确认 FFmpeg 已安装：`ffmpeg -version`
2. 多画面/低带宽场景：将 `camera.preview_stream` 改为 `sub`（子码流，ARM 设备推荐）
3. 调整 `storage.local.segment_duration` 与 HLS 切片时长
4. 检查磁盘写入速度是否成为瓶颈

### 移动侦测录像不触发
- 移动侦测**仅 ONVIF 摄像头可用**（依赖摄像头 ONVIF 事件上报），RTSP 直连摄像头无此选项
- 请确认：
  1. 摄像头以 ONVIF 方式接入且支持 ONVIF 事件（Analytics / MotionDetection）
  2. `camera.onvif_event.enabled` 为 `true`
  3. 录像类型设置为「移动侦测录像」

### 磁盘空间不足
1. **开启「仅存 WebDAV/MinIO」模式**（系统设置 → 存储设置）：录像上传成功即删本地副本，本地只保留上传前临时缓冲；旧录像回放自动走远程流式播放
2. 减小 `max_days` 或增大分段清理频率
3. 设置 `max_storage_gb` 容量上限
4. 启用 WebDAV / MinIO 远程归档分流
5. 手动触发清理：`POST /api/v1/storage/cleanup`

### 忘记管理员密码
- 当前无在线重置通道。备份过数据库的，可用备份恢复；否则删除 `data/surveillance.db` 重新初始化（**会丢失全部摄像头/录像索引配置**），重新走首次设置。

---

## 🤝 贡献指南

1. Fork 项目
2. 创建特性分支：`git checkout -b feat/amazing-feature`
3. 提交变更：`git commit -m 'feat: add amazing feature'`
4. 推送分支：`git push origin feat/amazing-feature`
5. 发起 Pull Request

---

## 🙏 致谢

- [FFmpeg](https://ffmpeg.org/) — 多媒体处理核心
- [ONVIF](https://www.onvif.org/) — 网络视频接口标准
- [Gin](https://github.com/gin-gonic/gin) — Go Web 框架
- [Vue.js](https://vuejs.org/) — 渐进式前端框架
- [Element Plus](https://element-plus.org/) — Vue 3 组件库
- [HLS.js](https://github.com/video-dev/hls.js/) — HTTP Live Streaming 播放器
- [GORM](https://gorm.io/) — Go ORM 框架
- [MinIO](https://min.io/) — 高性能对象存储
