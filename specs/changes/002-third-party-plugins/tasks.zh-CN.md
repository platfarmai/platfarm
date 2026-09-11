# 002 实施清单（Stage 1~4 于 2026-09-04 完成；Stage 5 待排期）

[English](tasks.md) | [简体中文](tasks.zh-CN.md)

## Stage 1 — RS256 + JWKS ✅
- [x] auth：RSA 生成/加载（`.keys/pf-auth.pem`，auth 与 pctl 幂等自举）+ RS256 签发 + JWKS 端点
- [x] pctl sync：Kong consumer 改 `rsa_public_key` 注入
- [x] templates + svc-demo：公钥文件验签（`/pf/jwt.pub` 平台挂载）
- [x] `pctl check --e2e` 回归全绿；JWT_SECRET 概念退役（.env/.env.example 已清）

## Stage 2 — 网络与数据隔离 ✅
- [x] core-net 显式化；gateway/plugin-pg 移入生成文件（networks 列表随插件增减）
- [x] 一插件一网络（`net-plugin-*`，internal: true）；插件 DNS 仅见 gateway 与 plugin-pg
- [x] plugin-pg 独立实例 + 安装时开号（CREATE ROLE/DATABASE + REVOKE CONNECT FROM PUBLIC）
- [x] `.env.plugins/<id>.env` 生成注入；.gitignore 已补

## Stage 3 — pctl 生命周期 ✅
- [x] plugin.yaml 解析与校验（trust/source/permissions/resources；缺 digest 警告）
- [x] `install`（校验→env→sync→开号→凭据→起容器→契约测试，失败自动 disable）
- [x] `enable/disable`（.disabled 标记 + sync 摘挂路由）/ `uninstall [--purge]` / `upgrade`

## Stage 4 — 插件身份 ✅（一处降级实现，见偏差）
- [x] client_credentials：`/auth/service-token` + `-register-client` CLI，scopes=permissions.calls
- [x] 双 token OBO 进模板（service token 白名单 + X-PF-User-Token 解析）

## Stage 5 — console 最小版 ✅（实现为 `pctl serve`，非独立服务）
- [x] `pctl serve`：同一二进制的 Web 模式（基座 compose `console` 服务，仅 127.0.0.1:18001）
- [x] admin-only（RS256 验签 + role=admin；非 admin 403、无 token 401 实测）
- [x] 服务列表（清单 + Docker API 实时状态）/ 启停（改标记 + sync + 网关重启，真相源仍是文件）/ 日志投影 / 审计（.console-audit.jsonl）
- [x] 实测：console 停用 svc-demo → 网关 404 → 启用 → 200；审计留痕含操作人
- 边界：创建类操作（install/uninstall）不进 UI，宿主机 pctl CLI 执行（容器内 compose 路径映射限制，UI 内已提示）

## 验收 ✅
- [x] 恶意插件演习三连：平台服务 DNS SERVFAIL / 平台 PG 不可达（host.docker.internal 无法解析）/ 插件自己的 plugin-pg 可达
- [x] `--scale svc-demo=2` 6 连发全 200；缩回正常
- [x] 存量契约回归全绿（RS256 切换后）

**偏差记录（文档同步见 architecture-v2.md 附录 H 注记）**：
1. `permissions.calls` 的 scope 放行目前在**被调服务侧**执行（模板白名单），网关级放行需 Kong 自定义插件，列入 Phase 6 余项
2. install 闸门为 staged-lite：先挂路由再跑契约测试、失败自动 disable（严格 staged 需网关支持影子路由）
3. `permissions.egress` 仅记录未执行（compose 网络 internal: true 全断；按域白名单需 egress 代理容器）
