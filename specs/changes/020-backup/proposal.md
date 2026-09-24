# 020: pctl backup / restore

## Motivation

Keys (`.keys/`), plugin credentials (`.env.plugins/`) and both PG instances had no backup story; `uninstall --purge` dropped plugin data irrecoverably.

## Design

- `pctl backup [--out dir]` → `backups/<timestamp>/`:
  - bundled postgres: `pg_dumpall -U platfarm` → `platform-pg.sql` (external-DB mode: prints the owner-side instruction and skips)
  - plugin-pg (if present): `pg_dumpall -U postgres` → `plugin-pg.sql`
  - copies `.keys/` + `.env.plugins/` (unrecoverable secrets), `0600`/`0700` perms
- `pctl restore <dir> --force` → feeds dumps back through `psql`, restores key/credential dirs; refuses without `--force`.
- `pctl uninstall --purge` now snapshots the plugin DB to `backups/<id>-purge-<ts>.sql` **before** dropping.
- `backups/` is gitignored (contains secrets).

## Blast radius

`tools/pctl` only (backup.go + purge hook + usage). No runtime behavior change.

## Acceptance

```bash
pctl backup → dir contains platform-pg.sql + .keys/ + backup-info.txt
psql-drop something → pctl restore <dir> --force → data back
pctl uninstall svc-plugin-x --purge → snapshot exists before drop
```
