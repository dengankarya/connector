ALTER TABLE merchant_settlement_snapshots
    DROP COLUMN IF EXISTS settled_balance,
    DROP COLUMN IF EXISTS settled_platform_fee,
    DROP COLUMN IF EXISTS settled_xendit_fee,
    DROP COLUMN IF EXISTS settled_vat,
    DROP COLUMN IF EXISTS settled_withholding;
