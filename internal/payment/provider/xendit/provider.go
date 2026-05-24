package xendit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/dengankarya/overwatch/internal/payment/provider"
)

const (
	defaultBaseURL   = "https://api.xendit.co"
	xenditAPIVersion = "2024-11-11"
)

// Provider implements provider.PaymentProvider for Xendit.
type Provider struct {
	apiKey       string
	webhookToken string
	baseURL      string
	http         *http.Client
}

// New creates a Xendit Provider.
func New(apiKey, webhookToken, baseURL string) *Provider {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Provider{
		apiKey:       apiKey,
		webhookToken: webhookToken,
		baseURL:      baseURL,
		http:         &http.Client{Timeout: 30 * time.Second},
	}
}

// ProviderName returns the canonical name for this provider.
func (p *Provider) ProviderName() string { return "xendit" }

// CreateInvoice creates a Xendit Payment Session (POST /sessions).
// Uses session_type=PAY and mode=PAYMENT_LINK to produce a hosted checkout URL.
func (p *Provider) CreateInvoice(ctx context.Context, req provider.CreateInvoiceRequest) (*provider.Invoice, error) {
	dto := toCreateSessionDTO(req)
	body, err := json.Marshal(dto)
	if err != nil {
		return nil, fmt.Errorf("xendit: marshal create session: %w", err)
	}

	var resp sessionResponseDTO
	if err := p.post(ctx, "/sessions", body, req.ForUserID, &resp); err != nil {
		return nil, fmt.Errorf("xendit: create session: %w", err)
	}
	return fromSessionResponseDTO(resp), nil
}

// GetInvoice fetches the current state of a Xendit Payment Session (GET /sessions/{id}).
func (p *Provider) GetInvoice(ctx context.Context, sessionID, forUserID string) (*provider.Invoice, error) {
	var resp sessionResponseDTO
	if err := p.get(ctx, "/sessions/"+sessionID, forUserID, &resp); err != nil {
		return nil, fmt.Errorf("xendit: get session %s: %w", sessionID, err)
	}
	return fromSessionResponseDTO(resp), nil
}

// CancelInvoice cancels an active Xendit Payment Session (POST /sessions/{id}/cancel).
func (p *Provider) CancelInvoice(ctx context.Context, sessionID, forUserID string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.baseURL+"/sessions/"+sessionID+"/cancel", nil)
	if err != nil {
		return fmt.Errorf("xendit: build cancel session request: %w", err)
	}
	p.setHeaders(req, forUserID)

	resp, err := p.http.Do(req)
	if err != nil {
		return fmt.Errorf("xendit: cancel session: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("xendit: cancel session returned %d", resp.StatusCode)
	}
	return nil
}

// CreateRefund initiates a Xendit refund against a Payment Request.
func (p *Provider) CreateRefund(ctx context.Context, req provider.CreateRefundRequest) (*provider.Refund, error) {
	dto := createRefundDTO{
		PaymentRequestID: req.ProviderInvoiceID,
		Amount:           req.Amount,
		Reason:           req.Reason,
		ReferenceID:      req.ExternalID,
	}
	body, err := json.Marshal(dto)
	if err != nil {
		return nil, fmt.Errorf("xendit: marshal refund: %w", err)
	}

	var resp refundResponseDTO
	if err := p.post(ctx, "/refunds", body, req.ForUserID, &resp); err != nil {
		return nil, fmt.Errorf("xendit: create refund: %w", err)
	}
	return &provider.Refund{
		ProviderRefundID: resp.ID,
		Amount:           resp.Amount,
		Status:           resp.Status,
	}, nil
}

// ParseWebhookEvent decodes a raw Xendit webhook payload into a provider-agnostic event.
func (p *Provider) ParseWebhookEvent(_ context.Context, payload []byte) (*provider.WebhookEvent, error) {
	return parseWebhookPayload(payload)
}

// Transfer moves funds from the platform account to a merchant sub-account.
// Calls POST /transfers on the XenPlatform API.
func (p *Provider) Transfer(ctx context.Context, req provider.TransferRequest) (*provider.TransferResponse, error) {
	dto := createTransferDTO{
		Reference:         req.Reference,
		Amount:            req.Amount,
		DestinationUserID: req.DestinationUserID,
	}
	body, err := json.Marshal(dto)
	if err != nil {
		return nil, fmt.Errorf("xendit: marshal transfer: %w", err)
	}

	var resp transferResponseDTO
	// Transfers are platform-level — no for-user-id header.
	if err := p.post(ctx, "/transfers", body, "", &resp); err != nil {
		return nil, fmt.Errorf("xendit: transfer to %s: %w", req.DestinationUserID, err)
	}
	return &provider.TransferResponse{
		ProviderTransferID: resp.TransferID,
		Status:             resp.Status,
	}, nil
}

// CreatePayout initiates a Xendit disbursement.
func (p *Provider) CreatePayout(ctx context.Context, req provider.CreatePayoutRequest) (*provider.Payout, error) {
	dto := createPayoutDTO{
		ExternalID:    req.ExternalID,
		Amount:        req.Amount,
		BankCode:      req.BankCode,
		AccountNumber: req.AccountNumber,
		AccountName:   req.AccountName,
		Description:   req.Description,
	}
	body, err := json.Marshal(dto)
	if err != nil {
		return nil, fmt.Errorf("xendit: marshal payout: %w", err)
	}

	var resp payoutResponseDTO
	if err := p.post(ctx, "/disbursements", body, req.ForUserID, &resp); err != nil {
		return nil, fmt.Errorf("xendit: create payout: %w", err)
	}
	return &provider.Payout{
		ProviderPayoutID: resp.ID,
		Status:           resp.Status,
		Amount:           resp.Amount,
	}, nil
}

// GetBalance fetches the available balance for the given sub-account (or the platform account
// when ForUserID is empty). Calls GET /balance on the Xendit API.
func (p *Provider) GetBalance(ctx context.Context, req provider.BalanceRequest) (*provider.Balance, error) {
	var resp struct {
		Balance int `json:"balance"`
	}
	if err := p.get(ctx, "/balance", req.ForUserID, &resp); err != nil {
		return nil, fmt.Errorf("xendit: get balance: %w", err)
	}
	return &provider.Balance{Balance: resp.Balance}, nil
}

// func (p *Provider) GetTransactions(ctx context.Context, req provider.GetTransactionsRequest)

// ─── HTTP helpers ─────────────────────────────────────────────────────────────

func (p *Provider) post(ctx context.Context, path string, body []byte, forUserID string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	p.setHeaders(req, forUserID)
	return p.do(req, out)
}

func (p *Provider) get(ctx context.Context, path string, forUserID string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	p.setHeaders(req, forUserID)
	return p.do(req, out)
}

// setHeaders sets authentication and content-type headers.
// forUserID is the Xendit sub-account business ID; when non-empty the request
// is scoped to that sub-account via the for-user-id header.
func (p *Provider) setHeaders(req *http.Request, forUserID string) {
	req.SetBasicAuth(p.apiKey, "")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("api-version", xenditAPIVersion)
	if forUserID != "" {
		req.Header.Set("for-user-id", forUserID)
	}
}

func (p *Provider) do(req *http.Request, out any) error {
	resp, err := p.http.Do(req)
	if err != nil {
		return fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		var errBody struct {
			ErrorCode string `json:"error_code"`
			Message   string `json:"message"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&errBody)
		return fmt.Errorf("xendit %s %d: %s — %s",
			req.Method, resp.StatusCode, errBody.ErrorCode, errBody.Message)
	}

	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}
