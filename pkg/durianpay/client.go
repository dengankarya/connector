// Package durianpay implements the PaymentProvider interface for DurianPay Direct Charge API.
// Auth: Basic Auth with API key as username and empty password.
// Supported methods: EWALLET (OVO, GOPAY, DANA, SHOPEEPAY, LINKAJA), VA, QRIS.
package durianpay

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/dengankarya/connector/internal/payment/provider"
	"github.com/sirupsen/logrus"
)

const defaultBaseURL = "https://api.durianpay.id"

// Client is a DurianPay API client using Basic Auth.
type Client struct {
	apiKey  string
	baseURL string
	http    *http.Client
	logger  *logrus.Logger
}

// NewClient creates a DurianPay client.
// apiKey is used as the Basic Auth username (empty password).
// baseURL defaults to https://api.durianpay.id if empty.
func NewClient(apiKey, baseURL string, logger *logrus.Logger) *Client {
	if logger == nil {
		logger = logrus.New()
	}
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		apiKey:  apiKey,
		baseURL: baseURL,
		http:    &http.Client{Timeout: 30 * time.Second},
		logger:  logger,
	}
}

func (c *Client) ProviderName() string { return "durianpay" }

// ── DurianPay order & charge DTOs ──────────────────────────────────────────

type createOrderRequest struct {
	Amount     string        `json:"amount"`
	Currency   string        `json:"currency"`
	OrderRefID string        `json:"order_ref_id,omitempty"`
	Customer   orderCustomer `json:"customer"`
	ExpiryDate string        `json:"expiry_date,omitempty"` // RFC3339 in GMT+7
}

type orderCustomer struct {
	Email  string `json:"email,omitempty"`
	Mobile string `json:"mobile,omitempty"`
}

type createOrderResponse struct {
	ID string `json:"id"` // e.g. "ord_0dIWbuDJQ84078"
}

type chargeRequest struct {
	Type    string      `json:"type"`    // "VA" | "EWALLET" | "QRIS"
	Request chargeInner `json:"request"` // method-specific body
}

type chargeInner struct {
	OrderID string `json:"order_id"`
	// VA fields
	BankCode string `json:"bank_code,omitempty"`
	Name     string `json:"name,omitempty"`
	Amount   string `json:"amount"`
	// EWALLET fields
	Mobile     string `json:"mobile,omitempty"`
	WalletType string `json:"wallet_type,omitempty"`
}

// chargeResponse covers all three method types; only the relevant fields will be set.
type chargeResponse struct {
	Data struct {
		Type     string `json:"type"`
		Response struct {
			PaymentID      string `json:"payment_id"`
			OrderID        string `json:"order_id"`
			Status         string `json:"status"`
			ExpirationTime string `json:"expiration_time"` // ISO 8601

			// VA-specific
			AccountNumber      string         `json:"account_number,omitempty"`
			PaymentInstruction map[string]any `json:"payment_instruction,omitempty"`

			// EWALLET-specific
			CheckoutURL string `json:"checkout_url,omitempty"`
			Mobile      string `json:"mobile,omitempty"`

			// QRIS-specific
			QRString string `json:"qr_string,omitempty"` // base64 PNG
			QRCode   string `json:"qr_code,omitempty"`   // EMV string
		} `json:"response"`
	} `json:"data"`
}

// ── CreateInvoice ─────────────────────────────────────────────────────────

