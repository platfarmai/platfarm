# 013: Open-platform metering via log pipeline (Loki)

[English](proposal.md) | [简体中文](proposal.zh-CN.md)

## Motivation

Open-platform apps (specs/009) are rate-limited per `X-PF-App-Key`, but there is no usage aggregation for quota/billing. Build the log pipeline the platform planned (architecture-v2 附录 B): gateway JSON access logs → Loki, keyed by `X-PF-App-Key` / `X-Request-Id`, and a usage view in the svc-openapi console via LogQL.

## Design

### Gateway: structured access log with app key

Kong emits JSON access logs (`file-log` or `http-log` plugin) including `X-PF-App-Key`, `X-Request-Id`, route, status, latency. A pre-function already sets `X-PF-App-Key` on open routes (specs/009); ensure it is also captured into the log serializer.

### Pipeline: Loki + shipper

`docker compose --profile observability`:
- `loki` (single-binary, filesystem storage; dev)
- `promtail` (or `vector`) tails the gateway container logs → Loki, parsing JSON fields into labels/structured metadata

Profile-gated so the base stack stays lean; enable when metering/observability is wanted.

### Console: usage view

svc-openapi adds a read-only usage panel: query Loki (`LOKI_URL`) with LogQL for per-app request counts over a window (last 24h / 7d), grouped by `appKey` and status. Backend proxies Loki queries (admin-only); no raw log exposure to partners in M2.

### Invariants

- Metering is derived from logs, not a second write path in the request hot path (no extra latency).
- Loki is optional (profile); without it the usage panel shows "pipeline not enabled".
- Billing/quota enforcement is **not** in M2 — this is visibility only. Rate limiting (specs/009) remains the live control.

## Blast radius

`pctl` compose gen (gateway JSON log serializer + observability profile), new compose services (loki, promtail), `svc-openapi` (usage view + Loki proxy), docs. Request path unchanged.

## Acceptance

```bash
docker compose --profile observability up -d
# make N app-token calls to an open route
# svc-openapi console usage view shows appKey with ~N requests in the window
# without the observability profile → usage view shows "not enabled", platform still works
```
