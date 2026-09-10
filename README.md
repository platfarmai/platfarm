# PlatFarm

**大模型时代的后端开发新范式** —— AI 负责生产代码，平台负责工程纪律。

PlatFarm 是基于容器服务的**融合开发模式**：一个 API 网关对外、每个服务一个容器；Go / Rust / Python / PHP 异构服务、第三方沙箱插件与 AI 生成的代码，在同一套契约（JWT 身份 × service.yaml 清单 × 契约测试）下融合交付。`pctl` 一条命令走完从"AI 产出"到"安全上线"，全程不改平台文件。

## 1. 框架目的

大模型让"写一个服务"只要几分钟，但把它**安全地**放进系统往往要几天：鉴权怎么接、路由怎么配、数据库怎么隔离、第三方代码敢不敢跑。PlatFarm 把这些做成平台纪律，让服务农场里的每一棵"AI 庄稼"生而合规：

- **AI 极速生产**：`pctl new` 四语言骨架（验签/OBO/契约测试内置），AI 只需填业务逻辑
- **纪律自动执行**：路由冲突、库隔离、权限声明由工具校验，契约测试三件套是上线闸门
- **异构融合**：性能敏感用 Go/Rust、AI 生态用 Python、存量团队用 PHP，同一契约互调无感
- **信任分级**：第一方信代码，第三方插件只信契约（digest 锁定 + 网络沙箱 + 最小权限发放）

**三个设计信条**：

1. **底座极薄**——平台唯一的"底座"是 auth（用户 + RS256 JWT 签发/吊销 + 粗角色，Go ≤1000 行），任何业务概念不得进入；文件管理、登录提供方等都是平级的"能力服务"
2. **契约即接口**——服务接入平台的全部负担 = 会验一个 JWT；平台管服务的全部依据 = 一份 `service.yaml` 清单。清单之外无口头约定
3. **文件是唯一真相源**——网关配置、编排、权限声明全部由 `pctl` 从清单生成并进 git；不存在"后台点一下就变了"的运行态

**信任分级**：第一方服务信代码（源码在 `services/`）；第三方插件只信契约——镜像按 digest 锁定，跑在专属网络沙箱里，能碰到的每样东西（网络、数据库、token、配置）都是按清单发放的最小集合。

完整设计与决策记录（16 条 ADR）：[docs/architecture-v2.md](docs/architecture-v2.md)

## 2. 上手流程

### 2.1 一键开箱（只需 Docker 的干净机器）

```bash
git clone <repo> platfarm && cd platfarm
cp .env.example .env                          # 默认指向捆绑数据库
docker compose --profile bundled-db up -d --build
```

验证（开发种子账号 `admin/admin123`、`alice/user123`）：

```bash
# 登录拿 token
curl -s -X POST http://localhost:18000/auth/login \
  -H "Content-Type: application/json" -d '{"Username":"admin","Password":"admin123"}'

# 带 token 调业务服务；无 token 会得到 401（网关层拦截）
curl -s http://localhost:18000/api/demo/me -H "Authorization: Bearer <accessToken>"

# 公开路由无需 token
curl -s http://localhost:18000/api/demo/public/ping
```

已有 PostgreSQL（宿主机实例 / AWS RDS）：改 `.env` 的 `DATABASE_URL`（三种模式见 [.env.example](.env.example)），去掉 `--profile bundled-db`。

### 2.2 构建平台 CLI（首次一次）

```bash
cd tools/pctl && go build -o pctl.exe . && cd ../..
```

### 2.3 新增一个第一方服务（30 秒）

```bash
./tools/pctl/pctl.exe new svc-hello             # 默认 Go(gin)；--lang rust|py|php 可选（优先级见 ADR #17）
./tools/pctl/pctl.exe sync                      # 清单 → 网关配置 + 编排
docker compose up -d --build && docker compose restart gateway
./tools/pctl/pctl.exe check --e2e               # 契约测试三件套闸门
```

详细教程：[docs/adding-a-service.md](docs/adding-a-service.md)

### 2.4 试玩第三方登录（mock provider，无需外部账号）

```bash
curl -sL http://localhost:18000/api/oauth/mock/authorize   # → {"exchangeCode": "..."}
curl -s -X POST http://localhost:18000/api/oauth/exchange \
  -H "Content-Type: application/json" -d '{"code":"<exchangeCode>"}'  # → token 对
```

接真实 GitHub：`.env` 填 `GITHUB_CLIENT_ID/SECRET`，入口换成 `/api/oauth/github/authorize`。

### 2.5 安装第三方插件（沙箱）

```bash
./tools/pctl/pctl.exe install ./path/to/plugin/   # 目录含 plugin.yaml（镜像交付）
# 自动完成：校验 → 专属网络 → 插件库开号 → 发凭据 → 起容器 → 契约测试闸门（失败自动 disable）
./tools/pctl/pctl.exe disable|enable|uninstall svc-plugin-x [--purge]
```

