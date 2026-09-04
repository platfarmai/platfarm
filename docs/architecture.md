# Platfarm 平台架构改造方案：以 LinaPro 为底座的可插拔容器化平台

> 版本：v1.0（2026-09-03）
> 状态：设计定稿，待实施
> 源码分析依据：`D:\work\linapro`（LinaPro v0.5.x，Go 1.25 + GoFrame v2.10）

---

## 1. 目标与设计原则

**目标**：构建一个"AI 快速产出服务、即插即用"的平台——对外只有一个 API 网关容器，每个业务服务一个独立容器，LinaPro 作为平台底座提供用户/认证/RBAC/多租户/管理台能力。

**原则**：

1. **可插拔**：新增一个服务 = 一个容器 + 一段网关路由声明，不改任何已有服务的代码
2. **单一约定接入**：业务容器只需要会一件事——解码 LinaPro 签发的 JWT，即可获得用户身份
3. **不拆 LinaPro**：整体作为一个容器使用，不做外科手术式模块拆分（auth 与 GoFrame ORM / bizctx / 表结构深耦合，拆分成本 > 收益）
4. **粗筛在网关，细判在服务**：网关做 token 有效性 + 限流；资源归属权限由各服务自己判定
5. **异构友好**：AI 服务可以是 Python / Node / Go 任意技术栈，通过容器边界隔离

---

## 2. 总体架构

```
                          公网
                           │
                  ┌────────▼────────┐
                  │  Kong 网关容器    │  唯一对外端口
                  │  路由/验签/限流   │
                  └───┬────┬────┬───┘
              内网（容器网络，业务容器不暴露端口）
        ┌─────────────┤    │    └─────────────┐
        │             │    │                  │
┌───────▼───────┐ ┌───▼────▼───┐    ┌────────▼────────┐
│  lina-core     │ │ service-a  │    │  service-b       │
│  平台底座容器   │ │ (Python AI)│    │  (Node/Go/...)   │
│ ─────────────  │ └─────┬──────┘    └────────┬────────┘
│ · 登录/签发JWT │       │    互调走内网服务名  │
│ · 用户/RBAC    │       └──────────┬─────────┘
│ · 多租户/会话  │                  │
│ · 管理台API    │           （透传用户 JWT）
└───────┬───────┘
        │
┌───────▼───────┐   ┌─────────┐
│  PostgreSQL    │   │  Redis   │ (lina-core 会话/集群协调，可选)
└───────────────┘   └─────────┘

前端：lina-vben 管理台（静态资源，走网关 /admin）+ 业务前端（任意）
```

### 组件职责

| 组件 | 容器 | 职责 |
|---|---|---|
| Kong | `gateway` | 唯一入口；路由分发；JWT 粗验；限流；后续可加日志/监控插件 |
| LinaPro lina-core | `lina-core` | 登录、JWT 签发/刷新/吊销、用户/角色/RBAC、多租户、管理台 API、任务调度 |
| LinaPro lina-vben | 静态资源或独立容器 | 平台管理台 UI |
| 业务服务 × N | `svc-*` | 各自业务；验 JWT 获得身份；资源权限自判 |
| PostgreSQL | 共享实例 | lina-core 主存储；业务服务各建自己的库（**不共享表**） |
| Redis | `redis`（可选） | lina-core 会话/分布式协调（`cluster.enabled=true` 时必须） |

---

## 3. 身份与权限设计（核心契约）

### 3.1 Token 签发（源码已确认的事实）

- 算法：**HS256 共享密钥**（`auth_impl.go:635`，`jwt.SigningMethodHS256`）
- 密钥：`manifest/config/config.yaml` → `jwt.secret`，有效期 `jwt.expire: 24h`
- access / refresh 双 token（`TokenType` claim 区分）
- 服务端会话校验 + 吊销机制内置（`sessionStore.TouchOrValidate`、`auth_revoke.go`）

### 3.2 Claims 契约（所有业务服务依赖的稳定接口）

