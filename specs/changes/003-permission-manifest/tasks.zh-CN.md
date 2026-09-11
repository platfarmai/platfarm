# 003 实施清单

[English](tasks.md) | [简体中文](tasks.zh-CN.md)

## 第 1 步 — 字段与校验（本次交付）

- [x] Manifest 增加 `manifest`（版本）/ `roles` / `exposes` 字段
- [x] permissions.go：命名/唯一性/bootstrap 校验 + calls↔exposes 跨清单校验
- [x] 四语言模板与存量清单加 `manifest: v1`
- [x] 正反例 QA + 文档（§3 示例、ADR #18）

## 后续（各自触发点）

- [ ] svc-grants 落地时消费 vocabulary/bootstrap（触发：第二个成员制服务）
- [ ] console 通用成员管理页按 vocabulary+desc 渲染
- [ ] 市场安装弹窗/升级 diff 纳入 roles 与 scopes 维度
- [ ] auth service token 的 scopes 改为经校验的声明来源
