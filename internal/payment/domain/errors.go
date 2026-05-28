package domain

import "fmt"

// ErrNotFound is returned when a requested entity does not exist.
type ErrNotFound struct {
	Entity string
	ID     string
}

func (e ErrNotFound) Error() string {
	return fmt.Sprintf("%s not found: %s", e.Entity, e.ID)
}

// ErrInvalidStatusTransition is returned when a state machine transition is illegal.
type ErrInvalidStatusTransition struct {
	From PaymentStatus
	To   PaymentStatus
}

func (e ErrInvalidStatusTransition) Error() string {
	return fmt.Sprintf("invalid payment status transition: %s → %s", e.From, e.To)
}

// ErrAlreadyInState is returned when a transition targets the current state (idempotent).
type ErrAlreadyInState struct {
	State PaymentStatus
}

func (e ErrAlreadyInState) Error() string {
	return fmt.Sprintf("transaction already in state: %s", e.State)
}

// ErrLedgerImbalance is returned when a journal's debits do not equal its credits.
type ErrLedgerImbalance struct {
	Debit  int64
	Credit int64
}

func (e ErrLedgerImbalance) Error() string {
	return fmt.Sprintf("ledger imbalance: debit=%d credit=%d diff=%d", e.Debit, e.Credit, e.Debit-e.Credit)
}

// ErrAmountMismatch is returned when amount != merchant_amount + platform_fee + shipping_fee.
type ErrAmountMismatch struct {
	Amount         int64
	MerchantAmount int64
	PlatformFee    int64
	ShippingFee    int64
}

func (e ErrAmountMismatch) Error() string {
	return fmt.Sprintf("amount mismatch: amount=%d merchant_amount=%d platform_fee=%d shipping_fee=%d sum=%d",
		e.Amount, e.MerchantAmount, e.PlatformFee, e.ShippingFee,
		e.MerchantAmount+e.PlatformFee+e.ShippingFee)
}

// Sentinel errors — compared with errors.Is.
var (
	ErrDuplicateIdempotencyKey = fmt.Errorf("duplicate idempotency key")
	ErrDuplicateWebhookEvent   = fmt.Errorf("duplicate webhook event")
	ErrDuplicateLedgerEntry    = fmt.Errorf("duplicate ledger entry")
	ErrVersionConflict         = fmt.Errorf("version conflict: row was concurrently modified")
	ErrInvalidSignature        = fmt.Errorf("invalid webhook signature")
	ErrMissingSignature        = fmt.Errorf("missing webhook signature header")
	ErrEventAlreadyProcessed   = fmt.Errorf("webhook event already processed (use force=true to replay)")
)
