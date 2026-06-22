// Package mayar implements the PaymentProvider interface for the Mayar payment gateway.
package mayar

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/dengankarya/connector/internal/payment/domain"
	"github.com/dengankarya/connector/internal/payment/provider"
	mayarpkg "github.com/dengankarya/connector/pkg/mayar"
	"github.com/sirupsen/logrus"
)

// Ensure Provider implements both interfaces at compile time.
var _ provider.PaymentProvider = (*Provider)(nil)
var _ provider.TransactionSyncer = (*Provider)(nil)

const providerName = "mayar"

// Provider implements provider.PaymentProvider for Mayar.
type Provider struct {
	client        *mayarpkg.Client
	callbackToken string // expected value of X-Callback-Token; empty = skip validation
	logger        *logrus.Logger
}

// NewProvider creates a Mayar PaymentProvider.
// callbackToken is the value you configured in the Mayar dashboard under webhook settings.
// Pass an empty string to skip signature validation (not recommended in production).
func NewProvider(client *mayarpkg.Client, callbackToken string) *Provider {
	return &Provider{
		client:        client,
		callbackToken: callbackToken,
		logger:        logrus.StandardLogger(),
	}
}

// ProviderName returns "mayar".
func (p *Provider) ProviderName() string { return providerName }

// ListTransactions fetches settled transactions from Mayar for settlement reconciliation.
// Implements provider.TransactionSyncer.
//
// Mayar fee mapping:
//
//	fee[balanceHistoryType="xendit_fee"].debit  → ProviderTransaction.XenditFee
//	fee[balanceHistoryType="mayar_fee"].debit   → ProviderTransaction.XenditWithholdingTax (repurposed)
func (p *Provider) ListTransactions(ctx context.Context, req provider.ListTransactionsRequest) (*provider.ListTransactionsResult, error) {
	page := 1
	if req.AfterID != "" {
		// AfterID encodes the page number as a string for Mayar's page-based pagination.
		// We increment by re-requesting the next page after the last seen ID.
		// For simplicity, we use page-based pagination and store the page as AfterID.
		// The caller (sync job) manages page advancement.
		fmt.Sscanf(req.AfterID, "%d", &page)
	}

	limit := req.Limit
	if limit <= 0 {
		limit = 50
	}

	var startAtMs int64
	if !req.CreatedGTE.IsZero() {
		startAtMs = req.CreatedGTE.UnixMilli()
	}

	resp, err := p.client.ListTransactions(ctx, mayarpkg.ListTransactionsRequest{
		Page:     page,
		PageSize: limit,
		Status:   "settled",
		StartAt:  startAtMs,
	})
	if err != nil {
		return nil, fmt.Errorf("mayar: list transactions: %w", err)
	}

	txns := make([]provider.ProviderTransaction, 0, len(resp.Data))
	for _, t := range resp.Data {
		var xenditFee, mayarFee int64
		for _, f := range t.Fee {
			switch f.BalanceHistoryType {
			case "xendit_fee":
				xenditFee = f.Debit
			case "mayar_fee":
				mayarFee = f.Debit
			}
		}

		created := time.UnixMilli(t.CreatedAt).UTC()
		txns = append(txns, provider.ProviderTransaction{
			ID:                   t.ID,
			SettlementStatus:     "SETTLED",
			XenditFee:            xenditFee,
			XenditWithholdingTax: mayarFee, // Mayar's own platform fee
			PaymentSessionID:     t.PaymentLinkTransactionID,
			Created:              created,
		})
	}

	nextPage := page + 1
	lastID := ""
	if resp.HasMore {
		lastID = fmt.Sprintf("%d", nextPage)
	}

	return &provider.ListTransactionsResult{
		Transactions: txns,
		HasMore:      resp.HasMore,
		LastID:       lastID,
	}, nil
}

