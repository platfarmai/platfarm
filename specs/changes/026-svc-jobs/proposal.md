# 026: svc-jobs — async task queue + cron scheduler

## Motivation

The platform had only synchronous calls plus svc-notify's private outbox. Every future background need (scheduled notifications, cleanup, report generation) would re-invent a worker. One capability service owns "run this later / on schedule".

## Design

Peer capability service `/api/jobs`, own DB `pf_svc_jobs` (015 migrations). Delivery model = **webhook callback**: a job is `{type, payload, webhook, runAt?, maxAttempts?}`; the worker POSTs `{jobId,type,payload}` to the webhook with svc-jobs' own service token (client_credentials, cached; unset creds = dev degradation without Authorization). Consumers whitelist `svc-jobs` in `accept_service_tokens` (svc-notify already does — scheduled notifications work day one).

- Queue worker: 5s poll, `FOR UPDATE SKIP LOCKED` batches (multi-replica safe), 2xx ⇒ done; else exponential backoff, `maxAttempts` (default 5) ⇒ `dead` with `last_error`.
- Cron: `schedules` (unique name, standard 5-field cron via robfig/cron/v3, enable/disable), 30s scheduler loop enqueues due jobs (`created_by=schedule:<name>`).
- API: enqueue (service tokens/admin), `GET /:id` (creator/admin), list + schedule CRUD (admin).

## Blast radius

New `services/svc-jobs/`; svc-notify accept list + env/initdb wiring. Delegated build (deep agent), integrated & QA'd by orchestrator.

## Acceptance

Verified: enqueue → webhook receiver hit, status done; unreachable webhook → attempts+backoff+last_error (→ dead at cap); `* * * * *` schedule fired 3 jobs in 3 minutes, all delivered; non-admin user enqueue ⇒ 403; contract trio green.
