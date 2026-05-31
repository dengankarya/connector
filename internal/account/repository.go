package account

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// txCtxKey matches internal/payment/repository so this repository participates
// in payment transactions when called from within RunInTx.
type txCtxKey string

const paymentTxKey txCtxKey = "pgx_tx"

type dbtx interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func dbFromContext(ctx context.Context, pool *pgxpool.Pool) dbtx {
	if tx, _ := ctx.Value(paymentTxKey).(pgx.Tx); tx != nil {
		return tx
	}
	return pool
}

func isDuplicateKeyError(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// Repository handles all account-level DB queries.
type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository creates a Repository.
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// ─── Activity feed ────────────────────────────────────────────────────────────

// ActivityCursorPoint is the (created_at, id) keyset used for cursor pagination
// on the unified activity feed.
type ActivityCursorPoint struct {
	CreatedAt time.Time
	ID        string // UUID as lowercase text
}

// ActivityListParams controls cursor-paginated listing of account activity.
type ActivityListParams struct {
	// Limit is the maximum number of rows to return. Pass (pageSize+1) to detect has_more.
	Limit int
	// Cursor, when non-nil, returns rows that sort after this point (older records in DESC order).
	Cursor *ActivityCursorPoint
	// Types restricts results to the given activity types; empty means all types.
	Types []ActivityType
	// From restricts to rows created at or after this time (inclusive).
	From *time.Time
	// To restricts to rows created before this time (exclusive).
	To *time.Time
}

// ListActivity returns account activity for a tenant, newest first, with optional
// cursor pagination, type filtering, and date range filtering.
func (r *Repository) ListActivity(ctx context.Context, tenantID int64, p ActivityListParams) ([]*ActivityItem, error) {
	// All dynamic predicates go on the outer query so they apply uniformly
	// across the three source tables after the UNION ALL is resolved.
	var (
		args       []any
		outerWhere []string
	)
	args = append(args, tenantID) // $1 — used by all three inner queries

	if len(p.Types) > 0 {
		typeStrs := make([]string, len(p.Types))
		for i, t := range p.Types {
			typeStrs[i] = string(t)
		}
		args = append(args, typeStrs)
		outerWhere = append(outerWhere, fmt.Sprintf("type = ANY($%d)", len(args)))
	}
	if p.From != nil {
		args = append(args, *p.From)
		outerWhere = append(outerWhere, fmt.Sprintf("created_at >= $%d", len(args)))
	}
	if p.To != nil {
		args = append(args, *p.To)
		outerWhere = append(outerWhere, fmt.Sprintf("created_at < $%d", len(args)))
	}
	if p.Cursor != nil {
		args = append(args, p.Cursor.CreatedAt, p.Cursor.ID)
		outerWhere = append(outerWhere, fmt.Sprintf("(created_at, id) < ($%d, $%d)", len(args)-1, len(args)))
	}

	args = append(args, p.Limit)
	limitIdx := len(args)

	outerWhereClause := ""
	if len(outerWhere) > 0 {
		outerWhereClause = "WHERE " + strings.Join(outerWhere, " AND ")
	}

	q := fmt.Sprintf(`
		SELECT id, type, amount, currency, order_number, status, note, created_at FROM (

			-- Full payment transactions (customer orders)
			SELECT
				id::text                      AS id,
				'payment'                     AS type,
				amount,
				currency,
				COALESCE(order_number, '')    AS order_number,
				status::text                  AS status,
				''                            AS note,
				created_at
			FROM payment_transactions
			WHERE tenant_id = $1

			UNION ALL

			-- Manual top-ups by the platform operator
			SELECT
				id::text                  AS id,
				'balance_topup'           AS type,
				amount,
				currency,
				''                        AS order_number,
				''                        AS status,
				COALESCE(note, '')        AS note,
				created_at
			FROM shipping_topups
			WHERE tenant_id = $1

			UNION ALL

			-- Shipping holds (type reflects current hold status)
			SELECT
				id::text                  AS id,
				CASE status
					WHEN 'holding'   THEN 'shipment_hold'
					WHEN 'confirmed' THEN 'shipment_confirmed'
					WHEN 'released'  THEN 'shipment_released'
				END                       AS type,
				amount,
				currency,
				order_number,
				status::text              AS status,
				''                        AS note,
				created_at
			FROM shipping_holds
			WHERE tenant_id = $1

		) activity
		%s
		ORDER BY created_at DESC, id DESC
		LIMIT $%d`,
		outerWhereClause, limitIdx)

	rows, err := dbFromContext(ctx, r.pool).Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list account activity for tenant %d: %w", tenantID, err)
	}
	defer rows.Close()

	var result []*ActivityItem
	for rows.Next() {
		var (
			item           ActivityItem
			idStr, typeStr string
		)
		if err := rows.Scan(
			&idStr, &typeStr, &item.Amount, &item.Currency,
			&item.OrderNumber, &item.Status, &item.Note, &item.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan activity item: %w", err)
		}
		item.ID, _ = uuid.Parse(idStr)
		item.Type = ActivityType(typeStr)
		result = append(result, &item)
	}
	return result, rows.Err()
}

