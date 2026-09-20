# 014: 用户列表分页 + 搜索

[English](proposal.md) | [简体中文](proposal.zh-CN.md)

## 动机

specs/011 无界列出全部用户——少量没问题,规模化就差。给 auth 用户接口和 svc-users 后台加服务端分页 + 用户名搜索。

## 方案

### auth：分页用户列表

`GET /internal/auth/users?limit=&offset=&q=`：
- `limit` 默认 20、最大 100；`offset` 默认 0
- `q` 可选,用户名不区分大小写子串（`ILIKE %q%`）
- 响应：`{ items: [...], total: N, limit, offset }`（total 用同过滤的 `count(*)`）

向后兼容：无参 → 前 20 + total。

### svc-users：搜索 + 翻页

后台 SPA：搜索框（防抖）+ 上一页/下一页,显示 `offset+1..offset+len / total`。后端把 `limit/offset/q` 透传给 auth。

### 不变式

- 密码/哈希永不返回（不变）。
- M2 搜索仅用户名（暂无角色/状态过滤）。
- `total` 让 UI 无需全量加载即可翻页。

## 影响面

`platform/auth`（list 查询参数 + count）、svc-users（后端透传 + SPA 翻页）、文档。其它用户接口不变。

## 验收标准

```bash
# 造 >25 用户；GET .../users → items=20, total>25
# ?limit=5&offset=5 → items=5 从第 6 个起
# ?q=ali → 仅含 "ali" 的用户名
# 后台：搜索过滤；翻页；显示总数
```
