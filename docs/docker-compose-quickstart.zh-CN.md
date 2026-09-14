# Docker Compose 一键使用说明

[English](docker-compose-quickstart.md) | [简体中文](docker-compose-quickstart.zh-CN.md)

**路径 A**：源码构建。**路径 B**：拉 GHCR 镜像，用 **Release 里的 `pctl` 二进制**生成密钥——**不需要**本机装 Go，也**不要**把二进制或 `.keys/` 提交进仓库根目录。

需要 Docker Compose v2。Podman 见 `.env.example`。

---

## 路径 A — 源码构建（第一次建议走这条）

```bash
git clone https://github.com/platfarmai/platfarm.git
cd platfarm
cp .env.example .env
docker compose --profile bundled-db up -d --build
```

| 地址 | 作用 |
|---|---|
| http://localhost:18000/ | 平台入口 |
| http://localhost:18000/api/demo/public/ping | 演示（无需 token） |
| http://localhost:18001/ | 管理台（`admin` / `admin123`） |

```bash
curl -s -X POST http://localhost:18000/auth/login \
  -H "Content-Type: application/json" \
  -d '{"Username":"admin","Password":"admin123"}'
```

`--profile bundled-db` 启动自带 Postgres；已有库则改 `DATABASE_URL` 并去掉 profile。密钥由首次 `pctl sync` / auth 启动写入 `.keys/`。

```bash
docker compose --profile bundled-db down
docker compose up -d --scale auth=2 --scale svc-demo=2
```

---

## 路径 B — 镜像 + `pctl` 二进制（不用 Go）

打 `v*` 后，[Releases](https://github.com/platfarmai/pctl/releases) 有各平台 `pctl_*`，GHCR 有 `auth` / `console`。

```bash
mkdir platfarm-run && cd platfarm-run
VER=v0.0.1   # pctl 版本（见 platfarmai/pctl Releases，与平台镜像 v* 独立）
curl -fsSL -o pctl "https://github.com/platfarmai/pctl/releases/download/${VER}/pctl_${VER}_linux_amd64"
chmod +x pctl

curl -fsSL https://raw.githubusercontent.com/platfarmai/platfarm/main/deploy/compose.release.yml -o compose.yml
curl -fsSL https://raw.githubusercontent.com/platfarmai/platfarm/main/deploy/env.release.example -o .env

./pctl init .          # 生成 .keys/ 与 gateway/kong.yml（无需 clone、无需 Go）
docker compose --profile bundled-db up -d
```

`pctl init` 产出：`.keys/pf-auth.pem`（私钥，勿提交）、`.keys/pf-auth.pem.pub`、`gateway/kong.yml`（仅 auth 路由，足够登录/JWKS）。以后在完整仓库里 `pctl sync` 再换完整 kong 配置。

Windows：`pctl_${VER}_windows_amd64.exe`。macOS ARM：`darwin_arm64`。

旧说法「在有 pctl 的开发机上 sync 再拷贝 `.keys/`」绕了一圈；Release 附件本身就是 pctl，应在运行目录直接 `pctl init`。

镜像与业务 `FROM runtime-*`：[consuming-images.zh-CN.md](consuming-images.zh-CN.md)。

---

## 端口与常见问题

| 端口 | 绑定 | 服务 |
|---|---|---|
| 18000 | 公网 | Kong |
| 18001 | 本机 | 管理台 |

路径 B 默认**没有** svc-demo；只有 auth/console/redis/postgres。要演示 API 请用路径 A，或自行加服务后再 `pctl sync`。
