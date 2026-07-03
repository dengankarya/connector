package xendit

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/dengankarya/connector/internal/account"
	"github.com/dengankarya/connector/internal/payment/domain"
	"github.com/dengankarya/connector/internal/payment/provider"
	"github.com/sirupsen/logrus"
)

const defaultBaseURL = "https://api.xendit.co"

// Client is a thin HTTP client for the Xendit XenPlatform API.
type Client struct {
	apiKey         string
	baseURL        string
	callbackToken  string // XENDIT_CALLBACK_TOKEN — used to validate X-Callback-Token on incoming webhooks
	webhookBaseURL string // WEBHOOK_BASE_URL — used to register callback URLs per sub-account
	http           *http.Client
	logger         *logrus.Logger
}

// NewClient creates a Xendit API client.
// baseURL: "https://api.xendit.co"; empty → defaults to production URL.
// callbackToken: static token for validating incoming webhook X-Callback-Token headers.
// webhookBaseURL: base URL where this connector is reachable (e.g. "https://connector.example.com").
func NewClient(apiKey, baseURL, callbackToken, webhookBaseURL string, logger *logrus.Logger) *Client {
	if logger == nil {
		logger = logrus.New()
	}
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		apiKey:         apiKey,
		baseURL:        baseURL,
		callbackToken:  callbackToken,
		webhookBaseURL: webhookBaseURL,
		http:           &http.Client{Timeout: 30 * time.Second},
		logger:         logger,
	}
}

// ── GatewayClient implementation ──────────────────────────────────────────────

// CreateSubAccount creates a MANAGED XenPlatform sub-account and registers our webhook URL for it.
// MANAGED is the only account type accepted in Indonesia — the merchant receives an invitation email
// and completes sign-up on Xendit's hosted page. The returned accountID is the Xendit account ID
// used as the for-user-id header in subsequent payment API calls.
func (c *Client) CreateSubAccount(ctx context.Context, email, name string) (gatewayAccountID, status string, err error) {
	req := createAccountRequest{
		Email: email,
		Type:  "OWNED",
	}
	req.PublicProfile.BusinessName = name
	var resp createAccountResponse
	if err := c.do(ctx, http.MethodPost, "/v2/accounts", "", req, &resp); err != nil {
		return "", "", fmt.Errorf("xendit: create sub-account: %w", err)
	}

	if c.webhookBaseURL != "" {
		if wErr := c.registerWebhooks(ctx, resp.ID); wErr != nil {
			// Log but don't fail — webhook registration can be retried; the account is created.
			c.logger.WithFields(logrus.Fields{
				"component":  "xendit",
				"account_id": resp.ID,
				"error":      wErr,
			}).Warn("xendit: sub-account created but webhook registration failed")
		}
	}

	return resp.ID, resp.Status, nil
}

// registerWebhooks registers our /webhook/xendit URL for the invoice callback type on behalf of
// the sub-account. The invoice callback type receives Payment Session COMPLETED/EXPIRED events.
func (c *Client) registerWebhooks(ctx context.Context, accountID string) error {
	url := c.webhookBaseURL + "/api/v1/webhook/xendit"
	body := setCallbackURLRequest{URL: url}

	if err := c.do(ctx, http.MethodPost, "/callback_urls/invoice", accountID, body, nil); err != nil {
		return fmt.Errorf("register invoice webhook: %w", err)
	}
	c.logger.WithFields(logrus.Fields{
		"component":    "xendit",
		"account_id":   accountID,
		"callback_url": url,
	}).Info("xendit: webhook registered for sub-account")
	return nil
}

// GetBalance is a stub — Xendit settlement balance requires a separate API (not yet implemented).
func (c *Client) GetBalance(_ context.Context, _ string) (pending, available int64, err error) {
	return 0, 0, nil
}

// SendPayout is not yet implemented for Xendit.
func (c *Client) SendPayout(_ context.Context, _ string, _ int64, _, _, _, _ string) (string, error) {
	return "", fmt.Errorf("xendit: SendPayout not yet implemented")
}

// ── Extra account methods ─────────────────────────────────────────────────────

// GetAccount fetches the current status and public profile of a sub-account by its Xendit account ID.
// Returns *account.XenditAccountInfo so that *xendit.Client satisfies the account.XenditGatewayClient interface.
func (c *Client) GetAccount(ctx context.Context, accountID string) (*account.XenditAccountInfo, error) {
	var resp AccountResponse
	if err := c.do(ctx, http.MethodGet, "/v2/accounts/"+accountID, "", nil, &resp); err != nil {
		return nil, fmt.Errorf("xendit: get account: %w", err)
	}
	info := &account.XenditAccountInfo{
		ID:     resp.ID,
		Type:   resp.Type,
		Email:  resp.Email,
		Status: resp.Status,
	}
	info.PublicProfile.Name = resp.PublicProfile.Name
	info.PublicProfile.Country = resp.PublicProfile.Country
	return info, nil
}

