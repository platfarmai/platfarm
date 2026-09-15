# 006 Implementation Checklist

[English](tasks.md) | [简体中文](tasks.zh-CN.md)

- [x] auth: set `pf_access` cookie on login/refresh; clear on logout (keep Bearer)
- [x] pctl manifest: parse+validate `admin_ui { path, embed, title, icon }`
- [x] pctl sync: Kong global `pre-function` (cookie→Bearer) + `/platform/console` route to console
- [x] pctl console: shell with app switcher (from `admin_ui`) + iframe; login writes cookie; `/auth/me` session
- [x] svc-demo (or CMS) declares `admin_ui` as first embed sample
- [x] QA: cookie login, gateway console, iframe no-relogin, cookie→Bearer 200, logout 401
- [x] docs: adding-a-service `admin_ui`; ADR #21
