# Platfarm 设计：LinaPro 设计思维 × 容器级可插拔平台

> 项目名：**Platfarm**（CLI：`pctl`，资源前缀：`pf_`）；本地目录暂为 `E:\work\platfarm`

> 版本：v2.0（2026-09-03），**取代** [architecture.md](architecture.md)（v1 = LinaPro 整体作底座方案，保留作参照）
> 定位：不复用 LinaPro 代码，只移植它的设计思维；底座自建、极薄
> 方向确认：auth 底座（用户+认证+粗角色）／文件管理独立能力服务／细粒度权限内嵌各服务

---

## 0. 从 LinaPro 移植的五个设计思维

LinaPro 的插拔单位是**进程内插件**，我们的插拔单位是**容器**。它的五个核心设计在容器粒度上逐一转译：

| LinaPro 的设计 | 它解决什么 | Platfarm 的转译 |
|---|---|---|
| ① `plugin.yaml` 声明式清单 | 插件自描述：路由、菜单、生命周期资源全在清单里，宿主只做同步与校验 | **`service.yaml` 服务清单**：每个服务容器自描述路由挂载/鉴权要求/限流/依赖，网关配置由清单生成 |
| ② `linactl` 聚合工具 | 扫描插件清单，自动聚合进构建，人不手改宿主 | **`pctl` 平台工具**：扫描 `services/*/service.yaml` 生成 kong.yml 与 compose 片段，校验路由冲突 |
| ③ 宿主只持有"稳定表面" | 宿主拥有顶级目录/契约，插件声明挂载点，缺失父级直接拒绝，避免孤儿 | **网关持有路由命名空间**：保留段（`/auth`、`/platform`）不可占用；服务声明挂载路径，冲突即拒绝同步 |
| ④ 治理内置（JWT·吊销·代持·审计） | 身份是框架能力而非业务负担 | **auth 底座**照抄其 token 设计：access/refresh 分离、tokenId 吊销、isImpersonation 代持、tenantId 预留 |
| ⑤ 规范驱动 + 强制 E2E | 每次变更锚定增量规范与 E2E 测试，AI 主导实现、人控方向 | **`specs/` 变更流** + 服务模板自带**契约测试**（401/200/403 三件套），`pctl check` 强制跑通才算接入 |

一句话：**LinaPro 把"可插拔"做成了工程纪律而非口号——清单、工具、稳定面、治理、规范五件套。我们把这套纪律搬到容器边界上。**

---

## 1. 总体架构

```
                        公网
                         │
                ┌────────▼─────────┐
                │   gateway (Kong)  │  唯一对外端口
                │  由 pctl 生成配置│
                └──┬──────┬─────┬──┘
          内网（业务容器零暴露端口）
        ┌──────┤          │         └───────────┐
        │      │          │                     │
┌───────▼──┐ ┌─▼────────┐ ┌▼──────────┐ ┌──────▼─────┐
│  auth     │ │ svc-file  │ │ svc-ai-x  │ │  svc-...    │
│  底座容器  │ │ 文件服务   │ │ AI服务    │ │  (任意栈)   │
│ 用户/JWT/  │ │ 元数据+   │ │ (Python)  │ └────────────┘
│ 粗角色/吊销│ │ 归属判定  │ └───────────┘
└─────┬─────┘ └──┬───┬───┘   每个服务一个 service.yaml
      │          │   │
┌─────▼─────┐    │ ┌─▼──────┐
│ PostgreSQL │◄───┘ │ MinIO  │ (对象存储，svc-file 专属后端)
│ 每服务一库  │      └────────┘
└───────────┘
┌───────────┐
│   Redis    │ auth 吊销黑名单 / 会话
└───────────┘
```

**分层定义**：

| 层 | 成员 | 纪律 |
|---|---|---|
| 入口层 | gateway | 配置只能由 `pctl sync` 生成，禁止手改 |
| 底座层 | auth（唯一底座） | 只放"身份"；任何业务概念不得进入 |
| 能力层 | svc-file 及未来的通用能力（通知、搜索…） | 与业务服务同级、同接入方式，无特权 |
| 业务层 | svc-*（AI 服务为主） | 任意技术栈；细粒度权限自判；数据自持 |

---

## 2. 核心契约一：身份（Identity Claims）

> 设计照抄 LinaPro `auth.Claims`（经源码验证的成熟结构），去掉平台用不上的字段版本化保留。

### 2.1 JWT Claims 规范

