package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/dengankarya/connector/internal/payment/domain"
	"github.com/dengankarya/connector/internal/payment/ledger"
	"github.com/dengankarya/connector/internal/payment/provider"
	"github.com/dengankarya/connector/internal/payment/repository"
	"github.com/dengankarya/connector/internal/worker"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/sirupsen/logrus"
)

// RefundStatusChecker fetches refund records for a given provider payment ID.
// durianpay.Client implements this interface.
type RefundStatusChecker interface {
	GetRefundsByPaymentID(ctx context.Context, paymentID string) ([]provider.Refund, error)
}

// PollRefundStatusPayload is the asynq task payload for TaskPollRefundStatus.
type PollRefundStatusPayload struct {
	TransactionID     string `json:"transaction_id"`
	ProviderPaymentID string `json:"provider_payment_id"`
	TenantID          int64  `json:"tenant_id"`
	RefundAmount      int64  `json:"refund_amount"`
}

// NewPollRefundStatusTask creates an asynq Task that polls for refund confirmation.
func NewPollRefundStatusTask(txnID uuid.UUID, providerPaymentID string, tenantID int64, refundAmount int64) (*asynq.Task, []asynq.Option) {
	payload, _ := json.Marshal(PollRefundStatusPayload{
		TransactionID:     txnID.String(),
		ProviderPaymentID: providerPaymentID,
		TenantID:          tenantID,
		RefundAmount:      refundAmount,
	})
	opts := []asynq.Option{
		asynq.ProcessIn(30 * time.Second),
		asynq.MaxRetry(20),
		asynq.Queue("default"),
		asynq.Timeout(30 * 1e9),              // 30s per attempt
		asynq.Retention(7 * 24 * 3600 * 1e9), // 7 days
	}
	return asynq.NewTask(worker.TaskPollRefundStatus, payload), opts
}

// PollRefundStatusJob is the asynq handler for TaskPollRefundStatus.
// It polls the payment provider until the refund reaches "done" status, then
// transitions the transaction to refunded and writes ledger entries.
type PollRefundStatusJob struct {
	checker  RefundStatusChecker
	txnRepo  *repository.TransactionRepository
	txRunner *repository.TxRunner
	ledger   *ledger.Service
	logger   *logrus.Logger
}

// NewPollRefundStatusJob creates a PollRefundStatusJob.
func NewPollRefundStatusJob(
	checker RefundStatusChecker,
	txnRepo *repository.TransactionRepository,
	txRunner *repository.TxRunner,
	ledgerSvc *ledger.Service,
	logger *logrus.Logger,
) *PollRefundStatusJob {
	return &PollRefundStatusJob{
		checker:  checker,
		txnRepo:  txnRepo,
		txRunner: txRunner,
		ledger:   ledgerSvc,
		logger:   logger,
	}
}

// ProcessTask implements asynq.Handler.
func (j *PollRefundStatusJob) ProcessTask(ctx context.Context, t *asynq.Task) error {
	var p PollRefundStatusPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("unmarshal payload: %w", asynq.SkipRetry)
	}

	txnID, err := uuid.Parse(p.TransactionID)
	if err != nil {
		return fmt.Errorf("invalid transaction_id %q: %w", p.TransactionID, asynq.SkipRetry)
	}

	log := j.logger.WithFields(logrus.Fields{
		"component":           "poll_refund_status_job",
		"transaction_id":      p.TransactionID,
		"provider_payment_id": p.ProviderPaymentID,
	})

	if j.checker == nil {
		log.Warn("poll refund status: checker not configured, skipping")
		return nil
	}

	expectedRefID := fmt.Sprintf("refund:%s", p.TransactionID)

	refunds, err := j.checker.GetRefundsByPaymentID(ctx, p.ProviderPaymentID)
	if err != nil {
		log.WithError(err).Error("poll refund status: fetch refunds failed")
		return fmt.Errorf("poll refund status %s: %w", p.TransactionID, err)
	}

	var matched *provider.Refund
	for i := range refunds {
		if refunds[i].ExternalID == expectedRefID {
			matched = &refunds[i]
			break
		}
	}
	if matched == nil {
		return fmt.Errorf("poll refund status: refund %s not found yet — will retry", expectedRefID)
	}
	if matched.Status != "done" {
		log.WithField("refund_status", matched.Status).Info("poll refund status: not done yet — will retry")
		return fmt.Errorf("poll refund status: status is %q, not done yet", matched.Status)
	}

	return j.txRunner.RunInTx(ctx, func(txCtx context.Context) error {
		txn, err := j.txnRepo.GetByIDForUpdate(txCtx, txnID)
		if err != nil {
			return fmt.Errorf("poll refund status: get transaction: %w", err)
		}

		if txn.Status == domain.StatusRefunded {
			log.Info("poll refund status: already refunded — idempotent")
			return nil
		}

		if err := txn.TransitionTo(domain.StatusRefunded); err != nil {
			if errors.As(err, new(domain.ErrAlreadyInState)) {
				return nil
			}
			return fmt.Errorf("poll refund status: transition: %w", err)
		}
		if err := j.txnRepo.Update(txCtx, txn); err != nil {
			return fmt.Errorf("poll refund status: update: %w", err)
		}
		if err := j.ledger.RecordRefund(txCtx, txn, uuid.New(), p.RefundAmount); err != nil {
			return fmt.Errorf("poll refund status: ledger: %w", err)
		}

		log.Info("poll refund status: refund confirmed, status updated to refunded")
		return nil
	})
}
