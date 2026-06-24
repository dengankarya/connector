package shipping

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/dengankarya/connector/common"
	"github.com/dengankarya/connector/internal/shipping/domain"
	"github.com/dengankarya/connector/internal/shipping/repository"
	"github.com/dengankarya/connector/pkg/biteship"
	"github.com/gofiber/fiber/v3"
	log "github.com/sirupsen/logrus"
)

// ShipmentWebhookForwarder forwards a normalized shipment event to an upstream system.
// The payload is connector-owned JSON (our internal IDs, mapped statuses) — not raw Biteship.
type ShipmentWebhookForwarder interface {
	ForwardShipmentWebhook(ctx context.Context, payload []byte) error
}

// ShipmentWebhookPayload is the normalized event forwarded to Tokokarya.
// It contains only our internal IDs and status — no Biteship-specific identifiers.
type ShipmentWebhookPayload struct {
	// ShipmentID is the connector's internal UUID — the same ID Tokokarya received
	// when the shipment was created. Use this to match the order on the Tokokarya side.
	ShipmentID  string `json:"shipment_id"`
	OrderNumber string `json:"order_number"`
	// Status is the connector's mapped status (e.g. "waiting_pickup", "in_transit", "delivered").
	Status         string    `json:"status"`
	TrackingNumber string    `json:"tracking_number,omitempty"`
	TrackingURL    string    `json:"tracking_url,omitempty"`
	ShippingCost   int64     `json:"shipping_cost,omitempty"`
	OldPrice       int64     `json:"old_price,omitempty"` // for order.price events
	PriceDiff      int64     `json:"price_diff,omitempty"` // new_price - old_price
	Event          string    `json:"event"` // "order.status" | "order.price" | "order.waybill_id"
	UpdatedAt      time.Time `json:"updated_at"`
}

type webhookController struct {
	signatureKey   string
	signatureValue string
	repo           *repository.ShipmentRepository // may be nil
	forwarder      ShipmentWebhookForwarder       // may be nil
	accountManager domain.AccountManager           // may be nil
	logger         *log.Logger
}

// RegisterWebhookHandler registers the public Biteship webhook endpoint on the root app
// (before the authenticated middleware). Biteship authenticates via a configurable
// header key/value pair set in the dashboard.
func RegisterWebhookHandler(
	app fiber.Router,
	signatureKey, signatureValue string,
	repo *repository.ShipmentRepository,
	forwarder ShipmentWebhookForwarder,
	accountManager domain.AccountManager,
	logger *log.Logger,
) {
	ctrl := webhookController{
		signatureKey:   signatureKey,
		signatureValue: signatureValue,
		repo:           repo,
		forwarder:      forwarder,
		accountManager: accountManager,
		logger:         logger,
	}
	app.Post("/webhook/biteship", ctrl.handleWebhook)
}

// handleWebhook godoc
//
//	@Summary		Biteship webhook
//	@Description	Receives Biteship shipment status update callbacks. Authenticated via a configurable header key/value pair set in the Biteship dashboard.
//	@Tags			Webhooks
//	@Accept			json
//	@Produce		json
//	@Success		200	{object}	common.Response	"OK"
//	@Router			/webhook/biteship [post]
func (ctrl *webhookController) handleWebhook(c fiber.Ctx) error {
	// Validate signature header when configured.
	if ctrl.signatureKey != "" && ctrl.signatureValue != "" {
		if c.Get(ctrl.signatureKey) != ctrl.signatureValue {
			ctrl.logger.Warn("invalid biteship webhook signature")
			return c.Status(http.StatusOK).JSON(common.Response{Status: "OK"})
		}
	}

	body := make([]byte, len(c.Body()))
	copy(body, c.Body())

	eventType, err := parseWebhookEventType(body)
	if err != nil {
		ctrl.logger.WithError(err).Error("failed to parse biteship webhook event type")
		return c.Status(http.StatusOK).JSON(common.Response{Status: "OK"})
	}

	ctrl.logger.WithField("event", eventType).Info("received biteship webhook")

	// Process event, update DB, and get the updated shipment back.
	var updated *domain.Shipment
	var oldPrice int64 // Track old price for price adjustment events
	if ctrl.repo != nil {
		updated, oldPrice, err = ctrl.processEventWithOldPrice(c.Context(), eventType, body)
		if err != nil {
			ctrl.logger.WithError(err).WithField("event", eventType).Error("failed to process biteship webhook")
		}
	}

	// Forward normalized payload to Tokokarya (fire-and-forget).
	// Only forward when we successfully found and updated the shipment.
	if ctrl.forwarder != nil && updated != nil {
		payload, merr := buildForwardPayload(updated, eventType, oldPrice)
		if merr != nil {
			ctrl.logger.WithError(merr).Error("failed to build biteship forward payload")
		} else {
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				if err := ctrl.forwarder.ForwardShipmentWebhook(ctx, payload); err != nil {
					ctrl.logger.WithError(err).Error("failed to forward biteship webhook to tokokarya")
				}
			}()
		}
	}

	return c.Status(http.StatusOK).JSON(common.Response{Status: "OK"})
}

