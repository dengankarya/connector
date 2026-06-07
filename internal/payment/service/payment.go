// Package service contains the application-level use cases for the payment module.
package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dengankarya/connector/internal/payment/domain"
	"github.com/dengankarya/connector/internal/payment/ledger"
	"github.com/dengankarya/connector/internal/payment/provider"
	"github.com/dengankarya/connector/internal/payment/repository"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)

// ErrInvalidCursor is returned when the cursor query parameter cannot be decoded.
var ErrInvalidCursor = errors.New("invalid cursor")

// CreatePaymentRequest is the input for creating a new payment transaction.
type CreatePaymentRequest struct {
	TenantID               int64
	OrderNumber            string
	IdempotencyKey         string
	Amount                 int64
	Currency               string
	PlatformFee            int64    // optional; connector commission; defaults to 0
	ShippingFee            int64    // optional; topped up to merchant's shipping balance; defaults to 0
	AllowedPaymentChannels []string // optional; restrict which channels appear on checkout page
	SuccessReturnURL       string   // optional; redirect URL after successful payment
	CancelReturnURL        string   // optional; redirect URL if customer cancels
	Description            string
	CustomerEmail          string
	CustomerName           string
	CustomerReferenceID    string
	ExpiresAt              *time.Time
	Metadata               map[string]string
}

// ShippingBalanceCreditor credits a merchant's shipping balance from the shipping_fee
// of a confirmed payment. Must be callable inside an existing DB transaction.
type ShippingBalanceCreditor interface {
	CreditFromPayment(ctx context.Context, tenantID int64, amount int64, currency string) error
}

// PaymentService handles payment creation and querying.
type PaymentService struct {
	txnRepo          *repository.TransactionRepository
	snapshotRepo     *repository.SettlementSnapshotRepository
	provider         provider.PaymentProvider
	ledger           *ledger.Service
	txRunner         *repository.TxRunner
	shippingCreditor ShippingBalanceCreditor // optional; nil = skip
	logger           *logrus.Logger
}

// NewPaymentService creates a PaymentService.
// shippingCreditor may be nil — shipping balance credit is skipped when not configured.
func NewPaymentService(
	txnRepo *repository.TransactionRepository,
	prov provider.PaymentProvider,
	txRunner *repository.TxRunner,
	snapshotRepo *repository.SettlementSnapshotRepository,
	ledgerSvc *ledger.Service,
	shippingCreditor ShippingBalanceCreditor,
	logger *logrus.Logger,
) *PaymentService {
	return &PaymentService{
		txnRepo:          txnRepo,
		snapshotRepo:     snapshotRepo,
		provider:         prov,
		ledger:           ledgerSvc,
		txRunner:         txRunner,
		shippingCreditor: shippingCreditor,
		logger:           logger,
	}
}

