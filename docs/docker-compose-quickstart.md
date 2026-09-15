# Docker Compose one-command guide

[English](docker-compose-quickstart.md) | [简体中文](docker-compose-quickstart.zh-CN.md)

Two ways to run PlatFarm with Compose. **Path A** builds from this repo. **Path B** pulls GHCR images and uses a **downloaded `pctl` binary** for keys — you do **not** need Go or a full checkout just to mint `.keys/`.

Need Docker Compose v2 (`docker compose version`). Podman: `PF_CONTAINER_CLI=podman` (see `.env.example`).

**Do not commit** `pctl` binaries or `.keys/` into the git repo root. Binaries ship on [GitHub Releases](https://github.com/platfarmai/pctl/releases); keys stay local.

---

## Path A — from source (recommended first time)

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
| http://localhost:18001/ | Admin console (`admin` / `admin123`) |

```bash
curl -s -X POST http://localhost:18000/auth/login \
  -H "Content-Type: application/json" \
  -d '{"Username":"admin","Password":"admin123"}'
```

Use `accessToken` as `Authorization: Bearer …` on `/api/demo/me`. Seed user: `alice` / `user123`.

**`--profile bundled-db`:** starts Postgres. Existing DB → set `DATABASE_URL` and omit the profile.

**Keys:** first `pctl sync` or first auth start creates `.keys/` (gitignored).

```bash
docker compose --profile bundled-db down          # keep volumes
docker compose --profile bundled-db down -v      # drop data
docker compose up -d --scale auth=2 --scale svc-demo=2
```

---

## Path B — pre-built images + `pctl` binary (no Go toolchain)

After a `v*` release, GHCR has `auth`/`console`, and the **Release** assets include `pctl_*` binaries.

```bash
mkdir platfarm-run && cd platfarm-run

# 1) CLI (example: Linux amd64 — pick your asset from the release page)
VER=v0.0.1   # pctl CLI tag (see platfarmai/pctl releases; independent of platfarm v*)
curl -fsSL -o pctl "https://github.com/platfarmai/pctl/releases/download/${VER}/pctl_${VER}_linux_amd64"
chmod +x pctl

# 2) Compose + env
curl -fsSL https://raw.githubusercontent.com/platfarmai/platfarm/main/deploy/compose.release.yml -o compose.yml
curl -fsSL https://raw.githubusercontent.com/platfarmai/platfarm/main/deploy/env.release.example -o .env
# optional: PF_IMAGE_TAG=$VER in .env

# 3) Keys + minimal kong.yml (no git clone, no Go)
./pctl init .

# 4) Start
docker compose --profile bundled-db up -d
```

`pctl init` writes:

| Path | Purpose |
|---|---|
| `.keys/pf-auth.pem` | RS256 private key (auth only — never commit) |
| `.keys/pf-auth.pem.pub` | Public key |
| `gateway/kong.yml` | Auth routes + JWT consumer (enough for login/JWKS) |

When you later add services from a full checkout, run `pctl sync` **in that checkout** (it looks for `services/*/plugin.yaml`) and copy the resulting `gateway/kong.yml` here. Do **not** run `pctl sync` inside `platfarm-run/` expecting `svc-*` to appear — Path B has no `services/` tree. Adding a first-party plugin on Path B: [adding-a-service-on-path-b.md](adding-a-service-on-path-b.md).

Windows: download `pctl_${VER}_windows_amd64.exe`, then `.\pctl.exe init .`.

macOS Apple Silicon: `pctl_${VER}_darwin_arm64`.

If GHCR packages are private: `docker login ghcr.io`, or set packages to Public.

Runtimes for your own services: [consuming-images.md](consuming-images.md).

---

## Ports and files

| Port | Binding | Service |
|---|---|---|
| 18000 | `0.0.0.0` | Kong |
| 18001 | `127.0.0.1` | Console |
| — | internal | auth, redis, postgres, apps |

| File | Role |
|---|---|
| Path A: `docker-compose.yml` + generated `docker-compose.services.yml` | Full stack |
| Path B: `compose.yml` from `deploy/compose.release.yml` | GHCR images |
| `.env` | URLs / passwords |
| `.keys/` | Local only |

---

## Common failures

| Symptom | Likely cause |
|---|---|
| `auth` never healthy | Bad `DATABASE_URL` / missing `bundled-db` |
| Login 502 | Gateway not healthy yet |
| `401` on `/api/demo/me` | Path B has no demo service until you add one / use Path A |
| JWT verify fails | `kong.yml` public key ≠ `.keys/*.pub` — re-run `pctl init` or `pctl sync` |
| Port 18000 busy | Change host port or stop the other process |
| Redis AUTH errors / Kong rate-limit fail | Set `PF_REDIS_URL=redis://:password@host:6379/0`, run `pctl sync`, recreate gateway — **no need to rebuild auth/oauth images** |
| `no such service: svc-xxx` | Path B `compose.yml` has no plugin services; `-f compose.yml` also skips `docker-compose.override.yml`. See [adding-a-service-on-path-b.md](adding-a-service-on-path-b.md) |
| `pctl sync` service count unchanged | Ran sync in `platfarm-run/` with no `services/` |
| Auth empty / wrong tables | `.env` `DATABASE_URL` was pointed at a plugin database |
