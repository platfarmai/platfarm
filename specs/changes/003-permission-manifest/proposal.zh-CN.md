# 003: 清单权限声明规范 v1（roles / exposes / 跨清单校验）

[English](proposal.md) | [简体中文](proposal.zh-CN.md)

## 动机

清单目前只声明"我需要什么"（`permissions.calls/egress`），缺另外两个面向：

- 服务/插件**定义什么角色给用户**（svc-cms 的 editor/author 这类业务角色，svc-grants 与通用成员 UI 的数据来源）
- 服务/插件**暴露什么 scope 给其它服务**（现在 `calls: ["svc-file:read"]` 里的 scope 是裸字符串约定，无人校验）

一份声明，五处消费：pctl 跨清单校验、svc-grants 词汇表约束、通用成员管理 UI、市场权限弹窗与升级 diff、auth 发 service token 的 scopes 来源。

## 方案

### 清单扩展（全部可选，向后兼容）

```yaml
manifest: v1                        # 规范版本锚点（缺省视为 v1）

permissions:                        # ① 我需要什么（已有）
  calls: ["svc-file:read"]          #    必须命中目标服务 exposes.scopes

roles:                              # ② 我定义什么角色给用户（新增）
  vocabulary:
    - { name: admin,  desc: 管理成员与全部内容 }
    - { name: editor, desc: 编辑并发布全部内容 }
  bootstrap: platform-admin=admin   # 平台 admin 首访自动授予（仅允许此形式）

exposes:                            # ③ 我暴露什么给其它服务（新增）
  scopes:
    - { name: read, desc: 读取已发布内容 }
```

### 校验规则（pctl check / sync 强制）

1. `manifest` 仅接受空或 `v1`
2. 角色/scope 名：`^[a-z][a-z0-9_-]{0,31}$`，各自唯一
3. `bootstrap` 形如 `platform-admin=<role>` 且 role 在 vocabulary 内
4. `permissions.calls` 条目形如 `<svc-id>:<scope>`；目标服务必须存在于本平台清单且声明了该 scope（**跨清单校验**，依赖先装）

### 设计纪律（不变式）

- 清单只声明"存在什么"，**永不声明判定规则**（"editor 能否 publish"在服务代码，L3/Casbin 讨论结论）
- 字段全可选：不需要成员体系的服务一行不加

## 影响面

pctl（manifest 解析 + 校验）、四语言模板与存量清单（加 `manifest: v1`）、architecture-v2 §3 与 ADR。svc-grants / 成员 UI / 市场弹窗为后续消费方，不在本提案。

## 验收标准

```bash
pctl check                                    # 存量服务全部通过（向后兼容）
# 非法清单逐条拒绝：坏版本 / 重复角色 / bootstrap 引用未声明角色 /
#   calls 指向不存在的服务或未暴露的 scope
```
