-- Remove all Xendit sub-account (XenPlatform) references.
-- Master account only — merchants identified via tenant_id in our DB
-- and metadata on Xendit invoices.

ALTER TABLE payment_transactions DROP COLUMN IF EXISTS xendit_account_id;
ALTER TABLE merchant_settlement_snapshots DROP COLUMN IF EXISTS xendit_account_id;
ALTER TABLE payment_payouts DROP COLUMN IF EXISTS for_user_id;
