# 022: Multi-tenant activation

## Motivation

`tenantId` sat in the claims contract since v2.0 as a reserved 0. A comprehensive base hosting multiple independent apps/customers needs the identity side to be real: tenant entities, user membership, claims that carry it.

## Design

Identity-domain only — **data isolation stays L3** (each service filters by `claims.tenantId`; the platform does not adjudicate):

- auth: `tenants` table (id/name/status), `users.tenant_id` (default 0 = unassigned/single-tenant), `signUser` stamps `TenantId` into claims.
- Internal admin API (service-token whitelist + admin OBO): `GET/POST /internal/auth/tenants`, `PATCH /internal/auth/tenants/{id}` (disable → user-level revoke of every member), `PATCH /internal/auth/users/{id}` accepts `tenantId` (reassign → revoke old tokens, their claim is stale).

## Blast radius

auth only (user struct/loadUser/migrate/tenants.go/users.go PATCH). Services opt in by reading `claims.tenantId`; tenant 0 behavior identical to before.

## Acceptance

Verified: create tenant `acme` (id 1) → PATCH user tenantId=1 → old token revoked → re-login carries `tenantId:1` in `/auth/me`.
