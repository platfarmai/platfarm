# 004 Implementation Checklist

[English](tasks.md) | [简体中文](tasks.zh-CN.md)

- [x] `pctl new svc-cms --lang go` + roles vocabulary / bootstrap / exposes in service.yaml
- [x] Migrate: `cms_members` + `contents` tables; seed DB via env `DATABASE_URL`
- [x] Auth middleware (platform JWT) + role accessor + bootstrap platform-admin
- [x] Content APIs: create/list/get/update + transition (submit/publish/archive) gated by matrix
- [x] Members APIs: list/invite/update-role (admin only)
- [x] Public routes: list/get published contents
- [x] Contract tests: 401 / 200 / author-cannot-publish 403 / public-only-published
- [x] `pctl sync` + up + `check --e2e` green
