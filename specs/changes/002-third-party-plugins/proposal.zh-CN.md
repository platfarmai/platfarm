# 002: 第三方插件体系 MVP（容器即沙箱）

[English](proposal.md) | [简体中文](proposal.zh-CN.md)

## 动机

可插拔从"第一方 AI 服务"推向"外部开发者交付的插件"。第三方只信契约不信代码，现有设计有三个洞：HS256 共享密钥可伪造 token、扁平内网可绕过网关、pctl 假设源码在 services/。

## 方案

设计定稿见 [architecture-v2.md 附录 H](../../../docs/architecture-v2.md)。要点：

1. **RS256 + JWKS**（硬前置）：`JWT_SECRET` 退役，全员公钥验签
2. **plugin.yaml**：service.yaml 超集（trust / source.image+digest / permissions / resources）
3. **一插件一网络**：插件只见 gateway 与自己的 plugin-pg；`internal: true` 断外网
4. **数据双层隔离**：独立角色+库（REVOKE CONNECT FROM PUBLIC）+ 独立 plugin-pg 实例
5. **双 token 上下文**：service token（我是谁/scope）+ X-PF-User-Token（代表谁）
6. **svc-console**：pctl 的 Web 外壳，唯一持有 docker socket，admin-only
7. **无状态铁律**：状态出口 = 自库 / Redis 租约

### 契约变更（平台级，必须走本提案）

- claims 无结构变化，但签名算法 HS256 → RS256（**所有服务模板与存量服务需改 JWKS 验签**——本提案最大的迁移面）
- service.yaml 新增 `trust` / `source` / `permissions` / `resources` 字段（第一方默认值向后兼容）
- pctl 新增 install/enable/disable/uninstall/upgrade 子命令

## 影响面

- auth（签发/JWKS）、gateway 配置生成、pctl（大改）、templates（验签方式）、存量 svc-demo（验签方式）
- 新增：plugin-pg 服务、svc-console 服务、.env.plugins/ 目录

## 验收标准

```bash
# 恶意插件演习（用一个故意越权的测试插件验证沙箱）：

[English](proposal.md) | [简体中文](proposal.zh-CN.md)
docker compose exec svc-plugin-evil ping svc-demo        # 应不可达（DNS 无此名）
docker compose exec svc-plugin-evil psql <平台PG>        # 应连接超时（无路由）
curl 网关 /api/file/... -H "Authorization: <插件token>"  # calls 未声明 → 403
# 正常路径：

[English](proposal.md) | [简体中文](proposal.zh-CN.md)
pctl install ./test-plugin && pctl check --e2e         # 安装闸门全绿
docker compose up -d --scale svc-plugin-test=3           # 多副本轮询正常
# 迁移回归：

[English](proposal.md) | [简体中文](proposal.zh-CN.md)
pctl check --e2e                                        # RS256 切换后存量服务契约不回归
```

## 分阶段（每步独立可合）

RS256+JWKS → 网络分段 → plugin-pg+开号 → pctl 生命周期 → client_credentials+scope 放行 → svc-console
