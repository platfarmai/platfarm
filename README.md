# PlatFarm

English | [简体中文](README.zh-CN.md)

**A new backend development paradigm for the LLM era** — AI writes the code, the platform enforces the engineering discipline.

PlatFarm is a **fusion development model built on containerized services**: one API gateway facing the world, one container per service. Heterogeneous services in Go / Rust / Python / PHP, sandboxed third-party plugins, and AI-generated code are all delivered under a single contract (JWT identity × `service.yaml` manifest × contract tests). A single `pctl` command takes a service from "AI output" to "safely live" — without touching a single platform file.

## 1. Why PlatFarm

LLMs can write a service in minutes, but putting it into a system **safely** still takes days: wiring auth, configuring routes, isolating databases, deciding whether third-party code can be trusted. PlatFarm turns all of that into platform discipline, so every "AI-grown crop" on the service farm is born compliant:

- **AI-speed production** — `pctl new` scaffolds services in four languages with JWT verification, On-Behalf-Of calls, and contract tests built in; AI only fills in business logic
- **Discipline enforced by tooling** — route conflicts, database isolation, and permission declarations are validated by `pctl`; the contract-test trio is the go-live gate
- **Heterogeneous fusion** — Go/Rust for performance-critical paths, Python for the AI ecosystem, PHP for legacy teams; all interoperate under the same contract
- **Tiered trust** — first-party services are trusted by source; third-party plugins are trusted only by contract (digest pinning + network sandbox + least-privilege grants)

**Three design tenets**:

1. **A razor-thin base** — the only "base" is `auth` (users + RS256 JWT issuing/revocation + coarse roles, ≤1000 lines of Go). No business concept may enter it; file storage, login providers, etc. are peer-level capability services
2. **The contract is the interface** — a service's entire integration burden is verifying one JWT; the platform's entire knowledge of a service is one `service.yaml` manifest. Nothing exists outside the manifest
3. **Files are the single source of truth** — gateway config, orchestration, and permission declarations are all generated from manifests by `pctl` and committed to git. There is no "click a button and runtime state changes" backdoor

Full design & 17 ADRs: [docs/architecture-v2.md](docs/architecture-v2.md)

## 2. Getting Started

### 2.1 One-command bootstrap (a clean machine with only Docker)

```bash
git clone git@github.com:platfarmai/platfarm.git platfarm && cd platfarm
cp .env.example .env                          # defaults to the bundled database
docker compose --profile bundled-db up -d --build
```

Verify (dev seed accounts `admin/admin123`, `alice/user123`):

```bash
# Log in and get a token pair
curl -s -X POST http://localhost:18000/auth/login \
  -H "Content-Type: application/json" -d '{"Username":"admin","Password":"admin123"}'

# Call a business service with the token; without it you get 401 at the gateway
curl -s http://localhost:18000/api/demo/me -H "Authorization: Bearer <accessToken>"

# Public routes need no token
curl -s http://localhost:18000/api/demo/public/ping
```

Already have PostgreSQL (host instance / AWS RDS)? Set `DATABASE_URL` in `.env` (three modes documented in [.env.example](.env.example)) and drop `--profile bundled-db`.

### 2.2 Build the platform CLI (once)

```bash
cd tools/pctl && go build -o pctl . && cd ../..
```

### 2.3 Add a first-party service (30 seconds)

```bash
./tools/pctl/pctl new svc-hello              # Go(gin) by default; --lang rust|py|php (see ADR #17)
./tools/pctl/pctl sync                       # manifests → gateway config + orchestration
docker compose up -d --build && docker compose restart gateway
./tools/pctl/pctl check --e2e                # contract-test trio gate
```

Tutorial: [docs/adding-a-service.md](docs/adding-a-service.md)

### 2.4 Try third-party login (mock provider, no external account needed)

```bash
curl -sL http://localhost:18000/api/oauth/mock/authorize   # → {"exchangeCode": "..."}
curl -s -X POST http://localhost:18000/api/oauth/exchange \
  -H "Content-Type: application/json" -d '{"code":"<exchangeCode>"}'  # → token pair
```

For real GitHub login, set `GITHUB_CLIENT_ID/SECRET` in `.env` and use `/api/oauth/github/authorize`.

### 2.5 Install a third-party plugin (sandboxed)

```bash
./tools/pctl/pctl install ./path/to/plugin/   # directory with plugin.yaml (image delivery)
# Automatic: validate → dedicated network → plugin DB provisioning → credentials → start → contract-test gate
./tools/pctl/pctl disable|enable|uninstall svc-plugin-x [--purge]
```

Sandbox guarantee: a plugin's DNS world contains only the gateway and its own plugin-pg; platform services and the platform database are unreachable at the TCP level (verified in specs/changes/002).

