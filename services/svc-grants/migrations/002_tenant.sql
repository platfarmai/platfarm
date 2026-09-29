ALTER TABLE grants ADD COLUMN IF NOT EXISTS tenant_id INT NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS grants_tenant_idx ON grants (tenant_id);
