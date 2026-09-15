# 007: 可选表前缀 table_prefix

[English](proposal.md) | [简体中文](proposal.zh-CN.md)

## 动机

平台已按库隔离（`pf_*`，一服务一库），跨服务表名不会撞，通常无需表前缀。但两种场景仍需要：接入**遗留/共享库**，以及团队**强制命名风格**。因此提供一个可选声明，不强制、不碰业务 SQL。

## 方案

清单新增可选字段：

```yaml
data:
  database: pf_svc_cms
  table_prefix: ""       # 默认空；如接共享库可填 "cms_"
```

- 校验：空 **或** 匹配 `^[a-z][a-z0-9_]*_$`（小写、以 `_` 结尾）。
- `pctl sync` 仅在**非空**时向服务容器注入 `PF_TABLE_PREFIX=<值>`。
- 平台不改业务 SQL——服务自己读 `PF_TABLE_PREFIX` 拼表名。模板给一个小辅助。

### 不变式

- 默认空 → 现有服务不变（无 env、无行为变化）。
- 一服务一库仍是主隔离；前缀是兼容开关，不是要求。
- 平台不创建/修改业务表。

## 影响面

`pctl`（清单字段+校验+compose 注入）、模板（辅助+文档）、文档。存量服务不改，按清单自选启用。

## 验收标准

```bash
pctl check                     # 现有清单通过（空前缀）
# table_prefix: "cms_" → 生成 compose 含 PF_TABLE_PREFIX=cms_
# 非法前缀 "Cms" 或 "cms"（无结尾 _）→ pctl check 失败
# 空/未设 → compose 无 PF_TABLE_PREFIX
```
