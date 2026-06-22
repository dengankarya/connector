package jobs

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dengankarya/connector/internal/payment/domain"
	"github.com/dengankarya/connector/internal/payment/ledger"
	"github.com/dengankarya/connector/internal/payment/provider"
	"github.com/dengankarya/connector/internal/payment/repository"
	"github.com/sirupsen/logrus"
)

const syncMayarPageSize = 50

// SyncMayarSettlementsJob polls the Mayar transactions API for newly settled transactions,
// backfills provider fee data, and transitions matching transactions to status=settled.
// Run periodically (e.g. every 30 minutes) via the asynq scheduler.
type SyncMayarSettlementsJob struct {
	syncer   provider.TransactionSyncer
	txnRepo  *repository.TransactionRepository
	ledger   *ledger.Service
	txRunner *repository.TxRunner
	logger   *logrus.Logger
}

// NewSyncMayarSettlementsJob creates a SyncMayarSettlementsJob.
func NewSyncMayarSettlementsJob(
	syncer provider.TransactionSyncer,
	txnRepo *repository.TransactionRepository,
	ledger *ledger.Service,
	txRunner *repository.TxRunner,
	logger *logrus.Logger,
) *SyncMayarSettlementsJob {
	return &SyncMayarSettlementsJob{
		syncer:   syncer,
		txnRepo:  txnRepo,
		ledger:   ledger,
		txRunner: txRunner,
		logger:   logger,
	}
}

// Run fetches all pages of settled transactions from Mayar since the last 48 hours
// and settles any matching payment_transactions rows.
//
// The lookback window of 48 hours is conservative — settlements typically arrive
// within hours of payment, but the window prevents missed events during outages.
func (j *SyncMayarSettlementsJob) Run(ctx context.Context) error {
	start := time.Now()
	log := j.logger.WithField("component", "sync_mayar_settlements_job")

	createdGTE := time.Now().UTC().Add(-48 * time.Hour)
	var afterID string
	var settled, skipped, notFound int

	for {
		resp, err := j.syncer.ListTransactions(ctx, provider.ListTransactionsRequest{
			CreatedGTE: createdGTE,
			AfterID:    afterID,
			Limit:      syncMayarPageSize,
		})
		if err != nil {
			log.WithError(err).Error("sync mayar settlements: list transactions failed")
			return fmt.Errorf("sync mayar settlements: %w", err)
		}

		for _, pt := range resp.Transactions {
			if pt.PaymentSessionID == "" {
				skipped++
				continue
			}

			result, err := j.settleOne(ctx, pt)
			switch result {
			case "settled":
				settled++
			case "skipped":
				skipped++
			case "not_found":
				notFound++
			}
			if err != nil {
				log.WithFields(logrus.Fields{
					"provider_transaction_id": pt.ID,
					"payment_session_id":      pt.PaymentSessionID,
					"error":                   err.Error(),
				}).Error("sync mayar settlements: failed to settle transaction")
			}
		}

		if !resp.HasMore {
			break
		}
		afterID = resp.LastID
	}

	log.WithFields(logrus.Fields{
		"settled":     settled,
		"skipped":     skipped,
		"not_found":   notFound,
		"duration_ms": time.Since(start).Milliseconds(),
	}).Info("sync mayar settlements: complete")
	return nil
}

// settleOne processes a single provider transaction. Returns a result label for metrics.
func (j *SyncMayarSettlementsJob) settleOne(ctx context.Context, pt provider.ProviderTransaction) (result string, err error) {
	var txn *domain.PaymentTransaction

	err = j.txRunner.RunInTx(ctx, func(txCtx context.Context) error {
		var innerErr error
		txn, innerErr = j.txnRepo.GetByProviderInvoiceIDForUpdate(txCtx, "mayar", pt.PaymentSessionID)
		if innerErr != nil {
			if errors.As(innerErr, new(domain.ErrNotFound)) {
				result = "not_found"
				return nil // not an error — transaction may belong to another system
			}
			return fmt.Errorf("get transaction for settlement: %w", innerErr)
		}

		// Backfill fee data regardless of status — idempotent.
		txn.XenditFee = pt.XenditFee
		txn.XenditWithholdingTax = pt.XenditWithholdingTax // Mayar's platform fee

		if txn.Status == domain.StatusSettled {
			// Already settled — just update fees if changed (version bump needed).
			txn.Version++
			if innerErr = j.txnRepo.Update(txCtx, txn); innerErr != nil {
				return fmt.Errorf("backfill fees on settled transaction: %w", innerErr)
			}
			result = "skipped"
			return nil
		}

		if innerErr = txn.TransitionTo(domain.StatusSettled); innerErr != nil {
			if errors.As(innerErr, new(domain.ErrAlreadyInState)) {
				result = "skipped"
				return nil
			}
			return fmt.Errorf("transition to settled: %w", innerErr)
		}

		now := time.Now().UTC()
		txn.SettledAt = &now

		if innerErr = j.txnRepo.Update(txCtx, txn); innerErr != nil {
			return fmt.Errorf("update settled transaction: %w", innerErr)
		}

		// Write settlement ledger entries. eventID is nil — settlement detected via polling.
		if innerErr = j.ledger.RecordSettlement(txCtx, txn, nil); innerErr != nil {
			return fmt.Errorf("record settlement ledger: %w", innerErr)
		}

		result = "settled"
		return nil
	})

	return result, err
}
