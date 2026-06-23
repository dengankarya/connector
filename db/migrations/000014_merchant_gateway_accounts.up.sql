CREATE TABLE merchant_gateway_accounts (
    id                 UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id          BIGINT      NOT NULL,
    gateway            TEXT        NOT NULL,         -- 'doku'
    gateway_account_id TEXT        NOT NULL,         -- e.g. SAC-0000-0000000000001
    email              TEXT        NOT NULL,
    name               TEXT        NOT NULL,
    status             TEXT        NOT NULL DEFAULT 'pending',
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, gateway)                      -- one sub-account per gateway per tenant
);
