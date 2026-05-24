-- webhook_request_logs captures every raw inbound HTTP request that hits a
-- webhook endpoint, before signature validation or payload parsing.
-- It is an append-only audit table — rows are never updated or deleted.

CREATE TABLE webhook_request_logs (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    source_ip   TEXT        NOT NULL,
    raw_body    BYTEA       NOT NULL,
    headers     JSONB,
    received_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Most queries will filter or sort by time (recent requests, incident windows).
CREATE INDEX webhook_request_logs_received_at_idx ON webhook_request_logs (received_at DESC);
