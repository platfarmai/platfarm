# 013 Implementation Checklist

[English](tasks.md) | [简体中文](tasks.zh-CN.md)

- [x] pctl compose: gateway JSON access log (http-log/file-log) incl X-PF-App-Key, X-Request-Id
- [x] base compose: observability profile — loki + promtail (tail gateway logs → Loki)
- [x] svc-openapi: GET /api/openapi/usage → proxy Loki LogQL (admin-only); "not enabled" fallback
- [x] svc-openapi SPA: usage panel (per-app counts, window选择)
- [x] docs ADR #28; .env.example LOKI_URL
- [x] QA: N calls → usage shows ~N; profile off → graceful "not enabled"
