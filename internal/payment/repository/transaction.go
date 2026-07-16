package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dengankarya/connector/internal/payment/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TransactionRepository manages payment rows in the unified transactions table.
type TransactionRepository struct {
	pool *pgxpool.Pool
}

// NewTransactionRepository creates a TransactionRepository.
func NewTransactionRepository(pool *pgxpool.Pool) *TransactionRepository {
	return &TransactionRepository{pool: pool}
}

// Create inserts a new payment transaction. Returns domain.ErrDuplicateIdempotencyKey on conflict.
func (r *TransactionRepository) Create(ctx context.Context, txn *domain.PaymentTransaction) error {
	if txn.ID == uuid.Nil {
		txn.ID = uuid.New()
	}
	now := time.Now().UTC()
	txn.CreatedAt = now
	txn.UpdatedAt = now
	txn.Version = 1

	metaJSON, metaValid := marshalJSON(txn.Metadata)
	payDataJSON, payDataValid := marshalJSON(txn.PaymentData)

	q := `
		INSERT INTO transactions (
			id, tenant_id, type, order_number, idempotency_key,
			provider, provider_invoice_id, provider_payment_id, checkout_url,
			payment_method, payment_channel,
			amount, currency, platform_fee, shipping_fee, merchant_amount,
			status, description, metadata, payment_data,
			expires_at, created_at, updated_at, version
		) VALUES (
			$1, $2, 'payment', $3, $4,
			$5, $6, $7, $8,
			$9, $10,
			$11, $12, $13, $14, $15,
			$16, $17, $18::jsonb, $19::jsonb,
			$20, $21, $22, $23
		)
		ON CONFLICT (tenant_id, idempotency_key) WHERE idempotency_key IS NOT NULL DO NOTHING`

	tag, err := dbFromContext(ctx, r.pool).Exec(ctx, q,
		txn.ID, txn.TenantID, nilIfEmpty(txn.OrderNumber), txn.IdempotencyKey,
		txn.Provider, nilIfEmpty(txn.ProviderInvoiceID), nilIfEmpty(txn.ProviderPaymentID), nilIfEmpty(txn.CheckoutURL),
		nilIfEmpty(txn.PaymentMethod), nilIfEmpty(txn.PaymentChannel),
		txn.Amount, txn.Currency, txn.PlatformFee, txn.ShippingFee, txn.MerchantAmount,
		string(txn.Status), nilIfEmpty(txn.Description), jsonParam(metaJSON, metaValid), jsonParam(payDataJSON, payDataValid),
		txn.ExpiresAt, txn.CreatedAt, txn.UpdatedAt, txn.Version,
	)
	if err != nil {
		if isDuplicateKeyError(err) {
			return domain.ErrDuplicateIdempotencyKey
		}
		return fmt.Errorf("insert transaction (payment): %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrDuplicateIdempotencyKey
	}
	return nil
}

// GetByID fetches a payment transaction by primary key (no lock).
func (r *TransactionRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.PaymentTransaction, error) {
	row := dbFromContext(ctx, r.pool).QueryRow(ctx,
		`SELECT `+txnColumns+` FROM transactions WHERE id = $1 AND type = 'payment'`, id)
	return scanTransaction(row)
}

// GetByIDForUpdate fetches a payment transaction with SELECT FOR UPDATE.
// Must be called within a DB transaction (ctx must carry a pgx.Tx via WithTx).
func (r *TransactionRepository) GetByIDForUpdate(ctx context.Context, id uuid.UUID) (*domain.PaymentTransaction, error) {
	row := dbFromContext(ctx, r.pool).QueryRow(ctx,
		`SELECT `+txnColumns+` FROM transactions WHERE id = $1 AND type = 'payment' FOR UPDATE`, id)
	return scanTransaction(row)
}

// GetByProviderInvoiceIDForUpdate fetches a payment transaction by provider+invoiceID with a row lock.
// Must be called within a DB transaction.
func (r *TransactionRepository) GetByProviderInvoiceIDForUpdate(ctx context.Context, provider, invoiceID string) (*domain.PaymentTransaction, error) {
	row := dbFromContext(ctx, r.pool).QueryRow(ctx,
		`SELECT `+txnColumns+` FROM transactions
		 WHERE provider = $1 AND provider_invoice_id = $2 AND type = 'payment'
		 FOR UPDATE`,
		provider, invoiceID)
	return scanTransaction(row)
}

// GetByProviderInvoiceID fetches a payment transaction by provider + invoice ID without a row lock.
// Use GetByProviderInvoiceIDForUpdate inside a transaction when mutation follows.
func (r *TransactionRepository) GetByProviderInvoiceID(ctx context.Context, prov, invoiceID string) (*domain.PaymentTransaction, error) {
	row := dbFromContext(ctx, r.pool).QueryRow(ctx,
		`SELECT `+txnColumns+` FROM transactions WHERE provider = $1 AND provider_invoice_id = $2 AND type = 'payment'`,
		prov, invoiceID)
	return scanTransaction(row)
}

// Update persists all mutable fields and enforces the optimistic lock version.
// Returns domain.ErrVersionConflict if the row was concurrently modified.
func (r *TransactionRepository) Update(ctx context.Context, txn *domain.PaymentTransaction) error {
	txn.UpdatedAt = time.Now().UTC()
	metaJSON, metaValid := marshalJSON(txn.Metadata)

	q := `
		UPDATE transactions SET
			provider_invoice_id       = $1,
			provider_payment_id       = $2,
			payment_method            = $3,
			payment_channel           = $4,
			status                    = $5,
			description               = $6,
			metadata                  = $7::jsonb,
			expires_at                = $8,
			paid_at                   = $9,
			settled_at                = $10,
			xendit_fee                = $11,
			vat                       = $12,
			xendit_withholding_tax    = $13,
			third_party_wht           = $14,
			estimated_settlement_time = $15,
			updated_at                = $16,
			version                   = $17
		WHERE id = $18 AND version = $19 AND type = 'payment'`

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
		txn.XenditFee,
		txn.VAT,
		txn.XenditWithholdingTax,
		txn.ThirdPartyWHT,
		txn.EstimatedSettlementTime,
		txn.UpdatedAt,
		txn.Version, // new version (already incremented by TransitionTo)
		txn.ID,
		txn.Version-1, // expected old version
	)
	if err != nil {
		return fmt.Errorf("update transaction: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrVersionConflict
	}
	return nil
}

// ListDistinctTenants returns all unique tenant_ids that have payment transactions.
// Used by the settlement sync job to know which tenants to snapshot.
func (r *TransactionRepository) ListDistinctTenants(ctx context.Context) ([]int64, error) {
	rows, err := dbFromContext(ctx, r.pool).Query(ctx, `
		SELECT DISTINCT tenant_id FROM transactions WHERE type = 'payment' ORDER BY tenant_id`)
	if err != nil {
		return nil, fmt.Errorf("list distinct tenants: %w", err)
	}
	defer rows.Close()

	var result []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan tenant id: %w", err)
		}
		result = append(result, id)
	}
	return result, rows.Err()
}

