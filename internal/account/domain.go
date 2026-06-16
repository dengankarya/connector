// Package account provides the unified merchant account ledger view.
// It merges payment transactions, shipping balance topups, and shipping holds
// into a single activity feed accessible at GET /accounts/transactions.
package account

import (
	"time"

	"github.com/dengankarya/connector/common"
	"github.com/google/uuid"
)

var (
	ErrNotFound            = common.NewDomainError("NF_TRANSACTION_NOT_FOUND", "transaction not found")
	ErrInsufficientBalance = common.ErrInsufficientBalance // PR_INSUFFICIENT_BALANCE
	ErrHoldNotFound        = common.NewDomainError("NF_HOLD_NOT_FOUND", "shipping hold not found")
	ErrHoldAlreadyActioned = common.NewDomainError("CF_HOLD_ALREADY_ACTIONED", "shipping hold already confirmed or released")
	ErrDuplicateHold       = common.NewDomainError("CF_DUPLICATE_HOLD", "an active hold already exists for this order")
	ErrInvalidCursor       = common.NewDomainError("BR_INVALID_CURSOR", "invalid pagination cursor")
)

// TransactionFilter controls cursor-paginated listing of account activity.
type TransactionFilter struct {
	// Limit is the maximum number of items per page (default 20, max 100).
	Limit int
	// Cursor is the opaque token returned by the previous response; empty for the first page.
	Cursor string
	// Types restricts results to the given activity types; empty means all types.
	Types []ActivityType
	// From, when non-nil, restricts to items created at or after this time (inclusive).
	From *time.Time
	// To, when non-nil, restricts to items created before this time (exclusive).
	To *time.Time
}

// ActivityType classifies a single entry in the unified activity feed.
type ActivityType string

const (
	ActivityPayment                 ActivityType = "payment"                   // customer payment session
	ActivityBalanceTopup            ActivityType = "balance_topup"             // manual top-up by platform operator
	ActivityShipmentHold            ActivityType = "shipment_hold"             // funds reserved for a draft order
	ActivityShipmentConfirmed       ActivityType = "shipment_confirmed"        // shipment confirmed, funds disbursed
	ActivityShipmentReleased        ActivityType = "shipment_released"         // order cancelled, funds returned
	ActivityShipmentPriceAdjustment ActivityType = "shipment_price_adjustment" // actual weight differed from estimate
)

// ActivityItem is a single entry in the unified merchant activity feed.
// Read-only projection across payment_transactions, shipping_topups, and shipping_holds.
type ActivityItem struct {
	ID          uuid.UUID    `json:"id"`
	Type        ActivityType `json:"type"`
	Amount      int64        `json:"amount"`
	Currency    string       `json:"currency"`
	OrderNumber string       `json:"order_number,omitempty"`
	Status      string       `json:"status,omitempty"` // payment status or hold status
	Note        string       `json:"note,omitempty"`
	CreatedAt   time.Time    `json:"created_at"`
}

