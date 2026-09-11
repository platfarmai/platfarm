# 002 Implementation Checklist

[English](tasks.md) | [简体中文](tasks.zh-CN.md)

Stages 1–5 completed 2026-09-04 / 2026-09-09 (verified).

## Stage 1 — RS256 + JWKS ✅
- [x] auth: RSA generate/load (`.keys/pf-auth.pem`, idempotent with pctl) + RS256 signing + JWKS endpoint
- [x] pctl sync: Kong consumer injects `rsa_public_key`
- [x] templates + svc-demo: verify via mounted public key (`/pf/jwt.pub`)
- [x] `pctl check --e2e` green; `JWT_SECRET` concept retired

## Stage 2 — Network & data isolation ✅
- [x] core-net explicit; gateway/plugin-pg moved into generated file (networks list grows with plugins)
- [x] One network per plugin (`net-plugin-*`, `internal: true`); plugin DNS sees only gateway + plugin-pg
- [x] plugin-pg dedicated instance + install-time provisioning (CREATE ROLE/DATABASE + REVOKE CONNECT FROM PUBLIC)
- [x] `.env.plugins/<id>.env` generation/injection; gitignored

## Stage 3 — pctl lifecycle ✅
- [x] plugin.yaml parse + validate (trust/source/permissions/resources; missing digest warns)
- [x] `install` (validate → env → sync → provision → credentials → start → contract tests; failure auto-disables)
- [x] `enable/disable` (`.disabled` marker + sync unhooks routes) / `uninstall [--purge]` / `upgrade`

## Stage 4 — Plugin identity ✅ (one degraded implementation — see deviations)
- [x] client_credentials: `/auth/service-token` + `-register-client` CLI, scopes = permissions.calls
- [x] Dual-token OBO in templates (service-token whitelist + X-PF-User-Token parsing)

## Stage 5 — console MVP ✅ (implemented as `pctl serve`, not a separate service)
- [x] `pctl serve`: same binary in web mode (base compose `console` service, 127.0.0.1:18001 only)
- [x] admin-only (RS256 + role=admin; non-admin 403 / no-token 401 verified)
- [x] Service list (manifest + Docker API live status) / toggle (edit marker + sync + gateway restart; files remain SoT) / log projection / audit (`.console-audit.jsonl`)
- [x] Verified: console disables svc-demo → gateway 404 → enable → 200; audit records actor
- Boundary: creation ops (install/uninstall) stay on the host CLI (in-container compose path-mapping limits; UI already says so)

## Acceptance ✅
- [x] Malicious-plugin drill trio: platform service DNS SERVFAIL / platform PG unreachable / own plugin-pg reachable
- [x] `--scale svc-demo=2` six rapid calls all 200; scale-back clean
- [x] Existing contract regression green after RS256 switch

**Deviations** (also noted in architecture-v2 Appendix H):
1. `permissions.calls` scope allow currently runs **on the callee** (template whitelist); gateway-level allow needs a Kong custom plugin — Phase 6 remainder
2. install gate is staged-lite: routes mount first, failed contract tests auto-disable (strict staging needs shadow routes)
3. `permissions.egress` is recorded but not enforced (compose `internal: true` cuts all egress; per-domain allow needs an egress proxy container)