// GetDetailByID returns the full detail for one activity item.
// For payment items, ledger entries from payment_ledger_entries are included.
func (r *Repository) GetDetailByID(ctx context.Context, tenantID int64, id uuid.UUID, activityType ActivityType) (*ActivityDetail, error) {
	db := dbFromContext(ctx, r.pool)

	item := ActivityItem{ID: id, Type: activityType}

	var metadata map[string]any

	switch activityType {
	case ActivityPayment:
		row := db.QueryRow(ctx, `
			SELECT amount, currency, COALESCE(order_number, ''), status::text,
			       COALESCE(metadata, '{}'), created_at
			FROM payment_transactions
			WHERE id = $1 AND tenant_id = $2`,
			id, tenantID)
		if err := row.Scan(&item.Amount, &item.Currency, &item.OrderNumber, &item.Status,
			&metadata, &item.CreatedAt); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, ErrNotFound
			}
			return nil, fmt.Errorf("get payment detail: %w", err)
		}

	case ActivityBalanceTopup:
		row := db.QueryRow(ctx, `
			SELECT amount, currency, COALESCE(note, ''), created_at
			FROM shipping_topups
			WHERE id = $1 AND tenant_id = $2`,
			id, tenantID)
		if err := row.Scan(&item.Amount, &item.Currency, &item.Note, &item.CreatedAt); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, ErrNotFound
			}
			return nil, fmt.Errorf("get topup detail: %w", err)
		}

	case ActivityShipmentHold, ActivityShipmentConfirmed, ActivityShipmentReleased:
		row := db.QueryRow(ctx, `
			SELECT amount, currency, order_number, status::text, created_at
			FROM shipping_holds
			WHERE id = $1 AND tenant_id = $2`,
			id, tenantID)
		if err := row.Scan(&item.Amount, &item.Currency, &item.OrderNumber, &item.Status, &item.CreatedAt); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, ErrNotFound
			}
			return nil, fmt.Errorf("get hold detail: %w", err)
		}

	default:
		return nil, fmt.Errorf("unknown activity type: %s", activityType)
	}

	detail := &ActivityDetail{ActivityItem: item, Metadata: metadata}

	// Fetch ledger entries for payment transactions.
	if activityType == ActivityPayment {
		rows, err := db.Query(ctx, `
			SELECT account_type, direction, amount, currency, description, created_at
			FROM payment_ledger_entries
			WHERE transaction_id = $1
			ORDER BY created_at`,
			id)
		if err != nil {
			return nil, fmt.Errorf("get ledger entries for %s: %w", id, err)
		}
		defer rows.Close()
		for rows.Next() {
			var e LedgerEntry
			if err := rows.Scan(&e.AccountType, &e.Direction, &e.Amount, &e.Currency, &e.Description, &e.CreatedAt); err != nil {
				return nil, fmt.Errorf("scan ledger entry: %w", err)
			}
			detail.LedgerEntries = append(detail.LedgerEntries, e)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}

	return detail, nil
}

// ─── Payment balance ──────────────────────────────────────────────────────────

