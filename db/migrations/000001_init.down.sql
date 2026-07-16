-- Drop in reverse dependency order.
ALTER TABLE admin_users DROP COLUMN IF EXISTS role_id;
DROP TABLE IF EXISTS admin_role_permissions;
DROP TABLE IF EXISTS admin_roles;
DROP TABLE IF EXISTS admin_users;
DROP TABLE IF EXISTS whatsapp_integrations;
DROP TABLE IF EXISTS merchant_gateway_accounts;
DROP TABLE IF EXISTS merchant_settlement_snapshots;
DROP TABLE IF EXISTS merchant_shipping_balances;
DROP TABLE IF EXISTS shipments;
DROP TABLE IF EXISTS webhook_request_logs;
DROP TABLE IF EXISTS payment_idempotency_keys;
DROP TABLE IF EXISTS ledger_entries;
DROP TABLE IF EXISTS payment_webhook_events;
DROP TABLE IF EXISTS transactions;
DROP TYPE IF EXISTS shipment_status;
DROP TYPE IF EXISTS webhook_processing_status;