// PendingSettlementSums holds aggregate totals for all paid-but-not-settled transactions.
type PendingSettlementSums struct {
	MerchantAmount int64
	PlatformFee    int64
	XenditFee      int64
	VAT            int64
	Withholding    int64
}

// SumPendingSettlement aggregates all paid-but-not-settled transactions for a tenant.
// Manual payments (provider = 'manual_transfer') are excluded.
func (r *TransactionRepository) SumPendingSettlement(ctx context.Context, tenantID int64) (*PendingSettlementSums, error) {
	var s PendingSettlementSums
	err := dbFromContext(ctx, r.pool).QueryRow(ctx, `
		SELECT
			COALESCE(SUM(merchant_amount), 0),
			COALESCE(SUM(platform_fee), 0),
			COALESCE(SUM(xendit_fee), 0),
			COALESCE(SUM(vat), 0),
			COALESCE(SUM(xendit_withholding_tax + third_party_wht), 0)
		FROM transactions
		WHERE tenant_id = $1 AND type = 'payment' AND status = 'paid' AND provider != 'manual_transfer'`, tenantID).
		Scan(&s.MerchantAmount, &s.PlatformFee, &s.XenditFee, &s.VAT, &s.Withholding)
	if err != nil {
		return nil, fmt.Errorf("sum pending settlement for tenant %d: %w", tenantID, err)
	}
	return &s, nil
}

// SumSettled aggregates all settled transactions for a tenant.
// Manual payments (provider = 'manual_transfer') are excluded.
func (r *TransactionRepository) SumSettled(ctx context.Context, tenantID int64) (*PendingSettlementSums, error) {
	var s PendingSettlementSums
	err := dbFromContext(ctx, r.pool).QueryRow(ctx, `
		SELECT
			COALESCE(SUM(merchant_amount), 0),
			COALESCE(SUM(platform_fee), 0),
			COALESCE(SUM(xendit_fee), 0),
			COALESCE(SUM(vat), 0),
			COALESCE(SUM(xendit_withholding_tax + third_party_wht), 0)
		FROM transactions
		WHERE tenant_id = $1 AND type = 'payment' AND status = 'settled' AND provider != 'manual_transfer'`, tenantID).
		Scan(&s.MerchantAmount, &s.PlatformFee, &s.XenditFee, &s.VAT, &s.Withholding)
	if err != nil {
		return nil, fmt.Errorf("sum settled for tenant %d: %w", tenantID, err)
	}
	return &s, nil
}

// CursorPoint is the (created_at, id) keyset used for cursor pagination.
type CursorPoint struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

// ListParams controls cursor-paginated listing of transactions for a single tenant.
type ListParams struct {
	Limit       int
	Cursor      *CursorPoint
	Status      []domain.PaymentStatus
	CreatedFrom *time.Time
	CreatedTo   *time.Time
	Provider    string
}

