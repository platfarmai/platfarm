# 004 实施清单

[English](tasks.md) | [简体中文](tasks.zh-CN.md)

- [x] `pctl new svc-cms --lang go` + service.yaml 写入 roles vocabulary / bootstrap / exposes
- [x] 迁移：`cms_members` + `contents` 表；通过 `DATABASE_URL` 环境变量接库
- [x] 认证中间件（平台 JWT）+ 角色 accessor + platform-admin bootstrap
- [x] 内容 API：create/list/get/update + 状态迁移（submit/publish/archive）按矩阵闸门
- [x] 成员 API：list/invite/update-role（仅 admin）
- [x] 公开路由：list/get 已发布内容
- [x] 契约测试：401 / 200 / author 不能 publish 403 / 公开仅已发布
- [x] `pctl sync` + up + `check --e2e` 全绿
