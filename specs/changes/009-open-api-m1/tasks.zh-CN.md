# 009 实施清单

[English](tasks.md) | [简体中文](tasks.zh-CN.md)

- [x] auth：`oauth_apps` 表 + `-register-app` CLI（打印 app_key/secret）
- [x] auth：`POST /oauth/token`（app_key/secret → app token 带 scopes，tokenType=app）
- [x] pctl manifest：`open_api[{route,scope}]` 字段 + 校验 scope ∈ exposes.scopes
- [x] pctl sync：每条开放路由生成网关 pre-function（要求 app token + scope，写 X-PF-App-Key）+ 按 app 限流
- [x] 模板：开放路由接受 tokenType=app（appKey 身份，无用户）
- [x] svc-demo：声明 open_api 样例 `GET /api/demo/data` + handler
- [x] 文档：adding-a-service open_api；ADR #24
- [x] QA：app token 200 / 缺 scope 403 / app 走保护路由 403 / 超频 429 / 用户路径不变；提交推送
