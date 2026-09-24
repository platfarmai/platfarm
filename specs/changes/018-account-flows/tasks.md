# 018 tasks

- [x] auth: users email/totp columns (idempotent), register (PF_SELF_REGISTER), TOTP stdlib (RFC 6238 vector-checked), internal lookup / set-password / reset-totp
- [x] login: TOTP branch (`401 {mfaRequired:true}` without code)
- [x] svc-users: reset.go (forgot/reset public routes, pf_svc_users reset_codes, rate limit, constant response), callService/callNotify clients
- [x] compose: PF_SELF_REGISTER injected into auth
- [x] manual E2E: register → login; forgot → dev-mail code → wrong code 401 → reset → old pw 401 / new pw 200; totp enable → no-code 401 mfaRequired → with-code 200 → disable
