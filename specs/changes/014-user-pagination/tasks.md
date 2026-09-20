# 014 Implementation Checklist

[English](tasks.md) | [简体中文](tasks.zh-CN.md)

- [x] auth: users list accepts limit/offset/q; returns {items,total,limit,offset}; ILIKE search; cap limit 100
- [x] svc-users backend: pass limit/offset/q through to auth
- [x] svc-users SPA: debounced search box + prev/next pager + total
- [x] docs ADR #29
- [x] QA: default 20+total / limit+offset window / q filter / console search+pager
