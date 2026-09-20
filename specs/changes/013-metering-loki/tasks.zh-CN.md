# 013 实施清单

[English](tasks.md) | [简体中文](tasks.zh-CN.md)

- [x] pctl compose：网关 JSON 访问日志（http-log/file-log）含 X-PF-App-Key、X-Request-Id
- [x] 基座 compose：observability profile —— loki + promtail（采集网关日志 → Loki）
- [x] svc-openapi：GET /api/openapi/usage → 代理 Loki LogQL（admin-only）；"未启用"兜底
- [x] svc-openapi SPA：用量面板（按 app 计数、窗口选择）
- [x] 文档 ADR #28；.env.example LOKI_URL
- [x] QA：发 N 次 → 用量显示约 N；profile 关 → 优雅"未启用"
