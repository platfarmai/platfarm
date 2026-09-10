# 001: 第三方登录（svc-oauth + external-login 兑换端点）

## 动机

平台目前仅用户名密码登录。需要接入 GitHub / 微信等第三方登录，且不违背"auth 底座永不增肥"红线（不引入任何第三方 SDK 进 auth）。

## 方案

设计定稿见 [architecture-v2.md 附录 G](../../../docs/architecture-v2.md)。要点：

- auth 一次性改动：`external_identities` 绑定表 + `POST /internal/auth/external-login`（仅内网 + service token 白名单）+ `POST /auth/bind-external`
- svc-oauth 能力服务承担全部 OAuth 舞步（authorize/callback/state/PKCE），每个提供方一个模块
- 前端交付用 30 秒一次性兑换码，禁止 JWT 进重定向 URL

### 契约变更

- auth 新增两个端点（见上）；claims 结构**不变**
- svc-oauth 的 service.yaml：`mount.path: /api/oauth`，authorize/callback 为 public_routes
- auth 的 `accept_service_tokens` 概念首次实际启用（白名单 svc-oauth）

## 影响面

- `platform/auth`：+1 表、+2 端点（预计 +150 行内）
- 新服务 `services/svc-oauth`
- 网关配置：pctl sync 自动生成，无手工改动
- 现有服务：零影响

## 验收标准

```bash
# GitHub 全流程（手动）：/api/oauth/github/authorize → 授权 → 落地页拿一次性码 → exchange 得 token 对
# 自动化：
pctl check --e2e                          # 存量契约不回归
curl -X POST .../internal/auth/external-login  # 网关外 404；内网无 service token 401
# 同一 externalId 二次登录返回同一 userId（幂等）
```
