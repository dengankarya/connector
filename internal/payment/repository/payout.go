package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/dengankarya/overwatch/internal/payment/domain"
)

// PayoutRepository manages payment_payouts rows.
type PayoutRepository struct {
	pool *pgxpool.Pool
}

// NewPayoutRepository creates a PayoutRepository.
func NewPayoutRepository(pool *pgxpool.Pool) *PayoutRepository {
	return &PayoutRepository{pool: pool}
}

// Create inserts a new payout record.
func (r *PayoutRepository) Create(ctx context.Context, p *domain.Payout) error {
	if p.ID == uuid.Nil {
		p.ID = uuid.New()
	}
	now := time.Now().UTC()
	p.CreatedAt = now
	p.UpdatedAt = now

	q := `
		INSERT INTO payment_payouts (
			id, tenant_id, for_user_id, provider, provider_payout_id,
			amount, currency, status,
			bank_code, account_number, account_name,
			description, retry_count, max_retries,
			scheduled_at, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8,
			$9, $10, $11,
			$12, $13, $14,
			$15, $16, $17
		)`

	_, err := dbFromContext(ctx, r.pool).Exec(ctx, q,
		p.ID, p.TenantID, nilIfEmpty(p.ForUserID), p.Provider, nilIfEmpty(p.ProviderPayoutID),
		p.Amount, p.Currency, string(p.Status),
		nilIfEmpty(p.BankCode), nilIfEmpty(p.AccountNumber), nilIfEmpty(p.AccountName),
		nilIfEmpty(p.Description), p.RetryCount, p.MaxRetries,
		p.ScheduledAt, p.CreatedAt, p.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert payout: %w", err)
	}
	return nil
}

// GetByIDForUpdate fetches a payout with SELECT FOR UPDATE.
func (r *PayoutRepository) GetByIDForUpdate(ctx context.Context, id uuid.UUID) (*domain.Payout, error) {
	row := dbFromContext(ctx, r.pool).QueryRow(ctx,
		`SELECT `+payoutColumns+` FROM payment_payouts WHERE id = $1 FOR UPDATE`, id)
	return scanPayout(row)
}

// Update persists mutable fields on a payout.
func (r *PayoutRepository) Update(ctx context.Context, p *domain.Payout) error {
	p.UpdatedAt = time.Now().UTC()

	q := `
		UPDATE payment_payouts SET
			provider_payout_id = $1,
			status             = $2,
			failure_reason     = $3,
			retry_count        = $4,
			processed_at       = $5,
			updated_at         = $6
		WHERE id = $7`

	_, err := dbFromContext(ctx, r.pool).Exec(ctx, q,
		nilIfEmpty(p.ProviderPayoutID),
		string(p.Status),
		nilIfEmpty(p.FailureReason),
		p.RetryCount,
		p.ProcessedAt,
		p.UpdatedAt,
		p.ID,
	)
	if err != nil {
		return fmt.Errorf("update payout: %w", err)
	}
	return nil
}

// ListPending returns up to limit pending payouts (SKIP LOCKED for parallel workers).
func (r *PayoutRepository) ListPending(ctx context.Context, limit int) ([]*domain.Payout, error) {
	rows, err := dbFromContext(ctx, r.pool).Query(ctx, `
		SELECT `+payoutColumns+`
		FROM payment_payouts
		WHERE status = 'pending'
		  AND (scheduled_at IS NULL OR scheduled_at <= NOW())
		ORDER BY created_at
		LIMIT $1
		FOR UPDATE SKIP LOCKED`, limit)
	if err != nil {
		return nil, fmt.Errorf("list pending payouts: %w", err)
	}
	defer rows.Close()
	return collectPayouts(rows)
}

// ListByTenant returns payouts for a tenant with pagination.
func (r *PayoutRepository) ListByTenant(ctx context.Context, tenantID int64, limit, offset int) ([]*domain.Payout, error) {
	rows, err := dbFromContext(ctx, r.pool).Query(ctx, `
		SELECT `+payoutColumns+`
		FROM payment_payouts
		WHERE tenant_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3`, tenantID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list payouts by tenant: %w", err)
	}
	defer rows.Close()
	return collectPayouts(rows)
}

// ─── column list & scanners ───────────────────────────────────────────────────

const payoutColumns = `
	id::text, tenant_id, COALESCE(for_user_id, ''), provider,
	COALESCE(provider_payout_id, ''),
	amount, currency, status,
	COALESCE(bank_code, ''),
	COALESCE(account_number, ''),
	COALESCE(account_name, ''),
	COALESCE(description, ''),
	COALESCE(failure_reason, ''),
	retry_count, max_retries,
	scheduled_at, processed_at,
	created_at, updated_at`

func scanPayout(row pgx.Row) (*domain.Payout, error) {
	var (
		p                        domain.Payout
		idStr, status            string
		scheduledAt, processedAt *time.Time
	)
	err := row.Scan(
		&idStr, &p.TenantID, &p.ForUserID, &p.Provider,
		&p.ProviderPayoutID,
		&p.Amount, &p.Currency, &status,
		&p.BankCode,
		&p.AccountNumber,
		&p.AccountName,
		&p.Description,
		&p.FailureReason,
		&p.RetryCount, &p.MaxRetries,
		&scheduledAt, &processedAt,
		&p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound{Entity: "payout", ID: idStr}
		}
		return nil, fmt.Errorf("scan payout: %w", err)
	}
	p.ID = mustParseUUID(idStr)
	p.Status = domain.PayoutStatus(status)
	p.ScheduledAt = scheduledAt
	p.ProcessedAt = processedAt
	return &p, nil
}

func collectPayouts(rows pgx.Rows) ([]*domain.Payout, error) {
	var result []*domain.Payout
	for rows.Next() {
		p, err := scanPayout(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, p)
	}
	return result, rows.Err()
}