// GetPaymentBalance computes the merchant's transaction-derived payment balance.
// Settled and pending figures come from payment_transactions; paid_out from payment_payouts.
func (r *Repository) GetPaymentBalance(ctx context.Context, tenantID int64) (*MerchantPaymentBalance, error) {
	db := dbFromContext(ctx, r.pool)

	var settled, pending int64
	err := db.QueryRow(ctx, `
		SELECT
			COALESCE(SUM(merchant_amount) FILTER (WHERE status = 'settled'), 0),
			COALESCE(SUM(merchant_amount) FILTER (WHERE status = 'paid'), 0)
		FROM payment_transactions
		WHERE tenant_id = $1`, tenantID).Scan(&settled, &pending)
	if err != nil {
		return nil, fmt.Errorf("get payment balance for tenant %d: %w", tenantID, err)
	}

	var paidOut int64
	err = db.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount), 0)
		FROM payment_payouts
		WHERE tenant_id = $1 AND status = 'completed'`, tenantID).Scan(&paidOut)
	if err != nil {
		return nil, fmt.Errorf("get paid out for tenant %d: %w", tenantID, err)
	}

	return &MerchantPaymentBalance{
		TenantID:          tenantID,
		Settled:           settled,
		PendingSettlement: pending,
		PaidOut:           paidOut,
		AvailableToPayout: settled - paidOut,
		Currency:          "IDR",
	}, nil
}

// ─── Shipping balance ─────────────────────────────────────────────────────────

func (r *Repository) GetBalance(ctx context.Context, tenantID int64) (*ShippingBalance, error) {
	row := dbFromContext(ctx, r.pool).QueryRow(ctx, `
		SELECT id::text, tenant_id, available, on_hold, currency, created_at, updated_at
		FROM merchant_shipping_balances
		WHERE tenant_id = $1`, tenantID)

	var b ShippingBalance
	var idStr string
	err := row.Scan(&idStr, &b.TenantID, &b.Available, &b.OnHold, &b.Currency, &b.CreatedAt, &b.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return &ShippingBalance{Available: 0, OnHold: 0, Currency: "IDR"}, nil
		}
		return nil, fmt.Errorf("get shipping balance: %w", err)
	}
	b.ID, _ = uuid.Parse(idStr)
	return &b, nil
}

func (r *Repository) CreditAvailable(ctx context.Context, tenantID int64, amount int64, currency string) error {
	_, err := dbFromContext(ctx, r.pool).Exec(ctx, `
		INSERT INTO merchant_shipping_balances (tenant_id, available, on_hold, currency)
		VALUES ($1, $2, 0, $3)
		ON CONFLICT (tenant_id) DO UPDATE
		SET available  = merchant_shipping_balances.available + EXCLUDED.available,
		    updated_at = NOW()`,
		tenantID, amount, currency)
	if err != nil {
		return fmt.Errorf("credit available balance: %w", err)
	}
	return nil
}

func (r *Repository) DeductAvailableAndHold(ctx context.Context, tenantID int64, amount int64) error {
	tag, err := dbFromContext(ctx, r.pool).Exec(ctx, `
		UPDATE merchant_shipping_balances
		SET available  = available - $2,
		    on_hold    = on_hold   + $2,
		    updated_at = NOW()
		WHERE tenant_id = $1 AND available >= $2`,
		tenantID, amount)
	if err != nil {
		return fmt.Errorf("deduct available balance: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrInsufficientBalance
	}
	return nil
}

func (r *Repository) DeductHold(ctx context.Context, tenantID int64, amount int64) error {
	_, err := dbFromContext(ctx, r.pool).Exec(ctx, `
		UPDATE merchant_shipping_balances
		SET on_hold    = on_hold - $2,
		    updated_at = NOW()
		WHERE tenant_id = $1`,
		tenantID, amount)
	return err
}

func (r *Repository) ReturnHoldToAvailable(ctx context.Context, tenantID int64, amount int64) error {
	_, err := dbFromContext(ctx, r.pool).Exec(ctx, `
		UPDATE merchant_shipping_balances
		SET available  = available + $2,
		    on_hold    = on_hold   - $2,
		    updated_at = NOW()
		WHERE tenant_id = $1`,
		tenantID, amount)
	return err
}

// ─── Holds ────────────────────────────────────────────────────────────────────

func (r *Repository) CreateHold(ctx context.Context, h *ShippingHold) error {
	if h.ID == uuid.Nil {
		h.ID = uuid.New()
	}
	h.CreatedAt = time.Now().UTC()
	h.Status = HoldStatusHolding

	_, err := dbFromContext(ctx, r.pool).Exec(ctx, `
		INSERT INTO shipping_holds (id, tenant_id, order_number, amount, currency, status, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		h.ID, h.TenantID, h.OrderNumber, h.Amount, h.Currency, string(h.Status), h.CreatedAt)
	if err != nil {
		if isDuplicateKeyError(err) {
			return ErrDuplicateHold
		}
		return fmt.Errorf("insert shipping_hold: %w", err)
	}
	return nil
}

