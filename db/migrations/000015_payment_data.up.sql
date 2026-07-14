ALTER TABLE payment_transactions ADD COLUMN IF NOT EXISTS payment_data JSONB DEFAULT '{}';
