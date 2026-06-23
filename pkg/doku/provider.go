package doku

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/dengankarya/connector/internal/payment/provider"
)

// wib is UTC+7 (Jakarta/WIB), which DOKU uses for expired_date in checkout responses.
var wib = time.FixedZone("WIB", 7*60*60)

func (c *Client) ProviderName() string { return "doku" }

// CreateInvoice calls the DOKU Checkout API and returns the session as a provider.Invoice.
//
// ProviderInvoiceID is set to invoiceNumber (= req.ExternalID / idempotency key) — NOT the
// DOKU session_id — because DOKU's notification payload only sends back order.invoice_number.
// Using invoice_number ensures the webhook processor can look up the transaction by
// ProviderInvoiceID when the notification arrives.
func (c *Client) CreateInvoice(ctx context.Context, req provider.CreateInvoiceRequest) (*provider.Invoice, error) {
	var dueMins int
	if req.ExpiresAt != nil {
		if d := time.Until(*req.ExpiresAt); d > 0 {
			dueMins = int(d.Minutes())
		}
	}

	invoiceNumber := req.ExternalID
	if len(invoiceNumber) > 64 {
		invoiceNumber = invoiceNumber[:64]
	}

	result, err := c.CreateCheckout(ctx,
		invoiceNumber,
		req.Amount,
		dueMins,
		req.CustomerName,
		req.CustomerEmail,
		req.CustomerMobile,
	)
	if err != nil {
		return nil, fmt.Errorf("doku checkout: %w", err)
	}

	inv := &provider.Invoice{
		ProviderInvoiceID: invoiceNumber,
		CheckoutURL:       result.PaymentURL,
		Status:            "pending",
		Amount:            req.Amount,
		Currency:          req.Currency,
	}

	// DOKU returns expired_date as "yyyyMMddHHmmss" in WIB (UTC+7).
	if result.ExpiredDate != "" {
		if t, err := time.ParseInLocation("20060102150405", result.ExpiredDate, wib); err == nil {
			utc := t.UTC()
			inv.ExpiresAt = &utc
		}
	}

	return inv, nil
}

// dokuNotification is the JSON structure DOKU sends to our notification URL.
// Field names match the DOKU non-SNAP notification format.
type dokuNotification struct {
	Channel struct {
		ID string `json:"id"` // e.g. "VIRTUAL_ACCOUNT_BCA", "CREDIT_CARD", "QRIS"
	} `json:"channel"`
	Transaction struct {
		Status            string `json:"status"`              // "SUCCESS" | "FAILED" | "EXPIRED"
		Date              string `json:"date"`                // ISO 8601 UTC
		OriginalRequestID string `json:"original_request_id"` // unique event ID
	} `json:"transaction"`
	Order struct {
		InvoiceNumber string `json:"invoice_number"` // matches what we sent as invoice_number
		Amount        int64  `json:"amount"`
	} `json:"order"`
}

// ParseWebhookEvent parses a DOKU notification and returns a provider-agnostic event.
// DOKU status values are normalised to the event type strings the webhook processor handles.
func (c *Client) ParseWebhookEvent(_ context.Context, payload []byte) (*provider.WebhookEvent, error) {
	var notif dokuNotification
	if err := json.Unmarshal(payload, &notif); err != nil {
		return nil, fmt.Errorf("doku: parse notification: %w", err)
	}
	if notif.Order.InvoiceNumber == "" {
		return nil, fmt.Errorf("doku: notification missing order.invoice_number")
	}

	// Normalise DOKU transaction.status → processor event types.
	eventType := "payment_session.completed"
	switch strings.ToUpper(notif.Transaction.Status) {
	case "SUCCESS":
		eventType = "payment_session.completed"
	case "FAILED":
		eventType = "payment_session.failed"
	case "EXPIRED":
		eventType = "payment_session.expired"
	}

	// Use original_request_id as the unique event ID; fall back to a composite key.
	eventID := notif.Transaction.OriginalRequestID
	if eventID == "" {
		eventID = notif.Order.InvoiceNumber + ":" + notif.Transaction.Status
	}

	return &provider.WebhookEvent{
		ProviderEventID:   eventID,
		EventType:         eventType,
		ProviderInvoiceID: notif.Order.InvoiceNumber,
		Amount:            notif.Order.Amount,
		ChannelCode:       notif.Channel.ID,
		RawPayload:        payload,
	}, nil
}

// ValidateWebhookSignature verifies the HMAC-SHA256 signature DOKU attaches to notifications.
// The headers map must include "Client-Id", "Request-Id", "Request-Timestamp", "Signature",
// and "Request-Target" (the path of our webhook endpoint, injected by ingest.go via c.Path()).
// If the Signature header is absent the notification is accepted without verification —
// some DOKU configurations omit it.
func (c *Client) ValidateWebhookSignature(_ context.Context, payload []byte, headers map[string]string) error {
	sig := headers["Signature"]
	if sig == "" {
		return nil
	}

	expected := SignRequest(
		c.clientID,
		headers["Request-Id"],
		headers["Request-Timestamp"],
		headers["Request-Target"],
		c.secretKey,
		payload,
	)
	if sig != expected {
		return fmt.Errorf("doku: webhook signature mismatch")
	}
	return nil
}

func (c *Client) GetInvoice(_ context.Context, _ string) (*provider.Invoice, error) {
	return nil, fmt.Errorf("doku: GetInvoice not yet implemented")
}

func (c *Client) CancelInvoice(_ context.Context, _ string) error {
	return fmt.Errorf("doku: CancelInvoice not yet implemented")
}

func (c *Client) CreateRefund(_ context.Context, _ provider.CreateRefundRequest) (*provider.Refund, error) {
	return nil, fmt.Errorf("doku: CreateRefund not yet implemented")
}

func (c *Client) CreatePayout(_ context.Context, _ provider.CreatePayoutRequest) (*provider.Payout, error) {
	return nil, fmt.Errorf("doku: CreatePayout not yet implemented")
}
