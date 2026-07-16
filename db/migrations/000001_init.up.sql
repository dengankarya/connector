-- ============================================================
-- Connector — full schema
-- ============================================================

CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- ── ENUMs ─────────────────────────────────────────────────────

CREATE TYPE webhook_processing_status AS ENUM (
    'received', 'processing', 'processed', 'failed', 'dead_lettered'
);

CREATE TYPE shipment_status AS ENUM (
    'draft', 'waiting_pickup', 'courier_assigned', 'picked_up', 'in_transit',
    'out_for_delivery', 'delivered', 'cancelled', 'on_hold', 'returning', 'returned', 'failed'
);

-- ── Unified financial event log ────────────────────────────────

CREATE TABLE transactions (
    id                        UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id                 BIGINT      NOT NULL,
    type                      TEXT        NOT NULL CHECK (type IN (
                                  'payment', 'shipping_topup', 'payout',
                                  'shipping_hold', 'shipping_adjustment'
                              )),
    amount                    BIGINT      NOT NULL,  -- no > 0 check: adjustments can be negative
    currency                  CHAR(3)     NOT NULL DEFAULT 'IDR',
    description               TEXT        NOT NULL DEFAULT '',
    status                    TEXT        NOT NULL DEFAULT 'pending',

    -- Payment-specific (NULL for other types)
    idempotency_key           TEXT,
    provider                  TEXT,
    provider_invoice_id       TEXT,
    provider_payment_id       TEXT,
    order_number              TEXT,
    payment_method            TEXT,
    payment_channel           TEXT,
    checkout_url              TEXT,
    platform_fee              BIGINT,
    merchant_amount           BIGINT,
    shipping_fee              BIGINT,
    xendit_fee                BIGINT,
    vat                       BIGINT,
    xendit_withholding_tax    BIGINT,
    third_party_wht           BIGINT,
    expires_at                TIMESTAMPTZ,
    paid_at                   TIMESTAMPTZ,
    settled_at                TIMESTAMPTZ,
    estimated_settlement_time TIMESTAMPTZ,
    payment_data              JSONB,

    -- Payout-specific (NULL for other types)
    bank_code                 TEXT,
    account_number            TEXT,
    account_name              TEXT,
    failure_reason            TEXT,
    retry_count               INTEGER     DEFAULT 0,
    max_retries               INTEGER     DEFAULT 3,
    scheduled_at              TIMESTAMPTZ,
    processed_at              TIMESTAMPTZ,

    -- Shipping hold-specific (NULL for other types)
    confirmed_at              TIMESTAMPTZ,
    released_at               TIMESTAMPTZ,

    -- Shipping adjustment-specific (NULL for other types)
    old_price                 BIGINT,
    new_price                 BIGINT,

    -- Optimistic lock — used by payment state machine
    version                   INTEGER     NOT NULL DEFAULT 1,

    metadata                  JSONB,
    created_at                TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at                TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX idx_transactions_idempotency
    ON transactions (tenant_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;

CREATE UNIQUE INDEX idx_transactions_provider_invoice
    ON transactions (provider, provider_invoice_id)
    WHERE provider_invoice_id IS NOT NULL;

CREATE INDEX idx_transactions_tenant        ON transactions (tenant_id, created_at DESC);
CREATE INDEX idx_transactions_type          ON transactions (type, created_at DESC);
CREATE INDEX idx_transactions_created       ON transactions (created_at DESC);
CREATE INDEX idx_transactions_tenant_type   ON transactions (tenant_id, type, created_at DESC);
CREATE INDEX idx_transactions_expires       ON transactions (expires_at)
    WHERE status = 'awaiting_payment' AND expires_at IS NOT NULL;
CREATE INDEX idx_transactions_order         ON transactions (order_number)
    WHERE order_number IS NOT NULL;
CREATE INDEX idx_transactions_tenant_status ON transactions (tenant_id, status);
CREATE INDEX idx_transactions_hold_order    ON transactions (tenant_id, order_number)
    WHERE type = 'shipping_hold';

-- ── Webhook events (append-only raw event store) ──────────────

CREATE TABLE payment_webhook_events (
    id                  UUID                      PRIMARY KEY DEFAULT gen_random_uuid(),
    provider            TEXT                      NOT NULL DEFAULT 'xendit',
    provider_event_id   TEXT                      NOT NULL,
    event_type          TEXT                      NOT NULL,
    raw_payload         JSONB                     NOT NULL,
    headers             JSONB,
    signature           TEXT,
    signature_valid     BOOLEAN                   NOT NULL DEFAULT false,
    processing_status   webhook_processing_status NOT NULL DEFAULT 'received',
    processing_attempts INTEGER                   NOT NULL DEFAULT 0,
    last_error          TEXT,
    transaction_id      UUID                      REFERENCES transactions(id),
    processed_at        TIMESTAMPTZ,
    created_at          TIMESTAMPTZ               NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX idx_webhook_events_provider_event_id
    ON payment_webhook_events (provider, provider_event_id);

CREATE INDEX idx_webhook_events_processing_status
    ON payment_webhook_events (processing_status, created_at)
    WHERE processing_status IN ('received', 'failed');

CREATE INDEX idx_webhook_events_transaction_id
    ON payment_webhook_events (transaction_id)
    WHERE transaction_id IS NOT NULL;

CREATE INDEX idx_webhook_events_created_at
    ON payment_webhook_events (created_at DESC);

-- ── Ledger entries (double-entry, append-only) ────────────────

CREATE TABLE ledger_entries (
    id               UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    transaction_id   UUID        NOT NULL REFERENCES transactions(id),
    tenant_id        BIGINT      NOT NULL,
    webhook_event_id UUID        REFERENCES payment_webhook_events(id),
    account_type     TEXT        NOT NULL,
    direction        TEXT        NOT NULL CHECK (direction IN ('credit', 'debit')),
    amount           BIGINT      NOT NULL CHECK (amount > 0),
    currency         CHAR(3)     NOT NULL DEFAULT 'IDR',
    reference_id     TEXT        NOT NULL,
    description      TEXT        NOT NULL DEFAULT '',
    metadata         JSONB,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX idx_ledger_entries_reference_id
    ON ledger_entries (tenant_id, reference_id);

CREATE INDEX idx_ledger_entries_transaction
    ON ledger_entries (transaction_id);

CREATE INDEX idx_ledger_entries_tenant_account
    ON ledger_entries (tenant_id, account_type);

CREATE INDEX idx_ledger_entries_created_at
    ON ledger_entries (created_at DESC);

-- ── Idempotency keys (short-lived, prevents duplicate API calls) ──

CREATE TABLE payment_idempotency_keys (
    key             TEXT        PRIMARY KEY,
    request_hash    TEXT        NOT NULL,
    response_status INTEGER,
    response_body   JSONB,
    locked_at       TIMESTAMPTZ,
    expires_at      TIMESTAMPTZ NOT NULL DEFAULT NOW() + INTERVAL '24 hours',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_idempotency_keys_expires_at
    ON payment_idempotency_keys (expires_at);

-- ── Webhook request audit log ─────────────────────────────────

CREATE TABLE webhook_request_logs (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    source_ip   TEXT        NOT NULL,
    raw_body    BYTEA       NOT NULL,
    headers     JSONB,
    received_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX webhook_request_logs_received_at_idx
    ON webhook_request_logs (received_at DESC);

-- ── Shipments ─────────────────────────────────────────────────

CREATE TABLE shipments (
    id                      UUID            PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id               BIGINT          NOT NULL DEFAULT 0,
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

CREATE INDEX idx_shipments_provider_order_id
    ON shipments (provider, provider_order_id)
    WHERE provider_order_id IS NOT NULL;

CREATE INDEX idx_shipments_provider_draft_order_id
    ON shipments (provider, provider_draft_order_id)
    WHERE provider_draft_order_id IS NOT NULL;

CREATE INDEX idx_shipments_order_number
    ON shipments (order_number)
    WHERE order_number != '';

CREATE INDEX idx_shipments_tenant_id_created_at
    ON shipments (tenant_id, created_at DESC, id DESC)
    WHERE tenant_id != 0;

-- ── Merchant wallets ──────────────────────────────────────────

CREATE TABLE merchant_shipping_balances (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  BIGINT      NOT NULL UNIQUE,
    available  BIGINT      NOT NULL DEFAULT 0 CHECK (available >= 0),
    on_hold    BIGINT      NOT NULL DEFAULT 0 CHECK (on_hold >= 0),
    currency   CHAR(3)     NOT NULL DEFAULT 'IDR',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE merchant_settlement_snapshots (
    id                   UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id            BIGINT      NOT NULL UNIQUE,
    pending_balance      BIGINT      NOT NULL DEFAULT 0,
    pending_platform_fee BIGINT      NOT NULL DEFAULT 0,
    pending_xendit_fee   BIGINT      NOT NULL DEFAULT 0,
    pending_vat          BIGINT      NOT NULL DEFAULT 0,
    pending_withholding  BIGINT      NOT NULL DEFAULT 0,
    settled_balance      BIGINT      NOT NULL DEFAULT 0,
    settled_platform_fee BIGINT      NOT NULL DEFAULT 0,
    settled_xendit_fee   BIGINT      NOT NULL DEFAULT 0,
    settled_vat          BIGINT      NOT NULL DEFAULT 0,
    settled_withholding  BIGINT      NOT NULL DEFAULT 0,
    last_synced_at       TIMESTAMPTZ,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE merchant_gateway_accounts (
    id                 UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id          BIGINT      NOT NULL,
    gateway            TEXT        NOT NULL,
    gateway_account_id TEXT        NOT NULL,
    email              TEXT        NOT NULL,
    name               TEXT        NOT NULL,
    status             TEXT        NOT NULL DEFAULT 'pending',
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, gateway)
);

-- ── WhatsApp integration ──────────────────────────────────────

CREATE TABLE whatsapp_integrations (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id    TEXT        NOT NULL,
    tenant_id    TEXT        NOT NULL,
    jid          TEXT        NOT NULL,
    name         TEXT        NOT NULL,
    phone_number TEXT        NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ── Admin panel ───────────────────────────────────────────────

-- admin_users created first (without role_id) so admin_roles can FK to it.
CREATE TABLE admin_users (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    email          TEXT        NOT NULL UNIQUE,
    password_hash  TEXT        NOT NULL,
    is_super_admin BOOLEAN     NOT NULL DEFAULT false,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE admin_roles (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT        NOT NULL UNIQUE,
    description TEXT        NOT NULL DEFAULT '',
    created_by  UUID        REFERENCES admin_users(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE admin_role_permissions (
    role_id  UUID NOT NULL REFERENCES admin_roles(id) ON DELETE CASCADE,
    resource TEXT NOT NULL,
    action   TEXT NOT NULL,
    PRIMARY KEY (role_id, resource, action)
);

-- Add role_id after admin_roles exists (circular reference resolved via ALTER).
ALTER TABLE admin_users
    ADD COLUMN role_id UUID REFERENCES admin_roles(id) ON DELETE SET NULL;
