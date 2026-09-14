# 005 Implementation Checklist

[English](tasks.md) | [简体中文](tasks.zh-CN.md)

- [x] Bilingual proposal
- [x] Manifest `runtime.drain_seconds` (default 25)
- [x] pctl compose: healthcheck `/readyz`, `stop_grace_period`, Kong `retries: 2`
- [x] Engine API socket discovery (PF_CONTAINER_HOST / DOCKER_HOST / docker.sock / podman.sock)
- [x] CLI helper: PF_CONTAINER_CLI for compose exec/up (docker|podman|nerdctl)
- [x] auth + four templates + svc-demo + svc-oauth: `/readyz` + SIGTERM drain
- [x] Docs ADR #19; PHP drain limitation noted
