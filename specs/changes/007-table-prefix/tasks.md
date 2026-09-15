# 007 Implementation Checklist

[English](tasks.md) | [简体中文](tasks.zh-CN.md)

- [x] pctl manifest: `data.table_prefix` field
- [x] pctl validate: empty or `^[a-z][a-z0-9_]*_$`
- [x] pctl compose: inject `PF_TABLE_PREFIX` only when non-empty
- [x] templates: `PF_TABLE_PREFIX` helper (go + py) + doc note
- [x] docs: adding-a-service `table_prefix`; ADR #22
- [x] QA: valid injects env, invalid fails check, empty no env; commit/push
