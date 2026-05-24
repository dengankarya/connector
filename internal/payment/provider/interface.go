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
	ForUserID              string   // provider sub-account ID (e.g. Xendit for-user-id header)
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
	CustomerReferenceID    string
	ExpiresAt              *time.Time
	Metadata               map[string]any
}

// Invoice is a provider-agnostic invoice response.
type Invoice struct {
	ProviderInvoiceID string
	CheckoutURL       string
	Status            string
	Amount            int64
	Currency          string
	ExpiresAt         *time.Time
}

// CreateRefundRequest is the provider-agnostic refund request.
type CreateRefundRequest struct {
	ForUserID         string // provider sub-account ID (e.g. Xendit for-user-id header)
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
	ForUserID     string // provider sub-account ID (e.g. Xendit for-user-id header)
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

// TransferRequest is the provider-agnostic fund transfer request.
// Used to route merchant_amount to a sub-account after payment.
type TransferRequest struct {
	Reference         string // unique idempotency key for this transfer
	Amount            int64
	Currency          string
	DestinationUserID string // merchant's sub-account ID at the provider
}

// TransferResponse is the provider-agnostic transfer response.
type TransferResponse struct {
	ProviderTransferID string
	Status             string
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

// BalanceRequest is a provider-agnostic balance request
type BalanceRequest struct {
	ForUserID string
}

// Balance is a provider-agnostic balance
type Balance struct {
	Balance int
}

// PaymentProvider abstracts all interactions with an external payment gateway.
// Implementations live in sub-packages (xendit/, midtrans/, etc.).
type PaymentProvider interface {
	// CreateInvoice creates a payment request at the provider.
	CreateInvoice(ctx context.Context, req CreateInvoiceRequest) (*Invoice, error)

	// GetInvoice fetches the current provider-side state of an invoice.
	GetInvoice(ctx context.Context, invoiceID, forUserID string) (*Invoice, error)

	// CancelInvoice voids an invoice that has not yet been paid.
	CancelInvoice(ctx context.Context, invoiceID, forUserID string) error

	// CreateRefund initiates a refund for a paid invoice.
	CreateRefund(ctx context.Context, req CreateRefundRequest) (*Refund, error)

	// ValidateWebhookSignature verifies the webhook callback is authentic.
	// Returns domain.ErrInvalidSignature or domain.ErrMissingSignature on failure.
	ValidateWebhookSignature(ctx context.Context, payload []byte, headers map[string]string) error

	// ParseWebhookEvent converts a raw payload into a typed, provider-agnostic event.
	ParseWebhookEvent(ctx context.Context, payload []byte) (*WebhookEvent, error)

	// CreatePayout initiates a bank transfer disbursement.
	CreatePayout(ctx context.Context, req CreatePayoutRequest) (*Payout, error)

	// Transfer moves funds from the platform account to a merchant sub-account.
	// Called automatically after a payment is marked paid to route merchant_amount.
	Transfer(ctx context.Context, req TransferRequest) (*TransferResponse, error)

	// GetBalance retrieve merchant sub-account balance.
	GetBalance(ctx context.Context, req BalanceRequest) (*Balance, error)

	// ProviderName returns the canonical provider identifier (e.g. "xendit").
	ProviderName() string
}
