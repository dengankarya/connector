// Package domain contains the core payment entities and their business rules.
// No infrastructure dependencies — only stdlib and uuid.
package domain

import (
	"time"

	"github.com/google/uuid"
)

// PaymentStatus is the lifecycle state of a payment transaction.
type PaymentStatus string

const (
	StatusPending         PaymentStatus = "pending"
	StatusAwaitingPayment PaymentStatus = "awaiting_payment"
	StatusPaid            PaymentStatus = "paid"
	StatusSettled         PaymentStatus = "settled"
	StatusRefunding       PaymentStatus = "refunding"
	StatusRefunded        PaymentStatus = "refunded"
	StatusExpired         PaymentStatus = "expired"
	StatusFailed          PaymentStatus = "failed"
	StatusVoided          PaymentStatus = "voided"
)

// validTransitions is the authoritative state machine.
// Every allowed transition must be listed here — nothing else is permitted.
var validTransitions = map[PaymentStatus][]PaymentStatus{
	StatusPending:         {StatusAwaitingPayment, StatusFailed, StatusVoided},
	StatusAwaitingPayment: {StatusPaid, StatusExpired, StatusFailed},
	StatusPaid:            {StatusSettled, StatusRefunding},
	StatusSettled:         {StatusRefunding},
	StatusRefunding:       {StatusRefunded},
	StatusRefunded:        {},
	StatusExpired:         {},
	StatusFailed:          {},
	StatusVoided:          {},
}

// CanTransitionTo returns true when transitioning from s to next is a valid move.
func (s PaymentStatus) CanTransitionTo(next PaymentStatus) bool {
	for _, allowed := range validTransitions[s] {
		if allowed == next {
			return true
		}
	}
	return false
}

// PaymentTransaction is the central entity tracking a single payment's lifecycle.
// All monetary amounts are in the smallest currency unit (e.g. IDR integer, no decimals).
type PaymentTransaction struct {
	ID                uuid.UUID      `json:"id,omitempty"`
	TenantID          int64          `json:"tenant_id,omitempty"`
	OrderNumber       string         `json:"order_number,omitempty"` // Tokokarya order number (string, not UUID)
	IdempotencyKey    string         `json:"idempotency_key,omitempty"`
	Provider          string         `json:"provider,omitempty"`            // "xendit", "midtrans", etc.
	ProviderInvoiceID string         `json:"provider_invoice_id,omitempty"` // Xendit payment_session_id (ps-xxx)
	ProviderPaymentID string         `json:"provider_payment_id,omitempty"` // Xendit payment_id (py-xxx); populated on payment.capture
	CheckoutURL       string         `json:"checkout_url,omitempty"`        // Xendit invoice payment page URL returned to the caller
	XenditAccountID   string         `json:"xendit_account_id,omitempty"`   // Merchant's Xendit sub-account ID; used as for-user-id and transfer destination
	PaymentMethod     string         `json:"payment_method,omitempty"`      // e.g. "BANK_TRANSFER", "QRIS", "CREDIT_CARD"
	PaymentChannel    string         `json:"payment_channel,omitempty"`     // e.g. "BRI", "MANDIRI", "OVO"
	Amount            int64          `json:"amount,omitempty"`              // gross amount (merchant_amount + platform_fee)
	Currency          string         `json:"currency,omitempty"`
	PlatformFee       int64          `json:"platform_fee,omitempty"`
	MerchantAmount    int64          `json:"merchant_amount,omitempty"` // Amount - PlatformFee
	Status            PaymentStatus  `json:"status,omitempty"`
	Description       string         `json:"description,omitempty"`
	Metadata          map[string]any `json:"metadata,omitempty"`
	ExpiresAt         *time.Time     `json:"expires_at,omitempty"`
	PaidAt            *time.Time     `json:"paid_at,omitempty"`
	SettledAt         *time.Time     `json:"settled_at,omitempty"`
	CreatedAt         time.Time      `json:"created_at,omitempty"`
	UpdatedAt         time.Time      `json:"updated_at,omitempty"`
	Version           int            `json:"version,omitempty"` // optimistic lock version; increment on every write
}

// TransitionTo attempts a state transition.
// Returns ErrAlreadyInState (idempotent, not an error in most callers) if already there.
// Returns ErrInvalidStatusTransition for any other invalid transition.
func (t *PaymentTransaction) TransitionTo(next PaymentStatus) error {
	if t.Status == next {
		return ErrAlreadyInState{State: next}
	}
	if !t.Status.CanTransitionTo(next) {
		return ErrInvalidStatusTransition{From: t.Status, To: next}
	}
	t.Status = next
	t.Version++
	return nil
}

// IsFinalState returns true when no further transitions are possible.
func (t *PaymentTransaction) IsFinalState() bool {
	return len(validTransitions[t.Status]) == 0
}

// Validate checks internal consistency of the transaction amounts.
func (t *PaymentTransaction) Validate() error {
	if t.MerchantAmount+t.PlatformFee != t.Amount {
		return ErrAmountMismatch{
			Amount:         t.Amount,
			MerchantAmount: t.MerchantAmount,
			PlatformFee:    t.PlatformFee,
		}
	}
	return nil
}
