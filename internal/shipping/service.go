package shipping

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/dengankarya/connector/common"
	"github.com/dengankarya/connector/internal/shipping/domain"
	"github.com/dengankarya/connector/internal/shipping/provider"
	"github.com/dengankarya/connector/internal/shipping/repository"
	"github.com/dengankarya/connector/pkg/biteship"
	"github.com/google/uuid"
)

// ErrInvalidCursor is returned when the cursor query parameter cannot be decoded.
var ErrInvalidCursor = common.NewDomainError("BR_INVALID_CURSOR", "invalid pagination cursor")

type LogisticAggregator interface {
	GetCourierList(ctx context.Context, couriers []string) ([]biteship.Courier, error)
}

// BalanceValidator validates and deducts the merchant's shipping balance around a shipment confirmation.
type BalanceValidator interface {
	// ValidateShippingConfirm returns an error (including account.ErrInsufficientBalance) when
	// the merchant cannot cover the shipment cost. Returns nil when funds are available.
	ValidateShippingConfirm(ctx context.Context, tenantID int64, orderNumber string, requiredAmount int64) error
	// ConfirmHoldForOrder transitions an active shipping hold to confirmed, consuming the reserved funds.
	// No-op when no active hold exists for the order.
	ConfirmHoldForOrder(ctx context.Context, tenantID int64, orderNumber string) error
}

type ShippingService struct {
	repo             LogisticAggregator
	provider         provider.ShippingProvider
	shipmentRepo     *repository.ShipmentRepository // may be nil when DB is not configured
	balanceValidator BalanceValidator               // may be nil when account module is disabled
}

func NewShippingService(repo LogisticAggregator, prov provider.ShippingProvider, shipmentRepo *repository.ShipmentRepository, balanceValidator BalanceValidator) *ShippingService {
	return &ShippingService{repo: repo, provider: prov, shipmentRepo: shipmentRepo, balanceValidator: balanceValidator}
}

func (s *ShippingService) GetCourierList(ctx context.Context, couriers []string) ([]biteship.Courier, error) {
	return s.repo.GetCourierList(ctx, couriers)
}

// GetCourierRates fetches available courier rates from the provider.
// Used by the frontend to display pricing options before creating a draft order.
func (s *ShippingService) GetCourierRates(ctx context.Context, req provider.GetRatesRequest) (*provider.GetRatesResult, error) {
	return s.provider.GetRates(ctx, req)
}

// GetShipment returns a shipment by ID, enforcing tenant isolation.
func (s *ShippingService) GetShipment(ctx context.Context, tenantID int64, id uuid.UUID) (*domain.Shipment, error) {
	if s.shipmentRepo == nil {
		return nil, domain.ErrNotFound
	}
	shipment, err := s.shipmentRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if shipment.TenantID != tenantID {
		return nil, domain.ErrNotFound
	}
	return shipment, nil
}

// ConfirmShipment promotes a draft shipment to a live order at the provider and updates the DB record.
// Idempotent: if the shipment is already past draft status, returns the current record immediately
// without calling the provider again.
func (s *ShippingService) ConfirmShipment(ctx context.Context, tenantID int64, id uuid.UUID) (*domain.Shipment, error) {
	if s.shipmentRepo == nil {
		return nil, domain.ErrNotFound
	}

	shipment, err := s.shipmentRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if shipment.TenantID != tenantID {
		return nil, domain.ErrNotFound
	}
	if shipment.ProviderDraftOrderID == nil || *shipment.ProviderDraftOrderID == "" {
		return nil, fmt.Errorf("shipment %s has no draft order ID", id)
	}

	// Idempotency: if the shipment is already confirmed (or further along), skip the
	// provider call. Biteship returns an error for already-confirmed draft orders, which
	// would otherwise surface as a 500 on retries.
	if shipment.Status != domain.ShipmentStatusDraft {
		return shipment, nil
	}

	if s.balanceValidator != nil {
		if err := s.balanceValidator.ValidateShippingConfirm(ctx, tenantID, shipment.OrderNumber, shipment.ShippingCost); err != nil {
			return nil, err
		}
	}

	updated, err := s.provider.ConfirmShipment(ctx, *shipment.ProviderDraftOrderID)
	if err != nil {
		return nil, err
	}

	// Merge provider response into the existing record.
	shipment.ProviderOrderID = updated.ProviderOrderID
	shipment.Status = updated.Status
	shipment.ShippingCost = updated.ShippingCost
	shipment.TrackingNumber = updated.TrackingNumber
	shipment.TrackingURL = updated.TrackingURL
	if updated.ConfirmedAt != nil {
		shipment.ConfirmedAt = updated.ConfirmedAt
	}

	if err := s.shipmentRepo.Update(ctx, shipment); err != nil {
		return nil, fmt.Errorf("confirm shipment: update record: %w", err)
	}

	// Consume the shipping hold (if one was reserved for this order) now that
	// the provider has confirmed pickup. No-op when no active hold exists.
	if s.balanceValidator != nil {
		if err := s.balanceValidator.ConfirmHoldForOrder(ctx, tenantID, shipment.OrderNumber); err != nil {
			// Non-fatal: shipment is confirmed; log but don't fail the request.
			// The hold will stay in "holding" and can be reconciled manually.
			_ = err
		}
	}

	return shipment, nil
}

