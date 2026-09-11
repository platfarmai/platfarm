# 004: svc-cms — 内容管理服务（成员制 + 编辑角色）

[English](proposal.md) | [简体中文](proposal.zh-CN.md)

## 动机

在 PlatFarm 上搭建 CMS，作为首个真正使用权限清单词汇表（specs/003）的业务服务。编辑角色（`admin`/`editor`/`author`）落在服务内，不进平台 auth——L3 资源权限归内容的 owner 服务管。

## 方案

### 范围（本次交付 = 后端 MVP）

- 第一方 Go 服务 `svc-cms`（gin 模板）
- 表：`cms_members`（user_id, role）+ `contents`（title/body/status/owner_id）
- 角色矩阵 + 内容状态机（`draft → in_review → published → archived`）
- 已发布内容公开读；认证后的 CRUD 按角色闸门
- 契约测试：401 / 200 / **author 不能 publish → 403**

### 角色矩阵

| 动作 | admin | editor | author |
|---|---|---|---|
| 成员管理 | ✅ | ❌ | ❌ |
| 编辑/发布任何内容 | ✅ | ✅ | ❌ |
| 创建/编辑自己的草稿、提交审核 | ✅ | ✅ | ✅ |

### 清单（消费 003）

```yaml
manifest: v1
roles:
  vocabulary:
    - { name: admin,  desc: 管理成员与全部内容 }
    - { name: editor, desc: 编辑并发布全部内容 }
    - { name: author, desc: 创建和编辑自己的草稿 }
  bootstrap: platform-admin=admin
exposes:
  scopes:
    - { name: read, desc: 读取已发布内容 }
```

### 延后（不在本次）

- 共享 svc-grants（触发：第二个成员制服务）——CMS 用本地 `cms_members` 表，包在薄 accessor 后，将来切换零改动
- 管理端 SPA —— API 先行，UI 后续
- 媒体库（svc-file + MinIO）

## 影响面

仅新增 `services/svc-cms`。平台契约不变（使用 003 字段）。网关由 `pctl sync` 重新生成。

## 验收标准

```bash
pctl check --e2e                              # 含 svc-cms 全绿
# author token 不能 publish → 403
# editor/admin 可 publish → 200
# 公开 GET /api/cms/public/contents 仅返回已发布
# 平台 admin 首次访问自动 bootstrap 为 cms admin
```
