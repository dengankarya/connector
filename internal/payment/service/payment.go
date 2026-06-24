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

// GatewayAccountFinder resolves a tenant's payment gateway sub-account ID.
// account.Service satisfies this interface.
type GatewayAccountFinder interface {
	GetGatewayAccountIDForTenant(ctx context.Context, tenantID int64) (string, error)
}

// TransactionStore abstracts payment transaction persistence.
type TransactionStore interface {
	Create(ctx context.Context, txn *domain.PaymentTransaction) error
	GetByID(ctx context.Context, id uuid.UUID) (*domain.PaymentTransaction, error)
	GetByIDForUpdate(ctx context.Context, id uuid.UUID) (*domain.PaymentTransaction, error)
	GetByProviderInvoiceIDForUpdate(ctx context.Context, provider, invoiceID string) (*domain.PaymentTransaction, error)
	GetByProviderInvoiceID(ctx context.Context, provider, invoiceID string) (*domain.PaymentTransaction, error)
	Update(ctx context.Context, txn *domain.PaymentTransaction) error
	ListDistinctTenants(ctx context.Context) ([]int64, error)
	ListByTenant(ctx context.Context, tenantID int64, p repository.ListParams) ([]*domain.PaymentTransaction, error)
	ListExpired(ctx context.Context, limit int) ([]*domain.PaymentTransaction, error)
	SumPendingSettlement(ctx context.Context, tenantID int64) (*repository.PendingSettlementSums, error)
	SumSettled(ctx context.Context, tenantID int64) (*repository.PendingSettlementSums, error)
}

// WebhookEventStore abstracts webhook event persistence and status tracking.
type WebhookEventStore interface {
	Create(ctx context.Context, e *domain.WebhookEvent) error
	GetByID(ctx context.Context, id uuid.UUID) (*domain.WebhookEvent, error)
	GetByIDForUpdate(ctx context.Context, id uuid.UUID) (*domain.WebhookEvent, error)
	GetByProviderEventID(ctx context.Context, provider, eventID string) (*domain.WebhookEvent, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status domain.WebhookProcessingStatus, lastError string) error
	IncrementAttempts(ctx context.Context, id uuid.UUID) error
	MarkProcessed(ctx context.Context, eventID uuid.UUID, transactionID uuid.UUID) error
	ResetForReplay(ctx context.Context, id uuid.UUID) error
	ListFailed(ctx context.Context, limit int) ([]*domain.WebhookEvent, error)
	ListDeadLettered(ctx context.Context, limit, offset int) ([]*domain.WebhookEvent, error)
}

// PayoutStore abstracts payout persistence.
type PayoutStore interface {
	Create(ctx context.Context, p *domain.Payout) error
	GetByIDForUpdate(ctx context.Context, id uuid.UUID) (*domain.Payout, error)
	Update(ctx context.Context, p *domain.Payout) error
	ListPending(ctx context.Context, limit int) ([]*domain.Payout, error)
	ListByTenant(ctx context.Context, tenantID int64, limit, offset int) ([]*domain.Payout, error)
}

// PaymentService handles payment creation and querying.
type PaymentService struct {
	txnRepo       TransactionStore
	ledger        *ledger.Service
	txRunner      *repository.TxRunner
	gatewayFinder GatewayAccountFinder // optional; nil = skip sub-account lookup
	logger        *logrus.Logger
}

