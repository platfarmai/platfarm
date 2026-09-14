# Using published PlatFarm images

[English](consuming-images.md) | [简体中文](consuming-images.zh-CN.md)

**Split releases**

| Repo | On `v*` tag | Artifacts |
|---|---|---|
| [platfarmai/platfarm](https://github.com/platfarmai/platfarm) | **Release images** | GHCR: `auth`, `console`, `runtime-*` |
| [platfarmai/pctl](https://github.com/platfarmai/pctl) | **Release pctl** | Multi-platform `pctl_*` binaries + `SHA256SUMS.txt` |

Do **not** commit binaries into either repo. Download CLI from [pctl Releases](https://github.com/platfarmai/pctl/releases). Gateway remains `kong:3.9`. Set GHCR package visibility to **Public** after the first platfarm image release.

---

## A. Run without a Go toolchain

```bash
mkdir platfarm-run && cd platfarm-run
VER=v0.0.1   # pctl CLI version (independent of platfarm image tags)
curl -fsSL -o pctl "https://github.com/platfarmai/pctl/releases/download/${VER}/pctl_${VER}_linux_amd64"
chmod +x pctl
curl -fsSL https://raw.githubusercontent.com/platfarmai/platfarm/main/deploy/compose.release.yml -o compose.yml
curl -fsSL https://raw.githubusercontent.com/platfarmai/platfarm/main/deploy/env.release.example -o .env
./pctl init .
docker compose --profile bundled-db up -d
```

`pctl init` creates `.keys/` and a minimal `gateway/kong.yml`. Details: [docker-compose-quickstart.md](docker-compose-quickstart.md) Path B.

**Why not “copy .keys from a machine that has pctl”?** That was an awkward doc path: Path B should not assume a developer checkout. The release binary *is* pctl.

Auth can also create keys on first boot if the volume is empty — but Kong still needs the matching public key in `kong.yml`, so `pctl init` (or `pctl sync`) remains the supported bootstrap.

---

## B. Build a business service on a runtime image

```dockerfile
FROM ghcr.io/platfarmai/runtime-go:latest
COPY --chown=platfarm:platfarm server /server
ENTRYPOINT ["/server"]
```

Mount `./.keys/pf-auth.pem.pub:/pf/jwt.pub:ro`. Implement `/healthz` and `/readyz`. Verify JWT with RS256 and `iss=pf-auth`.

---

## C. Pull auth only

```yaml
services:
  auth:
    image: ghcr.io/platfarmai/auth:latest
    environment:
      DATABASE_URL: postgres://...
      JWT_PRIVATE_KEY_FILE: /keys/pf-auth.pem
      PF_REDIS_URL: redis://redis:6379/0
    volumes:
      - ./keys:/keys:ro
```

JWKS: `GET http://auth:8080/auth/.well-known/jwks.json`.