// ─── Event handlers ───────────────────────────────────────────────────────────

// processEvent routes the event to the appropriate handler and returns the updated shipment.
// Returns (nil, nil) for unknown event types.
func (ctrl *webhookController) processEvent(ctx context.Context, eventType string, body []byte) (*domain.Shipment, error) {
	switch eventType {
	case "order.status":
		return ctrl.handleOrderStatus(ctx, body)
	case "order.price":
		return ctrl.handleOrderPrice(ctx, body)
	case "order.waybill_id":
		return ctrl.handleOrderWaybillID(ctx, body)
	default:
		return nil, nil // unknown event types are ignored
	}
}

// processEventWithOldPrice is like processEvent but also returns the old shipping cost
// (used for price adjustment events to show merchants the difference).
func (ctrl *webhookController) processEventWithOldPrice(ctx context.Context, eventType string, body []byte) (*domain.Shipment, int64, error) {
	if eventType == "order.price" {
		return ctrl.handleOrderPriceWithOldPrice(ctx, body)
	}
	// For other events, call regular processEvent
	s, err := ctrl.processEvent(ctx, eventType, body)
	return s, 0, err
}

// orderStatusEvent is the payload for the order.status webhook.
type orderStatusEvent struct {
	Event             string `json:"event"`
	CourierTrackingID string `json:"courier_tracking_id"`
	CourierWaybillID  string `json:"courier_waybill_id"`
	CourierCompany    string `json:"courier_company"`
	CourierType       string `json:"courier_type"`
	CourierLink       string `json:"courier_link"`
	OrderID           string `json:"order_id"`
	OrderPrice        int64  `json:"order_price"`
	Status            string `json:"status"`
}

// orderPriceEvent is the payload for the order.price webhook.
type orderPriceEvent struct {
	Event             string `json:"event"`
	CourierTrackingID string `json:"courier_tracking_id"`
	CourierWaybillID  string `json:"courier_waybill_id"`
	OrderID           string `json:"order_id"`
	Price             int64  `json:"price"`
	ShipmentFee       int64  `json:"shippment_fee"`
	Status            string `json:"status"`
}

// orderWaybillIDEvent is the payload for the order.waybill_id webhook.
type orderWaybillIDEvent struct {
	Event             string `json:"event"`
	OrderID           string `json:"order_id"`
	CourierTrackingID string `json:"courier_tracking_id"`
	CourierWaybillID  string `json:"courier_waybill_id"`
	Status            string `json:"status"`
}

func (ctrl *webhookController) handleOrderStatus(ctx context.Context, body []byte) (*domain.Shipment, error) {
	var evt orderStatusEvent
	if err := json.Unmarshal(body, &evt); err != nil {
		return nil, fmt.Errorf("unmarshal order.status: %w", err)
	}

	s, err := ctrl.findShipment(ctx, evt.OrderID)
	if err != nil {
		return nil, err
	}

	s.Status = biteship.MapStatus(evt.Status)
	s.TrackingNumber = evt.CourierWaybillID
	s.TrackingURL = evt.CourierLink

	// Backfill provider_order_id if the shipment was only found via draft ID.
	if s.ProviderOrderID == nil || *s.ProviderOrderID == "" {
		s.ProviderOrderID = &evt.OrderID
	}

	now := time.Now().UTC()
	switch evt.Status {
	case "confirmed", "scheduled":
		if s.ConfirmedAt == nil {
			s.ConfirmedAt = &now
		}
	case "picked":
		if s.PickedUpAt == nil {
			s.PickedUpAt = &now
		}
	case "delivered":
		if s.DeliveredAt == nil {
			s.DeliveredAt = &now
		}
	}

	if err := ctrl.repo.Update(ctx, s); err != nil {
		return nil, err
	}

	// Automatically manage the shipping hold when the order status changes.
	if ctrl.accountManager != nil && s.OrderNumber != "" {
		switch evt.Status {
		case "confirmed", "scheduled":
			if err := ctrl.accountManager.ConfirmHoldForOrder(ctx, s.TenantID, s.OrderNumber, s.ShippingCost); err != nil {
				ctrl.logger.WithError(err).WithFields(log.Fields{
					"order_number": s.OrderNumber,
					"tenant_id":    s.TenantID,
				}).Error("failed to confirm shipping hold on order confirmed")
			}
		case "cancelled":
			if err := ctrl.accountManager.ReleaseHoldForOrder(ctx, s.TenantID, s.OrderNumber); err != nil {
				ctrl.logger.WithError(err).WithFields(log.Fields{
					"order_number": s.OrderNumber,
					"tenant_id":    s.TenantID,
				}).Error("failed to release shipping hold on order cancelled")
			}
		}
	}

	return s, nil
}

