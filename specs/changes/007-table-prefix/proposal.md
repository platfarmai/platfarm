# 007: optional table_prefix

[English](proposal.md) | [简体中文](proposal.zh-CN.md)

## Motivation

Platform already isolates by database (`pf_*`, one DB per service), so table names never collide across services — table prefixes are usually unnecessary. But two cases still want them: integrating a **legacy/shared database**, and teams that **mandate a naming style**. Give an opt-in declaration; do not force it or touch business SQL.

## Design

Manifest gains an optional field:

```yaml
data:
  database: pf_svc_cms
  table_prefix: ""       # default empty; e.g. "cms_" for a shared/legacy DB
```

- Validation: empty **or** matches `^[a-z][a-z0-9_]*_$` (lowercase, ends with `_`).
- `pctl sync` injects `PF_TABLE_PREFIX=<value>` into the service container **only when non-empty**.
- The platform never rewrites business SQL — the service reads `PF_TABLE_PREFIX` and prefixes its own table names. Templates ship a tiny helper.

### Invariants

- Default empty → existing services unchanged (no env, no behavior change).
- One-service-one-DB stays the primary isolation; prefix is a compatibility knob, not a requirement.
- Platform does not create/alter business tables.

## Blast radius

`pctl` (manifest field + validate + compose env injection), templates (helper + doc), docs. No existing service changes; adopting is opt-in per manifest.

## Acceptance

```bash
pctl check                     # existing manifests pass (empty prefix)
# service.yaml with table_prefix: "cms_" → generated compose has PF_TABLE_PREFIX=cms_
# invalid prefix "Cms" or "cms" (no trailing _) → pctl check fails
# empty/unset → no PF_TABLE_PREFIX in compose
```
