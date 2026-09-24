-- 已应用 001 的库补公开定位符。空串不参与唯一约束，避免私有文件互相冲突。
ALTER TABLE files ADD COLUMN IF NOT EXISTS public_token TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX IF NOT EXISTS files_public_token_idx ON files (public_token) WHERE public_token <> '';
