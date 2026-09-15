# Feature：统一管理壳（单点登录 + 嵌入后台）

[English](admin-shell.md) | [简体中文](admin-shell.zh-CN.md)

状态：设计。实施时走 platfarm + pctl 的 `specs/006`。

## 要解决什么

现在平台 console、CMS、以后的插件后台各自登录。Token 在不同源的 `localStorage`（`:18001` vs `/api/cms/console`）。期望：

1. 只登一次  
2. 所有应用后台集中在**一个壳**里打开（iframe / 页内标签）  
3. 切换应用不再要密码  

## 不做

- 把各应用 UI 合成一个大 SPA（页面仍由各应用自己管）  
- 把 CMS 的 editor 写进平台 JWT（平台仍是 `admin|user`）  
- 让第三方插件在自己的独立域名上吃平台 Cookie  

## 方案：同站 Cookie + 网关 + 清单声明入口

```
浏览器 → https://host:18000/platform/console/     壳
      → https://host:18000/auth/login             登录（Set-Cookie）
      → iframe src=/api/cms/console/              CMS 后台
      → /api/cms/*                                Cookie 自动带上
```

全部挂在 **同一个网关主机** 上，Cookie 才是第一方，iframe 里不用折腾第三方 Cookie。

### 1. 登录写 Cookie

`POST /auth/login` 除 JSON token 外再：

```
Set-Cookie: pf_access=<jwt>; Path=/; HttpOnly; Secure; SameSite=Lax
```

Logout 清 Cookie + Redis 吊销。**Bearer 保留**给 curl / 移动端。

Kong JWT 目前读 `Authorization`。建议用 **pre-function**：没有头但有 `pf_access` 时补上 `Bearer`，现有服务不用改。

### 2. Console 必须和 API 同源

现在 console 在 `127.0.0.1:18001`，和 `:18000` 不同源，iframe **共享不了 Cookie**。

改为经 Kong 挂 **`/platform/console/`**（`/platform` 已是保留前缀）。人打开 `https://host:18000/platform/console/`。`18001` 可留作本机调试。

### 3. 清单声明可嵌入后台

```yaml
admin_ui:
  path: /console          # 相对 mount → /api/cms/console
  embed: true
  title: CMS
```

- 路径必须在 `mount.path` 下  
- `embed: true` 则页面允许被同源 iframe（不要 `X-Frame-Options: DENY`）  
- 壳从清单列出应用，iframe 打开对应 path  

不能嵌的设 `embed: false`，新标签打开；只要同源，Cookie 仍免登。

### 4. iframe 里的 SPA

HttpOnly Cookie **JS 读不到**。子应用应：`GET /auth/me`（自动带 Cookie）判断已登录。不要把 JWT 拼进 iframe URL。

### 5. 应用侧角色

平台 JWT 不膨胀。CMS 的 editor/author 仍在应用自己的成员表（或以后的 svc-grants）。壳只保证「是谁」；「能不能发文章」仍是 L3。

## 实施切分

1. login/logout 写/清 Cookie + Kong 补 Bearer  
2. console 挂到 `/platform/console`  
3. `admin_ui` 字段 + 壳里的 iframe 切换  
4. CMS 管理端作为第一个嵌入应用  

不必再上 Authelia：已有唯一签发方 `pf-auth` 和唯一网关。