// CreateInvoice implements PaymentProvider.
// It performs two DurianPay API calls:
//  1. POST /v1/orders — create a DurianPay order to get order_id
//  2. POST /v1/payments/charge — charge with the specific method (VA/EWALLET/QRIS)
//
// The method and channel are read from ChannelProperties:
//
//	"method_type": "VA" | "EWALLET" | "QRIS"
//	"bank_code":   "BCA" | "BNI" | ...  (VA only)
//	"wallet_type": "OVO" | "GOPAY" | ... (EWALLET only)
//	"mobile":      "08..." (EWALLET only — customer phone)
func (c *Client) CreateInvoice(ctx context.Context, req provider.CreateInvoiceRequest) (*provider.Invoice, error) {
	// Step 1: create DurianPay order.
	amountStr := fmt.Sprintf("%d", req.Amount)

	orderReq := createOrderRequest{
		Amount:     amountStr,
		Currency:   req.Currency,
		OrderRefID: req.ExternalID,
		Customer: orderCustomer{
			Email:  req.CustomerEmail,
			Mobile: req.CustomerMobile,
		},
	}
	if req.ExpiresAt != nil {
		// DurianPay expects RFC3339 in GMT+7
		wib := time.FixedZone("WIB", 7*60*60)
		orderReq.ExpiryDate = req.ExpiresAt.In(wib).Format(time.RFC3339)
	}

	var orderResp createOrderResponse
	if err := c.post(ctx, "/v1/orders", orderReq, &orderResp); err != nil {
		return nil, fmt.Errorf("durianpay: create order: %w", err)
	}

	// Step 2: charge with the selected method.
	methodType := stringFromMap(req.ChannelProperties, "method_type")
	bankCode := stringFromMap(req.ChannelProperties, "bank_code")
	walletType := stringFromMap(req.ChannelProperties, "wallet_type")
	mobile := stringFromMap(req.ChannelProperties, "mobile")
	if mobile == "" {
		mobile = req.CustomerMobile
	}

	inner := chargeInner{
		OrderID:    orderResp.ID,
		Amount:     amountStr,
		Name:       req.CustomerName,
		BankCode:   bankCode,
		WalletType: walletType,
		Mobile:     mobile,
	}

	chargeReq := chargeRequest{
		Type:    methodType,
		Request: inner,
	}

	c.logger.WithFields(logrus.Fields{
		"method":      "CreateInvoice",
		"order_id":    orderResp.ID,
		"method_type": methodType,
		"bank_code":   bankCode,
		"wallet_type": walletType,
		"amount":      amountStr,
	}).Info("DurianPay charge request")

	var chargeResp chargeResponse
	if err := c.post(ctx, "/v1/payments/charge", chargeReq, &chargeResp); err != nil {
		return nil, fmt.Errorf("durianpay: charge %s: %w", methodType, err)
	}

	r := chargeResp.Data.Response
	inv := &provider.Invoice{
		ProviderInvoiceID:  r.PaymentID,
		Status:             r.Status,
		Amount:             req.Amount,
		Currency:           req.Currency,
		PaymentMethod:      methodType,
		PaymentChannel:     bankCode + walletType, // one will be empty
		CheckoutURL:        r.CheckoutURL,
		VANumber:           r.AccountNumber,
		PaymentInstruction: r.PaymentInstruction,
		QRString:           r.QRString,
		QRCode:             r.QRCode,
	}
	// Normalise PaymentChannel: only one of bankCode/walletType is set.
	if walletType != "" {
		inv.PaymentChannel = walletType
	} else if bankCode != "" {
		inv.PaymentChannel = bankCode
	}
	if methodType == "QRIS" {
		inv.PaymentChannel = "QRIS"
	}

	if r.ExpirationTime != "" {
		t, err := time.Parse(time.RFC3339, r.ExpirationTime)
		if err == nil {
			utc := t.UTC()
			inv.ExpiresAt = &utc
		}
	}

	return inv, nil
}

// ── Webhook ───────────────────────────────────────────────────────────────

// durianpayWebhook is the generic envelope for all DurianPay webhook events.
// "data" fields differ per event type; we extract only what we need.
type durianpayWebhook struct {
	// Event is one of:
	//   payment.completed, payment.failed, payment.expired, payment.cancelled
	//   order.created, order.completed
	//   settlement.settled
	Event      string         `json:"event"`
	Data       map[string]any `json:"data"`
	RetryCount int            `json:"retry_count"`
	Signature  string         `json:"signature,omitempty"`
}