```jsonc
{
  "tokenId":   "uuid",      // 唯一标识，吊销依据
  "tokenType": "access",    // access | refresh | service，验签方必须校验
  "clientType":"web",       // web | mobile | desktop | api
  "userId":    42,
  "username":  "alice",
  "role":      "user",      // 平台粗角色：admin | user（仅此两档，起步够用）
  "tenantId":  0,           // 预留，0 = 单租户模式
  "isImpersonation": false, // 管理员代持
  "actingUserId":    0,
  "iss": "pf-auth",         // 固定签发方（网关级验签依赖此字段，吸取 v1 教训）
  "exp": 0, "iat": 0
}
```

- 算法：HS256 共享密钥（`.env` → `JWT_SECRET`）；接第三方前升级 RS256（auth 只需换签名两行 + 公钥分发）
- 有效期：access **2h**，refresh **30d**（比 LinaPro 默认 24h 短，缩小吊销窗口）
- **service token**：服务间后台调用用 `tokenType: "service"` + `svc: "<caller-id>"` + exp ≤ 5min，被调方按 `service.yaml` 中的调用方白名单放行

### 2.2 服务接入约定（验签五步 + 追踪一步）

1. 取 `Authorization: Bearer <jwt>`
2. HS256 验签 + `exp` 检查（密钥来自环境变量）
3. 校验 `tokenType`（用户请求必须 `access`；内部调用必须 `service` 且调用方在白名单）
4. `userId/tenantId/role` 即身份；多租户开启后所有查询强制带 `tenantId`
5. 失败一律 401，无降级
6. **透传 `X-Request-Id`**：网关生成（correlation-id 插件），服务写入每条日志、调用下游时原样携带——跨容器排障的关联键（非鉴权步骤，见附录 B）

模板落地于 `templates/middleware/`（`fastapi_jwt.py` / `express_jwt.ts` / `gin_jwt.go`），新服务复制即用。

### 2.3 权限分层（定稿）

| 层 | 判什么 | 实现 |
|---|---|---|
| L1 网关 | 签名有效、未过期、`iss` 正确 | Kong jwt 插件（claims 带 iss，v1 的阻塞点已消除） |
| L2 路由 | 该路径是否免登录 / 是否仅 admin | `service.yaml` 的 `public_routes` / `admin_routes` 声明，pctl 生成对应网关规则 |
| L3 服务 | 资源归属与操作权（"42 能否删文件 1001"） | 各服务内嵌，查自己的表 |
| L4 平台 | 用户/角色管理操作 | auth 底座内部（仅 admin） |

**不设中央权限服务**。触发重新评估的信号：出现跨服务共享/协作模型（团队空间、跨服务资源授权）→ 届时引入 Casbin/SpiceDB 容器，接入方式与普通能力服务相同。

---

## 3. 核心契约二：服务清单（service.yaml）

> `plugin.yaml` 的容器级转译——服务自描述，平台工具消费。**这是本设计的可插拔核心。**

```yaml
# services/svc-file/service.yaml
id: svc-file                 # 全平台唯一，亦是容器名/内网域名
version: 0.1.0
lang: go                     # 仅信息用途，平台不限制技术栈

mount:
  path: /api/file            # 网关挂载点；保留段 /auth /platform 禁止占用
  strip_path: true
  public_routes:             # 免登录路由（相对挂载点）
    - GET /public/*
  admin_routes:              # 仅 admin 角色（L2 由网关判 role claim）
    - DELETE /purge

auth:
  required: true
  accept_service_tokens:     # 允许哪些服务用 service token 调我；空 = 不允许
    - svc-ai-x

limits:
  rate_per_minute: 120       # 生成网关 rate-limiting 配置

docs:
  openapi: ./openapi.yaml    # 接口契约（借鉴 smart-park/LinaPro 契约先行）；pctl sync 聚合到网关 /docs

grpc:                        # 可选：服务间 gRPC（附录 E）
  port: 9090                 # 仅内网，不经网关
  proto: contracts/svc-file/v1/  # 契约位置（buf 统一管理与 codegen）

runtime:
  port: 8080                 # 容器内监听端口
  health: /healthz           # 健康检查路径
  env:                       # 需要平台注入的环境变量（值来自 .env）
    - JWT_SECRET
    - S3_ENDPOINT

data:
  database: pf_svc_file  # 声明自有库；跨服务共享表被 pctl check 拒绝
```

**纪律（pctl check 强制执行）**：

1. `mount.path` 不得与既有服务冲突、不得占用保留段（对应 LinaPro"缺失父级即拒绝"）
2. 声明 `database` 的服务只能连自己的库；跨服务数据必须走 API
3. 契约测试三件套必须存在且通过（见 §6）
4. 未声明的环境变量不注入——服务依赖显式化

