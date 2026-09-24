# 018: Account self-service — register / password reset / TOTP

## Motivation

Users could only be created by an admin; no recovery, no MFA. A platform serving real end users needs self-registration (opt-in), forgot-password, and second-factor login — without fattening the auth base beyond identity.

## Design

Split per the Appendix G rule: *verification is the caller's job, auth only exchanges/updates*.

### auth (minimal additions, all identity-domain)

- `users` gains `email` (unique when non-empty), `totp_secret`, `totp_enabled` (idempotent ALTERs).
- `POST /auth/register {Username,Password,Email}` — enabled only when `PF_SELF_REGISTER=on`; creates `role=user` + issues a token pair.
- TOTP (RFC 6238, stdlib impl, no new deps): `POST /auth/totp/setup` → secret + otpauth URL (inactive), `/enable {code}` verifies once then enforces, `/disable {code}`. Login with TOTP enabled requires `Totp` in the body; missing → `401 {mfaRequired:true}`.
- Internal (service-token whitelist only): `GET /internal/auth/users/lookup?email=|username=`, `POST /internal/auth/users/{id}/set-password` (bcrypt + user-level revoke), `POST /internal/auth/users/{id}/reset-totp` (admin OBO, lost-device unlock).

### svc-users (owns the reset flow)

- New public routes `POST /forgot`, `POST /reset`; own DB `pf_svc_users` (`reset_codes`: sha256 hash, 15 min TTL, 5 attempts, single-use).
- `/forgot {email}`: constant response (no user enumeration), ≤3 codes/hour/email, code mailed via svc-notify (service token, direct core-net call).
- `/reset {email,code,newPassword}`: verify locally → auth `set-password` → mark used. All prior tokens die (user-level revoke).
- `USERS_DATABASE_URL` unset → both endpoints degrade to 503 (feature off, service still boots).

## Blast radius

`platform/auth` (login/TOTP/register/internal endpoints, user struct + loadUser), `services/svc-users` (reset.go + DB + manifest public routes), depends on 017. Existing login/refresh/console flows unchanged when flags are off.

## Acceptance

```bash
# PF_SELF_REGISTER=off → /auth/register 403; on → 200 + token pair, dup email → 409
# forgot → code row + dev-mode mail in svc-notify log → reset → old password 401, new 200
# totp setup+enable → login without code 401 mfaRequired, with code 200; admin reset-totp unlocks
```
