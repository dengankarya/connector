package domain

import (
	"time"

	"github.com/google/uuid"
)

// MerchantSettlementSnapshot holds precomputed balance totals for a single tenant.
// Updated nightly by SyncSettlementJob; read by the balance endpoint to avoid on-the-fly aggregation.
type MerchantSettlementSnapshot struct {
	ID                 uuid.UUID  `json:"id"`
	TenantID           int64      `json:"tenant_id"`
	PendingBalance     int64      `json:"pending_balance"`      // SUM(merchant_amount) where status=paid
	PendingPlatformFee int64      `json:"pending_platform_fee"` // SUM(platform_fee) where status=paid
	PendingXenditFee   int64      `json:"pending_xendit_fee"`   // SUM(xendit_fee) where status=paid
	PendingVAT         int64      `json:"pending_vat"`          // SUM(vat) where status=paid
	PendingWithholding int64      `json:"pending_withholding"`  // SUM(xendit_wht + third_party_wht) where status=paid
	SettledBalance     int64      `json:"settled_balance"`      // SUM(merchant_amount) where status=settled
	SettledPlatformFee int64      `json:"settled_platform_fee"` // SUM(platform_fee) where status=settled
	SettledXenditFee   int64      `json:"settled_xendit_fee"`   // SUM(xendit_fee) where status=settled
	SettledVAT         int64      `json:"settled_vat"`          // SUM(vat) where status=settled
	SettledWithholding int64      `json:"settled_withholding"`  // SUM(xendit_wht + third_party_wht) where status=settled
	LastSyncedAt       *time.Time `json:"last_synced_at"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}