---

## 4. 底座设计：auth（唯一的平台服务）

**规模控制目标：单一 Go 容器，核心逻辑 ≤ 1000 行，永不吸收业务概念。**

### 4.1 存储

- PostgreSQL `pf_auth` 库：`users`（id/username/password_hash/role/status/created_at）、`refresh_tokens`（tokenId/userId/exp/revoked）、`login_audit`（可选）
- Redis：吊销黑名单（`revoked:<tokenId>`，TTL = 剩余有效期）+ 在线会话计数

### 4.2 API 面（稳定表面，网关挂 `/auth`）

| 端点 | 说明 |
|---|---|
| `POST /auth/login` | 用户名密码 → access + refresh |
| `POST /auth/refresh` | refresh → 新 access（轮换 refresh） |
| `POST /auth/logout` | 吊销当前 tokenId（进 Redis 黑名单） |
| `GET  /auth/me` | 解码当前身份（前端用） |
| `GET/POST/PATCH /auth/users*` | 用户 CRUD（仅 admin；起步没有 UI，用 API/脚本管理） |
| `POST /internal/auth/introspect` | **仅内网路由**：tokenId 是否活跃；敏感操作用（消除本地验签的吊销窗口） |
| `POST /internal/auth/impersonate` | admin 代持签发（isImpersonation=true），排障用 |

### 4.3 明确不做的事

- ❌ 菜单/组织/字典/任务调度（业务或能力层的事）
- ❌ 细粒度权限存储（L3 归各服务）
- ❌ 管理台 UI（起步 API 管理；将来需要 UI 时优先评估挂 Casdoor 或薄前端，而非增厚 auth）

---

## 5. 能力服务示例：svc-file（文件管理）

与业务服务**完全同级**，按 §3 清单接入，无任何特权——它同时是"能力服务怎么写"的参考实现。

- **元数据**：`pf_svc_file` 库，`files`（id/owner_id/tenant_id/visibility/size/mime/storage_key/created_at）
- **存储后端**：MinIO 容器（S3 协议），svc-file 只管元数据 + 归属判定 + 预签名 URL，不代理文件字节流
- **权限（L3 示范）**：`visibility = private | public | shared`；private 仅 owner；上传/删除必须 `owner_id == claims.userId`；admin 可越权（`role == "admin"`）
- **对外接口**：`POST /api/file/upload-url`（预签名直传）、`GET /api/file/:id/download-url`、`DELETE /api/file/:id`、`GET /api/file/mine`

---

## 6. 规范驱动工作流（LinaPro openspec 的轻量版）

```
specs/
├── changes/                  # 每个变更一个目录：NNN-短名/
│   └── 001-add-svc-file/
│       ├── proposal.md       # 动机、影响面、service.yaml 差异
│       └── tasks.md          # 实施清单（AI 执行的锚点）
└── archive/                  # 完成后归档
```

**流程**：探索 → 提案（proposal.md）→ 实现（AI 主导，锚定 tasks.md）→ 审查（pctl check + 契约测试）→ 归档。

**强制契约测试（每个服务模板自带，pctl check 执行）**：

1. 无 token → 401
2. 有效 token → 200 且返回身份与 claims 一致
3. 篡改/过期 token → 401；非 admin 访问 admin_routes → 403

这是 LinaPro"强制 E2E"纪律的最小可行移植：**测的不是业务，是契约**——保证任何 AI 生成的服务都没有绕开平台约定。

---

## 7. 平台工具：pctl

> `linactl` 的容器级对应物。一个单文件 CLI（Go 或 TS 均可），平台的"纪律执行器"。

| 命令 | 行为 |
|---|---|
| `pctl new <id> --lang py\|go\|ts` | 从 `templates/` 生成服务骨架：Dockerfile + JWT 中间件 + service.yaml + 契约测试 + healthz |
| `pctl sync` | 扫描 `services/*/service.yaml` → 生成 `gateway/kong.yml` + `docker-compose.services.yml`；聚合各服务 openapi.yaml → 网关 `/docs`；路由冲突/保留段占用 → 报错退出 |
| `pctl check [id]` | 清单校验 + 起容器跑契约测试三件套 |
| `pctl list` | 平台服务清单总览（id/挂载点/版本/健康状态） |

**AI 接入闭环**（平台的最终目的）：

```
AI 拿到需求 → pctl new svc-x → 在骨架内写业务 → pctl sync && docker compose up -d
→ pctl check svc-x 全绿 → 服务上线，全程零人工改平台文件
```

---

## 8. 目录结构

