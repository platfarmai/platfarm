-- svc-data 初始表。与 db.go 的启动 DDL 一致，全部 IF NOT EXISTS / ADD COLUMN IF NOT EXISTS，
-- 已由旧启动路径建过表的库重放无副作用。
CREATE TABLE IF NOT EXISTS dataset (
	id             text NOT NULL PRIMARY KEY,
	name           text NOT NULL DEFAULT '',
	source         text NOT NULL DEFAULT '',
	webhook_secret text NOT NULL,
	pk_field       text NOT NULL DEFAULT 'id',
	index_rules    jsonb NOT NULL DEFAULT '[]',
	enabled        boolean NOT NULL DEFAULT true,
	created_at     timestamptz NOT NULL DEFAULT now(),
	updated_at     timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS record (
	dataset    text NOT NULL,
	pk         text NOT NULL,
	payload    jsonb NOT NULL,
	updated_at timestamptz NOT NULL DEFAULT now(),
	PRIMARY KEY (dataset, pk)
);

CREATE TABLE IF NOT EXISTS record_index (
	dataset text NOT NULL,
	pk      text NOT NULL,
	field   text NOT NULL,
	k       text NOT NULL DEFAULT '',
	num     numeric NULL,
	txt     text NULL,
	PRIMARY KEY (dataset, pk, field, k)
);
CREATE INDEX IF NOT EXISTS idx_ri_num ON record_index (dataset, field, k, num);
CREATE INDEX IF NOT EXISTS idx_ri_txt ON record_index (dataset, field, txt);

CREATE TABLE IF NOT EXISTS delivery (
	dataset    text NOT NULL,
	delivery   text NOT NULL,
	result     jsonb NOT NULL,
	created_at timestamptz NOT NULL DEFAULT now(),
	PRIMARY KEY (dataset, delivery)
);

CREATE TABLE IF NOT EXISTS dataset_audit (
	id         bigserial PRIMARY KEY,
	dataset    text NOT NULL,
	actor      text NOT NULL,
	action     text NOT NULL,
	detail     jsonb NOT NULL DEFAULT '{}',
	created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_audit_ds ON dataset_audit (dataset, created_at DESC);

ALTER TABLE dataset ADD COLUMN IF NOT EXISTS retention_days int NOT NULL DEFAULT 0;
ALTER TABLE dataset ADD COLUMN IF NOT EXISTS notify_url text NOT NULL DEFAULT '';
ALTER TABLE dataset ADD COLUMN IF NOT EXISTS notify_secret text NOT NULL DEFAULT '';