// ListByTenant returns payment transactions for tenantID in reverse-chronological order (newest first).
func (r *TransactionRepository) ListByTenant(ctx context.Context, tenantID int64, p ListParams) ([]*domain.PaymentTransaction, error) {
	var (
		args  []any
		where []string
	)

	args = append(args, tenantID)
	where = append(where, fmt.Sprintf("tenant_id = $%d", len(args)))
	where = append(where, "type = 'payment'")

	if len(p.Status) > 0 {
		statuses := make([]string, len(p.Status))
		for i, s := range p.Status {
			statuses[i] = string(s)
		}
		args = append(args, statuses)
		where = append(where, fmt.Sprintf("status = ANY($%d)", len(args)))
	}

	if p.CreatedFrom != nil {
		args = append(args, *p.CreatedFrom)
		where = append(where, fmt.Sprintf("created_at >= $%d", len(args)))
	}

	if p.CreatedTo != nil {
		args = append(args, *p.CreatedTo)
		where = append(where, fmt.Sprintf("created_at < $%d", len(args)))
	}

	if p.Cursor != nil {
		args = append(args, p.Cursor.CreatedAt, p.Cursor.ID)
		where = append(where, fmt.Sprintf("(created_at, id) < ($%d, $%d)", len(args)-1, len(args)))
	}

	if p.Provider != "" {
		args = append(args, p.Provider)
		where = append(where, fmt.Sprintf("provider = $%d", len(args)))
	}

	args = append(args, p.Limit)
	q := fmt.Sprintf(
		`SELECT %s FROM transactions WHERE %s ORDER BY created_at DESC, id DESC LIMIT $%d`,
		txnColumns, strings.Join(where, " AND "), len(args),
	)

	rows, err := dbFromContext(ctx, r.pool).Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list transactions for tenant %d: %w", tenantID, err)
	}
	defer rows.Close()
	return collectTransactions(rows)
}

// ListExpired returns up to limit payment transactions past their expiry in awaiting_payment state.
// Uses SKIP LOCKED so parallel job workers don't block each other.
func (r *TransactionRepository) ListExpired(ctx context.Context, limit int) ([]*domain.PaymentTransaction, error) {
	rows, err := dbFromContext(ctx, r.pool).Query(ctx, `
		SELECT `+txnColumns+`
		FROM transactions
		WHERE type = 'payment' AND status = 'awaiting_payment' AND expires_at < NOW()
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

// ListPaidByProvider returns up to limit paid payment transactions for the given provider
// that have a provider_payment_id set and have not yet been settled.
// Uses SKIP LOCKED so parallel job instances don't block each other.
func (r *TransactionRepository) ListPaidByProvider(ctx context.Context, providerName string, limit int) ([]*domain.PaymentTransaction, error) {
	rows, err := dbFromContext(ctx, r.pool).Query(ctx, `
		SELECT `+txnColumns+`
		FROM transactions
		WHERE type = 'payment'
		  AND status = 'paid'
		  AND provider = $1
		  AND provider_payment_id IS NOT NULL
		  AND provider_payment_id != ''
		ORDER BY paid_at ASC
		LIMIT $2
		FOR UPDATE SKIP LOCKED`,
		providerName, limit)
	if err != nil {
		return nil, fmt.Errorf("list paid transactions for provider %q: %w", providerName, err)
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
	COALESCE(payment_method, ''),
	COALESCE(payment_channel, ''),
	amount, currency, platform_fee, shipping_fee, merchant_amount,
	status,
	COALESCE(description, ''),
	metadata,
	payment_data,
	expires_at, paid_at, settled_at,
	xendit_fee, vat, xendit_withholding_tax, third_party_wht,
	estimated_settlement_time,
	created_at, updated_at, version`

func scanTransaction(row pgx.Row) (*domain.PaymentTransaction, error) {
	var (
		t                                               domain.PaymentTransaction
		idStr                                           string
		status                                          string
		metaBytes, payDataBytes                         []byte
		expiresAt, paidAt, settledAt, estSettlementTime *time.Time
	)
	err := row.Scan(
		&idStr, &t.TenantID, &t.OrderNumber, &t.IdempotencyKey,
		&t.Provider,
		&t.ProviderInvoiceID,
		&t.ProviderPaymentID,
		&t.CheckoutURL,
		&t.PaymentMethod,
		&t.PaymentChannel,
		&t.Amount, &t.Currency, &t.PlatformFee, &t.ShippingFee, &t.MerchantAmount,
		&status,
		&t.Description,
		&metaBytes,
		&payDataBytes,
		&expiresAt, &paidAt, &settledAt,
		&t.XenditFee, &t.VAT, &t.XenditWithholdingTax, &t.ThirdPartyWHT,
		&estSettlementTime,
		&t.CreatedAt, &t.UpdatedAt, &t.Version,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound{Entity: "transaction", ID: idStr}
		}
		return nil, fmt.Errorf("scan transaction: %w", err)
	}

	t.ID = mustParseUUID(idStr)
	t.Status = domain.PaymentStatus(status)
	t.ExpiresAt = expiresAt
	t.PaidAt = paidAt
	t.SettledAt = settledAt
	t.EstimatedSettlementTime = estSettlementTime
	_ = unmarshalJSON(metaBytes, &t.Metadata)
	_ = unmarshalJSON(payDataBytes, &t.PaymentData)
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
