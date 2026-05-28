DROP INDEX IF EXISTS idx_shipments_tenant_id_created_at;

ALTER TABLE shipments DROP COLUMN IF EXISTS tenant_id;
