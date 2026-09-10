# 001 实施清单（2026-09-04 完成，实测记录见下）

- [x] auth：migrate 增加 `external_identities` 表（provider+external_id 联合主键）
- [x] auth：`POST /internal/auth/external-login` — service token 白名单校验 → 查绑定 → 发 token 对 / 自动建号
- [x] auth：`POST /internal/auth/bind-external`（OBO：service token + X-PF-User-Token）
- [x] auth：service token 签发（`POST /auth/service-token`，client_credentials；凭据由 `-register-client` CLI 开出）
- [x] svc-oauth：GitHub provider + **mock provider**（开发/E2E 用，无需外部账号）
- [x] svc-oauth：state 防 CSRF（内存 TTL）+ 一次性兑换码 `POST /api/oauth/exchange`
- [x] 契约测试：mock 全流程 / 兑换码复用 401 / 伪造 state 401 —— `pctl check --e2e` 全绿
- [x] docs：README §2.4 试玩流程；adding-a-service.md FAQ 更新
- [x] 验收：mock 登录自动建号（`mock_mock-user-1`）、二次登录同 userId（幂等）、`/internal/*` 网关外 404

**偏差记录**：PKCE 未实现（GitHub OAuth App 不支持；接支持 PKCE 的提供方时在其 provider 模块内补）。
