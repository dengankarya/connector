// Package provider defines the abstraction layer between the payment module and
// external payment gateways (Xendit, Midtrans, etc.).
// Add a new provider by implementing PaymentProvider — no changes to callers needed.
package provider

import (
	"context"
	"time"
)

// CreateInvoiceRequest is the provider-agnostic payment session creation input.
// Maps to Xendit POST /sessions with session_type=PAY, mode=PAYMENT_LINK.
type CreateInvoiceRequest struct {
	ExternalID             string   // caller's idempotency key / order reference (→ reference_id)
	Amount                 int64    // gross amount in smallest currency unit
	Currency               string   // e.g. "IDR"
	Country                string   // ISO 3166-1 alpha-2, e.g. "ID"; defaults to "ID" when empty
	AllowedPaymentChannels []string // optional; restrict which channels appear on checkout page
	SuccessReturnURL       string   // optional; redirect URL after successful payment
	CancelReturnURL        string   // optional; redirect URL if customer cancels
	Description            string
	CustomerEmail          string
	CustomerName           string
	CustomerMobile         string
	CustomerReferenceID    string
	ExpiresAt              *time.Time
	Metadata               map[string]string
	// GatewayAccountID is the provider sub-account ID to associate this payment with.
	// For DOKU this is the SAC account ID (e.g. "SAC-0000-..."), sent as
	// additional_info.account.id. Empty when the tenant has no sub-account.
	GatewayAccountID string
	// PaymentType is the transaction type: "SALE" (default), "INSTALLMENT", or "AUTHORIZE".
	PaymentType string
	// PaymentMethodTypes restricts which payment channels appear on the checkout page.
	// When empty, the provider shows all available methods.
	PaymentMethodTypes []string
	// ResultURL is provider-specific result page URL. Used by DOKU as callback_url_result. Optional.
	ResultURL string
}

// Invoice is a provider-agnostic invoice response.
type Invoice struct {
	ProviderInvoiceID      string
	CheckoutURL            string
	Status                 string
	Amount                 int64
	Currency               string
	ExpiresAt              *time.Time
	AllowedPaymentChannels []string // set on GET /sessions — used as payment_method fallback
}

// CreateRefundRequest is the provider-agnostic refund request.
type CreateRefundRequest struct {
	ProviderInvoiceID string
	Amount            int64
	Reason            string
	ExternalID        string // caller's idempotency key for this refund
}

// Refund is a provider-agnostic refund response.
type Refund struct {
	ProviderRefundID string
	Amount           int64
	Status           string
}

// CreatePayoutRequest is the provider-agnostic payout/disbursement request.
type CreatePayoutRequest struct {
	ExternalID    string
	Amount        int64
	Currency      string
	BankCode      string
	AccountNumber string
	AccountName   string
	Description   string
}

// Payout is a provider-agnostic payout response.
type Payout struct {
	ProviderPayoutID string
	Status           string
	Amount           int64
}

// WebhookEvent is a provider-agnostic parsed event from a webhook callback.
type WebhookEvent struct {
	ProviderEventID   string
	EventType         string // normalised: "payment.capture", "payment.failure", etc.
	ProviderInvoiceID string // payment_session_id (ps-xxx) in Xendit Sessions API
	PaymentID         string // payment_id (py-xxx) populated on payment.capture
	Amount            int64
	Currency          string
	ChannelCode       string // Xendit channel_code, e.g. "QRIS", "BCA"
	PaymentMethod     string // derived from ChannelCode for backwards compatibility
	PaymentChannel    string // derived from ChannelCode for backwards compatibility
	FailureCode       string // failure_code on payment.failure events
	PaidAt            *time.Time
	RawPayload        []byte
}

// ── Settlement sync ───────────────────────────────────────────────────────────

// ListTransactionsRequest is the input for fetching transactions from a provider.
type ListTransactionsRequest struct {
	CreatedGTE time.Time // only return transactions created at or after this time
	AfterID    string    // cursor for next page (last ID from previous response)
	Limit      int       // max results per page; provider may cap this
}

// ProviderTransaction is a provider-agnostic representation of a settled/pending transaction
// returned by the provider's transaction list API.
type ProviderTransaction struct {
	ID                      string
	SettlementStatus        string // "PENDING" | "SETTLED"
	XenditFee               int64
	VAT                     int64
	XenditWithholdingTax    int64
	ThirdPartyWHT           int64
	EstimatedSettlementTime *time.Time
	PaymentSessionID        string // links to our provider_invoice_id
	Created                 time.Time
}

// ListTransactionsResult is the paginated response from the provider transaction list.
type ListTransactionsResult struct {
	Transactions []ProviderTransaction
	HasMore      bool
	LastID       string // pass as AfterID in the next request
}

// TransactionSyncer is implemented by providers that expose a transaction list API
// for settlement reconciliation. Not all providers support this — the settlement
// sync job depends on this interface, not PaymentProvider.
type TransactionSyncer interface {
	ListTransactions(ctx context.Context, req ListTransactionsRequest) (*ListTransactionsResult, error)
}

// PaymentProvider abstracts all interactions with an external payment gateway.
// Implementations live in sub-packages (xendit/, midtrans/, etc.).
type PaymentProvider interface {
	// CreateInvoice creates a payment request at the provider.
	CreateInvoice(ctx context.Context, req CreateInvoiceRequest) (*Invoice, error)

	// GetInvoice fetches the current provider-side state of an invoice.
	GetInvoice(ctx context.Context, invoiceID string) (*Invoice, error)

	// CancelInvoice voids an invoice that has not yet been paid.
	CancelInvoice(ctx context.Context, invoiceID string) error

	// CreateRefund initiates a refund for a paid invoice.
	CreateRefund(ctx context.Context, req CreateRefundRequest) (*Refund, error)

	// ValidateWebhookSignature verifies the webhook callback is authentic.
	// Returns domain.ErrInvalidSignature or domain.ErrMissingSignature on failure.
	ValidateWebhookSignature(ctx context.Context, payload []byte, headers map[string]string) error

	// ParseWebhookEvent converts a raw payload into a typed, provider-agnostic event.
	ParseWebhookEvent(ctx context.Context, payload []byte) (*WebhookEvent, error)

	// CreatePayout initiates a bank transfer disbursement to a merchant's bank account.
	CreatePayout(ctx context.Context, req CreatePayoutRequest) (*Payout, error)

	// ProviderName returns the canonical provider identifier (e.g. "xendit").
	ProviderName() string
}
