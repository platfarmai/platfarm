# 使用已发布的 PlatFarm 镜像

[English](consuming-images.md) | [简体中文](consuming-images.zh-CN.md)

每次打 `v*` 标签（或手动跑 **Release images**）会推送到 [GHCR](https://github.com/orgs/platfarmai/packages)：

| 镜像 | 用途 |
|---|---|
| `ghcr.io/platfarmai/auth` | 身份服务 |
| `ghcr.io/platfarmai/console` | 管理台 |
| `ghcr.io/platfarmai/runtime-go` | Go 二进制的 `FROM`（含 wget） |
| `ghcr.io/platfarmai/runtime-python` | Python + FastAPI/uvicorn/PyJWT |
| `ghcr.io/platfarmai/runtime-php` | PHP 8.3 CLI |
| `ghcr.io/platfarmai/runtime-rust` | Rust 二进制的 `FROM` |

标签：`v1.2.3`、`1.2`、`latest`、git SHA。需在 GHCR 把包可见性改为 Public。

网关仍用 Docker Hub 的 `kong:3.9`；Postgres/Redis 用官方镜像。

---

## A. 不 clone 源码、拉镜像启动

仍需要工作区（密钥 + `gateway/kong.yml`）：

```bash
mkdir platfarm-run && cd platfarm-run
curl -fsSL https://raw.githubusercontent.com/platfarmai/platfarm/main/deploy/compose.release.yml -o compose.yml
curl -fsSL https://raw.githubusercontent.com/platfarmai/platfarm/main/deploy/env.release.example -o .env
git clone --depth 1 https://github.com/platfarmai/platfarm.git _src
mkdir -p gateway .keys
cp _src/gateway/kong.yml ./gateway/kong.yml
# 密钥：在有 pctl 的机器上 pctl sync 生成后拷贝 .keys/
docker compose --profile bundled-db up -d
```

`.env` 里 `PF_IMAGE_TAG=v0.1.0` 可钉死版本。

**必备本地文件**：`.keys/pf-auth.pem`（私钥，勿提交）、`.keys/pf-auth.pem.pub`、`gateway/kong.yml`（`pctl sync` 产物）。

---

## B. 业务服务基于 runtime 镜像

Go：

```dockerfile
FROM ghcr.io/platfarmai/runtime-go:latest
COPY --chown=platfarm:platfarm server /server
ENTRYPOINT ["/server"]
```

Python：

```dockerfile
FROM ghcr.io/platfarmai/runtime-python:latest
COPY --chown=platfarm:platfarm . /app
CMD ["uvicorn", "main:app", "--host", "0.0.0.0", "--port", "8080", "--timeout-graceful-shutdown", "25"]
```

compose 挂公钥：`./.keys/pf-auth.pem.pub:/pf/jwt.pub:ro`。实现 `/healthz`、`/readyz`，RS256 验 `iss=pf-auth`。

---

## C. 只拉 auth 嵌进自己的 compose

见英文版 §C。JWKS：`GET http://auth:8080/auth/.well-known/jwks.json`。

首次 workflow 跑完后：组织 Packages → 各包 → 可见性改为 Public，否则下游需要 `docker login ghcr.io`。
