-- Add shipping_fee column to payment_transactions.
-- shipping_fee is the portion of the customer payment held as shipping credit for the merchant.
-- It is distinct from platform_fee (connector revenue) and merchant_amount (product revenue).
--
-- New invariant: merchant_amount + platform_fee + shipping_fee = amount

ALTER TABLE payment_transactions
    ADD COLUMN shipping_fee BIGINT NOT NULL DEFAULT 0 CHECK (shipping_fee >= 0);

-- Replace the old 2-way constraint with the 3-way one.
ALTER TABLE payment_transactions DROP CONSTRAINT chk_amounts;
ALTER TABLE payment_transactions
    ADD CONSTRAINT chk_amounts CHECK (merchant_amount + platform_fee + shipping_fee = amount);

-- New ledger account types for the shipping balance wallet.
ALTER TYPE ledger_account_type ADD VALUE IF NOT EXISTS 'shipping_balance';
ALTER TYPE ledger_account_type ADD VALUE IF NOT EXISTS 'shipping_disbursed';