```
E:\work\platfarm\
├── docker-compose.yml            # 基座：gateway/auth/redis/minio（手维护）
├── docker-compose.services.yml   # 业务服务编排（pctl sync 生成，勿手改）
├── .env                          # JWT_SECRET 等（gitignore）
├── gateway/
│   └── kong.yml                  # pctl sync 生成，勿手改
├── platform/
│   └── auth/                     # 底座源码（Go）
├── contracts/                    # 服务间 gRPC 契约（buf.yaml + <svc>/v1/*.proto）
├── services/                     # 能力+业务服务，一目录一容器
│   └── svc-file/
│       ├── service.yaml
│       ├── Dockerfile
│       ├── src/...
│       └── tests/contract/       # 契约测试三件套
├── templates/                    # pctl new 的骨架（py/go/ts）
├── tools/pctl/                # 平台 CLI
├── specs/                        # 变更规范流（§6）
└── docs/
    ├── architecture.md           # v1（已废弃，留档）
    └── architecture-v2.md        # 本文档
```

---

## 9. 实施路线

### Phase 1 — 底座与契约（2~3 天）
- [ ] `platform/auth`：登录/签发/刷新/吊销/me/users + Redis 黑名单（照 §2 claims 实现，含 iss）
- [ ] `templates/`：三语言 JWT 中间件 + 服务骨架 + 契约测试三件套
- [ ] 基座 compose：gateway + auth + redis 跑通；共享 PG 实例建 `pf_auth` 库
- **验收**：curl 走网关完成 login → me → logout → 旧 token 调 introspect 显示已吊销

### Phase 2 — pctl 最小版（1~2 天）
- [ ] `new` / `sync` / `check` 三命令；kong.yml 从 service.yaml 生成
- **验收**：`pctl new svc-demo --lang py` → sync → up → check 全绿，全程未手改任何平台文件

### Phase 3 — svc-file 能力服务 + gRPC 契约基建（2~3 天）
- [ ] MinIO 容器 + svc-file（§5），作为能力服务参考实现
- [ ] `contracts/` + buf 工具链：模板产出 proto 骨架与 Go/Python 双语言桩代码；svc-file 暴露一个内网 gRPC 接口作为参考（附录 E 约定）
- **验收**：契约三件套 + 归属判定（他人 private 文件 → 403）

### Phase 4 — 网关硬化与增强（按需）
- [ ] Kong jwt 插件启用（iss 已具备）→ L1 网关验签 + 按 userId 限流
- [ ] admin_routes 的 L2 role 检查落到网关
- [ ] 敏感操作接 introspect
- [ ] 日志管道：各服务 JSON 日志 → stdout → Vector/Filebeat 收集 → **Loki（推荐，轻）或 ELK**，按 `X-Request-Id` 关联全链路（附录 B）
- [ ] 若性能排障需要 span 级耗时瀑布：OTel SDK + Jaeger/Tempo（日志管道的升级，非替代）
- [ ] 评估 RS256（接第三方前置）/ Casdoor UI（需要用户管理界面时）
- [ ] 响应加密：网关加密插件 + auth 下发 dataKey + 前端解密 SDK + `pctl decrypt`（附录 F；依赖 pctl sync 就绪）
- [ ] SDK 目录化：`sdk/<lang>/` 收敛验签中间件唯一实现，`pctl new` 生成期 vendor + service.yaml 记 sdk_version，`check` 检测漂移；插件市场阶段升级为真实包发布（pypi/crates/packagist/go module）

### Phase 5 — 第三方登录（1~2 天，提案：specs/changes/001）
- [ ] auth：`external_identities` 表 + `/internal/auth/external-login` + `/auth/bind-external`
- [ ] svc-oauth 骨架 + 首个 provider（GitHub 最简）+ 一次性兑换码
- [ ] 微信 provider（code2session / unionid）按需

### Phase 6 — 第三方插件体系 MVP（4~6 天，提案：specs/changes/002）
- [ ] **RS256 + JWKS**（硬前置，独立可先做）：auth 换签发 + Kong 公钥验签 + 服务模板改 JWKS 验签
- [ ] 一插件一网络：pctl sync 生成 networks + gateway 多宿主
- [ ] plugin-pg 独立实例 + 安装时自动开号（REVOKE CONNECT FROM PUBLIC）
- [ ] `pctl install/enable/disable/uninstall/upgrade` + 安装闸门流水线
- [ ] 插件 service token（client_credentials + scopes）+ 网关 calls 放行
- [ ] svc-console 最小版（列表/安装/启停/审计）

