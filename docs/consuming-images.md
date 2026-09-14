# Using published PlatFarm images

[English](consuming-images.md) | [简体中文](consuming-images.zh-CN.md)

On every `v*` git tag (or manual **Release images** workflow), GitHub Actions pushes to [GHCR](https://github.com/orgs/platfarmai/packages):

| Image | Role |
|---|---|
| `ghcr.io/platfarmai/auth` | Identity service (login, JWT, JWKS) |
| `ghcr.io/platfarmai/console` | Admin UI (`pctl serve`) |
| `ghcr.io/platfarmai/runtime-go` | Alpine + wget — `FROM` for Go binaries |
| `ghcr.io/platfarmai/runtime-python` | Python 3 + FastAPI/uvicorn/PyJWT + wget |
| `ghcr.io/platfarmai/runtime-php` | PHP 8.3 CLI + wget |
| `ghcr.io/platfarmai/runtime-rust` | Alpine + wget — `FROM` for Rust binaries |

Tags: `v1.2.3`, `1.2`, `latest`, and the git SHA. Images are public once the package visibility is set to public in GHCR (org → Packages → each package → Change visibility).

Gateway is still `kong:3.9` from Docker Hub; Postgres/Redis are official images.

---

## A. Run the platform without cloning the repo (pre-built)

You still need a **workspace** (keys + `gateway/kong.yml`). Minimal path:

```bash
mkdir platfarm-run && cd platfarm-run
curl -fsSL https://raw.githubusercontent.com/platfarmai/platfarm/main/deploy/compose.release.yml -o compose.yml
curl -fsSL https://raw.githubusercontent.com/platfarmai/platfarm/main/deploy/env.release.example -o .env
# kong.yml: copy from a platfarm checkout after `pctl sync`, or clone once:
git clone --depth 1 https://github.com/platfarmai/platfarm.git _src
cp _src/gateway/kong.yml ./gateway/kong.yml
# keys: generate with pctl from a checkout, then:
#   mkdir -p .keys && cp _src/.keys/pf-auth.pem* .keys/
docker compose --profile bundled-db up -d
```

Set `PF_IMAGE_TAG=v0.1.0` in `.env` to pin a release. Default `latest` tracks the last tagged (or manually dispatched) build.

**Required local files**

| Path | Purpose |
|---|---|
| `.keys/pf-auth.pem` | RS256 **private** key (auth only, never commit) |
| `.keys/pf-auth.pem.pub` | Public key (gateway JWT plugin + services) |
| `gateway/kong.yml` | Declarative Kong config from `pctl sync` |

Without those, auth cannot sign tokens and Kong cannot verify them.

**First-time key generation** (one machine with Go):

```bash
git clone https://github.com/platfarmai/platfarm.git && cd platfarm
cp .env.example .env
go run ./tools/pctl sync   # writes .keys/ if missing
```

Copy `.keys/` into the run directory. Do not publish the private key.

---

## B. Build a business service on a runtime image

Go (after `CGO_ENABLED=0 go build -o server .`):

```dockerfile
FROM ghcr.io/platfarmai/runtime-go:latest
COPY --chown=platfarm:platfarm server /server
COPY --chown=platfarm:platfarm contract-test /contract-test
ENTRYPOINT ["/server"]
```

Python:

```dockerfile
FROM ghcr.io/platfarmai/runtime-python:latest
COPY --chown=platfarm:platfarm . /app
CMD ["uvicorn", "main:app", "--host", "0.0.0.0", "--port", "8080", "--timeout-graceful-shutdown", "25"]
```

Mount the platform public key in compose (same as first-party services):

```yaml
volumes:
  - ./.keys/pf-auth.pem.pub:/pf/jwt.pub:ro
```

Implement `GET /healthz` and `GET /readyz` (see specs/005). Verify JWTs with RS256 and `iss=pf-auth`.

PHP / Rust: `FROM ghcr.io/platfarmai/runtime-php` or `runtime-rust`, then COPY the app or binary.

---

## C. Pull auth only (embed in another compose)

```yaml
services:
  auth:
    image: ghcr.io/platfarmai/auth:latest
    environment:
      DATABASE_URL: postgres://...
      JWT_PRIVATE_KEY_FILE: /keys/pf-auth.pem
      PF_REDIS_URL: redis://redis:6379/0
    volumes:
      - ./keys:/keys:ro
```

JWKS: `GET http://auth:8080/auth/.well-known/jwks.json`.

---

## Packages visibility

GHCR packages default to private for new orgs. After the first release workflow:

GitHub → **platfarmai** → **Packages** → each image → **Package settings** → **Change visibility** → Public.

Until then, consumers need `docker login ghcr.io` with a PAT (`read:packages`).
