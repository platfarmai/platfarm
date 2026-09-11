# 001 Implementation Checklist

[English](tasks.md) | [简体中文](tasks.zh-CN.md)

Completed 2026-09-04 (verified end-to-end).

- [x] auth: migrate adds `external_identities` (provider + external_id composite PK)
- [x] auth: `POST /internal/auth/external-login` — service-token whitelist → lookup binding → issue token pair / auto-provision
- [x] auth: `POST /internal/auth/bind-external` (OBO: service token + X-PF-User-Token)
- [x] auth: service-token issuance (`POST /auth/service-token`, client_credentials; credentials via `-register-client` CLI)
- [x] svc-oauth: GitHub provider + **mock provider** (dev/E2E, no external account)
- [x] svc-oauth: state CSRF guard (in-memory TTL) + one-time exchange code `POST /api/oauth/exchange`
- [x] Contract tests: mock full flow / reused exchange code 401 / forged state 401 — `pctl check --e2e` green
- [x] Docs: README §2.4 try-it flow; adding-a-service.md FAQ update
- [x] Acceptance: mock login auto-provisions (`mock_mock-user-1`), second login same userId (idempotent), `/internal/*` 404 outside gateway

**Deviation**: PKCE not implemented (GitHub OAuth Apps don't support it; add inside the provider module when a PKCE-capable provider is wired).