func (r *Repository) GetHoldByID(ctx context.Context, id uuid.UUID) (*ShippingHold, error) {
	row := dbFromContext(ctx, r.pool).QueryRow(ctx, `
		SELECT id::text, tenant_id, order_number, amount, currency, status,
		       created_at, confirmed_at, released_at
		FROM shipping_holds WHERE id = $1`, id)
	return scanHold(row)
}

// GetHoldByOrderNumber returns the active (holding) hold for the given tenant and order number.
// Returns ErrHoldNotFound when no active hold exists for that order.
func (r *Repository) GetHoldByOrderNumber(ctx context.Context, tenantID int64, orderNumber string) (*ShippingHold, error) {
	row := dbFromContext(ctx, r.pool).QueryRow(ctx, `
		SELECT id::text, tenant_id, order_number, amount, currency, status,
		       created_at, confirmed_at, released_at
		FROM shipping_holds
		WHERE tenant_id = $1 AND order_number = $2 AND status = 'holding'
		LIMIT 1`, tenantID, orderNumber)
	return scanHold(row)
}

func (r *Repository) UpdateHoldStatus(ctx context.Context, id uuid.UUID, status HoldStatus, at time.Time) error {
	var q string
	switch status {
	case HoldStatusConfirmed:
		q = `UPDATE shipping_holds SET status = $2, confirmed_at = $3 WHERE id = $1`
	case HoldStatusReleased:
		q = `UPDATE shipping_holds SET status = $2, released_at = $3 WHERE id = $1`
	default:
		return fmt.Errorf("unsupported hold status: %s", status)
	}
	tag, err := dbFromContext(ctx, r.pool).Exec(ctx, q, id, string(status), at)
	if err != nil {
		return fmt.Errorf("update hold status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrHoldNotFound
	}
	return nil
}

func (r *Repository) ListHolds(ctx context.Context, tenantID int64) ([]*ShippingHold, error) {
	rows, err := dbFromContext(ctx, r.pool).Query(ctx, `
		SELECT id::text, tenant_id, order_number, amount, currency, status,
		       created_at, confirmed_at, released_at
		FROM shipping_holds
		WHERE tenant_id = $1
		ORDER BY created_at DESC`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list holds: %w", err)
	}
	defer rows.Close()

	var result []*ShippingHold
	for rows.Next() {
		h, err := scanHold(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, h)
	}
	return result, rows.Err()
}

func scanHold(row pgx.Row) (*ShippingHold, error) {
	var (
		h                       ShippingHold
		idStr, statusStr        string
		confirmedAt, releasedAt *time.Time
	)
	err := row.Scan(
		&idStr, &h.TenantID, &h.OrderNumber, &h.Amount, &h.Currency, &statusStr,
		&h.CreatedAt, &confirmedAt, &releasedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrHoldNotFound
		}
		return nil, fmt.Errorf("scan shipping_hold: %w", err)
	}
	h.ID, _ = uuid.Parse(idStr)
	h.Status = HoldStatus(statusStr)
	h.ConfirmedAt = confirmedAt
	h.ReleasedAt = releasedAt
	return &h, nil
}

// ─── Topups ───────────────────────────────────────────────────────────────────

func (r *Repository) CreateTopup(ctx context.Context, t *ShippingTopup) error {
	if t.ID == uuid.Nil {
		t.ID = uuid.New()
	}
	t.CreatedAt = time.Now().UTC()

	_, err := dbFromContext(ctx, r.pool).Exec(ctx, `
		INSERT INTO shipping_topups (id, tenant_id, amount, currency, note, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		t.ID, t.TenantID, t.Amount, t.Currency, nilIfEmpty(t.Note), t.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert shipping_topup: %w", err)
	}
	return nil
}

func (r *Repository) ListTopups(ctx context.Context, tenantID int64) ([]*ShippingTopup, error) {
	rows, err := dbFromContext(ctx, r.pool).Query(ctx, `
		SELECT id::text, tenant_id, amount, currency, COALESCE(note, ''), created_at
		FROM shipping_topups
		WHERE tenant_id = $1
		ORDER BY created_at DESC`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list topups: %w", err)
	}
	defer rows.Close()

	var result []*ShippingTopup
	for rows.Next() {
		var t ShippingTopup
		var idStr string
		if err := rows.Scan(&idStr, &t.TenantID, &t.Amount, &t.Currency, &t.Note, &t.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan topup: %w", err)
		}
		t.ID, _ = uuid.Parse(idStr)
		result = append(result, &t)
	}
	return result, rows.Err()
}
