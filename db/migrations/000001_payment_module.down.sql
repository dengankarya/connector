-- Rollback payment module schema
-- WARNING: this drops all payment data irreversibly.

DROP TABLE IF EXISTS payment_payout_items;
DROP TABLE IF EXISTS payment_payouts;
DROP TABLE IF EXISTS payment_ledger_entries;
DROP TABLE IF EXISTS payment_webhook_events;
DROP TABLE IF EXISTS payment_transactions;
DROP TABLE IF EXISTS payment_idempotency_keys;

DROP TYPE IF EXISTS payout_status;
DROP TYPE IF EXISTS ledger_account_type;
DROP TYPE IF EXISTS ledger_direction;
DROP TYPE IF EXISTS webhook_processing_status;
DROP TYPE IF EXISTS payment_status;
