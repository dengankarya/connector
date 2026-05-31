package domain

import (
	"time"

	"github.com/dengankarya/connector/common"
	"github.com/google/uuid"
)

var ErrNotFound = common.NewDomainError("NF_SHIPMENT_NOT_FOUND", "shipment not found")

type ShipmentStatus string

const (
	ShipmentStatusDraft           ShipmentStatus = "draft"
	ShipmentStatusWaitingPickup   ShipmentStatus = "waiting_pickup"
	ShipmentStatusCourierAssigned ShipmentStatus = "courier_assigned"
	ShipmentStatusPickedUp        ShipmentStatus = "picked_up"
	ShipmentStatusInTransit       ShipmentStatus = "in_transit"
	ShipmentStatusOutForDelivery  ShipmentStatus = "out_for_delivery"
	ShipmentStatusDelivered       ShipmentStatus = "delivered"

	ShipmentStatusCancelled ShipmentStatus = "cancelled"
	ShipmentStatusOnHold    ShipmentStatus = "on_hold"
	ShipmentStatusReturning ShipmentStatus = "returning"
	ShipmentStatusReturned  ShipmentStatus = "returned"
	ShipmentStatusFailed    ShipmentStatus = "failed"
)

type Shipment struct {
	ID uuid.UUID `json:"id,omitempty"`

	// TenantID is the merchant/tenant that owns this shipment.
	TenantID int64 `json:"tenant_id,omitempty"`

	// OrderNumber is the tokokarya's order number.
	OrderNumber string `json:"order_number,omitempty"`

	Provider             string  `json:"provider,omitempty"` // Current default is `biteship`.
	ProviderDraftOrderID *string `json:"provider_draft_order_id,omitempty"`
	ProviderOrderID      *string `json:"provider_order_id,omitempty"`

	CourierCode        string `json:"courier_code,omitempty"`
	CourierServiceCode string `json:"courier_service_code,omitempty"`
	TrackingNumber     string `json:"tracking_number,omitempty"`
	TrackingURL        string `json:"tracking_url,omitempty"`

	ShippingCost int64          `json:"shipping_cost,omitempty"`
	Status       ShipmentStatus `json:"status,omitempty"`

	ConfirmedAt *time.Time `json:"confirmed_at,omitempty"`
	PickedUpAt  *time.Time `json:"picked_up_at,omitempty"`
	DeliveredAt *time.Time `json:"delivered_at,omitempty"`

	CreatedAt time.Time `json:"created_at,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}
