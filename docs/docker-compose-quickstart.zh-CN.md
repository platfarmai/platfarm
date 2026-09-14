# Docker Compose 一键使用说明

[English](docker-compose-quickstart.md) | [简体中文](docker-compose-quickstart.zh-CN.md)

两种跑法：**路径 A** 从本仓库源码构建（开发默认）；**路径 B** 打 `v*` 后拉 GHCR 镜像（不编译业务代码）。

需要 Docker Compose v2。Podman：见 `.env.example` 的 `PF_CONTAINER_CLI`。

---

## 路径 A — 源码构建（第一次建议走这条）

```bash
git clone https://github.com/platfarmai/platfarm.git
cd platfarm
cp .env.example .env
docker compose --profile bundled-db up -d --build
```

等 `auth` healthy（`docker compose ps`）后：

| 地址 | 作用 |
|---|---|
| http://localhost:18000/ | 平台入口 JSON |
| http://localhost:18000/api/demo/public/ping | 演示服务（无需 token） |
| http://localhost:18001/ | 管理台（仅本机；`admin` / `admin123`） |

登录：

```bash
curl -s -X POST http://localhost:18000/auth/login \
  -H "Content-Type: application/json" \
  -d '{"Username":"admin","Password":"admin123"}'
```

`accessToken` 放到 `Authorization: Bearer …` 调 `/api/demo/me`。另一个种子用户：`alice` / `user123`。

**`--profile bundled-db`：** 启动自带 Postgres（库 `pf_auth`）。已有数据库则在 `.env` 写 `DATABASE_URL`（见 `.env.example` 模式 B/C），不要加 profile：

```bash
docker compose up -d --build
```

**密钥：** 首次 `pctl sync` 或 auth 启动会写 `.keys/`（已 gitignore），不要提交。

**停止：**

```bash
docker compose --profile bundled-db down          # 保留数据卷
docker compose --profile bundled-db down -v      # 清空 Postgres/Redis
```

**多副本**（栈内已有 Redis）：

```bash
docker compose up -d --scale auth=2 --scale svc-demo=2
```

---

## 路径 B — 拉已发布镜像（不要 `--build`）

需已跑过 [Release images](../.github/workflows/release-images.yml)。本地仍要 **密钥 + `gateway/kong.yml`**。

```bash
mkdir platfarm-run && cd platfarm-run
curl -fsSL https://raw.githubusercontent.com/platfarmai/platfarm/main/deploy/compose.release.yml -o compose.yml
curl -fsSL https://raw.githubusercontent.com/platfarmai/platfarm/main/deploy/env.release.example -o .env
mkdir -p gateway .keys
# 从已跑过路径 A 的目录拷贝：
#   cp .../gateway/kong.yml gateway/
#   cp .../.keys/pf-auth.pem* .keys/
docker compose --profile bundled-db up -d
```

`.env` 里 `PF_IMAGE_TAG=v0.1.0` 可钉版本。

GHCR 包若仍是私有：`docker login ghcr.io`，或在组织 Packages 改为 Public。

镜像列表与业务 `FROM runtime-*`：[consuming-images.zh-CN.md](consuming-images.zh-CN.md)。

---

## 端口与文件（两条路径相同）

| 端口 | 绑定 | 服务 |
|---|---|---|
| 18000 | `0.0.0.0` | Kong（唯一对外入口） |
| 18001 | `127.0.0.1` | 管理台 |
| 无宿主机端口 | 内网 | auth、redis、postgres、业务服务 |

| 文件 | 作用 |
|---|---|
| `docker-compose.yml` | 基座：auth、console、redis、可选 postgres |
| `docker-compose.services.yml` | **生成物**（`pctl sync`），勿手改 |
| `gateway/kong.yml` | **生成物** Kong 配置 |
| `.env` | 从 `.env.example` 复制 |
| `.keys/` | RS256 密钥对 |

---

## 常见问题

| 现象 | 原因 |
|---|---|
| `auth` 一直不 healthy | 连不上 Postgres：检查 `DATABASE_URL` / 是否加了 `bundled-db` |
| 登录 502 | 等 gateway **healthy** |
| `/api/demo/me` 401 | 没带或过期了 Bearer，重新 login |
| 拷贝密钥后 Kong JWT 失败 | `kong.yml` 里公钥须与 `.keys/*.pub` 一致，在源码树执行 `pctl sync` |
| 18000 被占用 | 停掉占用进程或改 compose 宿主机端口 |
