-- ============================================================
-- Payment Module — initial schema
-- Run with: psql $DATABASE_URL -f 001_payment_module.sql
-- ============================================================

-- Required extension for gen_random_uuid()
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- ============================================================
-- ENUM TYPES
-- ============================================================

CREATE TYPE payment_status AS ENUM (
    'pending',
    'awaiting_payment',
    'paid',
    'settled',
    'refunding',
    'refunded',
    'expired',
    'failed',
    'voided'
);

CREATE TYPE webhook_processing_status AS ENUM (
    'received',
    'processing',
    'processed',
    'failed',
    'dead_lettered'
);

CREATE TYPE ledger_direction AS ENUM ('credit', 'debit');

CREATE TYPE ledger_account_type AS ENUM (
    'escrow',
    'merchant_payable',
    'platform_fee',
    'payout',
    'refund'
);

CREATE TYPE payout_status AS ENUM (
    'pending',
    'processing',
    'completed',
    'failed',
    'cancelled'
);

-- ============================================================
-- PAYMENT TRANSACTIONS
-- ============================================================

CREATE TABLE payment_transactions (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id           BIGINT      NOT NULL,
    order_number        TEXT        NOT NULL,
    idempotency_key     TEXT        NOT NULL,
    provider            TEXT        NOT NULL DEFAULT 'xendit',
    provider_invoice_id TEXT,                          -- Xendit invoice ID
    provider_payment_id TEXT,                          -- Xendit payment request ID
    checkout_url        TEXT,                          -- Xendit invoice payment page URL; returned to caller on create
    xendit_account_id   TEXT,                          -- Merchant's Xendit sub-account ID (for-user-id + transfer destination)
    payment_method      TEXT,                          -- e.g. "BANK_TRANSFER", "QRIS"
    payment_channel     TEXT,                          -- e.g. "BRI", "OVO"
    amount              BIGINT      NOT NULL CHECK (amount > 0),
    currency            CHAR(3)     NOT NULL DEFAULT 'IDR',
    platform_fee        BIGINT      NOT NULL DEFAULT 0 CHECK (platform_fee >= 0),
    merchant_amount     BIGINT      NOT NULL CHECK (merchant_amount >= 0),
    status              payment_status NOT NULL DEFAULT 'pending',
    description         TEXT,
    metadata            JSONB,
    expires_at          TIMESTAMPTZ,
    paid_at             TIMESTAMPTZ,
    settled_at          TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    version             INTEGER     NOT NULL DEFAULT 1,

    CONSTRAINT chk_amounts CHECK (merchant_amount + platform_fee = amount)
);

-- One (tenant, idempotency_key) pair → one transaction.
CREATE UNIQUE INDEX idx_payment_transactions_idempotency
    ON payment_transactions (tenant_id, idempotency_key);

-- Avoid duplicate provider invoices.
CREATE UNIQUE INDEX idx_payment_transactions_provider_invoice
    ON payment_transactions (provider, provider_invoice_id)
    WHERE provider_invoice_id IS NOT NULL;

-- Order lookups (e.g. "what's the payment status for order X?").
CREATE INDEX idx_payment_transactions_order_number
    ON payment_transactions (order_number);

-- Job queries: find expired awaiting_payment transactions.
CREATE INDEX idx_payment_transactions_expires_at
    ON payment_transactions (expires_at)
    WHERE status = 'awaiting_payment' AND expires_at IS NOT NULL;

-- Tenant-scoped status queries.
CREATE INDEX idx_payment_transactions_tenant_status
    ON payment_transactions (tenant_id, status);

-- ============================================================
-- PAYMENT WEBHOOK EVENTS (append-only raw event store)
-- ============================================================

