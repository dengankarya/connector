package doku

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/dengankarya/connector/internal/payment/provider"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)

// Client is a thin HTTP client for the Doku Sub-Account (SAC) API.
type Client struct {
	clientID       string
	secretKey      string
	baseURL        string
	webhookBaseURL string // base URL for constructing webhook notification URL
	http           *http.Client
	logger         *logrus.Logger
}

// NewClient creates a Doku API client.
// baseURL: "https://api.doku.com" (production) or "https://api-sandbox.doku.com" (sandbox).
// webhookBaseURL: base URL for webhook notifications (e.g. "https://connector.example.com"); empty to omit override.
// If logger is nil, a default logger is used.
func NewClient(clientID, secretKey, baseURL, webhookBaseURL string, logger *logrus.Logger) *Client {
	if logger == nil {
		logger = logrus.New()
	}
	return &Client{
		clientID:       clientID,
		secretKey:      secretKey,
		baseURL:        baseURL,
		webhookBaseURL: webhookBaseURL,
		http:           &http.Client{Timeout: 30 * time.Second},
		logger:         logger,
	}
}

// sanitizeName removes special characters from the merchant name to comply with Doku's "Safe String" requirement.
// Keeps alphanumeric, spaces, hyphens, and underscores.
func sanitizeName(name string) string {
	re := regexp.MustCompile(`[^a-zA-Z0-9\s\-_]`)
	return re.ReplaceAllString(name, "")
}

