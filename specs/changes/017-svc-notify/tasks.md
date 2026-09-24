# 017 tasks

- [x] `services/svc-notify`: manifest (accept_service_tokens: svc-users) + migrations/001_notifications
- [x] `POST /send` (service token or admin) email/webhook/inbox; `GET /mine` + `/:id/read`; admin `GET /outbox`
- [x] outbox worker: SKIP LOCKED batch, exponential backoff, 5 attempts → failed; SMTP unset = dev mode
- [x] contract trio + manual E2E: inbox send/read, user send 403, dev-mode email → sent