### Phase 7 — 插件市场（按需）
设计定稿见 [docs/features/plugin-marketplace.md](features/plugin-marketplace.md)（OCI 分发 + git 索引 + cosign 供应链 + 审查管线自动化，M1~M4 切分），实施时转 specs/changes/003 提案。

---

## 10. 决策记录

| # | 决策 | 理由 | 重估时机 |
|---|---|---|---|
| 1 | 弃 LinaPro 代码，移植其设计纪律 | 其重心（Go 全栈单体+进程内插件）与我们（异构容器）错位；其清单/工具/治理思维与栈无关，可平移 | LinaPro 生态爆发且我们需要其管理台时 |
| 2 | 底座唯一且只含身份 | 底座越薄插拔越纯；防止"小 LinaPro 化"（单体底座不断吸业务） | — |
| 3 | 文件管理为平级能力服务 | 负载特征独立；兼作接入规范的参考实现（对标 Supabase Storage/GoTrue 分离） | — |
| 4 | 权限不设中央服务 | L3 归属判定天然属于资源所在服务 | 出现跨服务协作/共享模型 |
| 5 | service.yaml + pctl 为可插拔核心 | 声明式清单+工具校验是 LinaPro 可插拔的本质，比"文档约定"强制得多 | — |
| 6 | claims 含 iss、access 2h | 消除 v1 网关验签阻塞点；缩吊销窗口 | — |
| 7 | HS256 起步 | 全第一方容器；RS256 升级路径已预留 | 接第三方服务前 |
| 8 | 项目名 Platfarm、CLI `pctl`、资源前缀 `pf_` | 直白易记；已知代价：项目名与 Docker/Kong 两个实现技术耦合 | 网关更换（APISIX）、迁移 K8s 或对外开源时 |
| 9 | **双协议**：对外 REST（经网关），服务间可选 gRPC（内网直连） | 流式/推送场景（订单推送等）需要 streaming 语义；契约集中 `contracts/` + buf 统一 codegen 把异构接入摩擦降到最低；对外保住 curl 可调试性。注意：鉴权仍是本地验签，**禁止**因上 gRPC 把鉴权改为每请求调 auth | 扇出/解耦推送需求成熟时评估 MQ（NATS/Redis Streams） |
| 10 | 可观测性日志先行：JSON 日志 + X-Request-Id → Loki/ELK | 覆盖 80% 排障需求，成本远低于全员埋 OTel；追踪是升级路径非替代 | 需要 span 级耗时分析时加 OTel |
| 11 | 响应加密在网关层实现，密钥 HKDF 从 tokenId 派生（附录 F） | 服务零感知保住插拔纯度；零存储、会话隔离、随 token 生命周期自动轮换；定位是反爬/提高逆向成本，非密码学保密（密钥终在前端） | 流式接口需要加密时、或出现请求签名/防重放需求时 |
| 12 | 第三方登录：OAuth 舞步在 svc-oauth，auth 只加 external-login 兑换端点（附录 G） | auth 底座不吸第三方 SDK；加第 N 个提供方零平台改动；验证与兑换职责分离（LinaPro ExternalLoginInput 模式） | — |
| 13 | 第三方插件沙箱 = 容器 + 一插件一网络 + 独立 plugin-pg，不做 WASM 进程内插件（附录 H） | 容器即沙箱：任意语言/GPU 依赖、TCP 层隔离平台数据；WASM 跑不了重型 AI 负载 | 插件密度极高、容器开销成为瓶颈时 |
| 14 | RS256 + JWKS 提前至第三方接入前置（兑现 #7 触发条件） | 第三方不能持有对称密钥；JWKS 后 JWT_SECRET 概念整体退役，第一方也受益 | — |
| 15 | 插件管理后台 svc-console = pctl 的 Web 外壳，全平台唯一持有 docker socket | 文件仍是唯一真相源；最高权限组件收敛为一个第一方 admin-only 服务 | — |
| 16 | 插件必须无状态；状态出口 = 自库 / Redis 租约 | 多副本扩缩容的前提；leader 选举/定时任务防重跑用 Redis lease（mmom Coordinator 实证） | — |
| 17 | 服务模板语言优先级 Go > Rust > Python > PHP，`pctl new` 默认 go | 基础能力服务重性能与长期稳定；py 留给 LLM/IO 密集与快速迭代；php 为极简模板（零 composer 依赖）服务简单接口与存量团队；语言按容器粒度可逆 | — |

---

## 附录（借鉴 smart-park 工程实践，评估记录见对话 2026-09-04）

### A. 统一主体模型（人 / 服务 / 设备）