// CreateSubAccount calls POST /sac-merchant/v1/accounts.
// Returns the gateway-assigned account ID (e.g. "SAC-0000-0000000000001") and initial status.
func (c *Client) CreateSubAccount(ctx context.Context, email, name string) (gatewayAccountID, status string, err error) {
	type reqBody struct {
		Account struct {
			Email string `json:"email"`
			Type  string `json:"type"`
			Name  string `json:"name"`
		} `json:"account"`
	}
	type respBody struct {
		Account struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"account"`
	}

	req := reqBody{}
	req.Account.Email = email
	req.Account.Type = "STANDARD"
	req.Account.Name = sanitizeName(name)

	var resp respBody
	if err := c.post(ctx, "/sac-merchant/v1/accounts", req, &resp); err != nil {
		return "", "", fmt.Errorf("doku: create sub-account: %w", err)
	}
	return resp.Account.ID, resp.Account.Status, nil
}

// GetBalance calls GET /sac-merchant/v1/balances/{account_id}.
// Returns pending and available amounts in the smallest currency unit (IDR).
func (c *Client) GetBalance(ctx context.Context, gatewayAccountID string) (pending, available int64, err error) {
	type respBody struct {
		Balance struct {
			Pending   string `json:"pending"`
			Available string `json:"available"`
		} `json:"balance"`
	}

	var resp respBody
	if err := c.get(ctx, "/sac-merchant/v1/balances/"+gatewayAccountID, &resp); err != nil {
		return 0, 0, fmt.Errorf("doku: get balance: %w", err)
	}

	p, _ := strconv.ParseInt(resp.Balance.Pending, 10, 64)
	a, _ := strconv.ParseInt(resp.Balance.Available, 10, 64)
	return p, a, nil
}

// SendPayout calls POST /sac-merchant/v1/payouts.
// Returns the payout status reported by Doku (e.g. "COMPLETED").
func (c *Client) SendPayout(
	ctx context.Context,
	gatewayAccountID string,
	amount int64,
	invoiceNumber, bankCode, bankAccountNumber, bankAccountName string,
) (status string, err error) {
	type reqBody struct {
		Account struct {
			ID string `json:"id"`
		} `json:"account"`
		Payout struct {
			Amount        int64  `json:"amount"`
			InvoiceNumber string `json:"invoice_number"`
		} `json:"payout"`
		Beneficiary struct {
			BankCode          string `json:"bank_code"`
			BankAccountNumber string `json:"bank_account_number"`
			BankAccountName   string `json:"bank_account_name"`
		} `json:"beneficiary"`
	}
	type respBody struct {
		Payout struct {
			Status string `json:"status"`
		} `json:"payout"`
	}

	req := reqBody{}
	req.Account.ID = gatewayAccountID
	req.Payout.Amount = amount
	req.Payout.InvoiceNumber = invoiceNumber
	req.Beneficiary.BankCode = bankCode
	req.Beneficiary.BankAccountNumber = bankAccountNumber
	req.Beneficiary.BankAccountName = bankAccountName

	var resp respBody
	if err := c.post(ctx, "/sac-merchant/v1/payouts", req, &resp); err != nil {
		return "", fmt.Errorf("doku: send payout: %w", err)
	}
	return resp.Payout.Status, nil
}

// CheckoutResult is the response from CreateCheckout.
type CheckoutResult struct {
	SessionID   string // DOKU session_id — use as ProviderInvoiceID
	PaymentURL  string // customer-facing checkout page URL
	ExpiredDate string // raw "yyyyMMddHHmmss" string in UTC+7 (WIB)
}

// CheckoutRequest is the input for CreateCheckout.
type CheckoutRequest struct {
	InvoiceNumber           string
	Amount                  int64
	DueMinutes              int // 0 = DOKU default (60 min)
	CustomerName            string
	CustomerEmail           string
	CustomerPhone           string
	AccountID               string   // SAC sub-account ID; omitted when empty
	PaymentType             string   // "SALE" (default) | "INSTALLMENT" | "AUTHORIZE"
	PaymentMethodTypes      []string // restrict channels; omitted when empty = show all
	OverrideNotificationURL string   // webhook URL for payment status; omitted when empty
	CallbackURL             string   // post-payment redirect URL; omitted when empty
	CallbackURLCancel       string   // cancel redirect URL; omitted when empty
	CallbackURLResult       string   // result page URL; omitted when empty
}

// CreateCheckout calls POST /checkout/v1/payment and returns the checkout session.
func (c *Client) CreateCheckout(ctx context.Context, req CheckoutRequest) (*CheckoutResult, error) {
	body := mapCheckoutRequest(req)
	bodyJSON, _ := json.Marshal(body)
	c.logger.WithFields(logrus.Fields{
		"method":                    "CreateCheckout",
		"invoice_number":            req.InvoiceNumber,
		"amount":                    req.Amount,
		"account_id":                req.AccountID,
		"has_additional_info":       req.AccountID != "",
		"override_notification_url": req.OverrideNotificationURL,
		"callback_url":              req.CallbackURL,
		"callback_url_cancel":       req.CallbackURLCancel,
		"callback_url_result":       req.CallbackURLResult,
		"request_body":              string(bodyJSON),
	}).Info("DOKU CreateCheckout request")

	var resp DokuCreateCheckoutResponse
	if err := c.post(ctx, "/checkout/v1/payment", body, &resp); err != nil {
		return nil, fmt.Errorf("doku: create checkout: %w", err)
	}
	return &CheckoutResult{
		SessionID:   resp.Response.Order.SessionID,
		PaymentURL:  resp.Response.Payment.URL,
		ExpiredDate: resp.Response.Payment.ExpiredDate,
	}, nil
}

// ── HTTP helpers ──────────────────────────────────────────────────────────────

func (c *Client) post(ctx context.Context, path string, body any, out any) error {
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	reqID := uuid.New().String()
	ts := time.Now().UTC().Format(time.RFC3339)
	sig := SignRequest(c.clientID, reqID, ts, path, c.secretKey, bodyBytes)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Client-Id", c.clientID)
	req.Header.Set("Request-Id", reqID)
	req.Header.Set("Request-Timestamp", ts)
	req.Header.Set("Signature", sig)

	return c.do(req, out)
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	reqID := uuid.New().String()
	ts := time.Now().UTC().Format(time.RFC3339)
	sig := SignRequest(c.clientID, reqID, ts, path, c.secretKey, nil)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Client-Id", c.clientID)
	req.Header.Set("Request-Id", reqID)
	req.Header.Set("Request-Timestamp", ts)
	req.Header.Set("Signature", sig)

	return c.do(req, out)
}

func (c *Client) do(req *http.Request, out any) error {
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
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

// ─── provider.PaymentProvider implementation ────────────────────────────────

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

	// Construct webhook notification URL if base URL is configured
	overrideNotificationURL := ""
	if c.webhookBaseURL != "" {
		overrideNotificationURL = c.webhookBaseURL + "/api/v1/webhook/doku"
	}

	result, err := c.CreateCheckout(ctx, CheckoutRequest{
		InvoiceNumber:           invoiceNumber,
		Amount:                  req.Amount,
		DueMinutes:              dueMins,
		CustomerName:            req.CustomerName,
		CustomerEmail:           req.CustomerEmail,
		CustomerPhone:           req.CustomerMobile,
		AccountID:               req.GatewayAccountID,
		PaymentType:             req.PaymentType,
		PaymentMethodTypes:      req.PaymentMethodTypes,
		OverrideNotificationURL: overrideNotificationURL,
		CallbackURL:             req.SuccessReturnURL,
		CallbackURLCancel:       req.CancelReturnURL,
		CallbackURLResult:       req.ResultURL,
	})
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
