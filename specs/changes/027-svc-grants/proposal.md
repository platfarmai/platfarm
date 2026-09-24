# 027: svc-grants — centralized business-role assignment (ADR #18 delivered)

## Motivation

ADR #18 deferred svc-grants until membership concepts multiplied. With the platform now hosting file/notify/jobs plus business services, role *assignment* (who is editor of what) must stop being copy-pasted per service.

## Design

Peer capability service `/api/grants`, own DB `pf_svc_grants`, single table `grants(service_id, role, user_id, granted_by)` UNIQUE-triple.

- **Assignment centralized, semantics stay L3**: this service stores *who has which named role where*; what "editor" allows remains the owning service's code.
- API: `GET /mine` (any user → own roles), `GET /users?service=&role=` (admin, or a service token whose `svc` equals the queried service — a service reads only its own membership), `PUT/DELETE /:service/:role/:userId` (platform admin, idempotent upsert / 404 on absent).
- No manifest vocabulary cross-validation in M1 (roles are free-form validated strings); revisit when a grants console lands.

## Blast radius

New `services/svc-grants/` (delegated build, deep agent); env/initdb wiring. No auth changes.

## Acceptance

Verified: admin PUT grant → user's `/mine` shows it; non-admin PUT ⇒ 403; service token reading its own membership via core-net ⇒ 200 (and via gateway without a `svc-grants:*` scope ⇒ 403 by specs/025 — layered as intended); contract trio green.