用户、内部服务、未来的 IoT 设备/Agent 都是 **principal**，共用同一套 token 机制，仅 `tokenType` 不同（`access` / `service` / 将来 `device`）。新增主体类型 = 扩展 claims + auth 增加签发路径，**不发明新认证机制**（smart-park 的设备网关 `X-Device-Id` 印证此模型）。

### B. 可观测性设计：日志先行

**分层认知**：集中日志（回答"发生了什么"）≠ 分布式追踪（回答"请求穿过哪些服务、各耗时多少"）。桥梁是 `X-Request-Id`。

```
网关 correlation-id 插件生成 X-Request-Id
  → 服务把它写进每条 JSON 日志（stdout）
  → 服务间调用原样透传
  → Vector/Filebeat 收集容器 stdout → Loki（推荐）或 ELK
  → 按 requestId 搜索 = 拉出整条链路的日志时序（"穷人版全链路追踪"）
```

**日志约定（进服务模板）**：stdout 输出单行 JSON，至少含 `ts` / `level` / `service` / `requestId` / `msg`；错误必须带 `requestId`。
**升级路径**：需要 span 级耗时瀑布时，加 OTel SDK + Jaeger/Tempo；日志管道保留不动。

### C. 部署分级路线（何时换形态）

| 规模 | 形态 | 触发信号 |
|---|---|---|
| 起步 | 单机 Docker Compose（现状） | — |
| 中型 | 双机主备 + rate-limiting policy 改 redis + auth 吊销黑名单改 Redis | 单机资源吃紧或需要高可用 |
| 大型 | Kubernetes（Ingress/Gateway API 替代 Kong DB-less 或 Kong Ingress Controller） | 服务数 >20、需要弹性伸缩/滚动发布 |

原则：**不为想象中的规模提前换形态**；service.yaml 契约在三种形态下不变，pctl 换生成目标即可。

### D. 写敏感服务 checklist（支付/扣费/删除类服务模板附录）

- [ ] 幂等键：写接口接受 `Idempotency-Key`，重复请求返回首次结果
- [ ] 并发互斥：同一资源的并发写用数据库唯一约束兜底（分布式锁仅作性能优化，不作正确性依据）
- [ ] 对账思维：涉及外部资金/配额的操作留存本地流水，支持与对方账单核对单边账
- [ ] 敏感操作调用 auth `introspect` 消除吊销窗口（§3.4 v1 / 本文 §4.2）

### E. 服务间 gRPC 约定（双协议）

**定位**：对外一律 REST（经网关，保住 curl 可调试性）；服务间可选 gRPC（内网直连，不经网关），流式/推送场景（如订单推送）优先 gRPC streaming。

1. **端口约定**：HTTP `8080`（网关面）+ gRPC `9090`（仅内网）；`service.yaml` 的 `grpc` 块声明后 pctl check 校验 proto 存在
2. **契约集中**：`contracts/<svc>/v1/*.proto`，buf 管理（lint + breaking-change 检测 + 多语言 codegen）；服务目录不各自散放 proto
3. **身份传递**：六步约定不变，token 放 gRPC metadata `authorization: Bearer <jwt>`（用户上下文透传用户 JWT；后台调用用 service token）
4. **鉴权红线**：验签仍是各服务本地 HMAC 解码（微秒级、零网络）。禁止把鉴权做成每请求调 auth 的 RPC——那会重造中心瓶颈，gRPC 再快也是负优化
5. **版本纪律**：proto 包名带 `v1`；破坏性变更开 `v2` 并行，由 buf breaking 在 CI 拦截
6. **升级路径**：高扇出事件推送（一对多、削峰、重放）超出 gRPC streaming 舒适区时，引入 NATS/Redis Streams 作为普通基础容器，proto 定义的消息体可复用

### F. 响应加密设计（网关层数据中间件）

**威胁模型（先说清楚防什么）**：TLS 已覆盖传输加密；本层防的是爬虫/薅接口脚本、抓包工具直读业务数据、中间盒查看内容。解密密钥最终在前端，**本质是提高逆向成本，不是密码学保密**——按"对抗成本"设计，不按"绝对安全"承诺。

**架构**：加密在网关插件实现，业务服务完全无感知（照旧返回明文 JSON），前端 fetch SDK 自动解密，业务前端代码也无感知。

```
svc（明文） → gateway 加密插件（Kong 自定义插件，Lua/Go） → 密文 → 前端 SDK 解密
```

**密钥设计（零存储，方案核心）**：

```
dataKey = HKDF(MASTER_DATA_SECRET, salt=tokenId)
```

