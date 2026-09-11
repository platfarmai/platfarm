# 003 Implementation Checklist

[English](tasks.md) | [简体中文](tasks.zh-CN.md)

## Step 1 — Fields & validation (this delivery)

- [x] Manifest adds `manifest` (version) / `roles` / `exposes` fields
- [x] `permissions.go`: naming / uniqueness / bootstrap checks + calls↔exposes cross-manifest checks
- [x] Four language templates and existing manifests stamped with `manifest: v1`
- [x] Positive/negative QA + docs (§3 example, ADR #18)

## Later (each has its own trigger)

- [ ] svc-grants consumes vocabulary/bootstrap (trigger: a second membership-bearing service)
- [ ] Console shared members page renders from vocabulary + desc
- [ ] Marketplace install dialog / upgrade diff include roles and scopes
- [ ] auth service-token scopes come from validated declarations
