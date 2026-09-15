# 006: SSO cookie + unified admin shell

[English](proposal.md) | [简体中文](proposal.zh-CN.md)

## Motivation

Each app admin UI (console, CMS, future plugins) logs in separately on different origins. Operators want: log in once, open every app admin from one shell (iframe), no re-login when switching. Design: [docs/features/admin-shell.md](../../../docs/features/admin-shell.md).

## Design (this delivery)

1. **Session cookie** — `POST /auth/login` and `/auth/refresh` also set `pf_access` (HttpOnly, `Path=/`, `SameSite=Lax`, `Secure` when TLS). `/auth/logout` clears it and still revokes `tokenId`. Bearer stays valid.
2. **Kong cookie→Bearer** — `pctl sync` adds a Kong `pre-function` that, when `Authorization` is absent but the `pf_access` cookie exists, sets `Authorization: Bearer <cookie>`. Existing services unchanged (still read Bearer).
3. **Console on the gateway** — route `/platform/console` → console container so the shell shares origin/cookies with `/api/*`. `18001` stays as a localhost debug bind.
4. **Manifest `admin_ui`** — services declare an embeddable admin entry:
   ```yaml
   admin_ui: { path: /console, embed: true, title: CMS, icon: cms }
   ```
5. **Shell** — console lists apps with `admin_ui`, renders an app switcher + iframe. Login stores the cookie; session detected via `GET /auth/me`.

### Contract changes

- auth: login/refresh responses gain a `Set-Cookie`; new `GET /auth/me` already exists. No claims change.
- service.yaml/plugin.yaml: optional `admin_ui` block (backward compatible).
- Kong config gains a global `pre-function` + a `/platform/console` route.

### Invariants

- Platform JWT stays `admin|user`; app roles stay in-app.
- Access token never placed in an iframe URL.
- Cookie is additive; Bearer/API/curl untouched.

## Blast radius

`platform/auth` (cookie), `pctl` (manifest `admin_ui`, sync pre-function + console route, console shell HTML), docs. Existing services need no change; they opt into embedding by adding `admin_ui`.

## Acceptance

```bash
# login returns Set-Cookie: pf_access=...
# open http://host:18000/platform/console/ → shell loads without a second login
# with only the cookie (no Authorization header), GET /api/demo/me → 200 (Kong injects Bearer)
# logout clears cookie; cookie-only request afterwards → 401
# pctl check green; svc-demo/oauth contracts unaffected
```
