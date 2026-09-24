# 016: svc-file — file capability service (+ MinIO)

## Motivation

architecture-v2 §5 designed svc-file as the reference capability service; it was never built. Nearly every business service needs uploads on day one — the P0 gap of the platform as a comprehensive base.

## Design

Peer-level capability service (no privileges), exactly per §5:

- **Metadata**: own DB `pf_svc_file`, `files` table (owner_id/tenant_id/visibility/mime/size/storage_key), applied via the specs/015 migration runner (reference implementation `migrate.go`).
- **Bytes**: MinIO container behind `--profile bundled-s3` (symmetric with `bundled-db`: the base stays razor-thin, MinIO is only the *bundled default* backend; external S3/OSS = point `S3_ENDPOINT`/`S3_PUBLIC_ENDPOINT` elsewhere and skip the profile). Volume `pf-minio-data`, host port `127.0.0.1:19000` so presigned URLs work from a browser. svc-file never proxies file bytes.
- **API** (`/api/file`, all authenticated):
  - `POST /upload-url` `{filename,mime,size,visibility}` → metadata row + presigned PUT (15 min)
  - `GET /:id/download-url` → presigned GET (attachment disposition)
  - `DELETE /:id` → object + row
  - `GET /mine` → own files
  - `GET /me` → contract-test surface
- **L3 ownership** (reference for all services): `private` owner-only; `public` any logged-in reader; admin overrides; writes owner/admin only.
- **Presign host duality**: internal client (`S3_ENDPOINT`) for bucket ops/deletes; signing client (`S3_PUBLIC_ENDPOINT`) so the signed Host matches what the browser hits. Production sets a public domain.
- Bucket ensured at boot (`MakeBucket` if missing).

## Blast radius

New `services/svc-file/`; base compose + `.env.example` gain minio/S3 vars; `deploy/initdb.sql` creates `pf_svc_file` on fresh bundled volumes (existing volumes / external DB: one manual `CREATE DATABASE`).

## Acceptance

```bash
pctl check --e2e   # contract trio via gateway
# upload-url → PUT bytes to MinIO → download-url returns the bytes
# other user's private file → 403; delete removes object + row
```
