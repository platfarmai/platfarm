# 023 tasks
- [x] auth: oauth_apps.quota_per_day + apps CRUD 透出 + quota.go Loki 查询（fail-open + 告警限频）
- [x] compose: auth 注入 LOKI_URL
- [x] QA：quota=2 → 3 次调用 → 续签 429 {usedToday:3}