// CreateInvoice creates a Mayar invoice and maps the response to a provider-agnostic Invoice.
func (p *Provider) CreateInvoice(ctx context.Context, req provider.CreateInvoiceRequest) (*provider.Invoice, error) {
	expiredAtStr := ""
	if req.ExpiresAt != nil {
		expiredAtStr = req.ExpiresAt.UTC().Format(time.RFC3339)
	}

	mayarReq := mayarpkg.CreateInvoiceRequest{
		Name:        req.CustomerName,
		Email:       req.CustomerEmail,
		Mobile:      req.CustomerMobile,
		RedirectURL: req.SuccessReturnURL,
		Description: req.Description,
		ExpiredAt:   expiredAtStr,
		Items: []mayarpkg.Item{
			{
				Quantity:    1,
				Rate:        req.Amount,
				Description: req.Description,
			},
		},
		ExtraData: mayarpkg.ExtraData{
			NoCustomer: req.ExternalID,
			IDProd:     req.Metadata["order_number"],
		},
	}

	resp, err := p.client.CreateInvoice(ctx, mayarReq)
	if err != nil {
		return nil, fmt.Errorf("mayar: create invoice: %w", err)
	}

	var expiresAt *time.Time
	if resp.Data.ExpiredAt > 0 {
		t := time.UnixMilli(resp.Data.ExpiredAt).UTC()
		expiresAt = &t
	}

	return &provider.Invoice{
		ProviderInvoiceID: resp.Data.ID,
		CheckoutURL:       resp.Data.Link,
		Status:            "PENDING",
		Amount:            req.Amount,
		Currency:          req.Currency,
		ExpiresAt:         expiresAt,
	}, nil
}

// GetInvoice is not yet supported for Mayar.
func (p *Provider) GetInvoice(_ context.Context, _ string) (*provider.Invoice, error) {
	return nil, fmt.Errorf("mayar: GetInvoice: %w", domain.ErrNotSupported)
}

// CancelInvoice is not yet supported for Mayar.
func (p *Provider) CancelInvoice(_ context.Context, _ string) error {
	return fmt.Errorf("mayar: CancelInvoice: %w", domain.ErrNotSupported)
}

// CreateRefund is not yet supported for Mayar.
func (p *Provider) CreateRefund(_ context.Context, _ provider.CreateRefundRequest) (*provider.Refund, error) {
	return nil, fmt.Errorf("mayar: CreateRefund: %w", domain.ErrNotSupported)
}

// CreatePayout is not yet supported for Mayar.
func (p *Provider) CreatePayout(_ context.Context, _ provider.CreatePayoutRequest) (*provider.Payout, error) {
	return nil, fmt.Errorf("mayar: CreatePayout: %w", domain.ErrNotSupported)
}

// ValidateWebhookSignature checks the X-Callback-Token header against the configured token.
// If callbackToken is empty, validation is skipped with a warning.
func (p *Provider) ValidateWebhookSignature(_ context.Context, _ []byte, headers map[string]string) error {
	if p.callbackToken == "" {
		p.logger.Warn("mayar: MAYAR_CALLBACK_TOKEN not set — skipping webhook signature validation")
		return nil
	}
	token := headers["X-Callback-Token"]
	if token == "" {
		return domain.ErrMissingSignature
	}
	if token != p.callbackToken {
		return domain.ErrInvalidSignature
	}
	return nil
}

// ParseWebhookEvent parses a raw Mayar webhook payload and returns a normalized WebhookEvent.
// Mayar event types are mapped to the internal event type vocabulary:
//
//	payment.received → payment.capture  (routes to handlePaid in the processor)
//	all others       → passed through as-is (processor treats unknown types as no-ops)
func (p *Provider) ParseWebhookEvent(_ context.Context, payload []byte) (*provider.WebhookEvent, error) {
	var envelope struct {
		Event string `json:"event"`
		Data  struct {
			ID             string `json:"id"`
			TransactionID  string `json:"transactionId"`
			Status         string `json:"status"`
			Amount         int    `json:"amount"`
			CustomerName   string `json:"customerName"`
			CustomerEmail  string `json:"customerEmail"`
			CustomerMobile string `json:"customerMobile"`
			MerchantID     string `json:"merchantId"`
		} `json:"data"`
	}

	if err := json.Unmarshal(payload, &envelope); err != nil {
		return nil, fmt.Errorf("mayar: parse webhook payload: %w", err)
	}

	if envelope.Data.ID == "" {
		return nil, fmt.Errorf("mayar: webhook payload missing data.id")
	}

	eventType := envelope.Event
	if eventType == "payment.received" {
		eventType = "payment.capture"
	}

	return &provider.WebhookEvent{
		ProviderEventID:   envelope.Data.ID,
		EventType:         eventType,
		ProviderInvoiceID: envelope.Data.ID,
		Amount:            int64(envelope.Data.Amount),
		RawPayload:        payload,
	}, nil
}
