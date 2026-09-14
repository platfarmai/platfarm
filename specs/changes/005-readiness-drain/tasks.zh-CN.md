# 005 实施清单

[English](tasks.md) | [简体中文](tasks.zh-CN.md)

- [x] 双语提案
- [x] Manifest `runtime.drain_seconds`（默认 25）
- [x] pctl compose：healthcheck `/readyz`、`stop_grace_period`、Kong `retries: 2`
- [x] 引擎 API socket 探测
- [x] CLI：`PF_CONTAINER_CLI`（docker|podman|nerdctl）
- [x] auth + 四模板 + svc-demo/oauth：`/readyz` + SIGTERM drain
- [x] 文档 ADR #19；PHP drain 限制说明
