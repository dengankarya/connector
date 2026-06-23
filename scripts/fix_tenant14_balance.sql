-- Correct tenant 14 shipping balance that was incorrectly credited from payment webhook.
-- Remove the erroneous 9000 credit that happened before the fix.
UPDATE merchant_shipping_balances
SET available = available - 9000,
    updated_at = NOW()
WHERE tenant_id = 14;
