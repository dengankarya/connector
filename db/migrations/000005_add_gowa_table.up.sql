
CREATE TABLE whatsapp_integrations (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id    TEXT        NOT NULL,
    tenant_id    TEXT        NOT NULL,
    jid          TEXT        NOT NULL,
    name         TEXT        NOT NULL,
    phone_number TEXT        NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
)