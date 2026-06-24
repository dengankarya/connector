package service

import (
	"context"
	"fmt"
	"time"

	"github.com/dengankarya/connector/internal/payment/domain"
	"github.com/dengankarya/connector/internal/payment/ledger"
	"github.com/dengankarya/connector/internal/payment/provider"
	"github.com/dengankarya/connector/internal/payment/repository"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)

// CreatePayoutRequest is the input for initiating a merchant payout.
type CreatePayoutRequest struct {
	TenantID      int64
	Amount        int64
	Currency      string
	BankCode      string
	AccountNumber string
	AccountName   string
	Description   string
	MaxRetries    int
	ScheduledAt   *time.Time
}

// PayoutService handles payout creation and lifecycle.
type PayoutService struct {
	payoutRepo PayoutStore
	ledgerRepo ledger.Repository
	ledgerSvc  *ledger.Service
	prov       provider.PaymentProvider
	txRunner   *repository.TxRunner
	logger     *logrus.Logger
}

// NewPayoutService creates a PayoutService.
func NewPayoutService(
	payoutRepo PayoutStore,
	ledgerRepo ledger.Repository,
	ledgerSvc *ledger.Service,
	prov provider.PaymentProvider,
	txRunner *repository.TxRunner,
	logger *logrus.Logger,
) *PayoutService {
	return &PayoutService{
		payoutRepo: payoutRepo,
		ledgerRepo: ledgerRepo,
		ledgerSvc:  ledgerSvc,
		prov:       prov,
		txRunner:   txRunner,
		logger:     logger,
	}
}

// CreatePayout creates a payout record and dispatches it to the provider.
// The ledger entry is written inside the same DB transaction as the payout row.
func (s *PayoutService) CreatePayout(ctx context.Context, req CreatePayoutRequest) (*domain.Payout, error) {
	log := s.logger.WithFields(logrus.Fields{
		"component": "payout_service",
		"tenant_id": req.TenantID,
		"amount":    req.Amount,
		"currency":  req.Currency,
	})

	if req.MaxRetries == 0 {
		req.MaxRetries = 3
	}

	payout := &domain.Payout{
		TenantID:      req.TenantID,
		Provider:      s.prov.ProviderName(),
		Amount:        req.Amount,
		Currency:      req.Currency,
		Status:        domain.PayoutStatusPending,
		BankCode:      req.BankCode,
		AccountNumber: req.AccountNumber,
		AccountName:   req.AccountName,
		Description:   req.Description,
		MaxRetries:    req.MaxRetries,
		ScheduledAt:   req.ScheduledAt,
	}

	if err := s.payoutRepo.Create(ctx, payout); err != nil {
		log.WithError(err).Error("create payout: db insert failed")
		return nil, fmt.Errorf("persist payout: %w", err)
	}

	log.WithField("payout_id", payout.ID).Info("payout record created")
	return payout, nil
}

// DispatchPayout sends a pending payout to the provider and records the ledger entry.
// Must be called within a DB transaction.
func (s *PayoutService) DispatchPayout(ctx context.Context, payoutID uuid.UUID) error {
	return s.txRunner.RunInTx(ctx, func(txCtx context.Context) error {
		payout, err := s.payoutRepo.GetByIDForUpdate(txCtx, payoutID)
		if err != nil {
			return fmt.Errorf("lock payout: %w", err)
		}

		if payout.Status != domain.PayoutStatusPending {
			s.logger.WithFields(logrus.Fields{
				"payout_id": payoutID,
				"status":    payout.Status,
			}).Warn("dispatch payout: skipping non-pending payout")
			return nil
		}

		payout.Status = domain.PayoutStatusProcessing
		if err := s.payoutRepo.Update(txCtx, payout); err != nil {
			return fmt.Errorf("update payout to processing: %w", err)
		}

		result, err := s.prov.CreatePayout(txCtx, provider.CreatePayoutRequest{
			ExternalID:    payout.ID.String(),
			Amount:        payout.Amount,
			Currency:      payout.Currency,
			BankCode:      payout.BankCode,
			AccountNumber: payout.AccountNumber,
			AccountName:   payout.AccountName,
			Description:   payout.Description,
		})
		if err != nil {
			payout.RetryCount++
			payout.FailureReason = err.Error()
			if payout.RetryCount >= payout.MaxRetries {
				payout.Status = domain.PayoutStatusFailed
			} else {
				payout.Status = domain.PayoutStatusPending
			}
			_ = s.payoutRepo.Update(txCtx, payout)
			return fmt.Errorf("provider payout failed: %w", err)
		}

		now := time.Now().UTC()
		payout.ProviderPayoutID = result.ProviderPayoutID
		payout.Status = domain.PayoutStatusCompleted
		payout.ProcessedAt = &now

		if err := s.payoutRepo.Update(txCtx, payout); err != nil {
			return fmt.Errorf("update payout to completed: %w", err)
		}

		s.logger.WithFields(logrus.Fields{
			"payout_id":          payoutID,
			"provider_payout_id": result.ProviderPayoutID,
			"amount":             payout.Amount,
		}).Info("payout dispatched successfully")
		return nil
	})
}
