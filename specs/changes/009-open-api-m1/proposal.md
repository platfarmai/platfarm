# 009: Open API M1 (app token + scope gate)

[English](proposal.md) | [简体中文](proposal.zh-CN.md)

## Motivation

Expose selected platform data to external partners/developers via APIs, with per-app authorization, scope-gated routes, and per-app rate limiting. This is a third identity line — the **app** — alongside user access tokens and internal service tokens. It reuses the existing `exposes.scopes` declaration and the gateway; it does not invent a new permission engine.

## Design (this delivery = M1)

### Identity: `tokenType: app`

`POST /oauth/token` exchanges `app_key` + `app_secret` (client_credentials) for a short-lived JWT with:

```jsonc
{ "tokenType": "app", "appKey": "ak_...", "scopes": ["data.orders.read"], "iss": "pf-auth", "exp": ... }
```

auth gains `oauth_apps` (app_key, secret_hash, name, owner_id, scopes, rate_per_min, status) and a `-register-app` CLI to mint credentials.

### Declaration: manifest `open_api`

Services declare which routes are open and what scope each needs (scope names must exist in `exposes.scopes`):

```yaml
exposes:
  scopes:
    - { name: data.orders.read, desc: Read orders (open API) }
open_api:
  - { route: "GET /api/demo/data", scope: data.orders.read }
```

`pctl check` validates each `open_api.scope` is declared in this service's `exposes.scopes`.

### Gate: gateway enforces scope

`pctl sync` emits, per open route, a Kong `pre-function` that:
1. Requires `tokenType == "app"` (or `access` for logged-in users — configurable; M1: app only on open routes)
2. Requires the route's scope ∈ token `scopes`, else **403**
3. Sets `X-PF-App-Key` header (for per-app rate limit + metering)

Per-app rate limit: `rate-limiting` `limit_by: header` on `X-PF-App-Key`. Metering = gateway access log carries `X-PF-App-Key` (feeds the log pipeline; billing data source).

### Service side

Templates accept `tokenType: app` **only** on routes the manifest marks open; app tokens carry no user identity, so row-level filtering uses `appKey`. No user impersonation via app tokens in M1.

### Deferred (M2)

- `svc-openapi` self-service console (partner app CRUD, scope grants, quota) via SSO shell
- Quota/billing aggregation from metering logs
- Partner API doc portal (openapi aggregation)

### Invariants

- Platform JWT claims unchanged for users; `app` is additive.
- Scope vocabulary stays in `exposes.scopes` (specs/003); open_api only maps route→scope.
- Data row filtering stays in the service (L3).

## Blast radius

`platform/auth` (oauth_apps + /oauth/token + register-app), `pctl` (open_api parse/validate + sync gate + per-app limit), templates (accept app token on open routes), svc-demo (sample open route), docs. Users/service tokens/existing routes unaffected.

## Acceptance

```bash
# register an app with scope data.orders.read
docker compose exec auth /auth -register-app demo-partner -scopes data.orders.read
# exchange for an app token
curl -X POST .../oauth/token -d '{"appKey":"ak_...","appSecret":"..."}'   # → {tokenType: app, scopes:[...]}
curl .../api/demo/data -H "Authorization: Bearer <app_token>"             # → 200
# app without the scope → 403 at gateway
# app token on a non-open protected route (/api/demo/me) → 403
# over per-app rate → 429
# normal user access token on /api/demo/me still → 200 (unaffected)
```
