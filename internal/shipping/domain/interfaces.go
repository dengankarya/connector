package domain

import "context"

// AccountManager abstracts all shipping-related account operations.
type AccountManager interface {
	// ValidateShippingConfirm returns an error (including account.ErrInsufficientBalance) when
	// the merchant cannot cover the shipment cost. Returns nil when funds are available.
	ValidateShippingConfirm(ctx context.Context, tenantID int64, orderNumber string, requiredAmount int64) error
	// ConfirmHoldForOrder transitions an active shipping hold to confirmed, consuming the reserved funds.
	// When no hold exists, deducts amount directly from available balance.
	ConfirmHoldForOrder(ctx context.Context, tenantID int64, orderNumber string, amount int64) error
	// ReleaseHoldForOrder releases the active hold for the order, returning funds to available.
	ReleaseHoldForOrder(ctx context.Context, tenantID int64, orderNumber string) error
	// AdjustShippingBalance applies a price correction to the merchant's available balance
	// and records an audit row. oldPrice and newPrice are the before/after shipping costs.
	AdjustShippingBalance(ctx context.Context, tenantID int64, oldPrice, newPrice int64, currency, orderNumber string) error
}
