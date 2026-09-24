# 021: Account center UI + email verification + svc-file orphan sweep

## Motivation

specs/018 shipped API-only account flows — real users cannot curl. Registration collected an email but never proved ownership. And svc-file accumulated metadata rows for presigned uploads that never happened.

## Design

### Account center (svc-users, public static page)

`/api/users/account/` — self-contained static SPA (go:embed `account/`, SPA fallback, no build step, no CDN): 登录 (with TOTP step-up on `mfaRequired`), 注册, 忘记密码 (code → reset), 账号安全 (identity card via extended `/auth/me`, email verify, change password, TOTP setup/enable/disable with copyable secret/otpauth URL). Session = Bearer in sessionStorage + same-origin `pf_access` cookie.

### Email verification

- auth: `users.email_verified` flag; `/auth/me` now also returns `email/emailVerified/totpEnabled`; internal `POST /internal/auth/users/{id}/set-email-verified` (whitelist); lookup returns `emailVerified`.
- svc-users: `reset_codes.purpose` column (migration 002, reset|verify); `POST /api/users/verify-email/send` (logged-in; fetches own email via internal lookup; 3 codes/hour) + `/confirm {code}` → auth set-email-verified. Verification stays the caller's job (Appendix G rule); auth records only the conclusion.

### svc-file orphan sweep

Hourly sweeper: rows older than 1h whose object is absent in MinIO (`StatObject` NoSuchKey) are deleted, 200/batch. Reverse direction (objects without rows) intentionally skipped — keys only ever come from this service.

## Blast radius

auth (me/lookup/flag/internal endpoint), svc-users (verify.go, migration 002, account/ page, reset queries now filter purpose), svc-file (sweeper.go). No gateway changes beyond existing public route `GET /account/*`.

## Acceptance

Browser (verified with Playwright): register → auto `#security` → 发送验证码 → dev-mail code → 确认验证 → identity card flips to 已验证. Reset/TOTP flows unchanged from 018 API behavior.
