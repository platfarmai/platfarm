# 007 实施清单

[English](tasks.md) | [简体中文](tasks.zh-CN.md)

- [x] pctl manifest：`data.table_prefix` 字段
- [x] pctl 校验：空 或 `^[a-z][a-z0-9_]*_$`
- [x] pctl compose：仅非空时注入 `PF_TABLE_PREFIX`
- [x] 模板：`PF_TABLE_PREFIX` 辅助（go + py）+ 文档
- [x] 文档：adding-a-service 加 `table_prefix`；ADR #22
- [x] QA：合法注入 env、非法 check 失败、空不注入；提交推送
