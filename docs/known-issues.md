# 已知问题

记录已复现、有明确证据、但尚未修复的平台缺陷。修复后请把条目移入变更记录并删除。

---

## PF-001：同一路由叠加多个 pre-function 时，Kong 拒绝加载配置（已修复）

**状态**：已修复（`tools/pctl/sync.go` 用 `mergeAccessLua` / `preFunctionPhases` 把同路由多段 access 合并成单个 `pre-function`）。当前 `gateway/kong.yml` 每条 protected/admin/open 路由只有一个 `pre-function`。条目保留作回归说明，勿再按「未修复」规避。

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
