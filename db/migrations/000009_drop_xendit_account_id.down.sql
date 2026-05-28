ALTER TABLE payment_transactions ADD COLUMN IF NOT EXISTS xendit_account_id TEXT;
ALTER TABLE merchant_settlement_snapshots ADD COLUMN IF NOT EXISTS xendit_account_id TEXT;
ALTER TABLE payment_payouts ADD COLUMN IF NOT EXISTS for_user_id TEXT;