// ParseWebhookEvent normalises a DurianPay webhook to a provider-agnostic event.
//
// Event mapping:
//   - payment.completed  → payment_session.completed  (payment confirmed)
//   - payment.failed     → payment_session.failed      (payment failed / timed out)
//   - payment.expired    → payment_session.expired     (payment window closed)
//   - payment.cancelled  → payment_session.failed      (customer-initiated cancel)
//   - settlement.settled → payment.settled             (funds disbursed — batch; one event per payment in array)
//   - order.created      → skipped (we created the order ourselves)
//   - order.completed    → skipped (redundant with payment.completed; payload has order_id not payment_id)
func (c *Client) ParseWebhookEvent(_ context.Context, payload []byte) (*provider.WebhookEvent, error) {
	var wh durianpayWebhook
	if err := json.Unmarshal(payload, &wh); err != nil {
		return nil, fmt.Errorf("durianpay: parse webhook: %w", err)
	}

	switch wh.Event {
	case "order.created":
		// We created the order ourselves; nothing to act on.
		return nil, nil
	case "order.completed":
		// Redundant with payment.completed. The payload carries order_id as data.id
		// but our ProviderInvoiceID is the payment_id — can't look up without a join.
		// payment.completed always fires first and is the canonical confirmation event.
		return nil, nil
	case "settlement.settled":
		// Batch event: one webhook covers multiple payments. The ingest handler
		// routes this to ParseSettlementEvent (called separately) rather than here.
		// Returning nil signals the ingest handler to skip normal single-tx processing.
		return nil, nil
	}

	paymentID, _ := wh.Data["id"].(string)
	if paymentID == "" {
		return nil, fmt.Errorf("durianpay: webhook event %q missing data.id", wh.Event)
	}

	// Normalise to the event type strings the webhook processor handles.
	var eventType string
	switch wh.Event {
	case "payment.completed":
		eventType = "payment_session.completed"
	case "payment.failed", "payment.cancelled":
		eventType = "payment_session.failed"
	case "payment.expired":
		eventType = "payment_session.expired"
	default:
		c.logger.WithField("event", wh.Event).Warn("durianpay: unrecognised event — skipping")
		return nil, nil
	}

	ev := &provider.WebhookEvent{
		// payment_id + event name is the idempotency key; safe against duplicate deliveries.
		ProviderEventID:   paymentID + ":" + wh.Event,
		EventType:         eventType,
		ProviderInvoiceID: paymentID,
		RawPayload:        payload,
	}

	// Derive channel code from payment_method + method-specific detail fields.
	paymentMethod, _ := wh.Data["payment_method"].(string)
	ev.PaymentMethod = paymentMethod
	switch paymentMethod {
	case "VA":
		if detail, ok := wh.Data["payment_detail"].(map[string]any); ok {
			ev.ChannelCode, _ = detail["va_bank_code"].(string)
		}
	case "EWALLET":
		if detail, ok := wh.Data["payment_detail"].(map[string]any); ok {
			ev.ChannelCode, _ = detail["wallet_type"].(string)
		}
	case "QRIS":
		ev.ChannelCode = "QRIS"
	}
	ev.PaymentChannel = ev.ChannelCode

	if paidAt, _ := wh.Data["paid_at"].(string); paidAt != "" {
		if t, err := time.Parse(time.RFC3339, paidAt); err == nil {
			utc := t.UTC()
			ev.PaidAt = &utc
		}
	}

	return ev, nil
}

// ParseSettlementEvent extracts individual payment IDs from a DurianPay settlement.settled
// webhook, which is a batch event covering multiple payments in one payload.
// Returns one provider-agnostic event per payment entry so each can be processed independently.
func (c *Client) ParseSettlementEvent(payload []byte) ([]*provider.WebhookEvent, error) {
	var wh durianpayWebhook
	if err := json.Unmarshal(payload, &wh); err != nil {
		return nil, fmt.Errorf("durianpay: parse settlement: %w", err)
	}
	if wh.Event != "settlement.settled" {
		return nil, fmt.Errorf("durianpay: ParseSettlementEvent called with non-settlement event %q", wh.Event)
	}

	settlementID, _ := wh.Data["settlement_id"].(string)

	payments, _ := wh.Data["payments"].([]any)
	var events []*provider.WebhookEvent
	for _, p := range payments {
		entry, ok := p.(map[string]any)
		if !ok {
			continue
		}
		paymentID, _ := entry["payment_id"].(string)
		if paymentID == "" {
			continue
		}
		events = append(events, &provider.WebhookEvent{
			ProviderEventID:   settlementID + ":" + paymentID,
			EventType:         "payment.settled",
			ProviderInvoiceID: paymentID,
			RawPayload:        payload,
		})
	}
	return events, nil
}