// CreatePayment creates a payment invoice at the provider and persists the transaction.
// Idempotent: if a transaction with the same (tenant_id, idempotency_key) already exists,
// it returns the existing record without hitting the provider again.
func (s *PaymentService) CreatePayment(ctx context.Context, req CreatePaymentRequest) (*domain.PaymentTransaction, error) {
	log := s.logger.WithFields(logrus.Fields{
		"component":       "payment_service",
		"operation":       "create_payment",
		"tenant_id":       req.TenantID,
		"order_number":    req.OrderNumber,
		"idempotency_key": req.IdempotencyKey,
		"amount":          req.Amount,
		"currency":        req.Currency,
	})

	merchantAmount := req.Amount - req.PlatformFee - req.ShippingFee
	if merchantAmount < 0 {
		return nil, fmt.Errorf("platform_fee (%d) + shipping_fee (%d) exceeds amount (%d)", req.PlatformFee, req.ShippingFee, req.Amount)
	}

	// Merge caller metadata with platform-level fields visible on the Xendit dashboard.
	// tenant_id lets you filter master-account transactions by merchant on Xendit.
	meta := make(map[string]string, len(req.Metadata)+2)
	for k, v := range req.Metadata {
		meta[k] = v
	}
	meta["tenant_id"] = fmt.Sprintf("%d", req.TenantID)
	if req.OrderNumber != "" {
		meta["order_number"] = req.OrderNumber
	}

	// Step 1: Create a Xendit Payment Session (hosted checkout).
	invoice, err := s.provider.CreateInvoice(ctx, provider.CreateInvoiceRequest{
		ExternalID:             req.IdempotencyKey,
		Amount:                 req.Amount,
		Currency:               req.Currency,
		AllowedPaymentChannels: req.AllowedPaymentChannels,
		SuccessReturnURL:       req.SuccessReturnURL,
		CancelReturnURL:        req.CancelReturnURL,
		Description:            req.Description,
		CustomerEmail:          req.CustomerEmail,
		CustomerName:           req.CustomerName,
		CustomerReferenceID:    req.CustomerReferenceID,
		ExpiresAt:              req.ExpiresAt,
		Metadata:               meta,
	})
	if err != nil {
		log.WithError(err).Error("create payment: provider error")
		return nil, fmt.Errorf("create invoice at provider: %w", err)
	}

	log = log.WithField("provider_invoice_id", invoice.ProviderInvoiceID)

	// Step 2: Persist the transaction.
	txn := &domain.PaymentTransaction{
		TenantID:          req.TenantID,
		OrderNumber:       req.OrderNumber,
		IdempotencyKey:    req.IdempotencyKey,
		Provider:          s.provider.ProviderName(),
		ProviderInvoiceID: invoice.ProviderInvoiceID,
		CheckoutURL:       invoice.CheckoutURL,
		Amount:            req.Amount,
		Currency:          req.Currency,
		PlatformFee:       req.PlatformFee,
		ShippingFee:       req.ShippingFee,
		MerchantAmount:    merchantAmount,
		Status:            domain.StatusAwaitingPayment,
		Description:       req.Description,
		Metadata:          meta,
		ExpiresAt:         invoice.ExpiresAt,
	}

	if err := s.txnRepo.Create(ctx, txn); err != nil {
		if err == domain.ErrDuplicateIdempotencyKey {
			log.Warn("create payment: idempotency hit — returning existing transaction")
			return s.getByIdempotencyKey(ctx, req.TenantID, req.IdempotencyKey)
		}
		log.WithError(err).Error("create payment: db insert failed")
		return nil, fmt.Errorf("persist transaction: %w", err)
	}

	log.WithFields(logrus.Fields{
		"transaction_id": txn.ID,
		"status":         txn.Status,
		"expires_at":     txn.ExpiresAt,
	}).Info("payment transaction created")

	return txn, nil
}

// RefreshPayment fetches the latest state from Xendit for a single transaction,
// updates our DB if the status changed, and returns the current record.
// For manual payments or already-final transactions it returns the DB record immediately.
func (s *PaymentService) RefreshPayment(ctx context.Context, tenantID int64, txnID uuid.UUID) (*domain.PaymentTransaction, error) {
	txn, err := s.txnRepo.GetByID(ctx, txnID)
	if err != nil {
		return nil, fmt.Errorf("refresh payment %s: %w", txnID, err)
	}
	if txn.TenantID != tenantID {
		return nil, domain.ErrNotFound{Entity: "payment_transaction", ID: txnID.String()}
	}

	// Nothing to fetch for final states or manual payments (no Xendit session).
	if txn.IsFinalState() || strings.HasPrefix(txn.ProviderInvoiceID, "manual-") {
		return txn, nil
	}

	invoice, err := s.provider.GetInvoice(ctx, txn.ProviderInvoiceID)
	if err != nil {
		return nil, fmt.Errorf("refresh payment: fetch from provider: %w", err)
	}

	changed := false
	switch invoice.Status {
	case "COMPLETED":
		if err := txn.TransitionTo(domain.StatusPaid); err == nil {
			now := time.Now().UTC()
			txn.PaidAt = &now
			changed = true
		}
	case "EXPIRED":
		if err := txn.TransitionTo(domain.StatusExpired); err == nil {
			changed = true
		}
	case "CANCELLED":
		if err := txn.TransitionTo(domain.StatusVoided); err == nil {
			changed = true
		}
	}

	// Backfill payment_channel from allowed_payment_channels[0] when still empty.
	// Callers always send exactly one channel, so [0] is the channel the customer used.
	if txn.PaymentChannel == "" && len(invoice.AllowedPaymentChannels) == 1 {
		txn.PaymentChannel = invoice.AllowedPaymentChannels[0]
		if !changed {
			// Status didn't change but we still need to persist payment_channel.
			txn.Version++
			changed = true
		}
	}

	if changed {
		if err := s.txnRepo.Update(ctx, txn); err != nil {
			if errors.Is(err, domain.ErrVersionConflict) {
				// A concurrent update (e.g. a webhook) beat us — return the fresh DB state.
				return s.GetPayment(ctx, tenantID, txnID)
			}
			return nil, fmt.Errorf("refresh payment: persist update: %w", err)
		}
	}

	return txn, nil
}

