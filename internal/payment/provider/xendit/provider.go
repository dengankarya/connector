package xendit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/dengankarya/connector/internal/payment/provider"
)

const (
	defaultBaseURL   = "https://api.xendit.co"
	xenditAPIVersion = "2024-11-11"
)

// Provider implements provider.PaymentProvider for Xendit using the master account.
// All requests go to the platform's master Xendit account — no for-user-id header.
// Merchant attribution is carried in Xendit invoice metadata (tenant_id key).
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
	if err := p.post(ctx, "/sessions", body, &resp); err != nil {
		return nil, fmt.Errorf("xendit: create session: %w", err)
	}
	return fromSessionResponseDTO(resp), nil
}

// GetInvoice fetches the current state of a Xendit Payment Session (GET /sessions/{id}).
func (p *Provider) GetInvoice(ctx context.Context, sessionID string) (*provider.Invoice, error) {
	var resp sessionResponseDTO
	if err := p.get(ctx, "/sessions/"+sessionID, &resp); err != nil {
		return nil, fmt.Errorf("xendit: get session %s: %w", sessionID, err)
	}
	return fromSessionResponseDTO(resp), nil
}

// CancelInvoice cancels an active Xendit Payment Session (POST /sessions/{id}/cancel).
func (p *Provider) CancelInvoice(ctx context.Context, sessionID string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.baseURL+"/sessions/"+sessionID+"/cancel", nil)
	if err != nil {
		return fmt.Errorf("xendit: build cancel session request: %w", err)
	}
	p.setHeaders(req)

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
	if err := p.post(ctx, "/refunds", body, &resp); err != nil {
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

// CreatePayout initiates a Xendit disbursement from the platform master account
// to a merchant's registered bank account.
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
	if err := p.post(ctx, "/disbursements", body, &resp); err != nil {
		return nil, fmt.Errorf("xendit: create payout: %w", err)
	}
	return &provider.Payout{
		ProviderPayoutID: resp.ID,
		Status:           resp.Status,
		Amount:           resp.Amount,
	}, nil
}

// ListTransactions fetches a page of MONEY_IN transactions from Xendit.
// Used by the settlement sync job to reconcile settlement_status and fee breakdowns.
// Implements provider.TransactionSyncer.
func (p *Provider) ListTransactions(ctx context.Context, req provider.ListTransactionsRequest) (*provider.ListTransactionsResult, error) {
	limit := req.Limit
	if limit <= 0 || limit > 100 {
		limit = 100
	}

	params := url.Values{}
	params.Set("types", "PAYMENT")
	params.Set("cashflow", "MONEY_IN")
	params.Set("limit", strconv.Itoa(limit))
	if !req.CreatedGTE.IsZero() {
		params.Set("after_created_at", req.CreatedGTE.UTC().Format(time.RFC3339))
	}
	if req.AfterID != "" {
		params.Set("after_id", req.AfterID)
	}

	var resp listTransactionsResponseDTO
	if err := p.getWithParams(ctx, "/transactions", params, &resp); err != nil {
		return nil, fmt.Errorf("xendit: list transactions: %w", err)
	}

	result := &provider.ListTransactionsResult{
		HasMore: resp.HasMore,
	}
	for _, dto := range resp.Data {
		t := provider.ProviderTransaction{
			ID:                      dto.ID,
			SettlementStatus:        dto.SettlementStatus,
			XenditFee:               dto.Fee.XenditFee,
			VAT:                     dto.Fee.ValueAddedTax,
			XenditWithholdingTax:    dto.Fee.XenditWithholdingTax,
			ThirdPartyWHT:           dto.Fee.ThirdPartyWithholdingTax,
			EstimatedSettlementTime: dto.EstimatedSettlementTime,
			PaymentSessionID:        dto.ProductData.PaymentSessionID,
			Created:                 dto.Created,
		}
		result.Transactions = append(result.Transactions, t)
		result.LastID = dto.ID
	}

	return result, nil
}

// ── DTOs for GET /transactions ────────────────────────────────────────────────

type listTransactionsResponseDTO struct {
	HasMore bool                   `json:"has_more"`
	Data    []xenditTransactionDTO `json:"data"`
}

type xenditTransactionDTO struct {
	ID                      string                  `json:"id"`
	Type                    string                  `json:"type"`
	Status                  string                  `json:"status"`
	SettlementStatus        string                  `json:"settlement_status"`
	Currency                string                  `json:"currency"`
	Amount                  int64                   `json:"amount"`
	Fee                     xenditTransactionFeeDTO `json:"fee"`
	EstimatedSettlementTime *time.Time              `json:"estimated_settlement_time"`
	ProductData             xenditProductDataDTO    `json:"product_data"`
	Created                 time.Time               `json:"created"`
}

type xenditTransactionFeeDTO struct {
	XenditFee                int64 `json:"xendit_fee"`
	ValueAddedTax            int64 `json:"value_added_tax"`
	XenditWithholdingTax     int64 `json:"xendit_withholding_tax"`
	ThirdPartyWithholdingTax int64 `json:"third_party_withholding_tax"`
}

type xenditProductDataDTO struct {
	PaymentSessionID string `json:"payment_session_id"`
}

// ─── HTTP helpers ─────────────────────────────────────────────────────────────

func (p *Provider) post(ctx context.Context, path string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	p.setHeaders(req)
	return p.do(req, out)
}

func (p *Provider) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	p.setHeaders(req)
	return p.do(req, out)
}

func (p *Provider) getWithParams(ctx context.Context, path string, params url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if len(params) > 0 {
		req.URL.RawQuery = params.Encode()
	}
	p.setHeaders(req)
	return p.do(req, out)
}

// setHeaders sets authentication and content-type headers for the master account.
func (p *Provider) setHeaders(req *http.Request) {
	req.SetBasicAuth(p.apiKey, "")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("api-version", xenditAPIVersion)
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
