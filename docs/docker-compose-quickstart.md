# Docker Compose one-command guide

[English](docker-compose-quickstart.md) | [简体中文](docker-compose-quickstart.zh-CN.md)

Two ways to run PlatFarm with Compose. **Path A** is the default for development (build from this repo). **Path B** pulls GHCR images after a `v*` release (no app compile).

Need Docker Compose v2 (`docker compose version`). Podman: `PF_CONTAINER_CLI=podman` (see `.env.example`).

---

## Path A — from source (recommended first time)

You have Git and Docker. Images are **built locally**.

```bash
git clone https://github.com/platfarmai/platfarm.git
cd platfarm
cp .env.example .env
docker compose --profile bundled-db up -d --build
```

Wait until `auth` is healthy (`docker compose ps`). Then:

| URL | What |
|---|---|
| http://localhost:18000/ | Platform index JSON |
| http://localhost:18000/api/demo/public/ping | Demo service (no token) |
| http://localhost:18001/ | Admin console (localhost only; `admin` / `admin123`) |

Login:

```bash
curl -s -X POST http://localhost:18000/auth/login \
  -H "Content-Type: application/json" \
  -d '{"Username":"admin","Password":"admin123"}'
```

Use `accessToken` as `Authorization: Bearer …` on `/api/demo/me`. Second seed user: `alice` / `user123`.

**What `--profile bundled-db` does:** starts the Postgres container (`pf_auth`). If you already have Postgres, set `DATABASE_URL` in `.env` (see `.env.example` modes B/C) and omit the profile:

```bash
docker compose up -d --build
```

**Keys:** first `pctl sync` or first auth start creates `.keys/` (gitignored). Do not commit them.

**Stop / wipe:**

```bash
docker compose --profile bundled-db down          # keep volumes
docker compose --profile bundled-db down -v      # drop Postgres/Redis data
```

**Scale replicas** (Redis is already in the stack):

```bash
docker compose up -d --scale auth=2 --scale svc-demo=2
```

---

## Path B — pre-built GHCR images (no `--build`)

Use after [Release images](../.github/workflows/release-images.yml) has published `ghcr.io/platfarmai/auth` (and console). You still need **keys + `gateway/kong.yml`** (signing/verification are not inside the public image).

```bash
mkdir platfarm-run && cd platfarm-run
curl -fsSL https://raw.githubusercontent.com/platfarmai/platfarm/main/deploy/compose.release.yml -o compose.yml
curl -fsSL https://raw.githubusercontent.com/platfarmai/platfarm/main/deploy/env.release.example -o .env
mkdir -p gateway .keys
# One-time from a platfarm checkout (or copy from a machine that already ran Path A):
#   cp /path/to/platfarm/gateway/kong.yml gateway/
#   cp /path/to/platfarm/.keys/pf-auth.pem* .keys/
docker compose --profile bundled-db up -d
```

Pin a version in `.env`: `PF_IMAGE_TAG=v0.1.0`.

If GHCR packages are still private: `echo $GITHUB_TOKEN | docker login ghcr.io -u USER --password-stdin`, or set packages to Public (org → Packages).

Full image list and `FROM ghcr.io/platfarmai/runtime-*` for your own services: [consuming-images.md](consuming-images.md).

---

## Ports and files (both paths)

| Port | Binding | Service |
|---|---|---|
| 18000 | `0.0.0.0` | Kong gateway (only public entry) |
| 18001 | `127.0.0.1` | Console |
| (none) | internal | auth, redis, postgres, business services |

| File | Role |
|---|---|
| `docker-compose.yml` | Base: auth, console, redis, optional postgres |
| `docker-compose.services.yml` | **Generated** (`pctl sync`): gateway + app services — do not edit |
| `gateway/kong.yml` | **Generated** Kong config |
| `.env` | Secrets and URLs (from `.env.example`) |
| `.keys/` | RS256 key pair |

---

## Common failures

| Symptom | Likely cause |
|---|---|
| `auth` never healthy | Postgres unreachable — check `DATABASE_URL` / `bundled-db` profile |
| Login 502 / gateway starting | Wait for `docker compose ps` gateway **healthy** |
| `401` on `/api/demo/me` | Missing or expired Bearer token; login again |
| Kong JWT errors after copying keys | `kong.yml` rsa_public_key must match `.keys/pf-auth.pem.pub` — run `pctl sync` on the source tree |
| Port 18000 in use | Stop the other process or change the host port in compose |