- 登录时：auth 派生 dataKey 随 login 响应下发（走 TLS），前端存内存
- 响应时：网关从 JWT 取 tokenId 现场派生同一把 key，AES-256-GCM 加密响应体
- 免费收益：每会话一密；logout/token 吊销 = 密钥作废；网关零存储纯计算
- `MASTER_DATA_SECRET` 仅存在于 auth 与网关，与 `JWT_SECRET` 分离

**线格式**（响应头 `X-PF-Encrypted: v1`）：

```jsonc
{ "v": 1, "alg": "A256GCM", "kid": "<tokenId>", "iv": "<b64>", "ct": "<b64，GCM tag 附尾>" }
```

**范围声明**（service.yaml，默认关闭，敏感数据才开）：

```yaml
crypto:
  encrypt_response: true
  exempt: ["GET /public/*"]
```

**强制豁免三类**：公开路由（无 token 无 key）、SSE/流式推送（逐 chunk 加密暂不支持）、文件下载（MinIO 预签名 URL 不过网关）。

**调试配套（必须同步交付）**：
- `pctl decrypt --token <jwt> <ciphertext>` 一键解密排障
- 开发环境 `PF_CRYPTO=off` 全局开关；契约测试跑明文

**扩展位**：同一套 dataKey 可复用于请求体加密与请求签名/防重放，需求出现再加。
**排期**：Phase 4 之后（依赖 pctl sync 生成插件配置）；主要工作量为 Kong 自定义插件（body transform + HKDF，约 2~3 天）。

### G. 第三方登录（svc-oauth 能力服务）

**原则**：auth 底座不集成任何第三方 SDK。OAuth 舞步（跳转/回调/code 换 profile）全部在 svc-oauth 能力服务；auth 只加一个"外部身份兑换"端点——外部身份的**验证**是调用方的责任，auth 只做**兑换**（设计源自 LinaPro `ExternalLoginInput`）。

**auth 的一次性改动**（之后加任何提供方不再动 auth）：

```sql
CREATE TABLE external_identities (
  provider    TEXT NOT NULL,      -- wechat / github / google...
  external_id TEXT NOT NULL,      -- 对方体系唯一 ID（openid/unionid/sub）
  user_id     INT  NOT NULL REFERENCES users(id),
  PRIMARY KEY (provider, external_id)
);
```

- `POST /internal/auth/external-login`：仅内网 + 白名单登录服务的 service token；`{provider, externalId, displayName}` → 已绑发 token 对 / 未绑按策略自动建号
- `POST /auth/bind-external`：已登录用户绑定新的第三方身份

**svc-oauth**（按普通服务插拔）：

- `GET /api/oauth/{provider}/authorize`（公开）→ 302 第三方，带 `state` 防 CSRF + PKCE
- `GET /api/oauth/{provider}/callback`（公开）→ 校验 state → code 换 profile → service token 调 external-login
- **交付前端**：禁止把 JWT 拼进重定向 URL（浏览器历史/Referer 泄露）；发 30 秒一次性兑换码，落地页 `POST /api/oauth/exchange` 换 token 对（LinaPro `auth_pre_token` 思路）
- 微信生态差异封在 provider 模块内：小程序 `code2session`（无重定向舞步）；多端并存用 unionid 作 externalId

### H. 第三方插件体系（容器即沙箱）

**信任模型**：第一方信代码，第三方只信契约——插件能碰到的每样东西（网络、库、token、配置）都是平台按清单发放的最小集合。

**H.1 清单（plugin.yaml = service.yaml 超集）**

```yaml
id: svc-plugin-translate
trust: third-party            # pctl 据此套沙箱策略
source:
  type: image                 # 第一方 build: / 第三方 image:
  image: ghcr.io/vendor/translate:1.2.0
  digest: sha256:ab12...      # 按 digest 锁定，防 tag 漂移
permissions:                  # 显式最小权限
  needs_identity: true        # 需要验用户身份 → 发 JWKS 公钥地址
  calls: ["svc-file:read"]    # 允许经网关调用的平台能力
  egress: ["api.deepl.com"]   # 出网白名单
resources: { memory: 512m, cpus: 0.5 }
```

**H.2 硬前置：RS256 + JWKS**（ADR #7 的触发条件正式兑现）
auth 改 RS256 签发 + 暴露 `GET /auth/.well-known/jwks.json`；全员（含第一方）用公钥验签，`JWT_SECRET` 概念退役——谁都无法伪造 token。

**H.3 数据隔离（双层保险）**

