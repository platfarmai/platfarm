# 003: Permission Manifest Spec v1 (roles / exposes / cross-manifest checks)

[English](proposal.md) | [简体中文](proposal.zh-CN.md)

## Motivation

Manifests currently declare only "what I need" (`permissions.calls/egress`). Two other faces are missing:

- What **roles a service/plugin defines for users** (e.g. CMS `editor`/`author` — the data source for svc-grants and a shared members UI)
- What **scopes a service/plugin exposes to others** (today `calls: ["svc-file:read"]` is a bare string convention with no validation)

One declaration, five consumers: pctl cross-manifest checks, svc-grants vocabulary constraints, a shared members UI, marketplace permission dialogs / upgrade diffs, and the source of scopes when auth issues service tokens.

## Design

### Manifest extensions (all optional, backward-compatible)

```yaml
manifest: v1                        # spec version anchor (defaults to v1)

permissions:                        # ① what I need (existing)
  calls: ["svc-file:read"]          #    must hit the target's exposes.scopes

roles:                              # ② what roles I define for users (new)
  vocabulary:
    - { name: admin,  desc: Manage members and all content }
    - { name: editor, desc: Edit and publish all content }
  bootstrap: platform-admin=admin   # auto-grant on first visit (this form only)

exposes:                            # ③ what I expose to other services (new)
  scopes:
    - { name: read, desc: Read published content }
```

### Validation rules (enforced by `pctl check` / `sync`)

1. `manifest` accepts empty or `v1` only
2. Role / scope names: `^[a-z][a-z0-9_-]{0,31}$`, unique within each list
3. `bootstrap` must be `platform-admin=<role>` and the role must exist in vocabulary
4. Each `permissions.calls` entry must be `<svc-id>:<scope>`; the target must exist in this platform's manifests and declare that scope (**cross-manifest check**; dependencies install first)

### Design invariants

- Manifests declare **existence only**, never decision rules ("can editor publish?" lives in service code — L3 / Casbin discussion conclusion)
- All fields are optional: services without a membership model add nothing

## Blast radius

pctl (manifest parsing + validation), four language templates and existing manifests (stamp `manifest: v1`), architecture-v2 §3 and ADR #18. svc-grants / members UI / marketplace dialogs are later consumers — out of scope here.

## Acceptance

```bash
pctl check                                    # all existing services pass (backward compatible)
# Illegal manifests rejected one by one: bad version / duplicate role /
#   bootstrap referencing undeclared role / calls pointing at a missing
#   service or an undeclared scope
```