// CreateAccountHolder submits KYC business details for a sub-account and returns the account_holder_id.
// Call LinkAccountHolder next to bind the holder to the sub-account and start the verification flow.
func (c *Client) CreateAccountHolder(ctx context.Context, subAccountID string, req account.CreateAccountHolderRequest) (string, error) {
	var resp createAccountHolderResponse
	if err := c.do(ctx, http.MethodPost, "/account_holders", subAccountID, req, &resp); err != nil {
		return "", fmt.Errorf("xendit: create account holder: %w", err)
	}
	return resp.ID, nil
}

// LinkAccountHolder patches the sub-account to associate the given account holder.
// This triggers Xendit's verification flow (REGISTERED → AWAITING_DOCS → PENDING_VERIFICATION → LIVE).
func (c *Client) LinkAccountHolder(ctx context.Context, subAccountID, accountHolderID string) error {
	body := patchAccountRequest{AccountHolderID: accountHolderID}
	if err := c.do(ctx, http.MethodPatch, "/v2/accounts/"+subAccountID, "", body, nil); err != nil {
		return fmt.Errorf("xendit: link account holder: %w", err)
	}
	return nil
}

// ── PaymentProvider implementation ───────────────────────────────────────────

func (c *Client) ProviderName() string { return "xendit" }

// CreateInvoice creates a Xendit Payment Session on behalf of the sub-account (via for-user-id).
// ProviderInvoiceID = payment_session_id (ps-...) stored in the DB.
// CheckoutURL = payment_link_url returned to the frontend.
func (c *Client) CreateInvoice(ctx context.Context, req provider.CreateInvoiceRequest) (*provider.Invoice, error) {
	country := req.Country
	if country == "" {
		country = "ID"
	}

	// Xendit Sessions API expects amount as float64 (full currency units for IDR amounts).
	// Our internal amounts are already in IDR smallest unit (same as full units for IDR).
	sessionReq := createSessionRequest{
		ReferenceID:            req.ExternalID,
		Currency:               req.Currency,
		Amount:                 float64(req.Amount),
		Country:                country,
		SessionType:            "PAY",
		Mode:                   "PAYMENT_LINK",
		Description:            req.Description,
		AllowedPaymentChannels: req.AllowedPaymentChannels,
		ExpiresAt:              req.ExpiresAt,
		SuccessReturnURL:       req.SuccessReturnURL,
		CancelReturnURL:        req.CancelReturnURL,
		Metadata:               req.Metadata,
		ChannelProperties:      req.ChannelProperties,
	}

	c.logger.WithFields(logrus.Fields{
		"component":    "xendit",
		"reference_id": req.ExternalID,
		"amount":       req.Amount,
		"account_id":   req.GatewayAccountID,
	}).Info("xendit: creating payment session")

	var resp createSessionResponse
	if err := c.do(ctx, http.MethodPost, "/sessions", req.GatewayAccountID, sessionReq, &resp); err != nil {
		return nil, fmt.Errorf("xendit: create session: %w", err)
	}

	inv := &provider.Invoice{
		ProviderInvoiceID: resp.PaymentSessionID,
		CheckoutURL:       resp.PaymentLinkURL,
		Status:            "pending",
		Amount:            req.Amount,
		Currency:          req.Currency,
	}

	if resp.ExpiresAt != "" {
		if t, err := time.Parse(time.RFC3339, resp.ExpiresAt); err == nil {
			utc := t.UTC()
			inv.ExpiresAt = &utc
		}
	}

	return inv, nil
}

// ValidateWebhookSignature verifies the X-Callback-Token header against the configured callback token.
func (c *Client) ValidateWebhookSignature(_ context.Context, _ []byte, headers map[string]string) error {
	token := headers["X-Callback-Token"]
	if token == "" {
		return domain.ErrMissingSignature
	}
	if subtle.ConstantTimeCompare([]byte(token), []byte(c.callbackToken)) != 1 {
		return domain.ErrInvalidSignature
	}
	return nil
}

