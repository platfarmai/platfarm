# 009: 开放平台 M1（app token + scope 校验）

[English](proposal.md) | [简体中文](proposal.zh-CN.md)

## 动机

把平台部分数据通过 API 下发给外部合作方/开发者，按 app 授权、按路由 scope 校验、按 app 限流。这是第三条身份线——**app**，与用户 access token、内部 service token 并列。复用已有的 `exposes.scopes` 声明与网关，不新造权限引擎。

## 方案（本次交付 = M1）

### 身份：`tokenType: app`

`POST /oauth/token` 用 `app_key` + `app_secret`（client_credentials）换短时效 JWT：

```jsonc
{ "tokenType": "app", "appKey": "ak_...", "scopes": ["data.orders.read"], "iss": "pf-auth", "exp": ... }
```

auth 新增 `oauth_apps`（app_key, secret_hash, name, owner_id, scopes, rate_per_min, status）及 `-register-app` CLI 发凭据。

### 声明：清单 `open_api`

服务声明哪些路由开放、各需什么 scope（scope 名须在 `exposes.scopes` 中）：

```yaml
exposes:
  scopes:
    - { name: data.orders.read, desc: 读取订单（开放 API） }
open_api:
  - { route: "GET /api/demo/data", scope: data.orders.read }
```

`pctl check` 校验每个 `open_api.scope` 已在本服务 `exposes.scopes` 声明。

### 校验：网关强制 scope

`pctl sync` 为每条开放路由生成 Kong `pre-function`：
1. 要求 `tokenType == "app"`（M1 开放路由仅 app）
2. 路由 scope ∈ token `scopes`，否则 **403**
3. 写 `X-PF-App-Key` 头（供按 app 限流 + 计量）

按 app 限流：`rate-limiting` `limit_by: header` 用 `X-PF-App-Key`。计量 = 网关访问日志带 `X-PF-App-Key`（进日志管道；账单数据源）。

### 服务侧

模板**仅**在清单标记的开放路由接受 `tokenType: app`；app token 无用户身份，行级过滤用 `appKey`。M1 不支持 app token 代持用户。

### 延后（M2）

- `svc-openapi` 合作方自助台（app CRUD、授 scope、配额）走 SSO 壳
- 计量日志聚合出配额/账单
- 合作方 API 文档门户（openapi 聚合）

### 不变式

- 用户 JWT claims 不变；`app` 是增量。
- scope 词汇仍在 `exposes.scopes`（specs/003）；open_api 只做 route→scope 映射。
- 数据行级过滤仍在服务（L3）。

## 影响面

`platform/auth`、`pctl`、模板、svc-demo、文档。用户/service token/既有路由不受影响。

## 验收标准

```bash
docker compose exec auth /auth -register-app demo-partner -scopes data.orders.read
curl -X POST .../oauth/token -d '{"appKey":"ak_...","appSecret":"..."}'   # → app token
curl .../api/demo/data -H "Authorization: Bearer <app_token>"             # → 200
# 缺 scope → 网关 403；app token 走非开放路由 /api/demo/me → 403
# 超 app 限流 → 429；普通用户 access token 走 /api/demo/me 仍 200
```
