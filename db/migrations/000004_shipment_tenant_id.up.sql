ALTER TABLE shipments
    ADD COLUMN IF NOT EXISTS tenant_id BIGINT NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_shipments_tenant_id_created_at
    ON shipments (tenant_id, created_at DESC, id DESC)
    WHERE tenant_id != 0;
