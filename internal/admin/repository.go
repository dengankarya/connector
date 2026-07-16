package admin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dengankarya/connector/pkg/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type dbtx = postgres.DBTX

func dbFromContext(ctx context.Context, pool *pgxpool.Pool) dbtx {
	return postgres.DBFromContext(ctx, pool)
}

// Repository handles admin_users DB queries and cross-tenant read queries.
type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository creates a Repository.
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// GetByEmail fetches an admin user by email address.
// Returns ErrInvalidCredentials when not found (collapses not-found and wrong-password for timing safety).
func (r *Repository) GetByEmail(ctx context.Context, email string) (*AdminUser, error) {
	row := dbFromContext(ctx, r.pool).QueryRow(ctx, `
		SELECT id::text, email, password_hash, created_at, updated_at
		FROM admin_users
		WHERE email = $1`, email)

	var u AdminUser
	var idStr string
	if err := row.Scan(&idStr, &u.Email, &u.PasswordHash, &u.CreatedAt, &u.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrInvalidCredentials
		}
		return nil, fmt.Errorf("get admin user by email: %w", err)
	}
	u.ID, _ = uuid.Parse(idStr)
	return &u, nil
}

// Create inserts a new admin user.
func (r *Repository) Create(ctx context.Context, u *AdminUser) error {
	if u.ID == uuid.Nil {
		u.ID = uuid.New()
	}
	now := time.Now().UTC()
	u.CreatedAt = now
	u.UpdatedAt = now

	_, err := dbFromContext(ctx, r.pool).Exec(ctx, `
		INSERT INTO admin_users (id, email, password_hash, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5)`,
		u.ID, u.Email, u.PasswordHash, u.CreatedAt, u.UpdatedAt)
	if err != nil {
		return fmt.Errorf("insert admin_user: %w", err)
	}
	return nil
}

// ─── Cross-tenant queries ────────────────────────────────────────────────────

const adminTxnColumns = `
	id::text, tenant_id, COALESCE(order_number, ''), provider,
	amount, currency, platform_fee, merchant_amount,
	status, COALESCE(payment_method, ''),
	created_at, paid_at`

func scanAdminTransaction(row pgx.Row) (*AdminTransaction, error) {
	var (
		t      AdminTransaction
		idStr  string
		paidAt *time.Time
	)
	if err := row.Scan(
		&idStr, &t.TenantID, &t.OrderNumber, &t.Provider,
		&t.Amount, &t.Currency, &t.PlatformFee, &t.MerchantAmount,
		&t.Status, &t.PaymentMethod,
		&t.CreatedAt, &paidAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("scan admin transaction: %w", err)
	}
	t.ID, _ = uuid.Parse(idStr)
	t.PaidAt = paidAt
	return &t, nil
}

// ListTransactions returns cross-tenant transactions, newest first.
// TenantID in filter is optional — nil means all tenants.
func (r *Repository) ListTransactions(ctx context.Context, f AdminTxnFilter, cursor *TxnCursorPoint) ([]*AdminTransaction, error) {
	var (
		args  []any
		where []string
	)

	if f.TenantID != nil {
		args = append(args, *f.TenantID)
		where = append(where, fmt.Sprintf("tenant_id = $%d", len(args)))
	}
	if f.From != nil {
		args = append(args, *f.From)
		where = append(where, fmt.Sprintf("created_at >= $%d", len(args)))
	}
	if f.To != nil {
		args = append(args, *f.To)
		where = append(where, fmt.Sprintf("created_at < $%d", len(args)))
	}
	if cursor != nil {
		args = append(args, cursor.CreatedAt, cursor.ID)
		where = append(where, fmt.Sprintf("(created_at, id) < ($%d, $%d)", len(args)-1, len(args)))
	}

	args = append(args, f.Limit)

	whereClause := ""
	if len(where) > 0 {
		whereClause = "WHERE " + strings.Join(where, " AND ")
	}

	q := fmt.Sprintf(
		`SELECT %s FROM payment_transactions %s ORDER BY created_at DESC, id DESC LIMIT $%d`,
		adminTxnColumns, whereClause, len(args),
	)

	rows, err := dbFromContext(ctx, r.pool).Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list admin transactions: %w", err)
	}
	defer rows.Close()

	var result []*AdminTransaction
	for rows.Next() {
		t, err := scanAdminTransaction(rows)
		if err != nil {
			return nil, err
		}
		if t != nil {
			result = append(result, t)
		}
	}
	return result, rows.Err()
}

const adminPayoutColumns = `
	id::text, tenant_id, provider,
	amount, currency, status,
	COALESCE(bank_code, ''),
	COALESCE(account_number, ''),
	COALESCE(account_name, ''),
	COALESCE(description, ''),
	COALESCE(failure_reason, ''),
	created_at, processed_at`

func scanAdminPayout(row pgx.Row) (*AdminPayout, error) {
	var (
		p           AdminPayout
		idStr       string
		processedAt *time.Time
	)
	if err := row.Scan(
		&idStr, &p.TenantID, &p.Provider,
		&p.Amount, &p.Currency, &p.Status,
		&p.BankCode,
		&p.AccountNumber,
		&p.AccountName,
		&p.Description,
		&p.FailureReason,
		&p.CreatedAt, &processedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("scan admin payout: %w", err)
	}
	p.ID, _ = uuid.Parse(idStr)
	p.ProcessedAt = processedAt
	return &p, nil
}

// ListPayouts returns all payouts across all tenants, newest first, with offset pagination.
func (r *Repository) ListPayouts(ctx context.Context, limit, offset int) ([]*AdminPayout, error) {
	rows, err := dbFromContext(ctx, r.pool).Query(ctx, `
		SELECT `+adminPayoutColumns+`
		FROM payment_payouts
		ORDER BY created_at DESC
		LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list admin payouts: %w", err)
	}
	defer rows.Close()

	var result []*AdminPayout
	for rows.Next() {
		p, err := scanAdminPayout(rows)
		if err != nil {
			return nil, err
		}
		if p != nil {
			result = append(result, p)
		}
	}
	return result, rows.Err()
}
