# 006: 单点登录 Cookie + 统一管理壳

[English](proposal.md) | [简体中文](proposal.zh-CN.md)

## 动机

各应用后台（console、CMS、以后的插件）在不同源各自登录。期望：登一次、一个壳里嵌所有后台、切换不再要密码。设计见 [docs/features/admin-shell.zh-CN.md](../../../docs/features/admin-shell.zh-CN.md)。

## 方案（本次交付）

1. **会话 Cookie** —— `POST /auth/login` 与 `/auth/refresh` 额外写 `pf_access`（HttpOnly、`Path=/`、`SameSite=Lax`、TLS 下 `Secure`）。`/auth/logout` 清 Cookie 并照常 Redis 吊销。Bearer 仍有效。
2. **Kong Cookie→Bearer** —— `pctl sync` 加全局 `pre-function`：无 `Authorization` 但有 `pf_access` 时补 `Bearer`。现有服务不改。
3. **Console 挂网关** —— `/platform/console` → console 容器，与 `/api/*` 同源共享 Cookie。`18001` 留作本机调试。
4. **清单 `admin_ui`** —— 服务声明可嵌入后台入口：
   ```yaml
   admin_ui: { path: /console, embed: true, title: CMS, icon: cms }
   ```
5. **壳** —— console 列出带 `admin_ui` 的应用，做应用切换 + iframe。登录写 Cookie，`GET /auth/me` 判断已登录。

### 契约变更

- auth：login/refresh 增加 `Set-Cookie`；claims 不变。
- service.yaml/plugin.yaml：可选 `admin_ui`（向后兼容）。
- Kong：全局 `pre-function` + `/platform/console` 路由。

### 不变式

- 平台 JWT 仍 `admin|user`；应用角色留应用内。
- access token 绝不进 iframe URL。
- Cookie 是增量；Bearer/API/curl 不受影响。

## 影响面

`platform/auth`（Cookie）、`pctl`（清单 `admin_ui`、sync pre-function + console 路由、壳 HTML）、文档。存量服务不改，加 `admin_ui` 才嵌入。

## 验收标准

```bash
# 登录响应含 Set-Cookie: pf_access=...
# 打开 http://host:18000/platform/console/ → 壳免二次登录
# 仅带 Cookie（无 Authorization）GET /api/demo/me → 200（Kong 补 Bearer）
# logout 后仅带 Cookie → 401
# pctl check 绿；svc-demo/oauth 契约不受影响
```
