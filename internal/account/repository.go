package account

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

// dbtx is the interface satisfied by both *pgxpool.Pool and pgx.Tx.
type dbtx = postgres.DBTX

func dbFromContext(ctx context.Context, pool *pgxpool.Pool) dbtx {
	return postgres.DBFromContext(ctx, pool)
}

func isDuplicateKeyError(err error) bool {
	return postgres.IsDuplicateKeyError(err)
}

func nilIfEmpty(s string) *string {
	return postgres.NilIfEmpty(s)
}

// Repository handles all account-level DB queries.
type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository creates a Repository.
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// ─── Payout requests ─────────────────────────────────────────────────────────

// InsertPayoutRequest creates a pending payout withdrawal record in the unified transactions table.
func (r *Repository) InsertPayoutRequest(ctx context.Context, req *PayoutRequest) error {
	req.ID = uuid.New()
	req.Status = "pending"
	req.CreatedAt = time.Now().UTC()

	_, err := r.pool.Exec(ctx, `
		INSERT INTO transactions (
			id, tenant_id, type, amount, currency, description, status,
			bank_code, account_number, account_name,
			retry_count, max_retries, version, created_at, updated_at
		) VALUES (
			$1, $2, 'payout', $3, $4, $5, 'pending',
			$6, $7, $8,
			0, 0, 1, $9, $9
		)`,
		req.ID, req.TenantID, req.Amount, req.Currency, req.Description,
		req.BankCode, req.AccountNumber, req.AccountName,
		req.CreatedAt,
	)
	return err
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
	Limit  int
	Cursor *ActivityCursorPoint
	Types  []ActivityType
	From   *time.Time
	To     *time.Time
}

