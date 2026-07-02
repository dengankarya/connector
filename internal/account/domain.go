// Package account provides the unified merchant account ledger view.
// It merges payment transactions, shipping balance topups, and shipping holds
// into a single activity feed accessible at GET /accounts/transactions.
package account

import (
	"context"
	"time"

	"github.com/dengankarya/connector/common"
	"github.com/google/uuid"
)

var (
	ErrNotFound               = common.NewDomainError("NF_TRANSACTION_NOT_FOUND", "transaction not found")
	ErrInsufficientBalance    = common.ErrInsufficientBalance // PR_INSUFFICIENT_BALANCE
	ErrHoldNotFound           = common.NewDomainError("NF_HOLD_NOT_FOUND", "shipping hold not found")
	ErrHoldAlreadyActioned    = common.NewDomainError("CF_HOLD_ALREADY_ACTIONED", "shipping hold already confirmed or released")
	ErrDuplicateHold          = common.NewDomainError("CF_DUPLICATE_HOLD", "an active hold already exists for this order")
	ErrInvalidCursor          = common.NewDomainError("BR_INVALID_CURSOR", "invalid pagination cursor")
	ErrGatewayAccountNotFound = common.NewDomainError("NF_GATEWAY_ACCOUNT_NOT_FOUND", "no gateway account found for this tenant")
	ErrGatewayAccountExists   = common.NewDomainError("CF_GATEWAY_ACCOUNT_EXISTS", "a gateway account already exists for this tenant")
	ErrGatewayNotConfigured   = common.NewDomainError("SV_GATEWAY_NOT_CONFIGURED", "payment gateway is not configured")
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
// payment_payouts in our DB — no real-time gateway API call is required.
type MerchantPaymentBalance struct {
	TenantID int64 `json:"tenant_id"`

	// Settled is the total merchant_amount across all 'settled' transactions.
	Settled int64 `json:"settled"`

	// PendingSettlement is merchant_amount across 'paid' transactions not yet
	// marked settled (typically T+1 or T+2 depending on the gateway).
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
// payment settlement balance (settled/pending/paid-out), and the live gateway sub-account balance.
type UnifiedBalance struct {
	// Shipping wallet — available funds and funds on hold for pending shipments.
	Shipping *ShippingBalance `json:"shipping"`
	// Payment settlement — computed from payment_transactions and payment_payouts.
	Payment *MerchantPaymentBalance `json:"payment"`
	// Gateway is the real-time balance from the payment gateway sub-account.
	// Nil when no gateway sub-account is configured for this tenant.
	Gateway *GatewayBalance `json:"gateway,omitempty"`
}

// ─── Gateway account types ────────────────────────────────────────────────────

// GatewayAccount is the stored record of a merchant's payment gateway sub-account.
type GatewayAccount struct {
	ID               uuid.UUID `json:"id"`
	TenantID         int64     `json:"tenant_id"`
	Gateway          string    `json:"gateway"`
	GatewayAccountID string    `json:"gateway_account_id"`
	Email            string    `json:"email"`
	Name             string    `json:"name"`
	Status           string    `json:"status"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// GatewayBalance is the real-time balance reported by the payment gateway sub-account.
type GatewayBalance struct {
	GatewayAccountID string `json:"gateway_account_id"`
	Pending          int64  `json:"pending"`
	Available        int64  `json:"available"`
	Currency         string `json:"currency"`
}

// GatewayClient is the interface for payment gateway sub-account operations.
// Implemented structurally by *doku.Client.
type GatewayClient interface {
	CreateSubAccount(ctx context.Context, email, name string) (gatewayAccountID, status string, err error)
	GetBalance(ctx context.Context, gatewayAccountID string) (pending, available int64, err error)
	SendPayout(ctx context.Context, gatewayAccountID string, amount int64, invoiceNumber, bankCode, bankAccountNumber, bankAccountName string) (status string, err error)
}

// XenditGatewayClient extends GatewayClient with Xendit-specific account management.
// Implemented structurally by *xendit.Client.
type XenditGatewayClient interface {
	GatewayClient
	// GetAccount fetches the current account status and public profile from Xendit.
	GetAccount(ctx context.Context, accountID string) (*XenditAccountInfo, error)
	// CreateAccountHolder submits KYC business details for a sub-account.
	// Returns the Xendit account_holder_id on success.
	CreateAccountHolder(ctx context.Context, subAccountID string, req CreateAccountHolderRequest) (accountHolderID string, err error)
	// LinkAccountHolder links an account holder to a sub-account via PATCH /v2/accounts/{id}.
	// This must be called after CreateAccountHolder to begin the verification flow.
	LinkAccountHolder(ctx context.Context, subAccountID, accountHolderID string) error
}

// AccountHolderBusinessDetail is the KYC business information for CreateAccountHolder.
type AccountHolderBusinessDetail struct {
	Type               string `json:"type"`                          // CORPORATION | PARTNERSHIP | SOLE_PROPRIETORSHIP | INDIVIDUAL
	LegalName          string `json:"legal_name"`
	TradingName        string `json:"trading_name,omitempty"`
	Description        string `json:"description,omitempty"`
	IndustryCategory   string `json:"industry_category,omitempty"`
	DateOfRegistration string `json:"date_of_registration,omitempty"` // YYYY-MM-DD
	CountryOfOperation string `json:"country_of_operation"`           // "ID"
}

// AccountHolderAddress is the registered address for CreateAccountHolder.
type AccountHolderAddress struct {
	Country       string `json:"country"`
	City          string `json:"city"`
	ProvinceState string `json:"province_state,omitempty"`
	StreetLine1   string `json:"street_line1"`
	PostalCode    string `json:"postal_code"`
}

// CreateAccountHolderRequest is the input for CreateAccountHolder.
// Defined here (not in pkg/xendit) so the interface can reference it without circular imports.
type CreateAccountHolderRequest struct {
	BusinessDetail AccountHolderBusinessDetail `json:"business_detail"`
	Address        AccountHolderAddress        `json:"address"`
	Email          string                      `json:"email"`
	PhoneNumber    string                      `json:"phone_number"`
	WebsiteURL     string                      `json:"website_url,omitempty"`
}

// XenditAccountInfo holds the live account status returned by Xendit's GET /v2/accounts/{id}.
// Defined here (not in pkg/xendit) so pkg/xendit can import internal/account without
// introducing a circular dependency (internal/account does not import pkg/xendit).
type XenditAccountInfo struct {
	ID            string `json:"id"`
	Type          string `json:"type"`
	Email         string `json:"email"`
	Status        string `json:"status"`
	PublicProfile struct {
		Name    string `json:"name"`
		Country string `json:"country"`
	} `json:"public_profile"`
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

// CreateGatewaySubAccountBody is the request body for POST /accounts/gateway/sub-account.
type CreateGatewaySubAccountBody struct {
	Email string `json:"email" example:"toko-abc@example.com"`
	Name  string `json:"name" example:"Toko ABC"`
}

// SendGatewayPayoutBody is the request body for POST /accounts/gateway/payout.
type SendGatewayPayoutBody struct {
	Amount            int64  `json:"amount" example:"100000"`
	InvoiceNumber     string `json:"invoice_number" example:"INV/2026/001"`
	BankCode          string `json:"bank_code" example:"BNINIDJA"`
	BankAccountNumber string `json:"bank_account_number" example:"0123456789"`
	BankAccountName   string `json:"bank_account_name" example:"Budi Santoso"`
}
