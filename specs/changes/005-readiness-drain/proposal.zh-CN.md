# 005: Readiness、优雅排空，以及容器运行时兼容

[English](proposal.md) | [简体中文](proposal.zh-CN.md)

## 动机

PlatFarm 已有基于 DNS 的发现（Compose 服务名 / 将来的 K8s Service）。缺的是**有序加入与退出**：新副本就绪前不应接流量；停旧副本应排空在途工作，而不是请求做到一半被杀。这不是 Nacos/Consul 能单独解决的。

运行时除 Docker 外还有 **Podman**、**containerd/nerdctl**。兼容目标是 **Compose 规范 + 容器引擎 API**，不是 Docker 品牌。

## 方案

### 运行时兼容

| 运行时 | PlatFarm 如何对接 |
|---|---|
| Docker Engine | `docker compose` + `/var/run/docker.sock` |
| Podman 4+ | `podman compose`（或 docker 别名）+ `podman.sock` |
| nerdctl + containerd | `nerdctl compose` + 对应 API socket |

规则：

- 宿主机 CLI：`PF_CONTAINER_CLI`（`docker` \| `podman` \| `nerdctl`），默认 PATH 上第一个
- Console / 容器内引擎 API：`PF_CONTAINER_HOST`。探测顺序：env → `DOCKER_HOST` → docker.sock → podman.sock
- 生成物保持 Compose 规范；不用 Podman/nerdctl 未实现的 Docker 专有扩展
- 控制台列表仍按 `com.docker.compose.project` 过滤（三家 compose 都会打）

**不承诺：** 各发行版 rootless Podman 挂 docker.sock 的边角；由运营设置 `PF_CONTAINER_HOST`。K8s 仍是附录 C 的后续生成器。

### 有序进出

```
GET /healthz   存活：进程在（已有）
GET /readyz    就绪：依赖 OK 且未进入 drain
```

SIGTERM：立刻 `/readyz`→503，排空在途 HTTP（上限 `runtime.drain_seconds`，默认 25s），再退出。Compose `stop_grace_period` = drain+5s，避免引擎先 SIGKILL。

Kong 生成 `retries: 2`，DNS 仍解析到正在 drain 的副本时连接失败可换下一个 A 记录。

PHP：`php -S` 无优雅停机；`/readyz` 仍可拒绝新流量；在途请求可能在 grace 结束时被切断（已知限制）。

## 影响面

各语言模板、auth、svc-demo/oauth、pctl compose/sync/console、Kong 生成、文档 ADR。

## 验收

```bash
pctl check
# sync 后 compose 含 healthcheck + stop_grace_period
# 运行中 GET /readyz → 200
# SIGTERM 窗口内 /readyz → 503（Go/Python/Rust；PHP 尽力）
```
