ALTER TABLE payment_webhook_events
    ADD COLUMN IF NOT EXISTS provider_invoice_id TEXT;
