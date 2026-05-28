-- Revert shipping_fee addition.
-- Note: PostgreSQL does not support removing ENUM values, so shipping_balance and
-- shipping_disbursed ledger account types are left in place.

ALTER TABLE payment_transactions DROP CONSTRAINT chk_amounts;
ALTER TABLE payment_transactions DROP COLUMN shipping_fee;
ALTER TABLE payment_transactions
    ADD CONSTRAINT chk_amounts CHECK (merchant_amount + platform_fee = amount);
