# 013: 开放平台计量（日志管道 Loki）

[English](proposal.md) | [简体中文](proposal.zh-CN.md)

## 动机

开放平台 app（specs/009）按 `X-PF-App-Key` 限流,但没有用量聚合供配额/账单。落地平台早已规划的日志管道（architecture-v2 附录 B）:网关 JSON 访问日志 → Loki,按 `X-PF-App-Key`/`X-Request-Id` 索引,svc-openapi 后台用 LogQL 出用量视图。

## 方案

### 网关：带 app key 的结构化访问日志

Kong 输出 JSON 访问日志（`file-log`/`http-log` 插件）,含 `X-PF-App-Key`、`X-Request-Id`、路由、状态、延迟。开放路由的 pre-function 已设 `X-PF-App-Key`（specs/009）,确保它进日志序列化器。

### 管道：Loki + 采集器

`docker compose --profile observability`：
- `loki`（单二进制、文件存储；开发）
- `promtail`（或 `vector`）采集网关容器日志 → Loki,把 JSON 字段解析成 label/结构化元数据

profile 隔离,基座保持精简,需要计量/可观测时再开。

### 后台：用量视图

svc-openapi 加只读用量面板:用 LogQL 查 Loki（`LOKI_URL`）,统计窗口内（近 24h/7d）按 `appKey`+状态的请求数。后端代理 Loki 查询（admin-only）;M2 不向合作方暴露原始日志。

### 不变式

- 计量从日志派生,不在请求热路径加第二写入（零额外延迟）。
- Loki 可选（profile）;未开时用量面板显示"管道未启用"。
- M2 **不**做账单/配额强制——仅可见性。限流（specs/009）仍是实时控制。

## 影响面

`pctl` compose 生成（网关 JSON 日志序列化器 + observability profile）、新 compose 服务（loki、promtail）、`svc-openapi`（用量视图 + Loki 代理）、文档。请求路径不变。

## 验收标准

```bash
docker compose --profile observability up -d
# 用 app token 对开放路由发 N 次
# svc-openapi 用量视图显示该 appKey 窗口内约 N 次
# 未开 observability profile → 用量视图显示"未启用",平台照常
```
