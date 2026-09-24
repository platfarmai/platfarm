-- 文件元数据（架构 §5）：归属判定的数据源；字节流在 MinIO（storage_key 定位）。
CREATE TABLE files (
    id          BIGSERIAL PRIMARY KEY,
    owner_id    INT    NOT NULL,
    tenant_id   INT    NOT NULL DEFAULT 0,
    visibility  TEXT   NOT NULL DEFAULT 'private', -- private | public
    filename    TEXT   NOT NULL,
    mime        TEXT   NOT NULL DEFAULT '',
    size        BIGINT NOT NULL DEFAULT 0,
    storage_key TEXT   UNIQUE NOT NULL,
    public_token TEXT NOT NULL DEFAULT '', -- 公开文件的不可枚举定位符；私有文件为空
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX files_owner_idx ON files (owner_id);
CREATE UNIQUE INDEX files_public_token_idx ON files (public_token) WHERE public_token <> '';