func (s *ShippingService) CreateShipment(ctx context.Context, tenantID int64, req provider.CreateShipmentRequest) (*domain.Shipment, error) {
	shipment, err := s.provider.CreateShipment(ctx, req)
	if err != nil {
		return nil, err
	}

	if s.shipmentRepo != nil {
		shipment.TenantID = tenantID
		shipment.OrderNumber = req.ReferenceID
		if err := s.shipmentRepo.Create(ctx, shipment); err != nil {
			return nil, err
		}
	}

	return shipment, nil
}

// ListShipmentsRequest is the input for paginated shipment listing.
type ListShipmentsRequest struct {
	TenantID int64
	// Limit is the page size (default 20, max 100).
	Limit int
	// Cursor is the opaque pagination token returned by the previous call; empty for the first page.
	Cursor string
	// Status filters by shipment status; empty means all statuses.
	Status []domain.ShipmentStatus
}

// ListShipmentsResult is the response for paginated shipment listing.
type ListShipmentsResult struct {
	Items      []*domain.Shipment
	NextCursor string // empty when there are no more pages
	HasMore    bool
}

// ListShipments returns a cursor-paginated list of shipments for a tenant.
func (s *ShippingService) ListShipments(ctx context.Context, req ListShipmentsRequest) (*ListShipmentsResult, error) {
	if s.shipmentRepo == nil {
		return &ListShipmentsResult{}, nil
	}

	limit := req.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	p := repository.ListParams{
		Limit:  limit + 1,
		Status: req.Status,
	}

	if req.Cursor != "" {
		cp, err := decodeShipmentCursor(req.Cursor)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidCursor, err)
		}
		p.Cursor = cp
	}

	shipments, err := s.shipmentRepo.ListByTenant(ctx, req.TenantID, p)
	if err != nil {
		return nil, fmt.Errorf("list shipments: %w", err)
	}

	result := &ListShipmentsResult{}
	if len(shipments) > limit {
		result.HasMore = true
		result.Items = shipments[:limit]
		last := shipments[limit-1]
		result.NextCursor = encodeShipmentCursor(last.CreatedAt, last.ID)
	} else {
		result.Items = shipments
	}

	return result, nil
}

// ─── cursor encoding ──────────────────────────────────────────────────────────

type shipmentCursorPayload struct {
	T time.Time `json:"t"`
	I string    `json:"i"`
}

func encodeShipmentCursor(createdAt time.Time, id uuid.UUID) string {
	b, _ := json.Marshal(shipmentCursorPayload{T: createdAt.UTC(), I: id.String()})
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeShipmentCursor(s string) (*repository.CursorPoint, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, err
	}
	var p shipmentCursorPayload
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, err
	}
	id, err := uuid.Parse(p.I)
	if err != nil {
		return nil, fmt.Errorf("invalid id in cursor: %w", err)
	}
	return &repository.CursorPoint{CreatedAt: p.T, ID: id}, nil
}