- 权限层：安装时开号 `CREATE ROLE pf_plugin_x LOGIN ... NOSUPERUSER NOCREATEDB NOCREATEROLE` + 独立库；所有 pf_* 库建库时统一 `REVOKE CONNECT ... FROM PUBLIC`（Postgres 默认 CONNECT 对 PUBLIC 开放，必须显式收回）
- 网络层（真保险）：插件库放**独立 plugin-pg 实例**挂插件网络；平台 PG 只在 core-net——插件到平台数据 TCP 层不可达
- 凭据交付：随机密码写 `.env.plugins/<id>.env`（gitignore，仅注入该插件容器）

**H.4 网络：一插件一网络，网关多宿主**

```yaml
services:
  svc-plugin-x:
    image: ghcr.io/vendor/x@sha256:...
    networks: [net-plugin-x]                 # 只有这一张网
    env_file: .env.plugins/svc-plugin-x.env
    mem_limit: 512m
    read_only: true
    security_opt: ["no-new-privileges:true"]
  gateway:
    networks: [core-net, net-plugin-x, ...]  # pctl sync 维护
networks:
  net-plugin-x: { internal: true }           # 默认断外网
```

插件 DNS 世界里只有 gateway 和自己的 plugin-pg；插件间互不可见；调平台能力必须经网关按 `permissions.calls` 放行。**出网控制的诚实边界**：compose 只能全断/全通，按域白名单需在插件网内加 egress 代理容器（squid/envoy），完整策略留给 K8s NetworkPolicy 阶段。

**H.5 上下文：双 token 约定（On-Behalf-Of）**

| 场景 | 约定 |
|---|---|
| 用户请求进插件 | 网关透传用户 JWT，插件 JWKS 验签取 userId/tenantId/role（与第一方同一约定） |
| 插件代用户调平台 | `Authorization: Bearer <service token>`（我是谁/什么 scope）+ `X-PF-User-Token: <用户JWT>`（我代表谁）；被调方两个都验 |
| 插件后台任务 | 仅 service token（client_credentials 换短时效 token，scopes = 清单 permissions） |
| 插件自身配置 | `.env.plugins/<id>.env`，改后重启生效——不引入配置中心（与 ADR 决策一致） |

**H.6 管理后台：svc-console = pctl 的 Web 外壳**

所有写操作经 pctl 落文件，UI 不存在第二真相源。能力：插件列表/状态/资源、安装（展示权限清单如手机装 App）、启停、升级（**权限 diff 高亮**）、配置编辑、日志只读投影、操作审计（pf_console 库）。console 是全平台唯一持有 docker socket 的容器——必须第一方、core-net、全路由 admin-only。

**安装闸门（顺序固定，失败自动回滚）**：校验清单 → 锁 digest → 建库开号 → 建网 → staged 启动 → 契约测试三件套 → 管理员确认权限 → enable 挂路由。

生命周期：`pctl install / enable / disable / uninstall [--purge] / upgrade`（upgrade 重跑契约测试 + 权限 diff）。

**H.7 多副本与无状态纪律**

`docker compose up -d --scale svc-plugin-x=3` 直接兼容：副本共享服务名 DNS（Kong 轮询）、共享 DATABASE_URL、无端口冲突、沙箱边界与副本数无关。已知行为：Docker DNS 不做健康剔除，缩容瞬间可能短暂 5xx（Kong 连接失败自动重试缓解）；compose 副本限单机，跨机走 K8s（映射：插件网络→Namespace+NetworkPolicy，服务名→Service，清单语义不变）。

**插件契约铁律：必须无状态**。内存不存会话/计数/缓存。状态出口：

| 需求 | 去处 |
|---|---|
| 持久数据 | 自己的 plugin-pg 库 |
| 缓存 / 分布式锁 / leader 选举 / 定时任务防重跑 | 平台 bundled-redis 的**租约**（Redis lease，mmom Coordinator 模式），或清单声明自带 Redis |
| 会话 | 不存在会话，一切状态在 token 里 |

**决策**：不做进程内/WASM 插件（LinaPro 路线）——容器 + 网络分段就是沙箱，支持任意语言与 GPU/重依赖，隔离更强；代价是每插件最小一个容器（约几十 MB），可接受。

**实现状态注记（2026-09-04，specs/changes/002 Stage 1~4 已落地）**：H.2~H.7 均已实现并通过恶意插件演习验收，三处降级：① `permissions.calls` 放行在被调服务侧（网关级需 Kong 自定义插件，Phase 6 余项）；② install 闸门为 staged-lite（先挂路由、测试失败自动 disable）；③ `egress` 白名单未执行（internal 网络全断出网）。svc-console（H.6）未实现。
