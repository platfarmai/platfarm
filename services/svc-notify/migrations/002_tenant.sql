ALTER TABLE notifications ADD COLUMN IF NOT EXISTS tenant_id INT NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS notifications_tenant_idx ON notifications (tenant_id);
