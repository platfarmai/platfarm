# 015: In-service SQL migration convention

## Motivation

Every service owns its database (`pf_` prefix, one DB per service), but nothing disciplines *schema evolution*: auth uses ad-hoc `CREATE TABLE IF NOT EXISTS`, and new services would each invent their own. As svc-file / svc-notify / svc-users gain real schemas (016–018), the platform needs one convention — enforced by `pctl check`, not by documentation goodwill.

## Design

### Convention (first-party services that declare `data.database`)

- Schema lives in `services/<id>/migrations/NNN_name.sql` (NNN = zero-padded ordinal, applied in lexical order).
- The service applies pending migrations **at boot, before listening**: each file runs in one transaction, then its version is recorded in `schema_migrations(version INT PRIMARY KEY, name TEXT, applied_at TIMESTAMPTZ)`.
- Migrations are append-only: editing an applied file is forbidden (fix forward with a new file). No down migrations — roll back via `pctl backup` restore (020).
- Reference runner: ~50 lines of Go (`migrate.go`, embedded FS) shipped in svc-file/svc-notify/svc-users; other languages copy the same contract.

### Enforcement

`pctl check` / `pctl sync` fail when a **first-party** manifest declares `data.database` but `migrations/*.sql` is missing. Third-party plugins are exempt (their schema ships inside their image; the platform only provisions the empty DB).

### Out of scope

- auth keeps its in-code migrate (platform base, not a `services/*` citizen).
- No external migration tool dependency (golang-migrate/atlas) — the runner is trivial and the platform stays single-binary friendly.

## Blast radius

`tools/pctl/manifest.go` (one validation), templates docs comment, new services adopt it from birth. No existing service declares `data.database`, so nothing breaks.

## Acceptance

```bash
# a first-party service.yaml with data.database but no migrations/ → pctl check fails
# svc-file boots twice → second boot applies nothing, schema_migrations rows unchanged
```