// ListActivity returns account activity for a tenant, newest first.
// All financial events now come from the unified transactions table.
// Payouts and manual_transfer payments are excluded from this feed.
func (r *Repository) ListActivity(ctx context.Context, tenantID int64, p ActivityListParams) ([]*ActivityItem, error) {
	var (
		args       []any
		outerWhere []string
	)
	args = append(args, tenantID) // $1

	if len(p.Types) > 0 {
		// Map external ActivityType values back to DB type + status pairs.
		// The outer WHERE uses the computed "type" alias from the subquery.
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

	// The subquery maps DB type+status to the legacy ActivityType strings
	// so the merchant-facing API response is backward compatible.
	q := fmt.Sprintf(`
		SELECT id, type, amount, currency, order_number, status, note, created_at FROM (
			SELECT
				id::text AS id,
				CASE type
					WHEN 'shipping_hold' THEN
						CASE status
							WHEN 'holding'   THEN 'shipment_hold'
							WHEN 'confirmed' THEN 'shipment_confirmed'
							WHEN 'released'  THEN 'shipment_released'
							ELSE 'shipment_hold'
						END
					WHEN 'shipping_topup'      THEN 'balance_topup'
					WHEN 'shipping_adjustment' THEN 'shipment_price_adjustment'
					ELSE type  -- 'payment'
				END AS type,
				amount,
				currency,
				COALESCE(order_number, '') AS order_number,
				COALESCE(status, '')       AS status,
				COALESCE(description, '') AS note,
				created_at
			FROM transactions
			WHERE tenant_id = $1
			  AND type != 'payout'
			  AND NOT (type = 'payment' AND provider = 'manual_transfer')
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
// For payment items, ledger entries from ledger_entries are included.
func (r *Repository) GetDetailByID(ctx context.Context, tenantID int64, id uuid.UUID, activityType ActivityType) (*ActivityDetail, error) {
	db := dbFromContext(ctx, r.pool)

	item := ActivityItem{ID: id, Type: activityType}

	var metadata map[string]any

	switch activityType {
	case ActivityPayment:
		row := db.QueryRow(ctx, `
			SELECT amount, currency, COALESCE(order_number, ''), status,
			       COALESCE(metadata, '{}'), created_at
			FROM transactions
			WHERE id = $1 AND tenant_id = $2 AND type = 'payment'`,
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
			SELECT amount, currency, COALESCE(description, ''), created_at
			FROM transactions
			WHERE id = $1 AND tenant_id = $2 AND type = 'shipping_topup'`,
			id, tenantID)
		if err := row.Scan(&item.Amount, &item.Currency, &item.Note, &item.CreatedAt); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, ErrNotFound
			}
			return nil, fmt.Errorf("get topup detail: %w", err)
		}

	case ActivityShipmentHold, ActivityShipmentConfirmed, ActivityShipmentReleased:
		row := db.QueryRow(ctx, `
			SELECT amount, currency, COALESCE(order_number, ''), status, created_at
			FROM transactions
			WHERE id = $1 AND tenant_id = $2 AND type = 'shipping_hold'`,
			id, tenantID)
		if err := row.Scan(&item.Amount, &item.Currency, &item.OrderNumber, &item.Status, &item.CreatedAt); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, ErrNotFound
			}
			return nil, fmt.Errorf("get hold detail: %w", err)
		}

	case ActivityShipmentPriceAdjustment:
		var adj ShippingPriceAdjustment
		row := db.QueryRow(ctx, `
			SELECT amount, currency, COALESCE(order_number, ''), old_price, new_price, created_at
			FROM transactions
			WHERE id = $1 AND tenant_id = $2 AND type = 'shipping_adjustment'`,
			id, tenantID)
		if err := row.Scan(&adj.Diff, &adj.Currency, &adj.OrderNumber, &adj.OldPrice, &adj.NewPrice, &adj.CreatedAt); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, ErrNotFound
			}
			return nil, fmt.Errorf("get price adjustment detail: %w", err)
		}
		item.Amount = adj.Diff
		item.Currency = adj.Currency
		item.OrderNumber = adj.OrderNumber
		item.CreatedAt = adj.CreatedAt
		metadata = map[string]any{
			"old_price": adj.OldPrice,
			"new_price": adj.NewPrice,
			"diff":      adj.Diff,
		}

	default:
		return nil, fmt.Errorf("unknown activity type: %s", activityType)
	}

	detail := &ActivityDetail{ActivityItem: item, Metadata: metadata}

	// Fetch ledger entries for payment transactions.
	if activityType == ActivityPayment {
		rows, err := db.Query(ctx, `
			SELECT account_type, direction, amount, currency, description, created_at
			FROM ledger_entries
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
func (r *Repository) GetPaymentBalance(ctx context.Context, tenantID int64) (*MerchantPaymentBalance, error) {
	db := dbFromContext(ctx, r.pool)

	var settled, pending int64
	err := db.QueryRow(ctx, `
		SELECT
			COALESCE(SUM(merchant_amount) FILTER (WHERE status = 'settled'), 0),
			COALESCE(SUM(merchant_amount) FILTER (WHERE status = 'paid'), 0)
		FROM transactions
		WHERE tenant_id = $1 AND type = 'payment' AND provider != 'manual_transfer'`, tenantID).
		Scan(&settled, &pending)
	if err != nil {
		return nil, fmt.Errorf("get payment balance for tenant %d: %w", tenantID, err)
	}

	var paidOut int64
	err = db.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount), 0)
		FROM transactions
		WHERE tenant_id = $1 AND type = 'payout' AND status = 'completed'`, tenantID).
		Scan(&paidOut)
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

// DeductAvailableOnly deducts amount from available balance without creating a hold.
// Used for shipping price adjustments (actual cost > estimated cost).
// Returns ErrInsufficientBalance when available < amount.
func (r *Repository) DeductAvailableOnly(ctx context.Context, tenantID int64, amount int64) error {
	tag, err := dbFromContext(ctx, r.pool).Exec(ctx, `
		UPDATE merchant_shipping_balances
		SET available  = available - $2,
		    updated_at = NOW()
		WHERE tenant_id = $1 AND available >= $2`,
		tenantID, amount)
	if err != nil {
		return fmt.Errorf("deduct available balance (price adjustment): %w", err)
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
		INSERT INTO transactions (id, tenant_id, type, order_number, amount, currency, status, version, created_at, updated_at)
		VALUES ($1, $2, 'shipping_hold', $3, $4, $5, $6, 1, $7, $7)`,
		h.ID, h.TenantID, h.OrderNumber, h.Amount, h.Currency, string(h.Status), h.CreatedAt)
	if err != nil {
		if isDuplicateKeyError(err) {
			return ErrDuplicateHold
		}
		return fmt.Errorf("insert shipping_hold: %w", err)
	}
	return nil
}

// InsertHold inserts a fully-populated ShippingHold, preserving the provided status,
// confirmed_at, and released_at.
func (r *Repository) InsertHold(ctx context.Context, h *ShippingHold) error {
	if h.ID == uuid.Nil {
		h.ID = uuid.New()
	}
	if h.CreatedAt.IsZero() {
		h.CreatedAt = time.Now().UTC()
	}
	_, err := dbFromContext(ctx, r.pool).Exec(ctx, `
		INSERT INTO transactions (id, tenant_id, type, order_number, amount, currency, status, version, created_at, updated_at, confirmed_at, released_at)
		VALUES ($1, $2, 'shipping_hold', $3, $4, $5, $6, 1, $7, $7, $8, $9)`,
		h.ID, h.TenantID, h.OrderNumber, h.Amount, h.Currency, string(h.Status), h.CreatedAt, h.ConfirmedAt, h.ReleasedAt)
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
		FROM transactions WHERE id = $1 AND type = 'shipping_hold'`, id)
	return scanHold(row)
}

// GetHoldByOrderNumber returns the active (holding) hold for the given tenant and order number.
func (r *Repository) GetHoldByOrderNumber(ctx context.Context, tenantID int64, orderNumber string) (*ShippingHold, error) {
	row := dbFromContext(ctx, r.pool).QueryRow(ctx, `
		SELECT id::text, tenant_id, order_number, amount, currency, status,
		       created_at, confirmed_at, released_at
		FROM transactions
		WHERE tenant_id = $1 AND order_number = $2 AND type = 'shipping_hold' AND status = 'holding'
		LIMIT 1`, tenantID, orderNumber)
	return scanHold(row)
}

func (r *Repository) UpdateHoldStatus(ctx context.Context, id uuid.UUID, status HoldStatus, at time.Time) error {
	var q string
	switch status {
	case HoldStatusConfirmed:
		q = `UPDATE transactions SET status = $2, confirmed_at = $3, updated_at = $3 WHERE id = $1 AND type = 'shipping_hold'`
	case HoldStatusReleased:
		q = `UPDATE transactions SET status = $2, released_at = $3, updated_at = $3 WHERE id = $1 AND type = 'shipping_hold'`
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
		FROM transactions
		WHERE tenant_id = $1 AND type = 'shipping_hold'
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

// ─── Price adjustments ────────────────────────────────────────────────────────

// CreatePriceAdjustment inserts an audit row for a shipping price correction.
// amount (diff) can be negative when actual weight was less than estimated.
func (r *Repository) CreatePriceAdjustment(ctx context.Context, adj *ShippingPriceAdjustment) error {
	if adj.ID == uuid.Nil {
		adj.ID = uuid.New()
	}
	adj.CreatedAt = time.Now().UTC()

	_, err := dbFromContext(ctx, r.pool).Exec(ctx, `
		INSERT INTO transactions
		    (id, tenant_id, type, order_number, amount, currency, old_price, new_price, status, version, created_at, updated_at)
		VALUES ($1, $2, 'shipping_adjustment', $3, $4, $5, $6, $7, 'completed', 1, $8, $8)`,
		adj.ID, adj.TenantID, adj.OrderNumber, adj.Diff, adj.Currency, adj.OldPrice, adj.NewPrice, adj.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert shipping_price_adjustment: %w", err)
	}
	return nil
}

// ─── Gateway accounts ─────────────────────────────────────────────────────────

// CreateGatewayAccount inserts a new merchant gateway sub-account record.
// Returns ErrGatewayAccountExists when the (tenant_id, gateway) pair already exists.
func (r *Repository) CreateGatewayAccount(ctx context.Context, a *GatewayAccount) error {
	if a.ID == uuid.Nil {
		a.ID = uuid.New()
	}
	a.CreatedAt = time.Now().UTC()
	a.UpdatedAt = a.CreatedAt

	_, err := dbFromContext(ctx, r.pool).Exec(ctx, `
		INSERT INTO merchant_gateway_accounts
		    (id, tenant_id, gateway, gateway_account_id, email, name, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		a.ID, a.TenantID, a.Gateway, a.GatewayAccountID, a.Email, a.Name, a.Status, a.CreatedAt, a.UpdatedAt)
	if err != nil {
		if isDuplicateKeyError(err) {
			return ErrGatewayAccountExists
		}
		return fmt.Errorf("insert merchant_gateway_accounts: %w", err)
	}
	return nil
}

// GetGatewayAccountByTenantID returns the gateway sub-account for the given tenant and gateway name.
func (r *Repository) GetGatewayAccountByTenantID(ctx context.Context, tenantID int64, gateway string) (*GatewayAccount, error) {
	row := dbFromContext(ctx, r.pool).QueryRow(ctx, `
		SELECT id::text, tenant_id, gateway, gateway_account_id, email, name, status, created_at, updated_at
		FROM merchant_gateway_accounts
		WHERE tenant_id = $1 AND gateway = $2`, tenantID, gateway)

	var a GatewayAccount
	var idStr string
	if err := row.Scan(&idStr, &a.TenantID, &a.Gateway, &a.GatewayAccountID, &a.Email, &a.Name, &a.Status, &a.CreatedAt, &a.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrGatewayAccountNotFound
		}
		return nil, fmt.Errorf("get merchant_gateway_accounts: %w", err)
	}
	a.ID, _ = uuid.Parse(idStr)
	return &a, nil
}

// UpdateSubAccountStatusByGatewayID updates the status of a gateway sub-account.
func (r *Repository) UpdateSubAccountStatusByGatewayID(ctx context.Context, gateway, gatewayAccountID, status string) error {
	_, err := dbFromContext(ctx, r.pool).Exec(ctx, `
		UPDATE merchant_gateway_accounts
		SET status = $1, updated_at = NOW()
		WHERE gateway = $2 AND gateway_account_id = $3`,
		status, gateway, gatewayAccountID)
	return err
}

// ─── Topups ───────────────────────────────────────────────────────────────────

func (r *Repository) CreateTopup(ctx context.Context, t *ShippingTopup) error {
	if t.ID == uuid.Nil {
		t.ID = uuid.New()
	}
	t.CreatedAt = time.Now().UTC()

	_, err := dbFromContext(ctx, r.pool).Exec(ctx, `
		INSERT INTO transactions (id, tenant_id, type, amount, currency, description, status, version, created_at, updated_at)
		VALUES ($1, $2, 'shipping_topup', $3, $4, $5, 'completed', 1, $6, $6)`,
		t.ID, t.TenantID, t.Amount, t.Currency, nilIfEmpty(t.Note), t.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert shipping_topup: %w", err)
	}
	return nil
}

func (r *Repository) ListTopups(ctx context.Context, tenantID int64) ([]*ShippingTopup, error) {
	rows, err := dbFromContext(ctx, r.pool).Query(ctx, `
		SELECT id::text, tenant_id, amount, currency, COALESCE(description, ''), created_at
		FROM transactions
		WHERE tenant_id = $1 AND type = 'shipping_topup'
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
