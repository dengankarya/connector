package midtrans

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/dengankarya/connector/internal/payment/provider"
)

const (
	defaultSnapBaseURL = "https://app.sandbox.midtrans.com"
	defaultCoreBaseURL = "https://api.sandbox.midtrans.com"
)

// Provider implements provider.PaymentProvider for Midtrans using the SNAP API
// for payment creation and the Core API for status/cancel/refund operations.
type Provider struct {
	serverKey   string
	snapBaseURL string // e.g. https://app.midtrans.com or https://app.sandbox.midtrans.com
	coreBaseURL string // e.g. https://api.midtrans.com or https://api.sandbox.midtrans.com
	http        *http.Client
}

// New creates a Midtrans Provider.
// baseURL is the SNAP API base (e.g. "https://app.sandbox.midtrans.com").
// The Core API base is derived automatically from it.
func New(serverKey, baseURL string) *Provider {
	if baseURL == "" {
		baseURL = defaultSnapBaseURL
	}
	return &Provider{
		serverKey:   serverKey,
		snapBaseURL: baseURL,
		coreBaseURL: deriveCoreBaseURL(baseURL),
		http:        &http.Client{Timeout: 30 * time.Second},
	}
}

// ProviderName returns the canonical name for this provider.
func (p *Provider) ProviderName() string { return "midtrans" }

// CreateInvoice creates a Midtrans SNAP transaction (POST /snap/v1/transactions).
// Returns a token (stored as ProviderInvoiceID) and a redirect URL (checkout page).
func (p *Provider) CreateInvoice(ctx context.Context, req provider.CreateInvoiceRequest) (*provider.Invoice, error) {
	dto := toSnapCreateDTO(req)
	body, err := json.Marshal(dto)
	if err != nil {
		return nil, fmt.Errorf("midtrans: marshal snap transaction: %w", err)
	}

	var resp snapResponseDTO
	if err := p.postSnap(ctx, "/snap/v1/transactions", body, &resp); err != nil {
		return nil, fmt.Errorf("midtrans: create snap transaction: %w", err)
	}

	// Midtrans SNAP returns a token and redirect URL.
	// We use order_id (ExternalID) as the ProviderInvoiceID — this is what
	// Midtrans uses to identify the transaction in webhooks and status API.
	return &provider.Invoice{
		ProviderInvoiceID: req.ExternalID,
		CheckoutURL:       resp.RedirectURL,
		Status:            "PENDING",
		Amount:            req.Amount,
		Currency:          req.Currency,
	}, nil
}

// GetInvoice fetches the current status of a Midtrans transaction (GET /v2/{order_id}/status).
func (p *Provider) GetInvoice(ctx context.Context, orderID string) (*provider.Invoice, error) {
	var resp statusResponseDTO
	if err := p.getCore(ctx, "/v2/"+orderID+"/status", &resp); err != nil {
		return nil, fmt.Errorf("midtrans: get transaction status %s: %w", orderID, err)
	}
	return fromStatusResponseDTO(resp), nil
}

// CancelInvoice cancels a Midtrans transaction (POST /v2/{order_id}/cancel).
func (p *Provider) CancelInvoice(ctx context.Context, orderID string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.coreBaseURL+"/v2/"+orderID+"/cancel", nil)
	if err != nil {
		return fmt.Errorf("midtrans: build cancel request: %w", err)
	}
	p.setCoreHeaders(req)

	resp, err := p.http.Do(req)
	if err != nil {
		return fmt.Errorf("midtrans: cancel transaction: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return p.decodeError(resp, "cancel transaction")
	}
	return nil
}

// CreateRefund initiates a Midtrans refund (POST /v2/{order_id}/refund).
func (p *Provider) CreateRefund(ctx context.Context, req provider.CreateRefundRequest) (*provider.Refund, error) {
	dto := struct {
		RefundKey string `json:"refund_key"`
		Amount    int64  `json:"amount"`
		Reason    string `json:"reason,omitempty"`
	}{
		RefundKey: req.ExternalID,
		Amount:    req.Amount,
		Reason:    req.Reason,
	}
	body, err := json.Marshal(dto)
	if err != nil {
		return nil, fmt.Errorf("midtrans: marshal refund: %w", err)
	}

	var resp struct {
		StatusCode    string `json:"status_code"`
		StatusMessage string `json:"status_message"`
		TransactionID string `json:"transaction_id"`
		RefundKey     string `json:"refund_key"`
		RefundAmount  string `json:"refund_amount"`
	}
	if err := p.postCore(ctx, "/v2/"+req.ProviderInvoiceID+"/refund", body, &resp); err != nil {
		return nil, fmt.Errorf("midtrans: create refund: %w", err)
	}

	amount, _ := parseGrossAmount(resp.RefundAmount)
	return &provider.Refund{
		ProviderRefundID: resp.TransactionID,
		Amount:           amount,
		Status:           resp.StatusCode,
	}, nil
}

// ParseWebhookEvent decodes a raw Midtrans webhook payload into a provider-agnostic event.
func (p *Provider) ParseWebhookEvent(_ context.Context, payload []byte) (*provider.WebhookEvent, error) {
	return parseWebhookNotification(payload)
}

// CreatePayout is not supported by Midtrans — returns an error.
// Midtrans does not provide a disbursement/payout API.
func (p *Provider) CreatePayout(_ context.Context, _ provider.CreatePayoutRequest) (*provider.Payout, error) {
	return nil, fmt.Errorf("midtrans: payout/disbursement is not supported")
}

// ─── HTTP helpers ─────────────────────────────────────────────────────────────

func (p *Provider) postSnap(ctx context.Context, path string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.snapBaseURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	p.setSnapHeaders(req)
	return p.do(req, out)
}

func (p *Provider) postCore(ctx context.Context, path string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.coreBaseURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	p.setCoreHeaders(req)
	return p.do(req, out)
}

func (p *Provider) getCore(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.coreBaseURL+path, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	p.setCoreHeaders(req)
	return p.do(req, out)
}

// setSnapHeaders sets Basic Auth and content-type for SNAP API requests.
func (p *Provider) setSnapHeaders(req *http.Request) {
	req.SetBasicAuth(p.serverKey, "")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
}

// setCoreHeaders sets Basic Auth and content-type for Core API requests.
func (p *Provider) setCoreHeaders(req *http.Request) {
	req.SetBasicAuth(p.serverKey, "")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
}

func (p *Provider) do(req *http.Request, out any) error {
	resp, err := p.http.Do(req)
	if err != nil {
		return fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return p.decodeError(resp, req.Method+" "+req.URL.Path)
	}

	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

func (p *Provider) decodeError(resp *http.Response, context string) error {
	var errBody struct {
		StatusCode    string   `json:"status_code"`
		StatusMessage string   `json:"status_message"`
		ErrorMessages []string `json:"error_messages"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&errBody)

	msg := errBody.StatusMessage
	if len(errBody.ErrorMessages) > 0 {
		msg = strings.Join(errBody.ErrorMessages, "; ")
	}
	return fmt.Errorf("midtrans %s %d: %s — %s", context, resp.StatusCode, errBody.StatusCode, msg)
}

// deriveCoreBaseURL converts a SNAP base URL to the corresponding Core API base URL.
// e.g. "https://app.sandbox.midtrans.com" → "https://api.sandbox.midtrans.com"
//
//	"https://app.midtrans.com"         → "https://api.midtrans.com"
func deriveCoreBaseURL(snapBase string) string {
	// Replace "app." prefix with "api." in the hostname.
	result := strings.Replace(snapBase, "://app.", "://api.", 1)
	return result
}
