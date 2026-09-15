# Adding a first-party service on Path B (image deploy)

Companion to [adding-a-service.md](adding-a-service.md). Path B (`deploy/compose.release.yml` copied to `platfarm-run/compose.yml`) has **no** `services/` tree and **does not** `include` `docker-compose.services.yml`. `pctl sync` in that directory emits a gateway with auth only. `docker compose up svc-ads` then fails with **`no such service`**.

Put the plugin at `_src/services/<id>/` (directory name must equal `plugin.yaml` `id`). Give it its **own** Postgres database (`pf_<id>`). Do **not** retarget the run-dir `.env` `DATABASE_URL` — that string is for **auth**.

Start with both files every time:

```bash
docker compose -f compose.yml -f compose.ads.yml up -d --build svc-ads
docker compose -f compose.yml -f compose.ads.yml restart gateway
```

`compose.ads.yml` must join `core-net`, mount `.keys/pf-auth.pem.pub` at `/pf/jwt.pub`, and use a healthcheck the slim image can actually run (`wget /readyz` and `CMD /server` both fail on typical Go alpine images).

Patch `gateway/kong.yml` from a full-repo `pctl sync` (keys must match) or merge the `public_routes` by hand, then **restart gateway**. `curl -I` is HEAD; Gin GET-only routes will 404.

Chinese write-up with the production checklist: [adding-a-service-on-path-b.md](adding-a-service-on-path-b.md).
