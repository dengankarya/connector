package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dengankarya/connector/internal/shipping/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ShipmentRepository manages shipments rows.
type ShipmentRepository struct {
	pool *pgxpool.Pool
}

// NewShipmentRepository creates a ShipmentRepository backed by pool.
func NewShipmentRepository(pool *pgxpool.Pool) *ShipmentRepository {
	return &ShipmentRepository{pool: pool}
}

// Create inserts a new shipment and sets ID, CreatedAt, UpdatedAt.
func (r *ShipmentRepository) Create(ctx context.Context, s *domain.Shipment) error {
	if s.ID == uuid.Nil {
		s.ID = uuid.New()
	}
	now := time.Now().UTC()
	s.CreatedAt = now
	s.UpdatedAt = now

	_, err := dbFromContext(ctx, r.pool).Exec(ctx, `
		INSERT INTO shipments (
			id, tenant_id, order_number, provider,
			provider_draft_order_id, provider_order_id,
			courier_code, courier_service_code,
			tracking_number, tracking_url,
			shipping_cost, status,
			confirmed_at, picked_up_at, delivered_at,
			created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17
		)`,
		s.ID, s.TenantID, s.OrderNumber, s.Provider,
		nilIfEmptyPtr(s.ProviderDraftOrderID), nilIfEmptyPtr(s.ProviderOrderID),
		s.CourierCode, s.CourierServiceCode,
		s.TrackingNumber, s.TrackingURL,
		s.ShippingCost, string(s.Status),
		s.ConfirmedAt, s.PickedUpAt, s.DeliveredAt,
		s.CreatedAt, s.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert shipment: %w", err)
	}
	return nil
}

// CursorPoint is the (created_at, id) keyset used for cursor pagination.
type CursorPoint struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

// ListParams controls cursor-paginated listing of shipments for a single tenant.
type ListParams struct {
	// Limit is the maximum number of rows to return. Callers typically pass (pageSize+1) to detect has_more.
	Limit int
	// Cursor, when non-nil, returns rows that sort after this point (i.e. older records in DESC order).
	Cursor *CursorPoint
	// Status restricts results to the given statuses; empty means all statuses.
	Status []domain.ShipmentStatus
}

// ListByTenant returns shipments for tenantID in reverse-chronological order (newest first).
// Keyset cursor on (created_at DESC, id DESC) ensures stable, index-friendly pagination.
func (r *ShipmentRepository) ListByTenant(ctx context.Context, tenantID int64, p ListParams) ([]*domain.Shipment, error) {
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
		`SELECT %s FROM shipments WHERE %s ORDER BY created_at DESC, id DESC LIMIT $%d`,
		shipmentColumns, strings.Join(where, " AND "), len(args),
	)

	rows, err := dbFromContext(ctx, r.pool).Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list shipments for tenant %d: %w", tenantID, err)
	}
	defer rows.Close()
	return collectShipments(rows)
}

// Update persists mutable shipment fields.
func (r *ShipmentRepository) Update(ctx context.Context, s *domain.Shipment) error {
	s.UpdatedAt = time.Now().UTC()

	tag, err := dbFromContext(ctx, r.pool).Exec(ctx, `
		UPDATE shipments SET
			provider_order_id    = $1,
			tracking_number      = $2,
			tracking_url         = $3,
			shipping_cost        = $4,
			status               = $5,
			confirmed_at         = $6,
			picked_up_at         = $7,
			delivered_at         = $8,
			updated_at           = $9
		WHERE id = $10`,
		nilIfEmptyPtr(s.ProviderOrderID),
		s.TrackingNumber, s.TrackingURL,
		s.ShippingCost, string(s.Status),
		s.ConfirmedAt, s.PickedUpAt, s.DeliveredAt,
		s.UpdatedAt, s.ID,
	)
	if err != nil {
		return fmt.Errorf("update shipment %s: %w", s.ID, err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// GetByID fetches a shipment by its primary key.
func (r *ShipmentRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.Shipment, error) {
	row := dbFromContext(ctx, r.pool).QueryRow(ctx,
		`SELECT `+shipmentColumns+` FROM shipments WHERE id = $1`, id)
	return scanShipment(row)
}

// GetByProviderOrderID fetches a shipment by its Biteship order ID.
func (r *ShipmentRepository) GetByProviderOrderID(ctx context.Context, provider, orderID string) (*domain.Shipment, error) {
	row := dbFromContext(ctx, r.pool).QueryRow(ctx,
		`SELECT `+shipmentColumns+` FROM shipments WHERE provider = $1 AND provider_order_id = $2`,
		provider, orderID)
	return scanShipment(row)
}

// GetByProviderDraftOrderID fetches a shipment by its Biteship draft order ID.
func (r *ShipmentRepository) GetByProviderDraftOrderID(ctx context.Context, provider, draftOrderID string) (*domain.Shipment, error) {
	row := dbFromContext(ctx, r.pool).QueryRow(ctx,
		`SELECT `+shipmentColumns+` FROM shipments WHERE provider = $1 AND provider_draft_order_id = $2`,
		provider, draftOrderID)
	return scanShipment(row)
}

// ─── column list & scanner ────────────────────────────────────────────────────

const shipmentColumns = `
	id::text, tenant_id, order_number, provider,
	provider_draft_order_id, provider_order_id,
	courier_code, courier_service_code,
	tracking_number, tracking_url,
	shipping_cost, status,
	confirmed_at, picked_up_at, delivered_at,
	created_at, updated_at`

func scanShipment(row pgx.Row) (*domain.Shipment, error) {
	var (
		s      domain.Shipment
		idStr  string
		status string
	)
	err := row.Scan(
		&idStr, &s.TenantID, &s.OrderNumber, &s.Provider,
		&s.ProviderDraftOrderID, &s.ProviderOrderID,
		&s.CourierCode, &s.CourierServiceCode,
		&s.TrackingNumber, &s.TrackingURL,
		&s.ShippingCost, &status,
		&s.ConfirmedAt, &s.PickedUpAt, &s.DeliveredAt,
		&s.CreatedAt, &s.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("scan shipment: %w", err)
	}

	s.ID, _ = uuid.Parse(idStr)
	s.Status = domain.ShipmentStatus(status)
	return &s, nil
}

func collectShipments(rows pgx.Rows) ([]*domain.Shipment, error) {
	var result []*domain.Shipment
	for rows.Next() {
		s, err := scanShipment(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, s)
	}
	return result, rows.Err()
}
