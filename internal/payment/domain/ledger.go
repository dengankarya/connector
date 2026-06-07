package domain

import (
	"time"

	"github.com/google/uuid"
)

// LedgerDirection indicates money flow direction within an account.
type LedgerDirection string

// LedgerAccountType classifies the accounting bucket for a ledger entry.
type LedgerAccountType string

const (
	DirectionCredit LedgerDirection = "credit"
	DirectionDebit  LedgerDirection = "debit"

	// AccountEscrow — gross funds held by the platform on behalf of merchants.
	AccountEscrow LedgerAccountType = "escrow"
	// AccountMerchantPayable — net amount the platform owes a merchant after fees.
	AccountMerchantPayable LedgerAccountType = "merchant_payable"
	// AccountPlatformFee — revenue earned by the platform (connector commission).
	AccountPlatformFee LedgerAccountType = "platform_fee"
	// AccountShippingBalance — shipping credits held for a merchant.
	// Topped up when a customer pays the shipping portion; debited when a shipment is dispatched.
	AccountShippingBalance LedgerAccountType = "shipping_balance"
	// AccountShippingDisbursed — funds paid out to the shipping provider (e.g. Biteship).
	AccountShippingDisbursed LedgerAccountType = "shipping_disbursed"
	// AccountPayout — disbursements made to merchants.
	AccountPayout LedgerAccountType = "payout"
	// AccountRefund — funds returned to customers.
	AccountRefund LedgerAccountType = "refund"
	// AccountMerchantDirect — funds received directly by the merchant (cash, bank transfer)
	// that never passed through platform escrow. Used only for manual payments.
	AccountMerchantDirect LedgerAccountType = "merchant_direct"
)

// LedgerEntry is an immutable, append-only record of a single financial movement.
// NEVER update or delete a ledger entry — only append.
type LedgerEntry struct {
	ID             uuid.UUID         `json:"id,omitempty"`
	TenantID       int64             `json:"tenant_id,omitempty"`
	TransactionID  uuid.UUID         `json:"transaction_id,omitempty"`
	WebhookEventID *uuid.UUID        `json:"webhook_event_id,omitempty"`
	AccountType    LedgerAccountType `json:"account_type,omitempty"`
	Direction      LedgerDirection   `json:"direction,omitempty"`
	Amount         int64             `json:"amount,omitempty"` // always positive; direction carries the sign
	Currency       string            `json:"currency,omitempty"`
	ReferenceID    string            `json:"reference_id,omitempty"` // globally unique idempotency key for this specific entry
	Description    string            `json:"description,omitempty"`
	Metadata       map[string]any    `json:"metadata,omitempty"`
	CreatedAt      time.Time         `json:"created_at,omitempty"`
}

// LedgerJournal groups a balanced set of debit/credit entries for one financial event.
// Double-entry invariant: sum(debits) must equal sum(credits).
type LedgerJournal struct {
	Entries []LedgerEntry
}

// Validate checks that the journal is balanced.
// An imbalanced journal is a programming bug and must panic before reaching storage.
func (j *LedgerJournal) Validate() error {
	var totalDebit, totalCredit int64
	for _, e := range j.Entries {
		switch e.Direction {
		case DirectionDebit:
			totalDebit += e.Amount
		case DirectionCredit:
			totalCredit += e.Amount
		}
	}
	if totalDebit != totalCredit {
		return ErrLedgerImbalance{Debit: totalDebit, Credit: totalCredit}
	}
	return nil
}