func (ctrl *webhookController) handleOrderPrice(ctx context.Context, body []byte) (*domain.Shipment, error) {
	s, _, err := ctrl.handleOrderPriceWithOldPrice(ctx, body)
	return s, err
}

func (ctrl *webhookController) handleOrderPriceWithOldPrice(ctx context.Context, body []byte) (*domain.Shipment, int64, error) {
	var evt orderPriceEvent
	if err := json.Unmarshal(body, &evt); err != nil {
		return nil, 0, fmt.Errorf("unmarshal order.price: %w", err)
	}

	s, err := ctrl.findShipment(ctx, evt.OrderID)
	if err != nil {
		return nil, 0, err
	}

	oldPrice := s.ShippingCost

	s.ShippingCost = evt.Price
	s.Status = biteship.MapStatus(evt.Status)

	if s.ProviderOrderID == nil || *s.ProviderOrderID == "" {
		s.ProviderOrderID = &evt.OrderID
	}

	if err := ctrl.repo.Update(ctx, s); err != nil {
		return nil, 0, err
	}

	// Auto-deduct price difference from merchant balance if price increased.
	// Biteship has already charged the customer, so we need to reconcile the merchant's balance.
	// Only deduct when price increased and shipment is confirmed (not draft).
	if ctrl.accountManager != nil && evt.Price > oldPrice && s.Status != domain.ShipmentStatusDraft {
		if err := ctrl.accountManager.AdjustShippingBalance(ctx, s.TenantID, oldPrice, evt.Price, "IDR", s.OrderNumber); err != nil {
			ctrl.logger.WithError(err).WithFields(log.Fields{
				"tenant_id":    s.TenantID,
				"order_number": s.OrderNumber,
				"old_price":    oldPrice,
				"new_price":    evt.Price,
			}).Error("failed to adjust shipping balance on price update")
		}
	}

	return s, oldPrice, nil
}

func (ctrl *webhookController) handleOrderWaybillID(ctx context.Context, body []byte) (*domain.Shipment, error) {
	var evt orderWaybillIDEvent
	if err := json.Unmarshal(body, &evt); err != nil {
		return nil, fmt.Errorf("unmarshal order.waybill_id: %w", err)
	}

	s, err := ctrl.findShipment(ctx, evt.OrderID)
	if err != nil {
		return nil, err
	}

	s.TrackingNumber = evt.CourierWaybillID
	s.Status = biteship.MapStatus(evt.Status)

	if s.ProviderOrderID == nil || *s.ProviderOrderID == "" {
		s.ProviderOrderID = &evt.OrderID
	}

	if err := ctrl.repo.Update(ctx, s); err != nil {
		return nil, err
	}
	return s, nil
}

// findShipment looks up a shipment by Biteship order_id, falling back to draft order ID.
func (ctrl *webhookController) findShipment(ctx context.Context, orderID string) (*domain.Shipment, error) {
	s, err := ctrl.repo.GetByProviderOrderID(ctx, "biteship", orderID)
	if err == nil {
		return s, nil
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return nil, fmt.Errorf("lookup shipment by order_id %q: %w", orderID, err)
	}

	// Fall back: the webhook may have fired before we stored the order_id
	// (draft order not yet confirmed). Try the draft order ID.
	s, err = ctrl.repo.GetByProviderDraftOrderID(ctx, "biteship", orderID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, fmt.Errorf("shipment not found for order_id %q", orderID)
		}
		return nil, fmt.Errorf("lookup shipment by draft_order_id %q: %w", orderID, err)
	}
	return s, nil
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

// buildForwardPayload builds the normalized payload sent to Tokokarya.
// It uses our internal IDs and mapped statuses — no Biteship-specific identifiers.
// For price adjustment events, it includes the old price and diff so FE can show merchant the change.
func buildForwardPayload(s *domain.Shipment, eventType string, oldPrice int64) ([]byte, error) {
	p := ShipmentWebhookPayload{
		ShipmentID:     s.ID.String(),
		OrderNumber:    s.OrderNumber,
		Status:         string(s.Status),
		TrackingNumber: s.TrackingNumber,
		TrackingURL:    s.TrackingURL,
		ShippingCost:   s.ShippingCost,
		Event:          eventType,
		UpdatedAt:      s.UpdatedAt,
	}

	// For price adjustment events, include the old price and diff so FE can show merchant.
	if eventType == "order.price" && oldPrice > 0 && oldPrice != s.ShippingCost {
		p.OldPrice = oldPrice
		p.PriceDiff = s.ShippingCost - oldPrice
	}

	return json.Marshal(p)
}

func parseWebhookEventType(payload []byte) (string, error) {
	var base struct {
		Event string `json:"event"`
	}
	if err := json.Unmarshal(payload, &base); err != nil {
		return "", fmt.Errorf("parse biteship webhook event: %w", err)
	}
	if base.Event == "" {
		return "", fmt.Errorf("parse biteship webhook event: missing event field")
	}
	return base.Event, nil
}
