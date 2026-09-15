# 008: Plugin marketplace M1 (git index + digest install)

[English](proposal.md) | [简体中文](proposal.zh-CN.md)

## Motivation

Move third-party plugins from "clone a repo, pctl install a dir" to "discover and install from a shared index". M1 delivers the smallest useful market: a git index repo + digest-locked install via `pctl market`. Design: [docs/features/plugin-marketplace.md](../../../docs/features/plugin-marketplace.md). cosign signature verification is deferred to M1.5.

## Design (this delivery)

**Index repo** `platfarmai/market-index`:

```
plugins/<id>/index.yaml     # all published versions of a plugin
```

```yaml
# plugins/svc-translate/index.yaml
id: svc-translate
versions:
  - version: 1.2.0
    image: ghcr.io/vendor/translate
    digest: sha256:ab12...      # required — install pins by digest
    manifest: { ... }           # embedded plugin.yaml (permissions shown before install)
    published: 2026-09-15
```

**pctl market** commands:

| Command | Behaviour |
|---|---|
| `pctl market search <q>` | grep index (id/title/desc) |
| `pctl market info <id>[@ver]` | show versions, permissions, digest |
| `pctl market install <id>[@ver]` | resolve version → write `plugin.yaml` (image+digest) into `services/<id>` → run existing install gate |

**Index source**: `PF_MARKET_INDEX` env or `--index`. Accepts a `file://` path (local/dev) or a git URL / raw base URL. Default `https://raw.githubusercontent.com/platfarmai/market-index/main`.

**Install path reuse**: `market install` produces a normal `plugin.yaml` (trust: third-party, source.image + digest) then calls the existing `runInstall` gate (validate → provision DB → credentials → start → contract tests). No new sandbox code.

### Deferred (M1.5+)

- cosign signature verification (double-lock)
- `pctl publish` (build → push → open index PR)
- CI review pipeline (trivy + sandbox smoke)
- market Web UI

### Invariants

- digest is **required** in the index — install always pins by digest, never a floating tag.
- Permissions from the embedded manifest are printed before install (App-Store-style).
- No change to the sandbox/install gate; market is only a discovery + fetch front.

## Blast radius

`pctl` (new `market` subcommand + index client). New repo `platfarmai/market-index`. Docs. No platform runtime change.

## Acceptance

```bash
pctl market search translate                 # lists matching plugin(s) from index
pctl market info svc-translate@1.2.0          # shows digest + permissions
PF_MARKET_INDEX=file://./_market pctl market install svc-translate@1.2.0
# → services/svc-translate/plugin.yaml has image@sha256; install gate runs
pctl market install svc-x                     # unknown id → clear error
# index entry missing digest → install refused
```
