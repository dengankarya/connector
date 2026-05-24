package repository

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// WebhookRequestLogRepository writes raw inbound webhook requests to the audit log.
// It always uses the pool directly — log writes must never participate in a payment transaction.
type WebhookRequestLogRepository struct {
	pool *pgxpool.Pool
}

// NewWebhookRequestLogRepository creates a WebhookRequestLogRepository.
func NewWebhookRequestLogRepository(pool *pgxpool.Pool) *WebhookRequestLogRepository {
	return &WebhookRequestLogRepository{pool: pool}
}

// Create inserts one row into webhook_request_logs.
// sourceIP is the remote address (e.g. "203.0.113.5", "203.0.113.5:12345").
// headers may be nil — stored as a NULL JSONB column when empty.
func (r *WebhookRequestLogRepository) Create(ctx context.Context, sourceIP string, rawBody []byte, headers map[string]string) error {
	var headersParam *string
	if len(headers) > 0 {
		b, _ := json.Marshal(headers)
		s := string(b)
		headersParam = &s
	}

	_, err := r.pool.Exec(ctx, `
		INSERT INTO webhook_request_logs (source_ip, raw_body, headers, received_at)
		VALUES ($1, $2, $3::jsonb, NOW())`,
		sourceIP, rawBody, headersParam,
	)
	if err != nil {
		return fmt.Errorf("insert webhook_request_log: %w", err)
	}
	return nil
}
