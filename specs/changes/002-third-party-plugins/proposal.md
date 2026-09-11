# 002: Third-party Plugin System MVP (container-as-sandbox)

[English](proposal.md) | [简体中文](proposal.zh-CN.md)

## Motivation

Pluggability expands from "first-party AI services" to "plugins delivered by external developers". Third parties are trusted by contract, not by code. The existing design had three holes: HS256 shared secrets could forge tokens, a flat internal network could bypass the gateway, and pctl assumed source lived under `services/`.

## Design

Final design: [architecture-v2.md Appendix H](../../../docs/architecture-v2.md). Key points:

1. **RS256 + JWKS** (hard prerequisite): `JWT_SECRET` retires; everyone verifies with the public key
2. **plugin.yaml**: service.yaml superset (`trust` / `source.image+digest` / `permissions` / `resources`)
3. **One network per plugin**: plugin sees only gateway + its own plugin-pg; `internal: true` cuts egress
4. **Two-layer data isolation**: dedicated role + DB (`REVOKE CONNECT FROM PUBLIC`) + separate plugin-pg instance
5. **Dual-token context**: service token (who I am / scopes) + `X-PF-User-Token` (who I act for)
6. **svc-console**: pctl's web shell, sole holder of the docker socket, admin-only
7. **Stateless iron rule**: state exits to own DB / Redis leases

### Contract changes (platform-level — this proposal is mandatory)

- Claims shape unchanged, but signing algorithm HS256 → RS256 (**every service template and existing service must switch to JWKS verification** — the biggest migration surface)
- service.yaml gains `trust` / `source` / `permissions` / `resources` (first-party defaults stay backward compatible)
- pctl gains `install/enable/disable/uninstall/upgrade`

## Blast radius

- auth (signing/JWKS), gateway config generation, pctl (major), templates (verification), existing svc-demo
- New: plugin-pg service, console service, `.env.plugins/` directory

## Acceptance

```bash
# Malicious-plugin drill (a deliberately overreaching test plugin validates the sandbox):
docker compose exec svc-plugin-evil ping svc-demo        # unreachable (no DNS name)
docker compose exec svc-plugin-evil psql <platform PG>   # connection timeout (no route)
curl gateway /api/file/... -H "Authorization: <plugin token>"  # undeclared calls → 403
# Happy path:
pctl install ./test-plugin && pctl check --e2e           # install gate green
docker compose up -d --scale svc-plugin-test=3           # multi-replica round-robin works
# Migration regression:
pctl check --e2e                                         # existing contracts stay green after RS256 switch
```

## Phasing (each step independently mergeable)

RS256+JWKS → network segmentation → plugin-pg + provisioning → pctl lifecycle → client_credentials + scope allow → svc-console
