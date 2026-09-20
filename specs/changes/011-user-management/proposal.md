# 011: Platform user management (auth API + svc-users console)

[English](proposal.md) | [简体中文](proposal.zh-CN.md)

## Motivation

auth has the `users` table but only `/auth/me` — no list, create, role change, disable, or password change. Give admins user management and let users change their own password. Platform users (login + coarse role admin|user) are distinct from app members (CMS editor etc., L3).

## Design (this delivery = M1)

### auth: user management API

| Endpoint | Who | Purpose |
|---|---|---|
| `GET /internal/auth/users` | service token + admin OBO | list (no password_hash) |
| `POST /internal/auth/users` | admin | create user → initial password shown once |
| `PATCH /internal/auth/users/{id}` | admin | change role / status |
| `POST /internal/auth/users/{id}/reset-password` | admin | reset → new password shown once |
| `POST /auth/change-password` | the user (access token) | change own password (verifies old) |

Rules:
- Passwords are bcrypt; never returned except the one-time value on create/reset.
- **Anti-lockout**: cannot demote/disable the last active admin; admin cannot disable/demote self.
- **Disable revokes**: setting `status=0` revokes the user's active tokens (via the existing revoke store) and login/refresh already reject `status != 1`.
- `POST /auth/change-password {oldPassword, newPassword}` verifies old, then updates hash and revokes the current token (forces re-login).

### svc-users: console

First-party service `svc-users` (mirrors svc-openapi): Go backend proxies the auth internal users API with a service token + admin OBO; embedded Vue SPA. Admin view: list / create (password once) / role / enable-disable / reset. Any logged-in user: self change-password page. `admin_ui` embeds it in the SSO shell.

### Boundary

- Platform `users.role` stays `admin|user`; app business roles stay in the app (L3).
- svc-users manages platform accounts + platform role + passwords, not app membership.

### Deferred (M2)

- Pagination/search for large user bases
- External-identity (OAuth) user linkage view
- Self-service registration flows

## Blast radius

`platform/auth` (user API + anti-lockout + revoke-on-disable + whitelist svc-users), new `svc-users`, docs. Login/`/auth/me`/existing tokens unchanged.

## Acceptance

```bash
# admin via SSO shell:
# list users; create "bob" (password shown once) → bob can login
# change bob role user→admin; disable bob → bob login 401; enable → login ok
# reset bob password → old fails, new works
# self change-password (verifies old); wrong old → 401
# anti-lockout: demote/disable the only admin → refused
# non-admin → user API 403, but self change-password allowed
```