// NewPaymentService creates a PaymentService.
// gatewayFinder may be nil.
func NewPaymentService(
	txnRepo TransactionStore,
	txRunner *repository.TxRunner,
	ledgerSvc *ledger.Service,
	gatewayFinder GatewayAccountFinder,
	logger *logrus.Logger,
) *PaymentService {
	return &PaymentService{
		txnRepo:       txnRepo,
		ledger:        ledgerSvc,
		txRunner:      txRunner,
		gatewayFinder: gatewayFinder,
		logger:        logger,
	}
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

// ─── Manual payment ───────────────────────────────────────────────────────────

// CreateManualPaymentRequest is the input for recording a transaction that is paid
// outside the platform (cash, direct bank transfer, etc.).
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
// The returned transaction has provider="manual_transfer". Confirm via POST /api/payments/manual/:id/confirm.
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

// ─── Provider payment ─────────────────────────────────────────────────────────

// CreateProviderPaymentRequest is the input for creating a payment via an external provider.
type CreateProviderPaymentRequest struct {
	TenantID           int64
	OrderNumber        string
	IdempotencyKey     string
	Amount             int64
	Currency           string
	PlatformFee        int64
	ShippingFee        int64
	CustomerName       string
	CustomerEmail      string
	CustomerMobile     string
	Description        string
	SuccessReturnURL   string
	CancelReturnURL    string
	ExpiresAt          *time.Time
	Metadata           map[string]string
	PaymentType        string
	PaymentMethodTypes []string
	ResultURL          string // result page URL for DOKU; empty = omitted
}

// CreateProviderPayment creates a payment session at the given provider and persists
// the transaction in awaiting_payment status. The returned transaction includes the
// checkout_url returned by the provider.
//
// Idempotent: if (tenant_id, idempotency_key) already exists, returns domain.ErrDuplicateIdempotencyKey.
func (s *PaymentService) CreateProviderPayment(ctx context.Context, prov provider.PaymentProvider, req CreateProviderPaymentRequest) (*domain.PaymentTransaction, error) {
	log := s.logger.WithFields(logrus.Fields{
		"component":       "payment_service",
		"operation":       "create_provider_payment",
		"provider":        prov.ProviderName(),
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

	var gatewayAccountID string
	if s.gatewayFinder != nil {
		if id, err := s.gatewayFinder.GetGatewayAccountIDForTenant(ctx, req.TenantID); err == nil {
			gatewayAccountID = id
		}
	}

	invoice, err := prov.CreateInvoice(ctx, provider.CreateInvoiceRequest{
		ExternalID:         req.IdempotencyKey,
		Amount:             req.Amount,
		Currency:           req.Currency,
		Description:        req.Description,
		CustomerName:       req.CustomerName,
		CustomerEmail:      req.CustomerEmail,
		CustomerMobile:     req.CustomerMobile,
		SuccessReturnURL:   req.SuccessReturnURL,
		CancelReturnURL:    req.CancelReturnURL,
		ExpiresAt:          req.ExpiresAt,
		Metadata:           req.Metadata,
		GatewayAccountID:   gatewayAccountID,
		PaymentType:        req.PaymentType,
		PaymentMethodTypes: req.PaymentMethodTypes,
		ResultURL:          req.ResultURL,
	})
	if err != nil {
		log.WithError(err).Error("create provider payment: create invoice failed")
		return nil, fmt.Errorf("create provider invoice: %w", err)
	}

	txn := &domain.PaymentTransaction{
		TenantID:          req.TenantID,
		OrderNumber:       req.OrderNumber,
		IdempotencyKey:    req.IdempotencyKey,
		Provider:          prov.ProviderName(),
		ProviderInvoiceID: invoice.ProviderInvoiceID,
		CheckoutURL:       invoice.CheckoutURL,
		Amount:            req.Amount,
		Currency:          req.Currency,
		PlatformFee:       req.PlatformFee,
		ShippingFee:       req.ShippingFee,
		MerchantAmount:    merchantAmount,
		Status:            domain.StatusAwaitingPayment,
		Description:       req.Description,
		ExpiresAt:         invoice.ExpiresAt,
		Metadata:          req.Metadata,
	}

	if err := s.txnRepo.Create(ctx, txn); err != nil {
		if err == domain.ErrDuplicateIdempotencyKey {
			log.Warn("create provider payment: idempotency hit")
			return nil, domain.ErrDuplicateIdempotencyKey
		}
		log.WithError(err).Error("create provider payment: db insert failed")
		return nil, fmt.Errorf("persist provider transaction: %w", err)
	}

	log.WithFields(logrus.Fields{
		"transaction_id":      txn.ID,
		"provider_invoice_id": txn.ProviderInvoiceID,
		"checkout_url":        txn.CheckoutURL,
	}).Info("provider payment transaction created")

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