```go
// D:\work\linapro\apps\lina-core\internal\service\auth\auth.go:238
{
  "tokenId":         "…",     // 唯一 token 标识，吊销检查用
  "tokenType":       "access",// 业务服务必须校验 == access
  "clientType":      "web",   // web / mobile / desktop
  "userId":          42,
  "username":        "alice",
  "status":          1,
  "tenantId":        0,       // 0 = 平台级，>0 = 租户
  "isImpersonation": false,   // 管理员代持标记
  "actingUserId":    0,
  // + jwt.RegisteredClaims (exp, iat, ...)
}
```

**平台级约定（写进每个服务的接入模板）**：

1. 从 `Authorization: Bearer <jwt>` 取 token
2. 用共享密钥（环境变量 `JWT_SECRET`）验 HS256 签名 + `exp`
3. 校验 `tokenType == "access"`
4. `userId` / `tenantId` 即当前用户身份；多租户服务的所有查询必须带 `tenantId` 过滤
5. 验签失败 / 过期 → 返回 401，不做任何降级

### 3.3 权限分层

| 层 | 判什么 | 由谁判 |
|---|---|---|
| L1 网关 | token 签名有效、未过期 | Kong（Phase 2 起） |
| L2 路由 | 该路由是否需要登录、需要什么 scope | Kong 路由配置 |
| L3 服务 | "用户 42 能否操作资源 1001"（归属判定） | 各业务服务自己 |
| L4 平台 | 平台管理操作（用户/角色/租户管理） | lina-core 内置 RBAC |

**注意**：LinaPro 的 RBAC 结果**不进 JWT**（角色是服务端查库判定的）。业务服务默认只依赖"身份"（userId/tenantId），不依赖"平台角色"。确需平台角色的服务，调用 lina-core 的用户接口查询并缓存（TTL ≤ 5min）。

### 3.4 吊销窗口的处理（已知取舍）

业务服务本地验签**感知不到** lina-core 侧的踢下线/登出，最坏有 24h 窗口。策略：

- **普通读操作**：接受窗口，不额外处理
- **敏感写操作**（支付、删除、导出）：调用 lina-core 会话校验接口确认 `tokenId` 仍活跃（Phase 4 提供统一的 `/internal/auth/introspect` 轻量端点）
- **中期**：将 `jwt.expire` 调短（如 2h）+ 依赖 refresh token 轮换，缩小窗口

### 3.5 安全边界与已知风险

| 风险 | 说明 | 缓解 |
|---|---|---|
| HS256 对称密钥 | 持有 secret 的服务**能伪造** token，不只是验证 | 现阶段所有容器均为第一方，可接受；**接入任何第三方服务前必须改 RS256**（改动点：`auth_impl.go` 签发/解析两处 + 配置，外部只发公钥）——列为 Phase 4 决策项 |
| 密钥下发 | secret 出现在多个容器的环境变量里 | 用 compose secrets / K8s Secret 管理；生产密钥绝不进 git |
| 内网横向 | 任一容器被攻破可直连其它容器 | 业务容器不暴露端口；后续按需分网段 / NetworkPolicy |

---

## 4. 服务间调用规范

1. **走内网服务名直连**（`http://svc-b:80`），**禁止绕回公网网关**（省一跳、不消耗用户限流额度）
2. **代表用户调用**：把收到的用户 JWT 原样放入 `Authorization` 转发，被调方照常验签——用户上下文自然传递
3. **后台任务/无用户上下文**：调用方用共享 secret 自签"服务 token"，约定 claims：`{"tokenType": "service", "svc": "svc-a", "exp": <短有效期≤5min>}`；被调方对 `tokenType == "service"` 的请求按服务白名单授权
4. **超时与重试**：所有服务间 HTTP 调用必须设超时（建议 5s）；只对幂等操作重试

---

## 5. 可插拔接入规范（新服务 Checklist）

新增一个服务只做三件事，**不改任何现有服务**：

```
1. 写 Dockerfile（任意技术栈），容器监听内网端口，不对外暴露
2. 实现 JWT 中间件（§3.2 五步约定，各语言模板见 templates/）
3. 在 gateway/kong.yml 追加一段 service 块 + 在 docker-compose.yml 追加一个 service
   → docker compose up -d 生效
```

