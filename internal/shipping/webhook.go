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

// ShipmentWebhookForwarder forwards raw Biteship webhook payloads to an upstream system.
type ShipmentWebhookForwarder interface {
	ForwardShipmentWebhook(ctx context.Context, payload []byte) error
}

// HoldManager confirms or releases a shipping hold in response to shipment status changes.
type HoldManager interface {
	// ConfirmHoldForOrder confirms the active hold for the order, consuming the reserved funds.
	ConfirmHoldForOrder(ctx context.Context, tenantID int64, orderNumber string) error
	// ReleaseHoldForOrder releases the active hold for the order, returning funds to available.
	ReleaseHoldForOrder(ctx context.Context, tenantID int64, orderNumber string) error
}

type webhookController struct {
	signatureKey   string
	signatureValue string
	repo           *repository.ShipmentRepository // may be nil
	forwarder      ShipmentWebhookForwarder       // may be nil
	holds          HoldManager                    // may be nil
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
	holds HoldManager,
	logger *log.Logger,
) {
	ctrl := webhookController{
		signatureKey:   signatureKey,
		signatureValue: signatureValue,
		repo:           repo,
		forwarder:      forwarder,
		holds:          holds,
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

	// Process the event and update DB.
	if ctrl.repo != nil {
		if err := ctrl.processEvent(c.Context(), eventType, body); err != nil {
			ctrl.logger.WithError(err).WithField("event", eventType).Error("failed to process biteship webhook")
		}
	}

	// Forward to Tokokarya fire-and-forget.
	if ctrl.forwarder != nil {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := ctrl.forwarder.ForwardShipmentWebhook(ctx, body); err != nil {
				ctrl.logger.WithError(err).Error("failed to forward biteship webhook")
			}
		}()
	}

	return c.Status(http.StatusOK).JSON(common.Response{Status: "OK"})
}

// ─── Event handlers ───────────────────────────────────────────────────────────

func (ctrl *webhookController) processEvent(ctx context.Context, eventType string, body []byte) error {
	switch eventType {
	case "order.status":
		return ctrl.handleOrderStatus(ctx, body)
	case "order.price":
		return ctrl.handleOrderPrice(ctx, body)
	case "order.waybill_id":
		return ctrl.handleOrderWaybillID(ctx, body)
	default:
		return nil // unknown event types are ignored
	}
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

func (ctrl *webhookController) handleOrderStatus(ctx context.Context, body []byte) error {
	var evt orderStatusEvent
	if err := json.Unmarshal(body, &evt); err != nil {
		return fmt.Errorf("unmarshal order.status: %w", err)
	}

	s, err := ctrl.findShipment(ctx, evt.OrderID)
	if err != nil {
		return err
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
		return err
	}

	// Automatically manage the shipping hold when the order status changes.
	if ctrl.holds != nil && s.OrderNumber != "" {
		switch evt.Status {
		case "confirmed", "scheduled":
			if err := ctrl.holds.ConfirmHoldForOrder(ctx, s.TenantID, s.OrderNumber); err != nil {
				ctrl.logger.WithError(err).WithFields(log.Fields{
					"order_number": s.OrderNumber,
					"tenant_id":    s.TenantID,
				}).Error("failed to confirm shipping hold on order confirmed")
			}
		case "cancelled":
			if err := ctrl.holds.ReleaseHoldForOrder(ctx, s.TenantID, s.OrderNumber); err != nil {
				ctrl.logger.WithError(err).WithFields(log.Fields{
					"order_number": s.OrderNumber,
					"tenant_id":    s.TenantID,
				}).Error("failed to release shipping hold on order cancelled")
			}
		}
	}

	return nil
}

func (ctrl *webhookController) handleOrderPrice(ctx context.Context, body []byte) error {
	var evt orderPriceEvent
	if err := json.Unmarshal(body, &evt); err != nil {
		return fmt.Errorf("unmarshal order.price: %w", err)
	}

	s, err := ctrl.findShipment(ctx, evt.OrderID)
	if err != nil {
		return err
	}

	s.ShippingCost = evt.Price
	s.Status = biteship.MapStatus(evt.Status)

	if s.ProviderOrderID == nil || *s.ProviderOrderID == "" {
		s.ProviderOrderID = &evt.OrderID
	}

	return ctrl.repo.Update(ctx, s)
}

func (ctrl *webhookController) handleOrderWaybillID(ctx context.Context, body []byte) error {
	var evt orderWaybillIDEvent
	if err := json.Unmarshal(body, &evt); err != nil {
		return fmt.Errorf("unmarshal order.waybill_id: %w", err)
	}

	s, err := ctrl.findShipment(ctx, evt.OrderID)
	if err != nil {
		return err
	}

	s.TrackingNumber = evt.CourierWaybillID
	s.Status = biteship.MapStatus(evt.Status)

	if s.ProviderOrderID == nil || *s.ProviderOrderID == "" {
		s.ProviderOrderID = &evt.OrderID
	}

	return ctrl.repo.Update(ctx, s)
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
