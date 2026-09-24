# Platfarm Kubernetes manifests (large-scale tier)
#
# Compose remains the default local/dev path. These static YAMLs are the
# contract for the large-scale deployment tier (architecture-v2.md appendix C):
# when service count or HA needs exceed Compose, apply this base and grow it.
#
# No Helm. No pctl k8s generator in this tree — manifests are hand-authored
# and committed. Wire secrets, images, and Ingress class to your cluster.

## Layout

```
deploy/k8s/
  README.md          this file
  base/
    namespace.yaml    Namespace platfarm
    auth.yaml        Deployment + Service for auth
    gateway-ingress.yaml  Service gateway + Ingress (/auth, /api)
```

## Prerequisites

1. A cluster with an Ingress controller (nginx, Traefik, Gateway API adapter, etc.).
2. Secret `platfarm-env` in namespace `platfarm` holding the same env vars Compose uses
   (at minimum `DATABASE_URL`, JWT key material paths or inline values your image expects).
3. Images available to the cluster (`platfarm/auth:local` for auth; gateway image as you wire it).

## Apply

```bash
kubectl apply -f deploy/k8s/base/namespace.yaml
kubectl -n platfarm create secret generic platfarm-env --from-env-file=.env   # example
kubectl apply -f deploy/k8s/base/
```

## What is included

| Resource | Purpose |
|---|---|
| Namespace `platfarm` | Isolation boundary for platform workloads |
| Deployment + Service `auth` | Identity base; readiness `/healthz` on port 8080; env from `platfarm-env` |
| Service `gateway` | Named target for Ingress (selector `app: gateway`) |
| Ingress | Routes external `/auth` and `/api` to Service `gateway` |

## What is intentionally omitted

- Full replica of every Compose service (add Deployments as you migrate).
- Prometheus / Grafana / Loki Operators (observability stays Compose profile or cluster-native).
- Helm charts / Kustomize overlays (base YAMLs are the source of truth; overlays are optional later).

## Relationship to Compose

| Concern | Compose (default) | Kubernetes (large-scale) |
|---|---|---|
| Local AI-speed iteration | `docker compose up` | not the primary path |
| Elastic scale / rolling update | `--scale` + Redis | Deployments + HPA |
| Ingress | host port 18000 → Kong | Ingress → `gateway` Service |
| Config truth | manifests + `pctl sync` | same files; apply generated or static YAML |

When in doubt, stay on Compose until appendix C trigger signals (service count, HA, rolling publish) are real.