`gateway/kong.yml` 中的服务块模板：

```yaml
  - name: svc-<name>
    url: http://svc-<name>:80
    routes:
      - name: svc-<name>-route
        paths: ["/api/<name>"]
        strip_path: true
```

**两层插拔的选型指引**（何时用容器、何时用 LinaPro 插件）：

| 场景 | 用哪层 |
|---|---|
| Python/Node AI 服务、GPU 推理、长耗时任务、异构技术栈 | **独立容器**（本方案主路径） |
| Go 写的管理后台 CRUD、审批流、配置页（要复用 RBAC/菜单/代码生成） | **LinaPro 源码插件**（`apps/lina-plugins/` 规范） |
| 轻量、可热插拔的扩展逻辑 | LinaPro WASM 动态插件 |

---

## 6. 网关策略

### Phase 2（起步）：路由 + 限流，验签留给服务

Kong 的 `jwt` 插件靠 `iss`（或指定 claim）匹配 consumer，而 LinaPro 的 token **未确认携带 iss**。起步阶段：

- 网关只做路由 + `rate-limiting`（`limit_by: ip`，或 `limit_by: header` 按 `Authorization` 值近似按 token 限流）
- JWT 验证完全由业务服务承担（反正 L3 必须验）

### Phase 4（硬化）：网关级验签

三选一（届时决策）：

1. 给 lina-core 的签发逻辑加一行 `iss: "lina-core"` claim → Kong `jwt` 插件直接可用（改动最小，**推荐**）
2. Kong `pre-function`/自定义插件做 HS256 验签
3. 换 APISIX（其 `jwt-auth` 对 claim 要求更松）

网关级验签就绪后，限流升级为按 `userId` claim 维度。

---

## 7. 部署形态

### 7.1 目标 docker-compose 结构

```yaml
services:
  gateway:            # 唯一对外端口 18000
    image: kong:3.9   # DB-less，声明式配置 gateway/kong.yml

  lina-core:          # 平台底座
    build: ./vendor/linapro/apps/lina-core   # 或官方镜像 make image
    environment:
      - JWT_SECRET=${JWT_SECRET}             # 与业务服务共享
    extra_hosts: ["host.docker.internal:host-gateway"]
    # DB: host.docker.internal:15433 / platfarm 库（本机共享 PG 实例约定）

  lina-vben:          # 管理台静态资源（或由 lina-core 托管）

  redis:              # lina-core 会话/协调（cluster 模式必须）
    image: redis:7-alpine

  svc-example:        # 业务服务模板（不暴露端口）
    build: ./services/example
    environment:
      - JWT_SECRET=${JWT_SECRET}
```

secret 统一放 `.env`（gitignore），生产换 secrets 管理。

### 7.2 数据库约定（本机开发环境）

遵循本机全局约定——**不新建 postgres 容器**，复用共享实例：

```powershell
docker exec open-mmom-local-postgres psql -U open_mmom -c "CREATE ROLE platfarm LOGIN PASSWORD 'platfarm_local' SUPERUSER;"
docker exec open-mmom-local-postgres psql -U open_mmom -c "CREATE DATABASE platfarm OWNER platfarm;"
```

- lina-core 连 `host.docker.internal:15433/platfarm`（LinaPro 要求 PG 14+，实例为 PG 18，满足）
- 每个业务服务需要存储时**各建自己的 database**（如 `platfarm_svc_a`），禁止跨服务共享表；跨服务数据只能走 API

### 7.3 目录规划

```
E:\work\platfarm\
├── docker-compose.yml        # 平台编排（现有文件演进）
├── .env                      # JWT_SECRET 等（gitignore）
├── gateway/
│   └── kong.yml              # 网关声明式配置（现有文件演进）
├── vendor/linapro/           # LinaPro（submodule 或定期同步 fork）
├── services/                 # 业务服务，一目录一容器
│   └── example/
├── templates/                # 新服务脚手架：JWT 中间件（py/ts/go）+ Dockerfile 模板
└── docs/
    └── architecture.md       # 本文档
```