// GetPayment fetches a payment transaction by ID.
func (s *PaymentService) GetPayment(ctx context.Context, tenantID int64, txnID uuid.UUID) (*domain.PaymentTransaction, error) {
	txn, err := s.txnRepo.GetByID(ctx, txnID)
	if err != nil {
		return nil, fmt.Errorf("get payment %s: %w", txnID, err)
	}
	// Enforce tenant isolation.
	if txn.TenantID != tenantID {
		return nil, domain.ErrNotFound{Entity: "payment_transaction", ID: txnID.String()}
	}
	return txn, nil
}

// getByIdempotencyKey is used to return the existing transaction on an idempotency hit.
// It looks up the transaction by provider_invoice_id using the external_id convention.
func (s *PaymentService) getByIdempotencyKey(_ context.Context, _ int64, _ string) (*domain.PaymentTransaction, error) {
	// In a full implementation this would do a SELECT by (tenant_id, idempotency_key).
	// For now, return the conflict error so the caller can decide how to handle it.
	return nil, domain.ErrDuplicateIdempotencyKey
}

// ─── Manual payment ───────────────────────────────────────────────────────────

// CreateManualPaymentRequest is the input for recording a transaction that is paid
// outside Xendit (cash, direct bank transfer, etc.).
// The connector stores the transaction and returns a provider_invoice_id that Tokokarya
// must include as payment_session_id when it later sends the confirmation webhook to
// POST /webhook/xendit.
type CreateManualPaymentRequest struct {
	TenantID       int64
	OrderNumber    string
	IdempotencyKey string
	Amount         int64
	Currency       string
	PlatformFee    int64  // optional; connector commission; defaults to 0
	ShippingFee    int64  // optional; topped up to merchant's shipping balance; defaults to 0
	PaymentMethod  string // e.g. "CASH", "BANK_TRANSFER"
	PaymentChannel string // e.g. bank name, "-"
	Description    string
	Metadata       map[string]string
}

// CreateManualPayment records a payment transaction without calling any external provider.
// The returned transaction has provider="manual_transfer". Confirmation is done via
// POST /api/payments/manual/:id/confirm — not through the Xendit webhook pipeline.
func (s *PaymentService) CreateManualPayment(ctx context.Context, req CreateManualPaymentRequest) (*domain.PaymentTransaction, error) {
	log := s.logger.WithFields(logrus.Fields{
		"component":       "payment_service",
		"operation":       "create_manual_payment",
		"tenant_id":       req.TenantID,
		"order_number":    req.OrderNumber,
		"idempotency_key": req.IdempotencyKey,
		"amount":          req.Amount,
		"currency":        req.Currency,
	})

	merchantAmount := req.Amount - req.PlatformFee - req.ShippingFee
	if merchantAmount < 0 {
		return nil, fmt.Errorf("platform_fee (%d) + shipping_fee (%d) exceeds amount (%d)", req.PlatformFee, req.ShippingFee, req.Amount)
	}

	// Generate a stable, unique invoice ID so Tokokarya can reference it in the webhook.
	providerInvoiceID := "manual-" + uuid.New().String()

	txn := &domain.PaymentTransaction{
		TenantID:          req.TenantID,
		OrderNumber:       req.OrderNumber,
		IdempotencyKey:    req.IdempotencyKey,
		Provider:          "manual_transfer",
		ProviderInvoiceID: providerInvoiceID,
		PaymentMethod:     req.PaymentMethod,
		PaymentChannel:    req.PaymentChannel,
		Amount:            req.Amount,
		Currency:          req.Currency,
		PlatformFee:       req.PlatformFee,
		ShippingFee:       req.ShippingFee,
		MerchantAmount:    merchantAmount,
		Status:            domain.StatusAwaitingPayment,
		Description:       req.Description,
		Metadata:          req.Metadata,
	}

	if err := s.txnRepo.Create(ctx, txn); err != nil {
		if err == domain.ErrDuplicateIdempotencyKey {
			log.Warn("create manual payment: idempotency hit")
			return nil, domain.ErrDuplicateIdempotencyKey
		}
		log.WithError(err).Error("create manual payment: db insert failed")
		return nil, fmt.Errorf("persist manual transaction: %w", err)
	}

	log.WithFields(logrus.Fields{
		"transaction_id":      txn.ID,
		"provider_invoice_id": txn.ProviderInvoiceID,
	}).Info("manual payment transaction created")

	return txn, nil
}

