# 017: svc-notify — notification capability service

## Motivation

Email / webhook / in-app messages are the second universal capability (and a hard dependency of 018 password reset). One outbox, one retry policy, one place to audit deliveries — instead of each service embedding SMTP.

## Design

Peer capability service at `/api/notify`, own DB `pf_svc_notify` (specs/015 migrations).

- **Senders**: service tokens (`auth.accept_service_tokens`, initially `svc-users`) or admin users. Regular users cannot send.
- **Channels** via `POST /send {channel,to,subject,body}`:
  - `email` → outbox row → worker delivers over SMTP (`SMTP_*` env). **SMTP unset = dev mode**: delivery logs to stdout and counts as sent (local dev never leaks mail).
  - `webhook` → worker POSTs `{subject,body}` JSON, 2xx = delivered.
  - `inbox` → row addressed to a userId, readable immediately.
- **Outbox worker**: `FOR UPDATE SKIP LOCKED` batches (multi-replica safe), exponential backoff (1→2→4→8 min), 5 attempts then `failed` with `last_error`.
- **Reads**: `GET /mine` + `POST /:id/read` (own inbox); `GET /outbox` (admin ops view of email/webhook deliveries).

## Blast radius

New `services/svc-notify/`; `.env.example` gains `NOTIFY_DATABASE_URL` + `SMTP_*`; `deploy/initdb.sql` creates the DB on fresh bundled volumes.

## Acceptance

```bash
pctl check --e2e
# admin send inbox → recipient sees it in /mine, /read flips status
# send email w/o SMTP → outbox shows sent (dev mode); bad webhook URL → attempts grow → failed
```
