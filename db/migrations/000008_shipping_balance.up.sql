-- Shipping balance wallet for merchants.
-- available: funds the merchant can spend on shipments.
-- on_hold:   funds reserved for a draft order, not yet disbursed.
-- Updated in-place (within the same DB transaction) on every balance-changing event.

CREATE TABLE merchant_shipping_balances (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   BIGINT      NOT NULL UNIQUE,
    available   BIGINT      NOT NULL DEFAULT 0 CHECK (available >= 0),
    on_hold     BIGINT      NOT NULL DEFAULT 0 CHECK (on_hold >= 0),
    currency    CHAR(3)     NOT NULL DEFAULT 'IDR',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Audit trail for manual top-ups performed by the platform operator.
CREATE TABLE shipping_topups (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   BIGINT      NOT NULL,
    amount      BIGINT      NOT NULL CHECK (amount > 0),
    currency    CHAR(3)     NOT NULL DEFAULT 'IDR',
    note        TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_shipping_topups_tenant ON shipping_topups (tenant_id, created_at DESC);

-- Per-order hold: one active hold per (tenant, order_number) at a time.
CREATE TYPE shipping_hold_status AS ENUM ('holding', 'confirmed', 'released');

CREATE TABLE shipping_holds (
    id              UUID                 PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       BIGINT               NOT NULL,
    order_number    TEXT                 NOT NULL,
    amount          BIGINT               NOT NULL CHECK (amount > 0),
    currency        CHAR(3)              NOT NULL DEFAULT 'IDR',
    status          shipping_hold_status NOT NULL DEFAULT 'holding',
    created_at      TIMESTAMPTZ          NOT NULL DEFAULT NOW(),
    confirmed_at    TIMESTAMPTZ,
    released_at     TIMESTAMPTZ
);

CREATE INDEX idx_shipping_holds_tenant ON shipping_holds (tenant_id, status);

-- Prevent two active holds on the same order for the same tenant.
CREATE UNIQUE INDEX idx_shipping_holds_active_order
    ON shipping_holds (tenant_id, order_number)
    WHERE status = 'holding';
