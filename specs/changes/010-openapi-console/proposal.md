# 010: Open-platform admin console (svc-openapi)

[English](proposal.md) | [简体中文](proposal.zh-CN.md)

## Motivation

specs/009 delivered app tokens + scope gate, but managing apps is CLI-only (`auth -register-app` + editing `oauth_apps`). Give admins a backend to **issue credentials and manage rules** (scopes, rate, enable/disable), embedded in the SSO shell — no new mechanism.

## Design (this delivery = M2)

### auth: internal apps API

Refactor the `register-app` logic into internal endpoints (internal network only, service-token whitelist + acting user must be platform admin via `X-PF-User-Token`):

| Endpoint | Purpose |
|---|---|
| `GET  /internal/auth/apps` | list apps (no secret) |
| `POST /internal/auth/apps` | create → returns secret **once** |
| `PATCH /internal/auth/apps/{key}` | update scopes / rate_per_min / status |
| `POST /internal/auth/apps/{key}/rotate` | new secret (shown once) |

The CLI `-register-app` stays (bootstrap/scripts). Secrets are bcrypt-hashed; never returned after creation.

### svc-openapi: capability service + Vue console

First-party service `svc-openapi`:

- Backend (Go): admin-only routes under `/api/openapi/*`; calls auth internal apps API with a service token + forwards the admin's `X-PF-User-Token` (OBO). Exposes a scope catalog (from platform manifests / a static list in M2) for the UI dropdown.
- SPA (Vue, embedded): app list, create (secret shown once + copy), edit scopes/rate, enable/disable, rotate secret. Same cookie-SSO pattern as CMS.
- `admin_ui: { path: /console, embed: true, title: OpenAPI }` → appears in the platform shell.

### Boundary (important)

- The console manages **long-lived credentials (app_key/secret) + rules**, not short-lived access tokens. Partners still call `POST /oauth/token` themselves.
- Secret is shown **once** at create/rotate; storage is bcrypt-only. Lost secret → rotate.
- All console routes are platform-admin only.

### Deferred (M3)

- Usage/quota aggregation from `X-PF-App-Key` metering logs
- Partner self-service portal (partners manage their own apps)
- Live scope catalog auto-derived from `pctl` manifest sync

## Blast radius

`platform/auth` (internal apps API + refactor), new `svc-openapi` (repo or platfarm service), docs. Existing app-token runtime (specs/009) unchanged.

## Acceptance

```bash
# via SSO shell as admin:
# create app "acme" with scope data.orders.read → secret shown once
# acme exchanges POST /oauth/token → app token with that scope → open route 200
# edit acme: remove scope → open route 403
# disable acme (status=0) → POST /oauth/token 401
# rotate secret → old secret 401, new secret works
# non-admin user → console 403
```
