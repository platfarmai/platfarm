# 011: 平台用户管理（auth 接口 + svc-users 后台）

[English](proposal.md) | [简体中文](proposal.zh-CN.md)

## 动机

auth 有 `users` 表但只有 `/auth/me`——没有列表、建号、改角色、禁用、改密码。给管理员用户管理,给用户自助改密码。平台用户(登录 + 粗角色 admin|user)与应用成员(CMS editor 等,L3)是两回事。

## 方案（本次交付 = M1）

### auth：用户管理接口

| 端点 | 谁 | 用途 |
|---|---|---|
| `GET /internal/auth/users` | service token + admin OBO | 列表（不含 password_hash） |
| `POST /internal/auth/users` | admin | 建用户 → 初始密码只显示一次 |
| `PATCH /internal/auth/users/{id}` | admin | 改角色 / status |
| `POST /internal/auth/users/{id}/reset-password` | admin | 重置 → 新密码只显示一次 |
| `POST /auth/change-password` | 用户本人（access token） | 改自己密码（验旧密码） |

规则：
- 密码 bcrypt；除创建/重置的一次性明文外永不返回。
- **防锁死**：不能降级/禁用最后一个可用 admin；admin 不能禁用/降级自己。
- **禁用即吊销**：`status=0` 时吊销该用户活跃 token（复用吊销存储）；login/refresh 已拒 `status != 1`。
- `POST /auth/change-password {oldPassword, newPassword}` 验旧 → 改哈希 → 吊销当前 token（强制重登）。

### svc-users：后台

第一方服务 `svc-users`（照 svc-openapi）：Go 后端用 service token + admin OBO 代理 auth 内网用户接口；内嵌 Vue SPA。admin：列表/建号(密码一次)/改角色/启停/重置。任意登录用户：自助改密码页。`admin_ui` 嵌入 SSO 壳。

### 边界

- 平台 `users.role` 仍 `admin|user`；应用业务角色留应用（L3）。
- svc-users 管平台账号+平台角色+密码,不管应用成员。

### 延后（M2）

- 大用户量的分页/搜索
- 外部身份(OAuth)绑定视图
- 自助注册流

## 影响面

`platform/auth`、新增 `svc-users`、文档。登录/`/auth/me`/既有 token 不变。

## 验收标准

```bash
# admin 经 SSO 壳：
# 列表；建 "bob"（密码只显示一次）→ bob 可登录
# 改 bob user→admin；禁用 bob → bob 登录 401；启用 → 登录成功
# 重置 bob 密码 → 旧失败新成功
# 自助改密码（验旧）；旧密码错 → 401
# 防锁死：降级/禁用唯一 admin → 拒绝
# 非 admin → 用户接口 403，但自助改密码可用
```
