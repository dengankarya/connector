-- Add Xendit fee breakdown columns to payment_transactions.
-- These are populated by the daily settlement sync job (SyncSettlementJob).
ALTER TABLE payment_transactions
    ADD COLUMN IF NOT EXISTS xendit_fee              BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS vat                     BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS xendit_withholding_tax  BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS third_party_wht         BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS estimated_settlement_time TIMESTAMPTZ;

-- One row per merchant; precomputed sums so the balance endpoint avoids full-table aggregates.
-- Updated nightly by SyncSettlementJob.
CREATE TABLE IF NOT EXISTS merchant_settlement_snapshots (
    id                   UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id            BIGINT      NOT NULL UNIQUE,
    xendit_account_id    TEXT        NOT NULL,
    pending_balance      BIGINT      NOT NULL DEFAULT 0,
    pending_platform_fee BIGINT      NOT NULL DEFAULT 0,
    pending_xendit_fee   BIGINT      NOT NULL DEFAULT 0,
    pending_vat          BIGINT      NOT NULL DEFAULT 0,
    pending_withholding  BIGINT      NOT NULL DEFAULT 0,
    last_synced_at       TIMESTAMPTZ,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
