package domain

import (
	"time"

	"github.com/google/uuid"
)

// PayoutStatus is the lifecycle of a merchant disbursement.
type PayoutStatus string

const (
	PayoutStatusPending    PayoutStatus = "pending"
	PayoutStatusProcessing PayoutStatus = "processing"
	PayoutStatusCompleted  PayoutStatus = "completed"
	PayoutStatusFailed     PayoutStatus = "failed"
	PayoutStatusCancelled  PayoutStatus = "cancelled"
)

// Payout tracks a single merchant settlement disbursement.
type Payout struct {
	ID               uuid.UUID    `json:"id,omitempty"`
	TenantID         int64        `json:"tenant_id,omitempty"`
	ForUserID        string       `json:"for_user_id,omitempty"` // Xendit sub-account ID used as the for-user-id request header
	Provider         string       `json:"provider,omitempty"`
	ProviderPayoutID string       `json:"provider_payout_id,omitempty"`
	Amount           int64        `json:"amount,omitempty"`
	Currency         string       `json:"currency,omitempty"`
	Status           PayoutStatus `json:"status,omitempty"`
	BankCode         string       `json:"bank_code,omitempty"`
	AccountNumber    string       `json:"account_number,omitempty"`
	AccountName      string       `json:"account_name,omitempty"`
	Description      string       `json:"description,omitempty"`
	FailureReason    string       `json:"failure_reason,omitempty"`
	RetryCount       int          `json:"retry_count,omitempty"`
	MaxRetries       int          `json:"max_retries,omitempty"`
	ScheduledAt      *time.Time   `json:"scheduled_at,omitempty"`
	ProcessedAt      *time.Time   `json:"processed_at,omitempty"`
	CreatedAt        time.Time    `json:"created_at,omitempty"`
	UpdatedAt        time.Time    `json:"updated_at,omitempty"`
}

// PayoutItem links a specific payment transaction to a payout batch.
// A transaction appears in at most one payout (enforced by unique index).
type PayoutItem struct {
	ID            uuid.UUID `json:"id,omitempty"`
	PayoutID      uuid.UUID `json:"payout_id,omitempty"`
	TransactionID uuid.UUID `json:"transaction_id,omitempty"`
	Amount        int64     `json:"amount,omitempty"`
	CreatedAt     time.Time `json:"created_at,omitempty"`
}