// ParseWebhookEvent parses a Xendit webhook payload into a provider-agnostic event.
// Xendit sends both payment session events and account-level events to the same URL.
// Account events (account.*, account_holder.*) are returned with EventType set but no
// ProviderInvoiceID — the ingest handler routes them to the account webhook handler.
func (c *Client) ParseWebhookEvent(_ context.Context, payload []byte) (*provider.WebhookEvent, error) {
	// Peek at event type before full parse — account events have a different structure.
	var envelope struct {
		Event      string `json:"event"`
		BusinessID string `json:"business_id"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return nil, fmt.Errorf("xendit: parse webhook: %w", err)
	}

	if strings.HasPrefix(envelope.Event, "account.") || strings.HasPrefix(envelope.Event, "account_holder.") {
		// Account-level event — no payment_session_id; ingest handler will route separately.
		return &provider.WebhookEvent{
			ProviderEventID: envelope.Event + ":" + envelope.BusinessID,
			EventType:       envelope.Event,
			RawPayload:      payload,
		}, nil
	}

	var notif sessionWebhookPayload
	if err := json.Unmarshal(payload, &notif); err != nil {
		return nil, fmt.Errorf("xendit: parse webhook: %w", err)
	}
	if notif.Data.PaymentSessionID == "" {
		return nil, fmt.Errorf("xendit: webhook missing data.payment_session_id")
	}

	eventType := "payment_session.completed"
	switch strings.ToUpper(notif.Data.Status) {
	case "COMPLETED":
		eventType = "payment_session.completed"
	case "EXPIRED":
		eventType = "payment_session.expired"
	case "CANCELED":
		eventType = "payment_session.failed"
	}

	// Use reference_id + status as the unique event ID when no dedicated event ID is present.
	eventID := notif.Data.ReferenceID + ":" + notif.Data.Status
	if eventID == ":" {
		eventID = notif.Data.PaymentSessionID + ":" + notif.Data.Status
	}

	return &provider.WebhookEvent{
		ProviderEventID:   eventID,
		EventType:         eventType,
		ProviderInvoiceID: notif.Data.PaymentSessionID,
		PaymentID:         notif.Data.PaymentID,
		Amount:            int64(math.Round(notif.Data.Amount)),
		Currency:          notif.Data.Currency,
		RawPayload:        payload,
	}, nil
}

// GetInvoice fetches the current status of a Payment Session.
func (c *Client) GetInvoice(ctx context.Context, invoiceID string) (*provider.Invoice, error) {
	var resp createSessionResponse
	if err := c.do(ctx, http.MethodGet, "/sessions/"+invoiceID, "", nil, &resp); err != nil {
		return nil, fmt.Errorf("xendit: get session: %w", err)
	}
	inv := &provider.Invoice{
		ProviderInvoiceID: resp.PaymentSessionID,
		CheckoutURL:       resp.PaymentLinkURL,
		Status:            resp.Status,
		Amount:            int64(math.Round(resp.Amount)),
		Currency:          resp.Currency,
	}
	if resp.ExpiresAt != "" {
		if t, err := time.Parse(time.RFC3339, resp.ExpiresAt); err == nil {
			utc := t.UTC()
			inv.ExpiresAt = &utc
		}
	}
	return inv, nil
}

// GetTransaction fetches settlement and fee details for a payment by its Xendit payment_id (py-xxx).
// Used by the settlement sync job to backfill fee fields and transition paid transactions to settled.
//
// Xendit returns monetary amounts as float64. IDR has 0 decimal places (ISO 4217), so the float64
// values are already in smallest-unit IDR. math.Round handles floating-point representation drift.
func (c *Client) GetTransaction(ctx context.Context, paymentID string) (*provider.ProviderTransaction, error) {
	var resp getTransactionResponse
	if err := c.do(ctx, http.MethodGet, "/transactions/"+paymentID, "", nil, &resp); err != nil {
		return nil, fmt.Errorf("xendit: get transaction: %w", err)
	}
	pt := &provider.ProviderTransaction{
		ID:                   resp.ID,
		SettlementStatus:     resp.SettlementStatus,
		XenditFee:            int64(math.Round(resp.Fee.XenditFee)),
		VAT:                  int64(math.Round(resp.Fee.ValueAddedTax)),
		XenditWithholdingTax: int64(math.Round(resp.Fee.XenditWithholdingTax)),
		ThirdPartyWHT:        int64(math.Round(resp.Fee.ThirdPartyWithholdingTax)),
	}
	if resp.EstimatedSettlementTime != "" {
		if t, err := time.Parse(time.RFC3339, resp.EstimatedSettlementTime); err == nil {
			utc := t.UTC()
			pt.EstimatedSettlementTime = &utc
		}
	}
	return pt, nil
}

func (c *Client) CancelInvoice(_ context.Context, _ string) error {
	return domain.ErrNotSupported
}

func (c *Client) CreateRefund(_ context.Context, _ provider.CreateRefundRequest) (*provider.Refund, error) {
	return nil, domain.ErrNotSupported
}

func (c *Client) CreatePayout(_ context.Context, _ provider.CreatePayoutRequest) (*provider.Payout, error) {
	return nil, domain.ErrNotSupported
}

// ── HTTP helper ───────────────────────────────────────────────────────────────

// do executes an authenticated Xendit API call.
// forUserID, when non-empty, sets the for-user-id header to scope the call to a sub-account.
func (c *Client) do(ctx context.Context, method, path, forUserID string, body, out any) error {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}

	// Xendit uses HTTP Basic Auth: apiKey as username, empty password.
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(c.apiKey+":")))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if forUserID != "" {
		req.Header.Set("for-user-id", forUserID)
	}

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
	if out != nil && len(respBytes) > 0 {
		if err := json.Unmarshal(respBytes, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}
