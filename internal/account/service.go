package account

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/dengankarya/connector/common"
	"github.com/dengankarya/connector/pkg/postgres"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)

// Service handles all account-level operations: activity feed, shipping balance, holds, topups.
type Service struct {
	repo     *Repository
	txRunner *postgres.TxRunner
	logger   *logrus.Logger
}

// NewService creates a Service.
func NewService(repo *Repository, txRunner *postgres.TxRunner, logger *logrus.Logger) *Service {
	return &Service{repo: repo, txRunner: txRunner, logger: logger}
}

// ─── Activity feed ────────────────────────────────────────────────────────────

const (
	defaultPageSize = 20
	maxPageSize     = 100
)

// ListTransactions returns a cursor-paginated unified activity feed for the tenant.
func (s *Service) ListTransactions(ctx context.Context, tenantID int64, filter TransactionFilter) (*common.PaginationResponse[*ActivityItem], error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = defaultPageSize
	} else if limit > maxPageSize {
		limit = maxPageSize
	}

	p := ActivityListParams{
		Limit: limit + 1, // fetch one extra to detect has_more
		Types: filter.Types,
		From:  filter.From,
		To:    filter.To,
	}

	if filter.Cursor != "" {
		cp, err := decodeActivityCursor(filter.Cursor)
		if err != nil {
			return nil, ErrInvalidCursor
		}
		p.Cursor = cp
	}

	items, err := s.repo.ListActivity(ctx, tenantID, p)
	if err != nil {
		return nil, err
	}

	result := &common.PaginationResponse[*ActivityItem]{Items: items}
	if len(items) > limit {
		result.Items = items[:limit]
		result.HasMore = true
		last := result.Items[limit-1]
		result.NextCursor = encodeActivityCursor(last.CreatedAt, last.ID.String())
	}
	return result, nil
}

// GetTransaction returns the full detail for a single activity item.
// For payment items, ledger entries are included.
func (s *Service) GetTransaction(ctx context.Context, tenantID int64, id uuid.UUID, activityType ActivityType) (*ActivityDetail, error) {
	return s.repo.GetDetailByID(ctx, tenantID, id, activityType)
}

// ─── cursor encoding ──────────────────────────────────────────────────────────

type activityCursorPayload struct {
	T time.Time `json:"t"`
	I string    `json:"i"` // UUID as string
}

func encodeActivityCursor(createdAt time.Time, id string) string {
	b, _ := json.Marshal(activityCursorPayload{T: createdAt.UTC(), I: id})
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeActivityCursor(s string) (*ActivityCursorPoint, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("base64 decode: %w", err)
	}
	var p activityCursorPayload
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("json unmarshal: %w", err)
	}
	return &ActivityCursorPoint{CreatedAt: p.T, ID: p.I}, nil
}

// ─── Shipping balance ─────────────────────────────────────────────────────────

// GetBalance returns the current available and on-hold totals for a tenant's shipping wallet.
func (s *Service) GetBalance(ctx context.Context, tenantID int64) (*ShippingBalance, error) {
	return s.repo.GetBalance(ctx, tenantID)
}

// GetPaymentBalance returns the transaction-derived payment settlement balance for a tenant.
// Settled/pending figures are aggregated from payment_transactions; paid_out from payment_payouts.
func (s *Service) GetPaymentBalance(ctx context.Context, tenantID int64) (*MerchantPaymentBalance, error) {
	return s.repo.GetPaymentBalance(ctx, tenantID)
}

// GetGatewayAccountIDForTenant returns the gateway sub-account ID for the given tenant and provider.
// Returns an empty string (not an error) when no sub-account has been provisioned.
func (s *Service) GetGatewayAccountIDForTenant(ctx context.Context, tenantID int64, provider string) (string, error) {
	acct, err := s.repo.GetGatewayAccountByTenantID(ctx, tenantID, provider)
	if err != nil {
		return "", err
	}
	return acct.GatewayAccountID, nil
}

