# 已知问题

记录已复现、有明确证据、但尚未修复的平台缺陷。修复后请把条目移入变更记录并删除。

---

## PF-001：同一路由叠加多个 pre-function 时，Kong 拒绝加载配置

**严重度**：高（网关无法启动，全站 502）
**影响范围**：任何声明了 `permissions.calls` 且启用了撤销校验的服务
**状态**：未修复。受影响部署需手工维护 `gateway/kong.yml`

### 现象

`pctl sync` 生成的 `gateway/kong.yml` 会让 Kong 启动失败并持续重启：

```
nginx: [error] init_by_lua error: kong/init.lua:731: error parsing declarative config file /kong/kong.yml:
in 'routes':
  - in entry 15 of 'routes':
    in 'plugins':
      - in entry 3 of 'plugins': uniqueness violation: 'plugins' entity with primary key
        set to '521c2443-1e79-57ea-906f-907e041f161e' already declared
```

网关反复重启，所有经过网关的请求返回 502。

### 根因

`renderKong` 为同一条 protected 路由依次追加多个 `pre-function` 插件：

| 来源 | 函数 | 触发条件 |
|---|---|---|
| 撤销校验 | `revocationGateYAML` | 默认启用 |
| 服务调用网关 | `serviceCallGateYAML` | 声明了 `permissions.calls` |
| SSO cookie 映射 | 全局 plugins 段 | 默认启用 |

Kong DB-less 模式下，**插件实体的主键由「插件名 + 配置内容」哈希得出**。当多个 `pre-function` 落在同一路由且配置结构相同时，主键相同 → 判定为重复声明 → 拒绝加载整份配置。

实测一条 `svc-ads-protected` 路由上出现 3 个 `pre-function`。

### 复现

```bash
# 服务清单需同时满足：trust=first-party、声明 public_routes、声明 permissions.calls
pctl sync
docker compose restart gateway
docker logs <gateway> --tail 20   # 看到 uniqueness violation
```

### 当前规避

手工维护 `gateway/kong.yml`，不使用 `pctl sync` 生成的网关配置。
`docker-compose.services.yml` 不受影响，可正常使用（见 [adding-a-service-on-path-b.md](adding-a-service-on-path-b.md)）。

### 建议修复方向

把落在同一路由的多个 `pre-function` **合并成一个插件**。Kong 的 `config.access` 本身接受 Lua 代码数组：

```yaml
- name: pre-function
  config:
    access:
      - |
        -- 撤销校验
      - |
        -- 服务调用网关
      - |
        -- SSO cookie 映射
```

语义不变（按顺序执行），但只产生一个插件实体，从根本上消除主键冲突。

**注意**：此改动影响所有服务的网关配置生成，需要覆盖以下回归场景后再发布：

- 仅有撤销校验的路由
- 撤销校验 + 服务调用
- 三者齐全
- 第三方插件（带 scope gate 与 app 限流）
- 修改后 `kong config parse` 能通过，且 JWT/撤销/SSO 行为不变

---

## PF-002：Path B 运行目录下 `pctl install` / `upgrade` 不可用

**严重度**：中（有规避手段）
**状态**：未修复

`install` 会先 `composeUp("plugin-pg")` 并在其中建库。镜像部署（`deploy/compose.release.yml`）没有 `plugin-pg` 服务，命令会卡住。

**规避**：手工建库 + 把源码放进 `PF_SERVICES_DIR` 指向的目录 + `pctl sync`。完整步骤见 [adding-a-service-on-path-b.md](adding-a-service-on-path-b.md)。

**修复方向**：第一方服务不应依赖 `plugin-pg`（它是第三方插件的隔离库）。`install` 需要区分 trust 级别，第一方走外部数据库连接串。

---

## PF-003：容器重建后网关上游失效，必须手工重启

**严重度**：低（有固定规避）
**状态**：未修复

`docker compose up -d --build <svc>` 重建服务容器后，Kong 仍持有旧容器的上游连接，请求返回 502。服务自身 healthy，日志正常。

**规避**：每次重建服务后固定执行 `docker compose restart gateway`。

**修复方向**：让网关对上游做 DNS 重解析，或在 `pctl` 里提供 `pctl reload <svc>` 封装这两步，避免依赖人工记忆。
