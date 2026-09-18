# 009 Implementation Checklist

[English](tasks.md) | [简体中文](tasks.zh-CN.md)

- [x] auth: `oauth_apps` table + `-register-app` CLI (prints app_key/secret)
- [x] auth: `POST /oauth/token` (app_key/secret → app token with scopes, tokenType=app)
- [x] pctl manifest: `open_api[{route,scope}]` field + validate scope ∈ exposes.scopes
- [x] pctl sync: per open route, gateway pre-function (require app token + scope, set X-PF-App-Key) + per-app rate limit
- [x] templates: accept tokenType=app on open routes (appKey identity, no user)
- [x] svc-demo: declare open_api sample `GET /api/demo/data` + handler
- [x] docs: adding-a-service open_api; ADR #24
- [x] QA: app token 200 / missing scope 403 / app on protected 403 / over-rate 429 / user path intact; commit/push
