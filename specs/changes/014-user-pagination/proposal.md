# 014: User list pagination + search

[English](proposal.md) | [简体中文](proposal.zh-CN.md)

## Motivation

specs/011 lists all users unbounded — fine for a handful, bad at scale. Add server-side pagination and username search to the auth users API and the svc-users console.

## Design

### auth: paged users list

`GET /internal/auth/users?limit=&offset=&q=`:
- `limit` default 20, max 100; `offset` default 0
- `q` optional case-insensitive username substring (`ILIKE %q%`)
- response: `{ items: [...], total: N, limit, offset }` (total from a `count(*)` with the same filter)

Backward compatible: no params → first 20 + total.

### svc-users: search + pager

Console SPA: a search box (debounced) + prev/next pager showing `offset+1..offset+len / total`. Backend passes `limit/offset/q` through to auth.

### Invariants

- Passwords/hashes never returned (unchanged).
- Search is username-only in M2 (no role/status filter yet).
- `total` lets the UI page without loading everything.

## Blast radius

`platform/auth` (list query params + count), svc-users (backend passthrough + SPA pager), docs. Other user endpoints unchanged.

## Acceptance

```bash
# seed >25 users; GET .../users → items=20, total>25
# ?limit=5&offset=5 → items=5 starting at 6th
# ?q=ali → only usernames containing "ali"
# console: search filters; pager moves through pages; total shown
```
