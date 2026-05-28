package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/dengankarya/connector/internal/payment/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// IdempotencyRecord holds the cached outcome of a previously completed request.
type IdempotencyRecord struct {
	Key            string
	RequestHash    string
	ResponseStatus int
	ResponseBody   []byte
	LockedAt       *time.Time
	ExpiresAt      time.Time
	CreatedAt      time.Time
}

// IdempotencyRepository manages payment_idempotency_keys rows.
type IdempotencyRepository struct {
	pool *pgxpool.Pool
}

// NewIdempotencyRepository creates an IdempotencyRepository.
func NewIdempotencyRepository(pool *pgxpool.Pool) *IdempotencyRepository {
	return &IdempotencyRepository{pool: pool}
}

// GetOrCreate returns an existing record for key, or inserts a new locked record.
// Returns (record, true) when an existing completed record is found (caller should return cached response).
// Returns (record, false) when a new lock is acquired (caller should proceed and then call Complete).
func (r *IdempotencyRepository) GetOrCreate(ctx context.Context, key, requestHash string) (*IdempotencyRecord, bool, error) {
	// Try to fetch existing record first.
	rec, err := r.getForUpdate(ctx, key)
	if err == nil {
		// Record exists. If it has a response, return cached.
		if rec.ResponseStatus > 0 {
			return rec, true, nil
		}
		// Record is locked by a concurrent request — conflict.
		return nil, false, fmt.Errorf("idempotency key %q is currently being processed", key)
	}
	if !errors.Is(err, pgx.ErrNoRows) && !isNotFoundError(err) {
		return nil, false, fmt.Errorf("get idempotency record: %w", err)
	}

	// Record does not exist — create a locked record.
	now := time.Now().UTC()
	rec = &IdempotencyRecord{
		Key:         key,
		RequestHash: requestHash,
		LockedAt:    &now,
		ExpiresAt:   now.Add(24 * time.Hour),
		CreatedAt:   now,
	}

	q := `
		INSERT INTO payment_idempotency_keys (key, request_hash, locked_at, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (key) DO NOTHING`

	tag, err := dbFromContext(ctx, r.pool).Exec(ctx, q,
		rec.Key, rec.RequestHash, rec.LockedAt, rec.ExpiresAt, rec.CreatedAt)
	if err != nil {
		return nil, false, fmt.Errorf("insert idempotency key: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Another goroutine inserted it between our read and write. Re-fetch.
		existing, err := r.getForUpdate(ctx, key)
		if err != nil {
			return nil, false, err
		}
		if existing.ResponseStatus > 0 {
			return existing, true, nil
		}
		return nil, false, fmt.Errorf("idempotency key %q is currently being processed", key)
	}
	return rec, false, nil
}

// Complete stores the response for a completed request, releasing the lock.
func (r *IdempotencyRepository) Complete(ctx context.Context, key string, status int, body any) error {
	bodyJSON, _ := json.Marshal(body)
	_, err := dbFromContext(ctx, r.pool).Exec(ctx, `
		UPDATE payment_idempotency_keys
		SET response_status = $1,
		    response_body   = $2::jsonb,
		    locked_at       = NULL
		WHERE key = $3`,
		status, string(bodyJSON), key)
	if err != nil {
		return fmt.Errorf("complete idempotency key: %w", err)
	}
	return nil
}

// DeleteExpired removes records past their expiry (run periodically).
func (r *IdempotencyRepository) DeleteExpired(ctx context.Context) (int64, error) {
	tag, err := dbFromContext(ctx, r.pool).Exec(ctx,
		`DELETE FROM payment_idempotency_keys WHERE expires_at < NOW()`)
	if err != nil {
		return 0, fmt.Errorf("delete expired idempotency keys: %w", err)
	}
	return tag.RowsAffected(), nil
}

func (r *IdempotencyRepository) getForUpdate(ctx context.Context, key string) (*IdempotencyRecord, error) {
	row := dbFromContext(ctx, r.pool).QueryRow(ctx, `
		SELECT key, request_hash,
		       COALESCE(response_status, 0),
		       response_body,
		       locked_at, expires_at, created_at
		FROM payment_idempotency_keys
		WHERE key = $1
		FOR UPDATE`, key)

	var rec IdempotencyRecord
	var bodyBytes []byte
	err := row.Scan(
		&rec.Key, &rec.RequestHash,
		&rec.ResponseStatus,
		&bodyBytes,
		&rec.LockedAt, &rec.ExpiresAt, &rec.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	rec.ResponseBody = bodyBytes
	return &rec, nil
}

func isNotFoundError(err error) bool {
	var nf domain.ErrNotFound
	return errors.As(err, &nf)
}
