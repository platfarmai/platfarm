# 019: Platform-wide log pipeline + Grafana

## Motivation

specs/013 wired Loki for gateway metering only. Appendix B's design — all services' JSON logs searchable by `X-Request-Id` — plus dashboards/alerting never landed: a dead service was only discoverable by curl.

## Design

All inside the existing `observability` profile (zero cost when off):

- **promtail**: second scrape job `platfarm-services` — every `platfarm-*` container's stdout (gateway keeps its dedicated metering job), labels `service`/`container`, best-effort JSON parse of `level` + `requestId` (plain-text lines kept verbatim).
- **Grafana** container (`127.0.0.1:18002`, admin password via `GF_ADMIN_PASSWORD`), fully provisioned from files (no click-ops, consistent with "files are the single source of truth"):
  - Loki datasource (`uid: loki`)
  - Dashboard "Platfarm · 日志总览": volume by service, gateway 5xx rate, error stream, X-Request-Id cross-service correlation panel
  - Alert rule: gateway 5xx > 20 in 5 min (contact point configurable in UI; webhook/mail via svc-notify later)

## Out of scope

OTel/tracing (upgrade path per ADR #10), Prometheus metrics (logs-first per Appendix B).

## Blast radius

`deploy/promtail.yml`, new `deploy/grafana/`, base compose grafana service. No service code changes.

## Acceptance

```bash
docker compose --profile observability up -d
# 18002 → dashboard shows per-service volume; kill a backend → 5xx panel + alert fires
# paste an X-Request-Id → correlated logs across gateway + services
```
