// Package jobs contains periodic background jobs for the payment module.
package jobs

import (
	"context"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/dengankarya/overwatch/internal/payment/domain"
	"github.com/dengankarya/overwatch/internal/payment/repository"
)

const expirePaymentsBatchSize = 100

// ExpirePaymentsJob marks awaiting_payment transactions as expired when they pass their expiry time.
// Run every 5 minutes via the asynq periodic task scheduler.
type ExpirePaymentsJob struct {
	txnRepo  *repository.TransactionRepository
	txRunner *repository.TxRunner
	logger   *logrus.Logger
}

// NewExpirePaymentsJob creates an ExpirePaymentsJob.
func NewExpirePaymentsJob(
	txnRepo *repository.TransactionRepository,
	txRunner *repository.TxRunner,
	logger *logrus.Logger,
) *ExpirePaymentsJob {
	return &ExpirePaymentsJob{txnRepo: txnRepo, txRunner: txRunner, logger: logger}
}

// Run executes one batch of expiry processing.
// Uses SKIP LOCKED so multiple concurrent job instances never step on each other.
func (j *ExpirePaymentsJob) Run(ctx context.Context) error {
	start := time.Now()
	log := j.logger.WithField("component", "expire_payments_job")

	// Each transaction is processed in its own mini-transaction to limit lock scope.
	txns, err := j.txnRepo.ListExpired(ctx, expirePaymentsBatchSize)
	if err != nil {
		log.WithError(err).Error("expire payments: list failed")
		return err
	}

	if len(txns) == 0 {
		return nil
	}

	log.WithField("count", len(txns)).Info("expire payments: processing batch")

	var expired, skipped int
	for _, txn := range txns {
		err := j.txRunner.RunInTx(ctx, func(txCtx context.Context) error {
			// Re-fetch with lock inside the transaction to avoid TOCTOU.
			locked, err := j.txnRepo.GetByIDForUpdate(txCtx, txn.ID)
			if err != nil {
				return err
			}

			if err := locked.TransitionTo(domain.StatusExpired); err != nil {
				// Already expired or in a final state — skip without error.
				skipped++
				return nil
			}

			return j.txnRepo.Update(txCtx, locked)
		})

		if err != nil {
			log.WithFields(logrus.Fields{
				"transaction_id": txn.ID,
				"tenant_id":      txn.TenantID,
				"expires_at":     txn.ExpiresAt,
				"error":          err.Error(),
			}).Error("expire payments: failed to expire transaction")
			continue
		}
		expired++
	}

	log.WithFields(logrus.Fields{
		"expired":     expired,
		"skipped":     skipped,
		"duration_ms": time.Since(start).Milliseconds(),
	}).Info("expire payments: batch complete")
	return nil
}
