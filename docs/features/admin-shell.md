# Feature: unified admin shell (SSO + embed)

[English](admin-shell.md) | [简体中文](admin-shell.zh-CN.md)

Status: design. Implementation later as `specs/006` in platfarm + pctl.

## Problem

Each app (platform console, CMS, future plugins) currently logs in separately. Tokens live in `localStorage` on different origins (`:18001` vs `/api/cms/console`). Operators want:

1. Log in once
2. Open every app admin UI from **one shell**, typically an iframe / tab inside the platform console
3. No second password prompt when switching apps

## Non-goals

- Merging app UIs into one SPA (each app still owns its pages)
- Putting app roles into platform JWT (`admin|user` stays coarse; CMS `editor` stays in the app)
- Third-party plugins receiving the platform cookie on their own domain (they stay behind the gateway)

## Design: same-site cookie + gateway + declared admin entry

```
Browser  →  https://admin.example.com/          (console shell)
         →  https://admin.example.com/auth/*    (login, refresh)
         →  https://admin.example.com/apps/cms  (iframe → /api/cms/console/)
         →  https://admin.example.com/api/cms/* (CMS APIs, Cookie sent automatically)
```

All of these are **one site** (same registrable domain + path on Kong). Then a first-party cookie works in the iframe without third-party-cookie hacks.

### 1. Session cookie (auth)

After `POST /auth/login`, auth **also** sets:

```
Set-Cookie: pf_access=<jwt>; Path=/; HttpOnly; Secure; SameSite=Lax; Max-Age=7200
```

Optional: `pf_refresh` with stricter path `/auth/refresh`.

Logout clears both cookies and still revokes `tokenId` in Redis.

**Bearer remains valid** for APIs, curl, and mobile. Cookie is for browsers.

Kong JWT plugin today reads `Authorization`. Compatibility options (pick one in implementation):

- **A.** Small Kong pre-function: if no `Authorization` but `pf_access` cookie exists, copy it to `Authorization: Bearer …`
- **B.** App middleware: accept cookie **or** Bearer (templates grow ~15 lines)

Recommend **A** so existing services do not change.

### 2. Shell = existing console, same origin as the gateway

Today console is `127.0.0.1:18001` (separate origin) → iframe to `:18000` will **not** share cookies.

Change: terminate console **behind Kong** on the same host, e.g. `/platform/console/` (reserved prefix `/platform` already exists). Operators open `https://host:18000/platform/console/`. Cookie `Path=/` is sent to `/api/cms/…` as well.

Keep `18001` as a localhost-only debug bind if needed.

### 3. Manifest: declare an embeddable admin UI

```yaml
# service.yaml / plugin.yaml
admin_ui:
  path: /console          # relative to mount.path → /api/cms/console
  embed: true             # allow iframe in the platform shell
  title: CMS
  icon: cms               # optional
```

Rules:

- Path must sit under `mount.path` (or be listed in `public_routes` for the HTML shell only — APIs stay JWT/cookie protected)
- `embed: true` implies the app sends `Content-Security-Policy: frame-ancestors 'self'` (same origin) — **not** `X-Frame-Options: DENY`
- Shell lists apps from manifests (`pctl` already loads them); clicking one sets `<iframe src="/api/cms/console/">`

Apps that are not iframe-safe set `embed: false` and open in a new tab; cookie still SSO if same site.

### 4. iframe handshake (optional, for SPA that stored token in localStorage)

Same-origin iframe can read nothing from HttpOnly cookie in JS. The child SPA should:

1. Call `GET /auth/me` (cookie sent automatically) → 200 means logged in
2. If it still needs a JWT in memory (e.g. for a WebSocket that cannot send cookies), call `POST /auth/token-from-cookie` (CSRF: SameSite + refresh) **or** keep using cookie-only XHR

Do **not** pass the access token in the iframe URL (leaks via Referer/history).

### 5. CSRF

Cookie + mutating API from a browser needs CSRF protection:

- `SameSite=Lax` stops most cross-site POST
- For APIs used from the iframe on the same site, Lax is enough for top-level GET; same-origin XHR POST is same-site
- Add `X-Requested-With` or double-submit later if you expose cookie auth to other subdomains

### 6. What each app must do (compatibility mode)

| App | Change |
|---|---|
| New templates | Document: admin HTML under `admin_ui.path`; no `X-Frame-Options: DENY`; session via `/auth/me` |
| CMS (platfarm-cms) | Serve `/api/cms/console/` SPA; use cookie or Bearer |
| Console | Become the shell: app switcher + iframe; login writes cookie via auth |
| Third-party plugins | Same as first-party if mounted on the gateway; no extra login |

Platform JWT claims **do not** grow. App-level roles stay in the app (CMS members table / future svc-grants).

## Phasing

1. Cookie on login/logout + Kong pre-function (Bearer from cookie)
2. Route console at `/platform/console` on Kong (same host as APIs)
3. `admin_ui` in manifest + shell iframe switcher
4. CMS console SPA as first embed

## Why not a separate SSO product

We already have one issuer (`pf-auth`) and one gateway. Extra IdP (Authelia, etc.) duplicates login. Cookie + same origin is the smallest SSO that matches “don’t log in again per app”.