// ValidateWebhookSignature verifies the webhook is authentic by calling DurianPay's
// POST /payments/{id}/verify endpoint with the signature embedded in the payload.
// If the signature field is absent, the webhook is accepted (some event types may omit it).
func (c *Client) ValidateWebhookSignature(ctx context.Context, payload []byte, _ map[string]string) error {
	var wh struct {
		Signature string         `json:"signature"`
		Data      map[string]any `json:"data"`
	}
	if err := json.Unmarshal(payload, &wh); err != nil {
		return fmt.Errorf("durianpay: parse webhook for signature check: %w", err)
	}

	// No signature in payload — accept it (some event types omit it).
	if wh.Signature == "" {
		return nil
	}

	paymentID, _ := wh.Data["id"].(string)
	if paymentID == "" {
		// Can't verify without a payment ID; accept and let ParseWebhookEvent handle it.
		return nil
	}

	var result struct {
		Data bool `json:"data"`
	}
	if err := c.post(ctx, "/v1/payments/"+paymentID+"/verify", map[string]string{
		"verification_signature": wh.Signature,
	}, &result); err != nil {
		return fmt.Errorf("durianpay: verify payment signature: %w", err)
	}
	if !result.Data {
		return fmt.Errorf("durianpay: webhook signature invalid for payment %s", paymentID)
	}
	return nil
}

// ── GetInvoice ────────────────────────────────────────────────────────────

// fetchPaymentResponse is the envelope returned by GET /v1/payments/:id.
type fetchPaymentResponse struct {
	Data struct {
		ID     string `json:"id"`
		Status string `json:"status"` // pending|processing|completed|failed|expired|cancelled
		Type   string `json:"type"`   // VA|EWALLET|QRIS
		Amount string `json:"amount"`
		PaidAt string `json:"paid_at,omitempty"` // RFC3339
	} `json:"data"`
}

// GetInvoice fetches the current status of a DurianPay payment by its payment_id.
// DurianPay statuses are mapped to the provider-agnostic Invoice.Status field:
//
//	completed → "paid"
//	failed | cancelled → "failed"
//	expired → "expired"
//	pending | processing → "awaiting_payment"
func (c *Client) GetInvoice(ctx context.Context, paymentID string) (*provider.Invoice, error) {
	var resp fetchPaymentResponse
	if err := c.get(ctx, "/v1/payments/"+paymentID, &resp); err != nil {
		return nil, fmt.Errorf("durianpay: fetch payment %s: %w", paymentID, err)
	}

	d := resp.Data
	inv := &provider.Invoice{
		ProviderInvoiceID: d.ID,
		Status:            d.Status,
		PaymentMethod:     d.Type,
	}

	// Normalise DurianPay status to our internal vocabulary.
	switch d.Status {
	case "completed":
		inv.Status = "paid"
	case "failed", "cancelled":
		inv.Status = "failed"
	case "expired":
		inv.Status = "expired"
	default:
		inv.Status = "awaiting_payment"
	}

	if d.PaidAt != "" {
		if t, err := time.Parse(time.RFC3339, d.PaidAt); err == nil {
			utc := t.UTC()
			inv.PaidAt = &utc
		}
	}

	return inv, nil
}

func (c *Client) CancelInvoice(_ context.Context, _ string) error {
	return fmt.Errorf("durianpay: CancelInvoice not implemented")
}

func (c *Client) CreateRefund(_ context.Context, _ provider.CreateRefundRequest) (*provider.Refund, error) {
	return nil, fmt.Errorf("durianpay: CreateRefund not implemented")
}

func (c *Client) CreatePayout(_ context.Context, _ provider.CreatePayoutRequest) (*provider.Payout, error) {
	return nil, fmt.Errorf("durianpay: CreatePayout not implemented")
}

// ── HTTP helpers ──────────────────────────────────────────────────────────

func (c *Client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.SetBasicAuth(c.apiKey, "")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("status %d: %s", resp.StatusCode, respBytes)
	}
	if out != nil {
		if err := json.Unmarshal(respBytes, out); err != nil {
			return fmt.Errorf("decode response (body: %s): %w", respBytes, err)
		}
	}
	return nil
}

func (c *Client) post(ctx context.Context, path string, body any, out any) error {
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	// Basic Auth: username = API key, password = empty string.
	req.SetBasicAuth(c.apiKey, "")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("status %d: %s", resp.StatusCode, respBytes)
	}
	if out != nil {
		if err := json.Unmarshal(respBytes, out); err != nil {
			return fmt.Errorf("decode response (body: %s): %w", respBytes, err)
		}
	}
	return nil
}

// stringFromMap safely reads a string value from a map[string]any.
func stringFromMap(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, ok := m[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}
