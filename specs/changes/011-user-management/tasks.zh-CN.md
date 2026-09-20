# 011 实施清单

[English](tasks.md) | [简体中文](tasks.zh-CN.md)

- [x] auth：内网用户接口（list/create/patch/reset-password）+ admin OBO 闸门
- [x] auth：`POST /auth/change-password`（验旧、吊销当前 token）
- [x] auth：防锁死（最后一个 admin 不可降级/禁用；不可禁用/降级自己）；禁用即吊销
- [x] auth：create/reset 返回一次性密码；list 不含 hash；白名单加 svc-users
- [x] svc-users 后端（Go）：/api/users/* 代理（service token + OBO）+ 自助改密码透传
- [x] svc-users Vue SPA：admin 列表/建号/改角色/启停/重置 + 自助改密码；cookie SSO
- [x] plugin.yaml admin_ui + 多阶段 Dockerfile；注册 svc-users 凭据
- [x] QA 验收矩阵；文档 ADR #26；提交推送
