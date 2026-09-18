# 010 实施清单

[English](tasks.md) | [简体中文](tasks.zh-CN.md)

- [x] auth：重构 register 逻辑；内网 apps 接口（list/create/update/rotate）+ service token + admin OBO 闸门
- [x] auth：create/rotate 返回一次性 secret；list/update 永不返回 secret
- [x] svc-openapi 后端（Go）：/api/openapi/* admin-only，用 service token + X-PF-User-Token 调 auth 内网接口，scope 目录
- [x] svc-openapi Vue SPA：列表/创建(secret 一次)/改 scope+限流/启停/换 secret；cookie SSO
- [x] plugin.yaml：admin_ui + accept_service_tokens；多阶段 Dockerfile
- [x] QA：创建→secret 一次→token→开放 200；去 scope→403；停用→401；换 secret→旧 401；非 admin 403
- [x] 文档 ADR #25；提交推送；bump submodule
