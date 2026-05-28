DROP TABLE IF EXISTS merchant_settlement_snapshots;

ALTER TABLE payment_transactions
    DROP COLUMN IF EXISTS xendit_fee,
    DROP COLUMN IF EXISTS vat,
    DROP COLUMN IF EXISTS xendit_withholding_tax,
    DROP COLUMN IF EXISTS third_party_wht,
    DROP COLUMN IF EXISTS estimated_settlement_time;
