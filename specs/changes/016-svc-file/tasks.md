# 016 tasks

- [x] base compose: minio behind `--profile bundled-s3` (quay.io mirror, healthcheck, `127.0.0.1:19000`) + `pf-minio-data`
- [x] `deploy/initdb.sql`: `pf_svc_file` on fresh bundled volumes; `.env.example` S3/FILE vars
- [x] `services/svc-file`: manifest + auth.go + migrate.go (015 reference runner) + migrations/001 + handlers
- [x] dual S3 clients (internal ops / public presign) with pinned region (no bucket-location probe)
- [x] contract trio + manual E2E: upload-url → PUT → download-url → bytes match; admin override; other-user private 403; delete
