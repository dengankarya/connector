-- Audit trail for shipping price corrections triggered by the Biteship order.price webhook.
-- diff > 0: actual weight higher than estimate → merchant balance was debited.
-- diff < 0: actual weight lower than estimate  → merchant balance was credited.

CREATE TABLE shipping_price_adjustments (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    BIGINT      NOT NULL,
    order_number TEXT        NOT NULL,
    old_price    BIGINT      NOT NULL,
    new_price    BIGINT      NOT NULL,
    diff         BIGINT      NOT NULL,
    currency     CHAR(3)     NOT NULL DEFAULT 'IDR',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_shipping_price_adjustments_tenant
    ON shipping_price_adjustments (tenant_id, created_at DESC);