---

## 8. 分阶段实施路线

### Phase 0 — 底座验证（0.5 天）
- [ ] 在共享 PG 实例创建 `platfarm` 库；lina-core 本地跑通（`make dev`），管理台可登录
- [ ] 抓一个真实 access token，用脚本以共享 secret 验签解码，确认 §3.2 claims 契约
- **产出**：契约确认记录（若与 §3.2 有出入，先修订本文档）

### Phase 1 — 底座容器化（1 天）
- [ ] lina-core 构建镜像（LinaPro 自带 `make image`），接入 compose，连共享 PG + redis
- [ ] lina-vben 构建产物挂到网关 `/admin` 路由
- **验收**：通过 `http://localhost:18000/admin` 登录管理台，全流程走网关

### Phase 2 — 网关整合（0.5 天）
- [ ] 现有 `gateway/kong.yml` 改造：`/admin` + `/api/platform` → lina-core；限流 `limit_by: ip`；**移除演示用 key-auth**（认证统一走 LinaPro JWT）
- **验收**：登录、刷新 token、调平台 API 均经网关成功；限流触发 429

### Phase 3 — 首个业务服务 + 接入模板（1~2 天）
- [ ] `templates/`：Python(FastAPI) + Node + Go 三份 JWT 中间件模板（§3.2 五步）
- [ ] `services/example`：示例服务，`GET /api/example/me` 返回解码出的 userId/tenantId
- [ ] 演练完整插拔流程：加服务 → 加路由 → up -d → 走网关带 token 调通
- **验收**：无 token 401 / 有效 token 200 且身份正确 / 篡改 token 401

### Phase 4 — 硬化（按需，各项独立）
- [ ] 网关级 JWT 验签（§6 三选一，推荐加 iss claim）+ 按 userId 限流
- [ ] lina-core 暴露 `/internal/auth/introspect`（仅内网路由），敏感操作接入
- [ ] `jwt.expire` 调短至 2h，前端接 refresh 轮换
- [ ] 评估 RS256 改造（接第三方前置条件）
- [ ] 服务 token（`tokenType: service`）规范落地 + 被调方白名单
- [ ] 可观测：Kong 访问日志 → 统一日志容器；服务健康检查

---

## 9. 决策记录（ADR 摘要)

| # | 决策 | 理由 | 重新评估时机 |
|---|---|---|---|
| 1 | LinaPro 整体作底座，不拆 auth 模块 | auth 与 GoFrame/bizctx/表结构深耦合；其 access+refresh+吊销+多租户设计比自研成熟 | 若 LinaPro 停止维护 |
| 2 | 容器级插拔为主，LinaPro 插件为辅 | AI 服务多为 Python/GPU，WASM 沙箱跑不了；容器边界隔离故障 | — |
| 3 | HS256 共享密钥起步 | 全部第一方容器，改造成本最低 | 接入第三方服务时 → RS256 |
| 4 | 业务服务本地验签，不做集中 introspection | 零网络开销、auth 不成为热点 | 出现强实时吊销需求时（§3.4） |
| 5 | 网关起步不验签 | Kong jwt 插件依赖 iss claim，LinaPro 默认未带 | Phase 4 加 iss 后升级 |
| 6 | Kong 而非 Traefik | 需要真实限流/后续鉴权插件生态；DB-less 声明式与"可插拔"契合 | 若运维成本过高可评估 APISIX |

---

## 10. 开放问题（Phase 0 需确认）

1. LinaPro token 的 `RegisteredClaims` 实际填充了哪些字段（iss/aud/sub）？——影响 §6 网关验签方案选择
2. lina-core 会话存储在 PG 还是 Redis？——影响 introspect 端点实现与 redis 是否必需
3. lina-vben 是独立静态容器还是由 lina-core 托管？——看 LinaPro `make image` 的产物形态
4. 多租户插件（`linapro-tenant-core`）是否启用？——不启用则全平台 `tenantId=0` 单租户模式，业务服务可忽略租户过滤