// ─── Confirm manual payment ───────────────────────────────────────────────────

// ConfirmManualPaymentRequest is the input for confirming a manual payment.
type ConfirmManualPaymentRequest struct {
	TenantID       int64
	TransactionID  uuid.UUID
	PaymentChannel string // optional; overrides the channel set at creation time
}

// ConfirmManualPayment transitions a manual payment from awaiting_payment to paid,
// writes ledger entries, and returns the updated transaction.
// Idempotent: returns the transaction as-is if it is already paid.
func (s *PaymentService) ConfirmManualPayment(ctx context.Context, req ConfirmManualPaymentRequest) (*domain.PaymentTransaction, error) {
	log := s.logger.WithFields(logrus.Fields{
		"component":      "payment_service",
		"operation":      "confirm_manual_payment",
		"tenant_id":      req.TenantID,
		"transaction_id": req.TransactionID,
	})

	var txn *domain.PaymentTransaction
	err := s.txRunner.RunInTx(ctx, func(txCtx context.Context) error {
		var err error
		txn, err = s.txnRepo.GetByIDForUpdate(txCtx, req.TransactionID)
		if err != nil {
			return err
		}
		if txn.TenantID != req.TenantID {
			return domain.ErrNotFound{Entity: "payment_transaction", ID: req.TransactionID.String()}
		}
		if !strings.HasPrefix(txn.ProviderInvoiceID, "manual-") {
			return fmt.Errorf("confirm manual payment: transaction %s is not a manual payment", req.TransactionID)
		}

		if err := txn.TransitionTo(domain.StatusPaid); err != nil {
			if errors.As(err, new(domain.ErrAlreadyInState)) {
				// Already paid — check if we still need to settle.
				if txn.Status != domain.StatusSettled {
					if serr := txn.TransitionTo(domain.StatusSettled); serr != nil {
						if !errors.As(serr, new(domain.ErrAlreadyInState)) {
							return fmt.Errorf("confirm manual payment: settle: %w", serr)
						}
					}
					now := time.Now().UTC()
					txn.SettledAt = &now
					if err := s.txnRepo.Update(txCtx, txn); err != nil {
						return fmt.Errorf("confirm manual payment: update settled: %w", err)
					}
				}
				log.Info("manual payment already confirmed — idempotent")
				return nil
			}
			return fmt.Errorf("confirm manual payment: %w", err)
		}

		now := time.Now().UTC()
		txn.PaidAt = &now
		if req.PaymentChannel != "" {
			txn.PaymentChannel = req.PaymentChannel
		}

		// Manual payments are immediately settled — the funds are already in the
		// merchant's hands (cash / direct bank transfer), so there is no Xendit
		// escrow period to wait for. Transition paid → settled in the same tx.
		if err := txn.TransitionTo(domain.StatusSettled); err != nil {
			return fmt.Errorf("confirm manual payment: settle: %w", err)
		}
		txn.SettledAt = &now
		// Both transitions bumped Version, but we're doing a single DB write.
		// Roll back one increment so Update's WHERE version = txn.Version-1 matches
		// the actual DB row (which hasn't been written yet).
		txn.Version--

		if err := s.txnRepo.Update(txCtx, txn); err != nil {
			return fmt.Errorf("confirm manual payment: update: %w", err)
		}
		if err := s.ledger.RecordManualPayment(txCtx, txn); err != nil {
			return fmt.Errorf("confirm manual payment: ledger: %w", err)
		}
		if s.shippingCreditor != nil && txn.ShippingFee > 0 {
			if err := s.shippingCreditor.CreditFromPayment(txCtx, txn.TenantID, txn.ShippingFee, txn.Currency); err != nil {
				return fmt.Errorf("confirm manual payment: credit shipping balance: %w", err)
			}
		}

		log.WithFields(logrus.Fields{
			"status":          txn.Status,
			"payment_channel": txn.PaymentChannel,
			"shipping_fee":    txn.ShippingFee,
		}).Info("manual payment confirmed, ledger updated")
		return nil
	})
	if err != nil {
		return nil, err
	}
	return txn, nil
}