// GetUnifiedBalance returns the combined shipping wallet and payment settlement balance.
func (s *Service) GetUnifiedBalance(ctx context.Context, tenantID int64) (*UnifiedBalance, error) {
	shipping, err := s.repo.GetBalance(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("unified balance: shipping: %w", err)
	}
	payment, err := s.repo.GetPaymentBalance(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("unified balance: payment: %w", err)
	}
	return &UnifiedBalance{Shipping: shipping, Payment: payment}, nil
}

// TopupRequest is the input for a manual balance top-up.
type TopupRequest struct {
	TenantID int64
	Amount   int64
	Currency string
	Note     string
}

// Topup credits the merchant's available balance and records the top-up audit row.
func (s *Service) Topup(ctx context.Context, req TopupRequest) (*ShippingTopup, error) {
	if req.Amount <= 0 {
		return nil, fmt.Errorf("topup amount must be positive")
	}
	topup := &ShippingTopup{
		TenantID: req.TenantID,
		Amount:   req.Amount,
		Currency: req.Currency,
		Note:     req.Note,
	}
	err := s.txRunner.RunInTx(ctx, func(txCtx context.Context) error {
		if err := s.repo.CreditAvailable(txCtx, req.TenantID, req.Amount, req.Currency); err != nil {
			return fmt.Errorf("topup: credit balance: %w", err)
		}
		if err := s.repo.CreateTopup(txCtx, topup); err != nil {
			return fmt.Errorf("topup: record audit: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.logger.WithFields(logrus.Fields{
		"component": "account",
		"tenant_id": req.TenantID,
		"amount":    req.Amount,
	}).Info("shipping balance topped up")
	return topup, nil
}

// AdjustShippingBalance applies a shipping price correction to the merchant's available balance
// and records an audit row in shipping_price_adjustments.
// diff = newPrice - originalPrice:
//   - diff > 0: actual cost was higher → deduct the extra from available
//   - diff < 0: actual cost was lower  → credit the saving back to available
//   - diff = 0: no-op
func (s *Service) AdjustShippingBalance(ctx context.Context, tenantID int64, oldPrice, newPrice int64, currency, orderNumber string) error {
	diff := newPrice - oldPrice
	if diff == 0 {
		return nil
	}
	err := s.txRunner.RunInTx(ctx, func(txCtx context.Context) error {
		if diff > 0 {
			if err := s.repo.DeductAvailableOnly(txCtx, tenantID, diff); err != nil {
				return err
			}
		} else {
			if err := s.repo.CreditAvailable(txCtx, tenantID, -diff, currency); err != nil {
				return err
			}
		}
		adj := &ShippingPriceAdjustment{
			TenantID:    tenantID,
			OrderNumber: orderNumber,
			OldPrice:    oldPrice,
			NewPrice:    newPrice,
			Diff:        diff,
			Currency:    currency,
		}
		return s.repo.CreatePriceAdjustment(txCtx, adj)
	})
	if err != nil {
		return err
	}
	s.logger.WithFields(logrus.Fields{
		"component":    "account",
		"tenant_id":    tenantID,
		"order_number": orderNumber,
		"old_price":    oldPrice,
		"new_price":    newPrice,
		"diff":         diff,
	}).Info("shipping balance adjusted for price correction")
	return nil
}

// ListTopups returns all topups for a tenant.
func (s *Service) ListTopups(ctx context.Context, tenantID int64) ([]*ShippingTopup, error) {
	return s.repo.ListTopups(ctx, tenantID)
}

// ─── Holds ────────────────────────────────────────────────────────────────────

// CreateHoldRequest is the input for reserving shipping funds for a draft order.
type CreateHoldRequest struct {
	TenantID    int64
	OrderNumber string
	Amount      int64
	Currency    string
}

// CreateHold deducts amount from available and places it on hold for the given order.
func (s *Service) CreateHold(ctx context.Context, req CreateHoldRequest) (*ShippingHold, error) {
	if req.Amount <= 0 {
		return nil, fmt.Errorf("hold amount must be positive")
	}
	hold := &ShippingHold{
		TenantID:    req.TenantID,
		OrderNumber: req.OrderNumber,
		Amount:      req.Amount,
		Currency:    req.Currency,
	}
	err := s.txRunner.RunInTx(ctx, func(txCtx context.Context) error {
		if err := s.repo.DeductAvailableAndHold(txCtx, req.TenantID, req.Amount); err != nil {
			return err
		}
		return s.repo.CreateHold(txCtx, hold)
	})
	if err != nil {
		return nil, err
	}
	s.logger.WithFields(logrus.Fields{
		"component":    "account",
		"tenant_id":    req.TenantID,
		"order_number": req.OrderNumber,
		"amount":       req.Amount,
	}).Info("shipping hold created")
	return hold, nil
}

// ConfirmHold transitions a hold from holding → confirmed and removes the on_hold amount.
func (s *Service) ConfirmHold(ctx context.Context, tenantID int64, holdID uuid.UUID) (*ShippingHold, error) {
	var hold *ShippingHold
	err := s.txRunner.RunInTx(ctx, func(txCtx context.Context) error {
		var err error
		hold, err = s.repo.GetHoldByID(txCtx, holdID)
		if err != nil {
			return err
		}
		if hold.TenantID != tenantID {
			return ErrHoldNotFound
		}
		if hold.Status != HoldStatusHolding {
			return ErrHoldAlreadyActioned
		}
		now := time.Now().UTC()
		if err := s.repo.UpdateHoldStatus(txCtx, holdID, HoldStatusConfirmed, now); err != nil {
			return err
		}
		if err := s.repo.DeductHold(txCtx, tenantID, hold.Amount); err != nil {
			return err
		}
		hold.Status = HoldStatusConfirmed
		hold.ConfirmedAt = &now
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.logger.WithFields(logrus.Fields{
		"component": "account",
		"tenant_id": tenantID,
		"hold_id":   holdID,
	}).Info("shipping hold confirmed")
	return hold, nil
}

// ReleaseHold transitions a hold from holding → released and returns funds to available.
func (s *Service) ReleaseHold(ctx context.Context, tenantID int64, holdID uuid.UUID) (*ShippingHold, error) {
	var hold *ShippingHold
	err := s.txRunner.RunInTx(ctx, func(txCtx context.Context) error {
		var err error
		hold, err = s.repo.GetHoldByID(txCtx, holdID)
		if err != nil {
			return err
		}
		if hold.TenantID != tenantID {
			return ErrHoldNotFound
		}
		if hold.Status != HoldStatusHolding {
			return ErrHoldAlreadyActioned
		}
		now := time.Now().UTC()
		if err := s.repo.UpdateHoldStatus(txCtx, holdID, HoldStatusReleased, now); err != nil {
			return err
		}
		if err := s.repo.ReturnHoldToAvailable(txCtx, tenantID, hold.Amount); err != nil {
			return err
		}
		hold.Status = HoldStatusReleased
		hold.ReleasedAt = &now
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.logger.WithFields(logrus.Fields{
		"component": "account",
		"tenant_id": tenantID,
		"hold_id":   holdID,
	}).Info("shipping hold released")
	return hold, nil
}

// ValidateShippingConfirm checks whether the tenant has sufficient funds to confirm a shipment.
// If an active hold exists for the order, it is treated as pre-reserved and the check passes.
// Otherwise, available_balance must be >= requiredAmount.
// Returns ErrInsufficientBalance when funds are insufficient.
func (s *Service) ValidateShippingConfirm(ctx context.Context, tenantID int64, orderNumber string, requiredAmount int64) error {
	_, err := s.repo.GetHoldByOrderNumber(ctx, tenantID, orderNumber)
	if err == nil {
		return nil // active hold exists — funds already reserved
	}
	if !errors.Is(err, ErrHoldNotFound) {
		return fmt.Errorf("validate shipping confirm: lookup hold: %w", err)
	}
	// No hold — check available balance directly.
	bal, err := s.repo.GetBalance(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("validate shipping confirm: get balance: %w", err)
	}
	if bal.Available < requiredAmount {
		return ErrInsufficientBalance
	}
	return nil
}

// ConfirmHoldForOrder confirms the active shipping hold for the given order number.
// When no hold exists, deducts amount directly from available balance and records a
// confirmed hold row so the deduction appears in the activity feed.
func (s *Service) ConfirmHoldForOrder(ctx context.Context, tenantID int64, orderNumber string, amount int64) error {
	hold, err := s.repo.GetHoldByOrderNumber(ctx, tenantID, orderNumber)
	if err != nil {
		if errors.Is(err, ErrHoldNotFound) {
			if amount <= 0 {
				return nil
			}
			// No hold was pre-created — deduct from available and record a confirmed hold
			// in a single transaction so the activity feed reflects the deduction.
			return s.txRunner.RunInTx(ctx, func(txCtx context.Context) error {
				if err := s.repo.DeductAvailableOnly(txCtx, tenantID, amount); err != nil {
					return err
				}
				now := time.Now().UTC()
				return s.repo.InsertHold(txCtx, &ShippingHold{
					TenantID:    tenantID,
					OrderNumber: orderNumber,
					Amount:      amount,
					Currency:    "IDR",
					Status:      HoldStatusConfirmed,
					ConfirmedAt: &now,
				})
			})
		}
		return fmt.Errorf("confirm hold for order %q: lookup: %w", orderNumber, err)
	}
	_, err = s.ConfirmHold(ctx, tenantID, hold.ID)
	if errors.Is(err, ErrHoldAlreadyActioned) {
		return nil // idempotent
	}
	return err
}

// ReleaseHoldForOrder releases the active shipping hold for the given order number,
// returning the reserved funds to the available balance.
// It is a no-op when no active hold exists or the hold is already actioned.
func (s *Service) ReleaseHoldForOrder(ctx context.Context, tenantID int64, orderNumber string) error {
	hold, err := s.repo.GetHoldByOrderNumber(ctx, tenantID, orderNumber)
	if err != nil {
		if errors.Is(err, ErrHoldNotFound) {
			return nil
		}
		return fmt.Errorf("release hold for order %q: lookup: %w", orderNumber, err)
	}
	_, err = s.ReleaseHold(ctx, tenantID, hold.ID)
	if errors.Is(err, ErrHoldAlreadyActioned) {
		return nil
	}
	return err
}

// ListHolds returns all holds for a tenant.
func (s *Service) ListHolds(ctx context.Context, tenantID int64) ([]*ShippingHold, error) {
	return s.repo.ListHolds(ctx, tenantID)
}

// ─── Payout requests ──────────────────────────────────────────────────────────

// RequestPayout creates a pending merchant payout withdrawal record.
// The platform operator processes the actual disbursement manually via DurianPay.
func (s *Service) RequestPayout(ctx context.Context, tenantID int64, body PayoutRequestBody) (*PayoutRequest, error) {
	req := &PayoutRequest{
		TenantID:      tenantID,
		Amount:        body.Amount,
		Currency:      body.Currency,
		BankCode:      body.BankCode,
		AccountNumber: body.AccountNumber,
		AccountName:   body.AccountName,
		Description:   body.Description,
	}
	if err := s.repo.InsertPayoutRequest(ctx, req); err != nil {
		s.logger.WithError(err).WithField("tenant_id", tenantID).Error("insert payout request failed")
		return nil, fmt.Errorf("create payout request: %w", err)
	}
	return req, nil
}
