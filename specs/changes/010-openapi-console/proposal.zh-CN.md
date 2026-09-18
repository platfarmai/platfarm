# 010: 开放平台管理后台（svc-openapi）

[English](proposal.md) | [简体中文](proposal.zh-CN.md)

## 动机

specs/009 做了 app token + scope 校验,但管理只有 CLI（`auth -register-app` + 改 `oauth_apps` 表）。给管理员一个后台**颁发凭据 + 管理规则**（scope、限流、启停），嵌进 SSO 壳,不新造机制。

## 方案（本次交付 = M2）

### auth：内网 apps 接口

把 `register-app` 逻辑抽成内网接口（仅内网 + service token 白名单 + `X-PF-User-Token` 代持的用户须平台 admin）：

| 端点 | 用途 |
|---|---|
| `GET  /internal/auth/apps` | 列出 app（不含 secret） |
| `POST /internal/auth/apps` | 创建 → secret **只返回一次** |
| `PATCH /internal/auth/apps/{key}` | 改 scopes / rate_per_min / status |
| `POST /internal/auth/apps/{key}/rotate` | 换 secret（只显示一次） |

CLI `-register-app` 保留（脚本/引导用）。secret 存 bcrypt 哈希,创建后不可回看。

### svc-openapi：能力服务 + Vue 后台

第一方服务 `svc-openapi`：

- 后端（Go）：`/api/openapi/*` admin-only；用 service token + 转发管理员 `X-PF-User-Token`（OBO）调 auth 内网接口。给 UI 提供 scope 目录（M2 用静态清单）。
- SPA（Vue,内嵌）：app 列表、创建（secret 一次性显示+复制）、改 scope/限流、启停、换 secret。与 CMS 同款 cookie-SSO。
- `admin_ui: { path: /console, embed: true, title: OpenAPI }` → 出现在平台壳。

### 边界（重要）

- 后台管的是**长期凭据（app_key/secret）+ 规则**,不是短时效 access token。合作方仍自己调 `POST /oauth/token`。
- secret 创建/换发时**只显示一次**;只存 bcrypt。丢了只能 rotate。
- 后台全路由 platform-admin only。

### 延后（M3）

- 从 `X-PF-App-Key` 计量日志聚合用量/配额
- 合作方自助门户（合作方自管 app）
- scope 目录从 `pctl` 清单自动派生

## 影响面

`platform/auth`（内网 apps 接口 + 重构）、新增 `svc-openapi`、文档。app token 运行时（specs/009）不变。

## 验收标准

```bash
# 以 admin 经 SSO 壳:
# 建 app "acme" 授 scope data.orders.read → secret 只显示一次
# acme 调 /oauth/token → app token → 开放路由 200
# 改 acme 去掉 scope → 开放路由 403
# 停用 acme（status=0）→ /oauth/token 401
# 换 secret → 旧 secret 401,新 secret 可用
# 非 admin → 后台 403
```