沙箱保证：插件的 DNS 里只有网关和自己的 plugin-pg；平台服务与平台数据库在 TCP 层不可达（实测见 specs/changes/002 验收）。

### 2.6 管理控制台

随基座自动启动：**http://localhost:18001**（仅本机；admin 账号登录）。能力：服务列表实时状态、启停（自动重新生成网关配置）、容器日志、操作审计。创建类操作（新服务/装插件）仍走 pctl CLI——控制台是 pctl 的只读投影 + 启停外壳，真相源永远是文件。

平台根路径 `GET http://localhost:18000/` 返回平台信息与入口索引；未匹配路径返回平台 JSON 404。

## 3. 接口目标（平台契约）

平台对内外承诺的稳定接口面。**目标：服务/插件/前端只依赖本节内容，即可与平台任意版本协作。**

### 3.1 身份接口（auth，挂 `/auth`）

| 端点 | 说明 |
|---|---|
| `POST /auth/login` | 用户名密码 → access(2h) + refresh(30d) 对 |
| `POST /auth/refresh` | refresh 轮换（旧 refresh 立即作废） |
| `POST /auth/logout` | 吊销当前 token（可带 refreshToken 一并吊销） |
| `GET /auth/me` | 当前身份 |
| `GET /auth/.well-known/jwks.json` | RS256 公钥（外部验签/轮换） |
| `POST /auth/service-token` | client_credentials → 5min 服务 token（服务/插件用） |
| `POST /internal/auth/*` | 仅内网：external-login（外部身份兑换）、bind-external、introspect |

### 3.2 Token 契约（所有服务的唯一接入负担）

Claims：`tokenId / tokenType(access|refresh|service) / userId / username / role(admin|user) / tenantId / svc / scopes / iss="pf-auth"`。

服务侧六步约定：取 Bearer → **RS256 公钥验签**（平台挂载 `/pf/jwt.pub`，公钥非密）→ 验 `tokenType` → 取身份 → 失败一律 401 → 透传 `X-Request-Id`。服务间代用户调用（OBO）：`Authorization: Bearer <service token>` + `X-PF-User-Token: <用户JWT>`。模板已内置全部逻辑（[templates/py-service](templates/py-service/main.py)）。

### 3.3 服务清单（service.yaml / plugin.yaml）

服务自描述、平台消费的唯一契约：`mount.path`（挂载点，保留段 `/auth /platform /internal /docs` 禁用）、`public_routes`（免登录）、`limits.rate_per_minute`（网关限流）、`auth.accept_service_tokens`（服务调用白名单）、`data.database`（自有库，`pf_` 前缀强制）；第三方插件另有 `trust / source.image+digest / permissions / resources`。完整字段：architecture-v2.md §3 + 附录 H.1。

### 3.4 pctl 命令面

| 命令 | 作用 |
|---|---|
| `new <id> --lang py` | 模板生成第一方服务骨架 |
| `sync` | 清单 → kong.yml + compose 片段（含 RS256 公钥注入与密钥自举） |
| `check [--e2e]` | 清单校验 + 容器内契约测试 |
| `list` | 服务总览（trust/状态/限流） |
| `install / enable / disable / uninstall [--purge] / upgrade` | 第三方插件生命周期 |

### 3.5 网关行为承诺

对外唯一端口 `18000`；无效/缺失 token 在 L1 被 401；超频 429；公开路由按清单放行；`/internal/*` 永不路由。生成的 `gateway/kong.yml` 与 `docker-compose.services.yml` **禁止手改**（下次 sync 覆盖）。

## 4. 目录导览

```
docker-compose.yml            基座（auth / 可选 postgres / redis）
docker-compose.services.yml   生成：gateway + plugin-pg + 全部服务与插件网络
gateway/kong.yml              生成：网关配置
platform/auth/                auth 底座（Go）
services/<id>/                服务：service.yaml + 代码 + tests/contract/
templates/py-service/         pctl new 骨架
tools/pctl/                平台 CLI
specs/                        变更规范流（平台契约变更必须走提案）
docs/                         架构设计 / 接入教程
.keys/ .env.plugins/          密钥与插件凭据（gitignore）
```

## 5. 平台纪律（四条底线）

1. 生成文件禁止手改；配置变更 = 改清单 + `pctl sync`
2. 每个服务只连自己的库（`pf_` 前缀），跨服务数据走 API
3. 契约测试三件套（无 token 401 / 有效 200 / 篡改 401）不过 = 未接入
4. 服务必须无状态；状态出口 = 自库 / Redis 租约（多副本 `--scale` 的前提）

## 6. 现状与路线

已落地：auth 底座（RS256+JWKS）、网关验签/限流、pctl 全命令、第三方登录（mock/GitHub）、第三方插件沙箱（专属网络+独立 plugin-pg+凭据体系）、多副本兼容。
路线图（Phase 4~6 余项：响应加密、svc-console 管理台、日志管道等）：architecture-v2.md §9；插件市场设计：[docs/features/plugin-marketplace.md](docs/features/plugin-marketplace.md)。