CREATE TABLE payment_webhook_events (
    id                   UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    provider             TEXT        NOT NULL DEFAULT 'xendit',
    provider_event_id    TEXT        NOT NULL,
    event_type           TEXT        NOT NULL,
    raw_payload          JSONB       NOT NULL,  -- unmodified body
    headers              JSONB,                 -- request headers for audit
    signature            TEXT,
    signature_valid      BOOLEAN     NOT NULL DEFAULT false,
    processing_status    webhook_processing_status NOT NULL DEFAULT 'received',
    processing_attempts  INTEGER     NOT NULL DEFAULT 0,
    last_error           TEXT,
    transaction_id       UUID        REFERENCES payment_transactions(id),
    processed_at         TIMESTAMPTZ,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Primary idempotency guard: one event per provider event ID.
CREATE UNIQUE INDEX idx_webhook_events_provider_event_id
    ON payment_webhook_events (provider, provider_event_id);

-- Job queries: find unprocessed/failed events.
CREATE INDEX idx_webhook_events_processing_status
    ON payment_webhook_events (processing_status, created_at)
    WHERE processing_status IN ('received', 'failed');

-- Link from event to transaction.
CREATE INDEX idx_webhook_events_transaction_id
    ON payment_webhook_events (transaction_id)
    WHERE transaction_id IS NOT NULL;

-- Chronological inspection.
CREATE INDEX idx_webhook_events_created_at
    ON payment_webhook_events (created_at DESC);

-- ============================================================
-- PAYMENT LEDGER ENTRIES (append-only, immutable)
-- ============================================================

CREATE TABLE payment_ledger_entries (
    id               UUID              PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id        BIGINT            NOT NULL,
    transaction_id   UUID              NOT NULL REFERENCES payment_transactions(id),
    webhook_event_id UUID              REFERENCES payment_webhook_events(id),
    account_type     ledger_account_type NOT NULL,
    direction        ledger_direction  NOT NULL,
    amount           BIGINT            NOT NULL CHECK (amount > 0),
    currency         CHAR(3)           NOT NULL DEFAULT 'IDR',
    reference_id     TEXT              NOT NULL,  -- idempotency key for this entry
    description      TEXT              NOT NULL,
    metadata         JSONB,
    created_at       TIMESTAMPTZ       NOT NULL DEFAULT NOW()
    -- NO updated_at — ledger entries are immutable by design
);

-- Prevent duplicate entries for the same financial event.
CREATE UNIQUE INDEX idx_ledger_entries_reference_id
    ON payment_ledger_entries (tenant_id, reference_id);

-- Common query: all entries for a transaction.
CREATE INDEX idx_ledger_entries_transaction_id
    ON payment_ledger_entries (transaction_id);

-- Balance queries per tenant + account type.
CREATE INDEX idx_ledger_entries_tenant_account
    ON payment_ledger_entries (tenant_id, account_type);

-- Chronological audit trail.
CREATE INDEX idx_ledger_entries_created_at
    ON payment_ledger_entries (created_at DESC);

-- ============================================================
-- PAYOUTS
-- ============================================================

CREATE TABLE payment_payouts (
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id         BIGINT      NOT NULL,
    for_user_id       TEXT,                          -- provider sub-account ID (e.g. Xendit for-user-id)
    provider          TEXT        NOT NULL DEFAULT 'xendit',
    provider_payout_id TEXT,
    amount            BIGINT      NOT NULL CHECK (amount > 0),
    currency          CHAR(3)     NOT NULL DEFAULT 'IDR',
    status            payout_status NOT NULL DEFAULT 'pending',
    bank_code         TEXT,
    account_number    TEXT,
    account_name      TEXT,
    description       TEXT,
    failure_reason    TEXT,
    retry_count       INTEGER     NOT NULL DEFAULT 0,
    max_retries       INTEGER     NOT NULL DEFAULT 3,
    scheduled_at      TIMESTAMPTZ,
    processed_at      TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_payouts_tenant_status
    ON payment_payouts (tenant_id, status);

-- Job queries: pending payouts ready to dispatch.
CREATE INDEX idx_payouts_scheduled_at
    ON payment_payouts (scheduled_at)
    WHERE status = 'pending';

-- ============================================================
-- PAYOUT ITEMS
-- ============================================================

CREATE TABLE payment_payout_items (
    id             UUID    PRIMARY KEY DEFAULT gen_random_uuid(),
    payout_id      UUID    NOT NULL REFERENCES payment_payouts(id),
    transaction_id UUID    NOT NULL REFERENCES payment_transactions(id),
    amount         BIGINT  NOT NULL CHECK (amount > 0),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- A transaction can only appear in one payout.
CREATE UNIQUE INDEX idx_payout_items_transaction
    ON payment_payout_items (transaction_id);

CREATE INDEX idx_payout_items_payout_id
    ON payment_payout_items (payout_id);

-- ============================================================
-- IDEMPOTENCY KEYS (short-lived, prevents duplicate API requests)
-- ============================================================

CREATE TABLE payment_idempotency_keys (
    key             TEXT        PRIMARY KEY,
    request_hash    TEXT        NOT NULL,
    response_status INTEGER,
    response_body   JSONB,
    locked_at       TIMESTAMPTZ,              -- NULL = available
    expires_at      TIMESTAMPTZ NOT NULL DEFAULT NOW() + INTERVAL '24 hours',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_idempotency_keys_expires_at
    ON payment_idempotency_keys (expires_at);
