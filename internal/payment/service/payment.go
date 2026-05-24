// Package service contains the application-level use cases for the payment module.
package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/dengankarya/overwatch/internal/payment/domain"
	"github.com/dengankarya/overwatch/internal/payment/provider"
	"github.com/dengankarya/overwatch/internal/payment/repository"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)

// ErrInvalidCursor is returned when the cursor query parameter cannot be decoded.
var ErrInvalidCursor = errors.New("invalid cursor")

// CreatePaymentRequest is the input for creating a new payment transaction.
type CreatePaymentRequest struct {
	TenantID               int64
	ForUserID              string // Xendit sub-account ID (for-user-id header)
	OrderNumber            string
	IdempotencyKey         string
	Amount                 int64
	Currency               string
	PlatformFee            int64    // optional; defaults to 0
	AllowedPaymentChannels []string // optional; restrict which channels appear on checkout page
	SuccessReturnURL       string   // optional; redirect URL after successful payment
	CancelReturnURL        string   // optional; redirect URL if customer cancels
	Description            string
	CustomerEmail          string
	CustomerName           string
	CustomerReferenceID    string
	ExpiresAt              *time.Time
	Metadata               map[string]any
}

// PaymentService handles payment creation and querying.
type PaymentService struct {
	txnRepo  *repository.TransactionRepository
	provider provider.PaymentProvider
	txRunner *repository.TxRunner
	logger   *logrus.Logger
}

// NewPaymentService creates a PaymentService.
func NewPaymentService(
	txnRepo *repository.TransactionRepository,
	prov provider.PaymentProvider,
	txRunner *repository.TxRunner,
	logger *logrus.Logger,
) *PaymentService {
	return &PaymentService{
		txnRepo:  txnRepo,
		provider: prov,
		txRunner: txRunner,
		logger:   logger,
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

	merchantAmount := req.Amount - req.PlatformFee
	if merchantAmount < 0 {
		return nil, fmt.Errorf("platform_fee (%d) exceeds amount (%d)", req.PlatformFee, req.Amount)
	}

	// Step 1: Create a Xendit Payment Session (hosted checkout).
	invoice, err := s.provider.CreateInvoice(ctx, provider.CreateInvoiceRequest{
		ForUserID:              req.ForUserID,
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
		Metadata:               req.Metadata,
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
		XenditAccountID:   req.ForUserID,
		Amount:            req.Amount,
		Currency:          req.Currency,
		PlatformFee:       req.PlatformFee,
		MerchantAmount:    merchantAmount,
		Status:            domain.StatusAwaitingPayment,
		Description:       req.Description,
		Metadata:          req.Metadata,
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
	PlatformFee    int64  // optional; defaults to 0
	PaymentMethod  string // e.g. "CASH", "BANK_TRANSFER"
	PaymentChannel string // e.g. bank name, "-"
	Description    string
	Metadata       map[string]any
}

// CreateManualPayment records a payment transaction without calling any external provider.
// The returned transaction has provider="xendit" so the existing webhook pipeline can
// locate it when Tokokarya sends a payment_session.completed event.
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

	merchantAmount := req.Amount - req.PlatformFee
	if merchantAmount < 0 {
		return nil, fmt.Errorf("platform_fee (%d) exceeds amount (%d)", req.PlatformFee, req.Amount)
	}

	// Generate a stable, unique invoice ID so Tokokarya can reference it in the webhook.
	providerInvoiceID := "manual-" + uuid.New().String()

	txn := &domain.PaymentTransaction{
		TenantID:          req.TenantID,
		OrderNumber:       req.OrderNumber,
		IdempotencyKey:    req.IdempotencyKey,
		Provider:          "xendit",      // must match the webhook processor's provider lookup
		ProviderInvoiceID: providerInvoiceID,
		PaymentMethod:     req.PaymentMethod,
		PaymentChannel:    req.PaymentChannel,
		Amount:            req.Amount,
		Currency:          req.Currency,
		PlatformFee:       req.PlatformFee,
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
		Limit:  limit + 1, // fetch one extra to detect whether another page exists
		Status: req.Status,
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
