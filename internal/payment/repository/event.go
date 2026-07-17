package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/dengankarya/connector/internal/payment/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// WebhookEventRepository manages payment_webhook_events rows.
type WebhookEventRepository struct {
	pool *pgxpool.Pool
}

// NewWebhookEventRepository creates a WebhookEventRepository.
func NewWebhookEventRepository(pool *pgxpool.Pool) *WebhookEventRepository {
	return &WebhookEventRepository{pool: pool}
}

// Create inserts a raw webhook event.
// Uses ON CONFLICT DO NOTHING on (provider, provider_event_id).
// Returns domain.ErrDuplicateWebhookEvent when the event was already received.
func (r *WebhookEventRepository) Create(ctx context.Context, e *domain.WebhookEvent) error {
	if e.ID == uuid.Nil {
		e.ID = uuid.New()
	}
	e.CreatedAt = time.Now().UTC()

	headersJSON, _ := json.Marshal(e.Headers)

	q := `
		INSERT INTO payment_webhook_events (
			id, provider, provider_event_id, event_type,
			raw_payload, headers, signature, signature_valid,
			processing_status, processing_attempts,
			provider_invoice_id,
			created_at
		) VALUES (
			$1, $2, $3, $4,
			$5, $6::jsonb, $7, $8,
			$9, $10,
			$11,
			$12
		)
		ON CONFLICT (provider, provider_event_id) DO NOTHING`

	tag, err := dbFromContext(ctx, r.pool).Exec(ctx, q,
		e.ID, e.Provider, e.ProviderEventID, e.EventType,
		e.RawPayload, string(headersJSON), nilIfEmpty(e.Signature), e.SignatureValid,
		string(e.ProcessingStatus), e.ProcessingAttempts,
		nilIfEmpty(e.ProviderInvoiceID),
		e.CreatedAt,
	)
	if err != nil {
		if isDuplicateKeyError(err) {
			return domain.ErrDuplicateWebhookEvent
		}
		return fmt.Errorf("insert webhook_event: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrDuplicateWebhookEvent
	}
	return nil
}

// GetByID fetches a webhook event (no lock).
func (r *WebhookEventRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.WebhookEvent, error) {
	row := dbFromContext(ctx, r.pool).QueryRow(ctx,
		`SELECT `+eventColumns+` FROM payment_webhook_events WHERE id = $1`, id)
	return scanEvent(row)
}

// GetByIDForUpdate fetches a webhook event with SELECT FOR UPDATE.
// Must be called within a DB transaction — this is the idempotency gate for processing.
func (r *WebhookEventRepository) GetByIDForUpdate(ctx context.Context, id uuid.UUID) (*domain.WebhookEvent, error) {
	row := dbFromContext(ctx, r.pool).QueryRow(ctx,
		`SELECT `+eventColumns+` FROM payment_webhook_events WHERE id = $1 FOR UPDATE`, id)
	return scanEvent(row)
}

// GetByProviderEventID checks whether we have already received a given provider event.
func (r *WebhookEventRepository) GetByProviderEventID(ctx context.Context, provider, eventID string) (*domain.WebhookEvent, error) {
	row := dbFromContext(ctx, r.pool).QueryRow(ctx,
		`SELECT `+eventColumns+` FROM payment_webhook_events
		 WHERE provider = $1 AND provider_event_id = $2`,
		provider, eventID)
	return scanEvent(row)
}

// UpdateStatus sets processing_status and last_error.
func (r *WebhookEventRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status domain.WebhookProcessingStatus, lastError string) error {
	_, err := dbFromContext(ctx, r.pool).Exec(ctx, `
		UPDATE payment_webhook_events
		SET processing_status = $1, last_error = $2
		WHERE id = $3`,
		string(status), nilIfEmpty(lastError), id)
	if err != nil {
		return fmt.Errorf("update webhook_event status: %w", err)
	}
	return nil
}

// IncrementAttempts atomically increments the processing_attempts counter.
func (r *WebhookEventRepository) IncrementAttempts(ctx context.Context, id uuid.UUID) error {
	_, err := dbFromContext(ctx, r.pool).Exec(ctx, `
		UPDATE payment_webhook_events
		SET processing_attempts = processing_attempts + 1
		WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("increment webhook_event attempts: %w", err)
	}
	return nil
}

// MarkProcessed sets status = processed, links the transaction, and records the timestamp.
func (r *WebhookEventRepository) MarkProcessed(ctx context.Context, id uuid.UUID, transactionID uuid.UUID) error {
	now := time.Now().UTC()
	_, err := dbFromContext(ctx, r.pool).Exec(ctx, `
		UPDATE payment_webhook_events
		SET processing_status = 'processed',
		    transaction_id    = $1,
		    processed_at      = $2,
		    last_error        = NULL
		WHERE id = $3`,
		transactionID, now, id)
	if err != nil {
		return fmt.Errorf("mark webhook_event processed: %w", err)
	}
	return nil
}

// ResetForReplay resets a failed/dead-lettered event back to "received" so it can be reprocessed.
func (r *WebhookEventRepository) ResetForReplay(ctx context.Context, id uuid.UUID) error {
	_, err := dbFromContext(ctx, r.pool).Exec(ctx, `
		UPDATE payment_webhook_events
		SET processing_status   = 'received',
		    last_error          = NULL,
		    processing_attempts = 0
		WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("reset webhook_event for replay: %w", err)
	}
	return nil
}

// ListFailed returns up to limit failed events eligible for retry (SKIP LOCKED).
func (r *WebhookEventRepository) ListFailed(ctx context.Context, limit int) ([]*domain.WebhookEvent, error) {
	rows, err := dbFromContext(ctx, r.pool).Query(ctx, `
		SELECT `+eventColumns+`
		FROM payment_webhook_events
		WHERE processing_status = 'failed'
		ORDER BY created_at
		LIMIT $1
		FOR UPDATE SKIP LOCKED`, limit)
	if err != nil {
		return nil, fmt.Errorf("list failed webhook_events: %w", err)
	}
	defer rows.Close()
	return collectEvents(rows)
}

// ListDeadLettered returns dead-lettered events for a tenant (for admin inspection).
func (r *WebhookEventRepository) ListDeadLettered(ctx context.Context, limit, offset int) ([]*domain.WebhookEvent, error) {
	rows, err := dbFromContext(ctx, r.pool).Query(ctx, `
		SELECT `+eventColumns+`
		FROM payment_webhook_events
		WHERE processing_status = 'dead_lettered'
		ORDER BY created_at DESC
		LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list dead_lettered webhook_events: %w", err)
	}
	defer rows.Close()
	return collectEvents(rows)
}

// ─── column list & scanners ───────────────────────────────────────────────────

const eventColumns = `
	id, provider, provider_event_id, event_type,
	raw_payload, headers, COALESCE(signature, ''), signature_valid,
	processing_status, processing_attempts,
	COALESCE(last_error, ''),
	transaction_id::text,
	processed_at,
	created_at,
	COALESCE(provider_invoice_id, '')`

func scanEvent(row pgx.Row) (*domain.WebhookEvent, error) {
	var (
		e            domain.WebhookEvent
		idStr        string
		status       string
		headersBytes []byte
		txnIDStr     *string
		processedAt  *time.Time
	)
	err := row.Scan(
		&idStr, &e.Provider, &e.ProviderEventID, &e.EventType,
		&e.RawPayload, &headersBytes, &e.Signature, &e.SignatureValid,
		&status, &e.ProcessingAttempts,
		&e.LastError,
		&txnIDStr,
		&processedAt,
		&e.CreatedAt,
		&e.ProviderInvoiceID,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound{Entity: "webhook_event", ID: idStr}
		}
		return nil, fmt.Errorf("scan webhook_event: %w", err)
	}

	e.ID = mustParseUUID(idStr)
	e.ProcessingStatus = domain.WebhookProcessingStatus(status)
	e.ProcessedAt = processedAt
	e.TransactionID = parseOptionalUUID(txnIDStr)

	if len(headersBytes) > 0 {
		_ = json.Unmarshal(headersBytes, &e.Headers)
	}
	return &e, nil
}

func collectEvents(rows pgx.Rows) ([]*domain.WebhookEvent, error) {
	var result []*domain.WebhookEvent
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, e)
	}
	return result, rows.Err()
}
