# 023: Open-platform daily quota enforcement

## Motivation

specs/013 made per-app usage *visible*; nothing *enforced* it. Partners could exceed any agreed volume with only the per-minute rate limit in the way.

## Design

Enforcement point = **token issuance** (`POST /oauth/token`). App tokens live 1h, so an over-quota app is refused its next token — worst case overshoot is one TTL window. Zero cost on the request hot path (unchanged specs/013 principle).

- `oauth_apps.quota_per_day` (0 = unlimited) — settable via apps create/PATCH (svc-openapi console passes through).
- On token request with quota > 0: auth queries Loki (`LOKI_URL`) for `sum(count_over_time({service_name="platfarm-gateway", pf_app_key="<key>"}[24h]))` → `used >= quota` ⇒ `429 {quotaPerDay, usedToday}`.
- **Fail-open**: Loki unset/unreachable ⇒ issue the token and log (rate-limited) — metering is an optional component and must not take partners down.

## Blast radius

auth (apps.go + quota.go), compose injects `LOKI_URL` into auth. Gateway/services untouched.

## Acceptance

Verified: app with quota 2 → 3 calls to an open route → next `/oauth/token` returns `429 {"quotaPerDay":2,"usedToday":3}`.