### 2.6 Admin console

Starts with the base stack: **http://localhost:18001** (localhost only; log in with an admin account). Features: live service list, enable/disable (regenerates gateway config automatically), container logs, and an audit trail. Creation-type operations (new services / plugin installs) stay on the host CLI — the console is a read-only projection plus start/stop shell over `pctl`; files remain the single source of truth.

`GET http://localhost:18000/` returns a platform info index; unmatched paths return a platform-branded JSON 404.

## 3. Interface Goals (the platform contract)

The stable interface surface the platform promises. **Goal: services, plugins, and frontends that depend only on this section work with any platform version.**

### 3.1 Identity API (`/auth`)

| Endpoint | Purpose |
|---|---|
| `POST /auth/login` | username/password → access (2h) + refresh (30d) pair |
| `POST /auth/refresh` | refresh rotation (old refresh token is revoked immediately) |
| `POST /auth/logout` | revoke current token (optionally the refresh token too) |
| `GET /auth/me` | current identity |
| `GET /auth/.well-known/jwks.json` | RS256 public key (external verification / rotation) |
| `POST /auth/service-token` | client_credentials → 5-minute service token (for services/plugins) |
| `POST /internal/auth/*` | internal network only: external-login, bind-external, introspect |

### 3.2 Token contract (a service's only integration burden)

Claims: `tokenId / tokenType(access|refresh|service) / userId / username / role(admin|user) / tenantId / svc / scopes / iss="pf-auth"`.

Six-step service-side convention: read Bearer → **verify RS256 with the platform-mounted public key** (`/pf/jwt.pub`, not secret) → check `tokenType` → extract identity → any failure is 401 → propagate `X-Request-Id`. On-Behalf-Of calls between services: `Authorization: Bearer <service token>` + `X-PF-User-Token: <user JWT>`. All templates ship this logic built in.

### 3.3 Service manifest (`service.yaml` / `plugin.yaml`)

The single contract a service declares and the platform consumes: `mount.path` (reserved prefixes `/auth /platform /internal /docs` are forbidden), `public_routes`, `limits.rate_per_minute`, `auth.accept_service_tokens`, `data.database` (own database, `pf_` prefix enforced). Third-party plugins add `trust / source.image+digest / permissions / resources`. Full schema: architecture-v2.md §3 + Appendix H.1.

### 3.4 pctl command surface

| Command | Purpose |
|---|---|
| `new <id> --lang go\|rust\|py\|php` | scaffold a first-party service (Go by default) |
| `sync` | manifests → kong.yml + compose fragment (with RS256 key bootstrap) |
| `check [--e2e]` | manifest validation + in-container contract tests |
| `list` | service overview (trust/state/rate limits) |
| `install / enable / disable / uninstall [--purge] / upgrade` | third-party plugin lifecycle |
| `serve` | web console (runs in the console container) |

### 3.5 Gateway behavior promises

Single external port `18000`; invalid/missing tokens are rejected with 401 at L1; rate overruns get 429; public routes pass per manifest; `/internal/*` is never routed. Generated `gateway/kong.yml` and `docker-compose.services.yml` **must never be hand-edited** (the next sync overwrites them).

## 4. Repository Layout

```
docker-compose.yml            base stack (auth / console / optional postgres, redis)
docker-compose.services.yml   generated: gateway + plugin-pg + all services & plugin networks
gateway/kong.yml              generated: gateway config
platform/auth/                the auth base (Go)
services/<id>/                services: service.yaml + code + tests/contract/
templates/<lang>-service/     pctl new scaffolds (go / rust / py / php)
tools/pctl/                   platform CLI (+ `serve` web console)
specs/                        change-proposal workflow (platform contract changes require one)
docs/                         architecture & tutorials
.keys/ .env.plugins/          keys & plugin credentials (gitignored)
```

## 5. Platform Discipline (four hard rules)

1. Generated files are never hand-edited; config changes = edit a manifest + `pctl sync`
2. Every service connects only to its own database (`pf_` prefix); cross-service data goes through APIs
3. A service that fails the contract-test trio (no token 401 / valid 200 / tampered 401) is not integrated
4. Services must be stateless; state lives in their own DB or Redis leases (the precondition for `--scale`)

## 6. Status & Roadmap

Shipped: auth base (RS256+JWKS), gateway verification/rate limiting, the full pctl command set, third-party login (mock/GitHub), the third-party plugin sandbox (dedicated networks + isolated plugin-pg + credential system), admin console, multi-replica compatibility, four verified language templates.

Roadmap (Phase 4+ remainder: response encryption, log pipeline, SDK consolidation): architecture-v2.md §9. Plugin marketplace design: [docs/features/plugin-marketplace.md](docs/features/plugin-marketplace.md).
