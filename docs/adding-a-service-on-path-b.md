# 路径 B（镜像部署）上挂第一方服务

[English](adding-a-service-on-path-b.md) · 本文记录生产环境把 `services/svc-*` 接到 `deploy/compose.release.yml` 时踩过的坑。源码仓接入仍看 [adding-a-service.md](adding-a-service.md)。

> **pctl 已内置支持（推荐先看这一节）**
>
> 在运行目录的 `.env` 里加一行，`pctl sync` 就能扫到放在别处的插件清单，自动生成 Kong 路由与 compose 片段，不必再手改 `gateway/kong.yml`：
>
> ```bash
> PF_SERVICES_DIR=_src/services
> ```
>
> 多个目录用 `:`（Windows 用 `;`）分隔，相对路径相对运行目录解析。
>
> 同时，生成的编排已做两项改进：
>
> - **健康检查默认不依赖 `wget`**，改用 PID 1 存活检查，精简 Alpine / distroless 镜像都能通过。需要真正的就绪探测时在清单里写 `runtime.healthcheck: http`（要求镜像自带 wget），不想要探活写 `none`。
> - **环境变量支持服务专属覆盖**：`${SVC_ADS_DATABASE_URL:-${DATABASE_URL}}`。给插件配独立数据库时只需在 `.env` 写 `SVC_ADS_DATABASE_URL=...`，**不会影响 auth**。
>
> 下面保留手工接法，供不想改 `.env` 或需要完全掌控的场景参考。

路径 B 的运行目录（例如 `platfarm-run/`）**只有** `compose.yml`（从 `deploy/compose.release.yml` 拷来）、`.env`、`.keys/`、`gateway/kong.yml`。它：

- **没有** `services/`，`pctl sync` 扫不到任何 `plugin.yaml` / `service.yaml`
- **没有** `include: docker-compose.services.yml`
- 启动命令是 `docker compose -f compose.yml …`，**不会**自动合并根目录的 `docker-compose.override.yml`
- 生成的健康检查默认 `wget http://127.0.0.1:8080/readyz`，精简 Alpine 业务镜像往往既没有 `wget` 也没有 `/readyz`

因此在运行目录执行 `docker compose up -d svc-ads` 会得到 **`no such service: svc-ads`**。这不是镜像坏了，是这份 compose 里根本没有该服务。

## 正确接法

把插件源码放到运行目录旁（不要解压成 `platfarm-ads/` 这种和 `id` 不一致的名字）：

```text
platfarm-run/
  compose.yml              # 路径 B，勿改
  compose.ads.yml          # 你加的业务服务片段
  .env
  .keys/
  gateway/kong.yml
  _src/services/svc-ads/   # 目录名 == plugin.yaml 的 id
      plugin.yaml
      Dockerfile
      …
```

### 1. 独立数据库

`data.database` 必须是 `pf_<id>`（如 `pf_svc_ads`）。**不要**把运行目录 `.env` 里的 `DATABASE_URL` 改成业务库——那条是 **auth** 用的。

在现有 Postgres 上：

```sql
CREATE ROLE <role> LOGIN PASSWORD '<pwd>';
CREATE DATABASE pf_svc_ads OWNER <role>;
```

业务容器用**另一条** URL，只写在 `compose.ads.yml`。

### 2. 业务 compose 片段

`compose.ads.yml` 示例（网络名必须和路径 B 一致：`core-net`）：

```yaml
services:
  svc-ads:
    build: ./_src/services/svc-ads
    extra_hosts:
      - "host.docker.internal:host-gateway"
    environment:
      DATABASE_URL: postgres://<role>:<pwd>@host.docker.internal:5432/pf_svc_ads
    volumes:
      - ./.keys/pf-auth.pem.pub:/pf/jwt.pub:ro
    healthcheck:
      test: ["CMD-SHELL", "kill -0 1"]
      interval: 5s
      timeout: 3s
      retries: 3
      start_period: 20s
    networks: [core-net]
    depends_on:
      auth:
        condition: service_healthy
```

不要用 `test: ["CMD", "/server"]`：健康检查会再拉起一份进程。精简镜像没有 `wget` 时，不要用生成编排里的 `/readyz` 探活。

### 3. 网关路由

`pctl sync` 在路径 B 运行目录是空转（没有 `services/`）。任选其一：

- 在**完整源码仓**里放好 `services/svc-ads` 后 `pctl sync`，把生成的 `gateway/kong.yml` **整份**拷到运行目录（公钥必须与该机 `.keys/*.pub` 一致）。
- 或按 `plugin.yaml` 的 `public_routes` 手工把服务块并进现有 `gateway/kong.yml`：公开路径免 JWT，挂载前缀 `/api/<name>` 走 jwt 插件。

改完必须 `docker compose -f compose.yml restart gateway`。Kong DB-less 只在启动时读 declarative 文件，只 `cp` 不重启会继续 404。

公开路由用浏览器 `GET` 测；`curl -I`（HEAD）打到只注册了 GET 的 Gin 路由会 404。

### 4. 启动命令（整条都要带两个 `-f`）

```bash
cd platfarm-run
docker compose -f compose.yml -f compose.ads.yml up -d --build svc-ads
docker compose -f compose.yml -f compose.ads.yml restart gateway
```

以后更新业务代码仍用上面两条，**不要**在运行目录裸 `docker compose up svc-ads`。

### 5. 站点配置进库，不进 compose

业务名单 / 购买密钥若已做成管理 API（如 ads 的 `/api/ads/sites`），compose 里不要再配 `ADS_ALLOWED_SITES` / `ADS_PURCHASE_TOKEN`。`.env` 只留 auth 的 `DATABASE_URL` 和 Redis。

## 对照表

| 现象 | 原因 | 处理 |
|---|---|---|
| `no such service: svc-ads` | 只用了 `compose.yml`，或目录不叫 `svc-ads` | `-f compose.yml -f compose.ads.yml`；目录名 = `id` |
| `pctl sync` 服务数没增加 | 运行目录没有 `services/*/plugin.yaml` | 源码放到 `_src/services/<id>`，sync 在完整仓做，或手改 kong |
| 改了 `docker-compose.override.yml` 无效 | 路径 B 启动没带默认 override 文件名，且命令写了 `-f compose.yml` | 显式 `-f compose.ads.yml` |
| auth 起不来 / 用户表空了 | 把 `.env` 的 `DATABASE_URL` 改成了业务库 | auth 保持原库；业务 URL 只写在业务片段 |
| 容器 `unhealthy` 但日志在听 8080 | 健康检查是 `wget /readyz` 或 `/server` 双进程 | `kill -0 1` 或镜像内真实 `/healthz` |
| 网关 404，文件里已有路由 | 没重启 gateway，或 HEAD 打 GET 路由 | `restart gateway`；用 GET |
| 网关 502 | 业务容器刚 recreate | 等 healthy 再打 |
| `Unknown site` | 采集按 Origin 认站，尚未在管理页登记域名 | 打开 `/api/<name>/public/console` 配 origins |

Nginx / 宝塔反代只需要把域名指到 `127.0.0.1:18000`。业务 UI 若挂在公开路由（如 `/api/ads/public/console`），走网关即可，不必再给 8080 开端口。
