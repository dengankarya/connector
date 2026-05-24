package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/dengankarya/overwatch/internal/payment/domain"
)


// TransactionRepository manages payment_transactions rows.
type TransactionRepository struct {
	pool *pgxpool.Pool
}

// NewTransactionRepository creates a TransactionRepository.
func NewTransactionRepository(pool *pgxpool.Pool) *TransactionRepository {
	return &TransactionRepository{pool: pool}
}

// Create inserts a new transaction. Returns domain.ErrDuplicateIdempotencyKey on conflict.
func (r *TransactionRepository) Create(ctx context.Context, txn *domain.PaymentTransaction) error {
	if txn.ID == uuid.Nil {
		txn.ID = uuid.New()
	}
	now := time.Now().UTC()
	txn.CreatedAt = now
	txn.UpdatedAt = now
	txn.Version = 1

	metaJSON, metaValid := marshalJSON(txn.Metadata)

	q := `
		INSERT INTO payment_transactions (
			id, tenant_id, order_number, idempotency_key,
			provider, provider_invoice_id, provider_payment_id, checkout_url, xendit_account_id,
			payment_method, payment_channel,
			amount, currency, platform_fee, merchant_amount,
			status, description, metadata,
			expires_at, created_at, updated_at, version
		) VALUES (
			$1, $2, $3, $4,
			$5, $6, $7, $8, $9,
			$10, $11,
			$12, $13, $14, $15,
			$16, $17, $18::jsonb,
			$19, $20, $21, $22
		)
		ON CONFLICT (tenant_id, idempotency_key) DO NOTHING`

	tag, err := dbFromContext(ctx, r.pool).Exec(ctx, q,
		txn.ID, txn.TenantID, nilIfEmpty(txn.OrderNumber), txn.IdempotencyKey,
		txn.Provider, nilIfEmpty(txn.ProviderInvoiceID), nilIfEmpty(txn.ProviderPaymentID), nilIfEmpty(txn.CheckoutURL), nilIfEmpty(txn.XenditAccountID),
		nilIfEmpty(txn.PaymentMethod), nilIfEmpty(txn.PaymentChannel),
		txn.Amount, txn.Currency, txn.PlatformFee, txn.MerchantAmount,
		string(txn.Status), nilIfEmpty(txn.Description), jsonParam(metaJSON, metaValid),
		txn.ExpiresAt, txn.CreatedAt, txn.UpdatedAt, txn.Version,
	)
	if err != nil {
		if isDuplicateKeyError(err) {
			return domain.ErrDuplicateIdempotencyKey
		}
		return fmt.Errorf("insert payment_transaction: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrDuplicateIdempotencyKey
	}
	return nil
}

// GetByID fetches a transaction by primary key (no lock).
func (r *TransactionRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.PaymentTransaction, error) {
	row := dbFromContext(ctx, r.pool).QueryRow(ctx,
		`SELECT `+txnColumns+` FROM payment_transactions WHERE id = $1`, id)
	return scanTransaction(row)
}

// GetByIDForUpdate fetches a transaction with SELECT FOR UPDATE.
// Must be called within a DB transaction (ctx must carry a pgx.Tx via WithTx).
func (r *TransactionRepository) GetByIDForUpdate(ctx context.Context, id uuid.UUID) (*domain.PaymentTransaction, error) {
	row := dbFromContext(ctx, r.pool).QueryRow(ctx,
		`SELECT `+txnColumns+` FROM payment_transactions WHERE id = $1 FOR UPDATE`, id)
	return scanTransaction(row)
}

// GetByProviderInvoiceIDForUpdate fetches a transaction by provider+invoiceID with a row lock.
// Must be called within a DB transaction.
func (r *TransactionRepository) GetByProviderInvoiceIDForUpdate(ctx context.Context, provider, invoiceID string) (*domain.PaymentTransaction, error) {
	row := dbFromContext(ctx, r.pool).QueryRow(ctx,
		`SELECT `+txnColumns+` FROM payment_transactions
		 WHERE provider = $1 AND provider_invoice_id = $2
		 FOR UPDATE`,
		provider, invoiceID)
	return scanTransaction(row)
}

// Update persists all mutable fields and enforces the optimistic lock version.
// Returns domain.ErrVersionConflict if the row was concurrently modified.
func (r *TransactionRepository) Update(ctx context.Context, txn *domain.PaymentTransaction) error {
	txn.UpdatedAt = time.Now().UTC()
	metaJSON, metaValid := marshalJSON(txn.Metadata)

	q := `
		UPDATE payment_transactions SET
			provider_invoice_id = $1,
			provider_payment_id = $2,
			payment_method      = $3,
			payment_channel     = $4,
			status              = $5,
			description         = $6,
			metadata            = $7::jsonb,
			expires_at          = $8,
			paid_at             = $9,
			settled_at          = $10,
			updated_at          = $11,
			version             = $12
		WHERE id = $13 AND version = $14`

	tag, err := dbFromContext(ctx, r.pool).Exec(ctx, q,
		nilIfEmpty(txn.ProviderInvoiceID),
		nilIfEmpty(txn.ProviderPaymentID),
		nilIfEmpty(txn.PaymentMethod),
		nilIfEmpty(txn.PaymentChannel),
		string(txn.Status),
		nilIfEmpty(txn.Description),
		jsonParam(metaJSON, metaValid),
		txn.ExpiresAt,
		txn.PaidAt,
		txn.SettledAt,
		txn.UpdatedAt,
		txn.Version,   // new version (already incremented by TransitionTo)
		txn.ID,
		txn.Version-1, // expected old version
	)
	if err != nil {
		return fmt.Errorf("update payment_transaction: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrVersionConflict
	}
	return nil
}

// CursorPoint is the (created_at, id) keyset used for cursor pagination.
type CursorPoint struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

// ListParams controls cursor-paginated listing of transactions for a single tenant.
type ListParams struct {
	// Limit is the maximum number of rows to return. Callers typically pass (pageSize+1) to detect has_more.
	Limit  int
	// Cursor, when non-nil, returns rows that sort after this point (i.e. older records in DESC order).
	Cursor *CursorPoint
	// Status restricts results to the given statuses; empty means all statuses.
	Status []domain.PaymentStatus
}

// ListByTenant returns transactions for tenantID in reverse-chronological order (newest first).
// Keyset cursor on (created_at DESC, id DESC) ensures stable, index-friendly pagination.
func (r *TransactionRepository) ListByTenant(ctx context.Context, tenantID int64, p ListParams) ([]*domain.PaymentTransaction, error) {
	var (
		args  []any
		where []string
	)

	args = append(args, tenantID)
	where = append(where, fmt.Sprintf("tenant_id = $%d", len(args)))

	if len(p.Status) > 0 {
		statuses := make([]string, len(p.Status))
		for i, s := range p.Status {
			statuses[i] = string(s)
		}
		args = append(args, statuses)
		where = append(where, fmt.Sprintf("status = ANY($%d)", len(args)))
	}

	if p.Cursor != nil {
		args = append(args, p.Cursor.CreatedAt, p.Cursor.ID)
		where = append(where, fmt.Sprintf("(created_at, id) < ($%d, $%d)", len(args)-1, len(args)))
	}

	args = append(args, p.Limit)
	q := fmt.Sprintf(
		`SELECT %s FROM payment_transactions WHERE %s ORDER BY created_at DESC, id DESC LIMIT $%d`,
		txnColumns, strings.Join(where, " AND "), len(args),
	)

	rows, err := dbFromContext(ctx, r.pool).Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list transactions for tenant %d: %w", tenantID, err)
	}
	defer rows.Close()
	return collectTransactions(rows)
}

// ListExpired returns up to limit transactions past their expiry in awaiting_payment state.
// Uses SKIP LOCKED so parallel job workers don't block each other.
func (r *TransactionRepository) ListExpired(ctx context.Context, limit int) ([]*domain.PaymentTransaction, error) {
	rows, err := dbFromContext(ctx, r.pool).Query(ctx, `
		SELECT `+txnColumns+`
		FROM payment_transactions
		WHERE status = 'awaiting_payment' AND expires_at < NOW()
		ORDER BY expires_at
		LIMIT $1
		FOR UPDATE SKIP LOCKED`,
		limit)
	if err != nil {
		return nil, fmt.Errorf("list expired transactions: %w", err)
	}
	defer rows.Close()
	return collectTransactions(rows)
}

// ─── column list & scanners ───────────────────────────────────────────────────

const txnColumns = `
	id::text, tenant_id, COALESCE(order_number, ''), idempotency_key,
	provider,
	COALESCE(provider_invoice_id, ''),
	COALESCE(provider_payment_id, ''),
	COALESCE(checkout_url, ''),
	COALESCE(xendit_account_id, ''),
	COALESCE(payment_method, ''),
	COALESCE(payment_channel, ''),
	amount, currency, platform_fee, merchant_amount,
	status,
	COALESCE(description, ''),
	metadata,
	expires_at, paid_at, settled_at,
	created_at, updated_at, version`

func scanTransaction(row pgx.Row) (*domain.PaymentTransaction, error) {
	var (
		t                            domain.PaymentTransaction
		idStr                        string
		status                       string
		metaBytes                    []byte
		expiresAt, paidAt, settledAt *time.Time
	)
	err := row.Scan(
		&idStr, &t.TenantID, &t.OrderNumber, &t.IdempotencyKey,
		&t.Provider,
		&t.ProviderInvoiceID,
		&t.ProviderPaymentID,
		&t.CheckoutURL,
		&t.XenditAccountID,
		&t.PaymentMethod,
		&t.PaymentChannel,
		&t.Amount, &t.Currency, &t.PlatformFee, &t.MerchantAmount,
		&status,
		&t.Description,
		&metaBytes,
		&expiresAt, &paidAt, &settledAt,
		&t.CreatedAt, &t.UpdatedAt, &t.Version,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound{Entity: "payment_transaction", ID: idStr}
		}
		return nil, fmt.Errorf("scan transaction: %w", err)
	}

	t.ID = mustParseUUID(idStr)
	t.Status = domain.PaymentStatus(status)
	t.ExpiresAt = expiresAt
	t.PaidAt = paidAt
	t.SettledAt = settledAt
	_ = unmarshalJSON(metaBytes, &t.Metadata)
	return &t, nil
}

func collectTransactions(rows pgx.Rows) ([]*domain.PaymentTransaction, error) {
	var result []*domain.PaymentTransaction
	for rows.Next() {
		txn, err := scanTransaction(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, txn)
	}
	return result, rows.Err()
}
