# 005: Readiness, graceful drain, and container-runtime compatibility

[English](proposal.md) | [简体中文](proposal.zh-CN.md)

## Motivation

PlatFarm already has DNS-based discovery (Compose service names / future K8s Services). What it lacks is **ordered join and leave**: new replicas should not receive traffic until they can serve; stopping replicas must drain in-flight work instead of dying mid-request. This is not solved by Nacos/Consul.

Operators also run **Podman** and **containerd/nerdctl**, not only Docker. Compatibility target is the **Compose spec + container engine API**, not the Docker brand.

## Design

### Runtime compatibility

| Runtime | How PlatFarm talks to it |
|---|---|
| Docker Engine | `docker compose` + `/var/run/docker.sock` |
| Podman 4+ | `podman compose` (or `docker` alias) + `podman.sock` (often `/run/user/$UID/podman/podman.sock`) |
| nerdctl + containerd | `nerdctl compose` + containerd/nerdctl API socket |

Rules:

- Host CLI: `PF_CONTAINER_CLI` (`docker` \| `podman` \| `nerdctl`), default first of those found on `PATH`
- Console / in-container Engine API: `PF_CONTAINER_HOST` (unix socket or `npipe:////./pipe/docker_engine` on Windows). Probe order: env → `DOCKER_HOST` → docker.sock → podman.sock
- Generated files stay Compose spec. No Docker-only extensions beyond what Podman/nerdctl implement (`healthcheck`, `stop_grace_period`, `depends_on: condition: service_healthy`)
- `com.docker.compose.project` labels: Docker Compose and Podman Compose both set them; nerdctl compose likewise. Console listing keeps that filter

**Not claimed:** rootless Podman + docker.sock bind-mount quirks on every distro; operators set `PF_CONTAINER_HOST`. K8s remains a later `pctl sync --target k8s` generator (Appendix C).

### Ordered join / leave (the real lifecycle)

```
GET /healthz   liveness  — process is up (existing)
GET /readyz    readiness — accepting traffic: deps OK AND not draining
```

On SIGTERM: flip ready → 503 immediately, finish in-flight HTTP (cap = `runtime.drain_seconds`, default 25), then exit. Compose `stop_grace_period` = drain + 5s so the engine does not SIGKILL first.

`pctl sync` emits per service:

- `healthcheck` against `http://127.0.0.1:<port>/readyz`
- `stop_grace_period: <drain+5>s`
- gateway `depends_on` auth with `condition: service_healthy` where the engine supports it

Kong: `retries: 2` on generated services so a draining replica that still has a stale DNS A record fails over to another replica (Compose has no Endpoints). K8s Endpoints drop non-Ready pods automatically.

PHP template: `php -S` has no graceful shutdown; `/readyz` still drains *new* traffic via 503; in-flight requests may be cut at `stop_grace_period`. Documented limitation.

## Blast radius

Templates (go/rust/py/php), `platform/auth`, `svc-demo`, `svc-oauth`, pctl compose/sync/console docker client, Kong generation, docs ADR.

## Acceptance

```bash
pctl check
# After sync, docker-compose.services.yml contains healthcheck + stop_grace_period
# GET /readyz → 200 while running
# curl /readyz during SIGTERM window → 503 (Go/Python/Rust; PHP best-effort)
# PF_CONTAINER_CLI=podman is accepted (CLI discovery); socket probe documented
```