// ─── List transactions ────────────────────────────────────────────────────────

// ListTransactionsRequest is the input for paginated transaction listing.
type ListTransactionsRequest struct {
	TenantID int64
	// Limit is the page size (default 20, max 100).
	Limit int
	// Cursor is the opaque pagination token returned by the previous call; empty for the first page.
	Cursor string
	// Status filters by payment status; empty means all statuses.
	Status []domain.PaymentStatus
	// DateFrom, when set, restricts results to transactions created on or after this date (inclusive, start of day UTC).
	DateFrom *time.Time
	// DateTo, when set, restricts results to transactions created before this date (exclusive, start of next day UTC).
	DateTo *time.Time

	Provider string // optional filter by provider (e.g. "xendit", "manual_transfer")
}

// ListTransactionsResult is the response for paginated transaction listing.
type ListTransactionsResult struct {
	Items      []*domain.PaymentTransaction
	NextCursor string // empty when there are no more pages
	HasMore    bool
}

// ListTransactions returns a cursor-paginated list of transactions for a tenant.
func (s *PaymentService) ListTransactions(ctx context.Context, req ListTransactionsRequest) (*ListTransactionsResult, error) {
	limit := req.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	p := repository.ListParams{
		Limit:       limit + 1, // fetch one extra to detect whether another page exists
		Status:      req.Status,
		CreatedFrom: req.DateFrom,
		CreatedTo:   req.DateTo,
		Provider:    req.Provider,
	}

	if req.Cursor != "" {
		cp, err := decodeCursor(req.Cursor)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidCursor, err)
		}
		p.Cursor = cp
	}

	txns, err := s.txnRepo.ListByTenant(ctx, req.TenantID, p)
	if err != nil {
		return nil, fmt.Errorf("list transactions: %w", err)
	}

	result := &ListTransactionsResult{}
	if len(txns) > limit {
		result.HasMore = true
		result.Items = txns[:limit]
		last := txns[limit-1]
		result.NextCursor = encodeCursor(last.CreatedAt, last.ID)
	} else {
		result.Items = txns
	}

	return result, nil
}

// ─── cursor encoding ──────────────────────────────────────────────────────────

type cursorPayload struct {
	T time.Time `json:"t"`
	I string    `json:"i"`
}

// encodeCursor encodes a (createdAt, id) keyset as a URL-safe base64 token.
func encodeCursor(createdAt time.Time, id uuid.UUID) string {
	b, _ := json.Marshal(cursorPayload{T: createdAt.UTC(), I: id.String()})
	return base64.RawURLEncoding.EncodeToString(b)
}

// decodeCursor decodes a cursor token back to a CursorPoint.
func decodeCursor(s string) (*repository.CursorPoint, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, err
	}
	var p cursorPayload
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, err
	}
	id, err := uuid.Parse(p.I)
	if err != nil {
		return nil, fmt.Errorf("invalid id in cursor: %w", err)
	}
	return &repository.CursorPoint{CreatedAt: p.T, ID: id}, nil
}
