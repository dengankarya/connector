-- ============================================================
-- Shipment Module — initial schema
-- ============================================================

CREATE TYPE shipment_status AS ENUM (
    'draft',
    'waiting_pickup',
    'courier_assigned',
    'picked_up',
    'in_transit',
    'out_for_delivery',
    'delivered',
    'cancelled',
    'on_hold',
    'returning',
    'returned',
    'failed'
);

CREATE TABLE shipments (
    id                      UUID            PRIMARY KEY DEFAULT gen_random_uuid(),
    order_number            TEXT            NOT NULL DEFAULT '',
    provider                TEXT            NOT NULL DEFAULT 'biteship',
    provider_draft_order_id TEXT,
    provider_order_id       TEXT,
    courier_code            TEXT            NOT NULL DEFAULT '',
    courier_service_code    TEXT            NOT NULL DEFAULT '',
    tracking_number         TEXT            NOT NULL DEFAULT '',
    tracking_url            TEXT            NOT NULL DEFAULT '',
    shipping_cost           BIGINT          NOT NULL DEFAULT 0,
    status                  shipment_status NOT NULL DEFAULT 'draft',
    confirmed_at            TIMESTAMPTZ,
    picked_up_at            TIMESTAMPTZ,
    delivered_at            TIMESTAMPTZ,
    created_at              TIMESTAMPTZ     NOT NULL DEFAULT NOW(),
    updated_at              TIMESTAMPTZ     NOT NULL DEFAULT NOW()
);

-- Webhook update lookup by Biteship order ID (set after draft is confirmed).
CREATE INDEX idx_shipments_provider_order_id
    ON shipments (provider, provider_order_id)
    WHERE provider_order_id IS NOT NULL;

-- Early-stage webhook lookup before confirmation assigns an order ID.
CREATE INDEX idx_shipments_provider_draft_order_id
    ON shipments (provider, provider_draft_order_id)
    WHERE provider_draft_order_id IS NOT NULL;

-- Order number lookup.
CREATE INDEX idx_shipments_order_number
    ON shipments (order_number)
    WHERE order_number != '';