// LedgerEntry is a simplified projection of payment_ledger_entries for the detail view.
type LedgerEntry struct {
	AccountType string    `json:"account_type"`
	Direction   string    `json:"direction"`
	Amount      int64     `json:"amount"`
	Currency    string    `json:"currency"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
}

// ActivityDetail is the full detail for a single activity item.
// LedgerEntries and Metadata are populated for payment type items only.
type ActivityDetail struct {
	ActivityItem
	Metadata      map[string]any `json:"metadata,omitempty"`
	LedgerEntries []LedgerEntry  `json:"ledger_entries,omitempty"`
}

// ─── Shipping balance types ───────────────────────────────────────────────────

// ShippingBalance is the snapshot of a merchant's shipping wallet.
type ShippingBalance struct {
	ID        uuid.UUID `json:"id"`
	TenantID  int64     `json:"tenant_id"`
	Available int64     `json:"available"`
	OnHold    int64     `json:"on_hold"`
	Currency  string    `json:"currency"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// HoldStatus is the lifecycle state of a shipping hold.
type HoldStatus string

const (
	HoldStatusHolding   HoldStatus = "holding"
	HoldStatusConfirmed HoldStatus = "confirmed"
	HoldStatusReleased  HoldStatus = "released"
)

// ShippingHold reserves funds for a single draft order.
type ShippingHold struct {
	ID          uuid.UUID  `json:"id"`
	TenantID    int64      `json:"tenant_id"`
	OrderNumber string     `json:"order_number"`
	Amount      int64      `json:"amount"`
	Currency    string     `json:"currency"`
	Status      HoldStatus `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	ConfirmedAt *time.Time `json:"confirmed_at,omitempty"`
	ReleasedAt  *time.Time `json:"released_at,omitempty"`
}

// ShippingPriceAdjustment records a single shipping cost correction for an order.
// Created when the Biteship order.price webhook fires with a price different from the estimate.
type ShippingPriceAdjustment struct {
	ID          uuid.UUID `json:"id"`
	TenantID    int64     `json:"tenant_id"`
	OrderNumber string    `json:"order_number"`
	OldPrice    int64     `json:"old_price"`
	NewPrice    int64     `json:"new_price"`
	// Diff = NewPrice - OldPrice. Positive means balance was debited; negative means credited.
	Diff      int64     `json:"diff"`
	Currency  string    `json:"currency"`
	CreatedAt time.Time `json:"created_at"`
}

// ShippingTopup records a single manual top-up by the platform operator.
type ShippingTopup struct {
	ID        uuid.UUID `json:"id"`
	TenantID  int64     `json:"tenant_id"`
	Amount    int64     `json:"amount"`
	Currency  string    `json:"currency"`
	Note      string    `json:"note,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// ─── Payment balance ──────────────────────────────────────────────────────────

// MerchantPaymentBalance is the transaction-derived balance for a merchant's
// payment settlements. All figures are computed from payment_transactions and
// payment_payouts — no real-time Xendit API call is required.
type MerchantPaymentBalance struct {
	TenantID int64 `json:"tenant_id"`

	// Settled is the total merchant_amount across all 'settled' transactions.
	// Xendit has confirmed these funds landed in the platform master account.
	Settled int64 `json:"settled"`

	// PendingSettlement is merchant_amount across 'paid' transactions not yet
	// confirmed as settled by Xendit (typically takes 1–3 business days).
	PendingSettlement int64 `json:"pending_settlement"`

	// PaidOut is the total amount already disbursed to the merchant via
	// completed payouts from the platform account.
	PaidOut int64 `json:"paid_out"`

	// AvailableToPayout is Settled minus PaidOut — the amount the platform
	// can still disburse to the merchant.
	AvailableToPayout int64 `json:"available_to_payout"`

	Currency string `json:"currency"`
}

// UnifiedBalance is the combined merchant wallet view returned by GET /accounts/balance.
// It merges the shipping wallet (available/on-hold) with the transaction-derived
// payment settlement balance (settled/pending/paid-out).
type UnifiedBalance struct {
	// Shipping wallet — available funds and funds on hold for pending shipments.
	Shipping *ShippingBalance `json:"shipping"`
	// Payment settlement — computed from payment_transactions and payment_payouts.
	Payment *MerchantPaymentBalance `json:"payment"`
}

// ─── Request body types (used by Swagger) ────────────────────────────────────

// TopupBody is the request body for POST /accounts/balance/topup.
type TopupBody struct {
	Amount   int64  `json:"amount" example:"500000"`
	Currency string `json:"currency" example:"IDR"`
	Note     string `json:"note" example:"Top up for merchant A"`
}

// CreateHoldBody is the request body for POST /accounts/holds.
type CreateHoldBody struct {
	OrderNumber string `json:"order_number" example:"ORD-001"`
	Amount      int64  `json:"amount" example:"35000"`
	Currency    string `json:"currency" example:"IDR"`
}
