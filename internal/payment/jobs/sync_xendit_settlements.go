package jobs

import (
	"context"
	"time"

	"github.com/dengankarya/connector/internal/payment/domain"
	"github.com/dengankarya/connector/internal/payment/ledger"
	"github.com/dengankarya/connector/internal/payment/provider"
	"github.com/dengankarya/connector/internal/payment/repository"
	"github.com/sirupsen/logrus"
)

const syncXenditSettlementsBatchSize = 100

// XenditTransactionFetcher is implemented by clients that can fetch settlement details for a payment.
// *xendit.Client satisfies this interface.
type XenditTransactionFetcher interface {
	GetTransaction(ctx context.Context, paymentID string) (*provider.ProviderTransaction, error)
}

// SyncXenditSettlementsJob polls Xendit for settlement status on paid transactions.
// Run every 6 hours via the asynq periodic task scheduler.
type SyncXenditSettlementsJob struct {
	txnRepo  *repository.TransactionRepository
	txRunner *repository.TxRunner
	ledger   *ledger.Service
	client   XenditTransactionFetcher
	logger   *logrus.Logger
}

// NewSyncXenditSettlementsJob creates a SyncXenditSettlementsJob.
func NewSyncXenditSettlementsJob(
	txnRepo *repository.TransactionRepository,
	txRunner *repository.TxRunner,
	ledgerSvc *ledger.Service,
	client XenditTransactionFetcher,
	logger *logrus.Logger,
) *SyncXenditSettlementsJob {
	return &SyncXenditSettlementsJob{txnRepo: txnRepo, txRunner: txRunner, ledger: ledgerSvc, client: client, logger: logger}
}

// Run fetches one batch of paid Xendit transactions and transitions settled ones to 'settled'.
// Uses SKIP LOCKED so parallel instances never step on each other.
func (j *SyncXenditSettlementsJob) Run(ctx context.Context) error {
	start := time.Now()
	log := j.logger.WithField("component", "sync_xendit_settlements_job")

	log.Info("sync xendit settlements: job started")

	txns, err := j.txnRepo.ListPaidByProvider(ctx, "xendit", syncXenditSettlementsBatchSize)
	if err != nil {
		log.WithError(err).Error("sync xendit settlements: list failed")
		return err
	}

	if len(txns) == 0 {
		return nil
	}

	log.WithField("count", len(txns)).Info("sync xendit settlements: checking batch")

	var settled, pending, failed int
	for _, txn := range txns {
		provTxn, err := j.client.GetTransaction(ctx, txn.ProviderPaymentID)
		if err != nil {
			log.WithFields(logrus.Fields{
				"transaction_id":      txn.ID,
				"provider_payment_id": txn.ProviderPaymentID,
				"error":               err.Error(),
			}).Warn("sync xendit settlements: failed to fetch transaction; skipping")
			failed++
			continue
		}

		// Treat both SETTLED and EARLY_SETTLED as fully settled.
		if provTxn.SettlementStatus != "SETTLED" && provTxn.SettlementStatus != "EARLY_SETTLED" {
			pending++
			continue
		}

		err = j.txRunner.RunInTx(ctx, func(txCtx context.Context) error {
			locked, err := j.txnRepo.GetByIDForUpdate(txCtx, txn.ID)
			if err != nil {
				return err
			}

			if err := locked.TransitionTo(domain.StatusSettled); err != nil {
				// Already settled or in a terminal state — skip without error.
				return nil
			}

			now := time.Now().UTC()
			locked.SettledAt = &now
			locked.XenditFee = provTxn.XenditFee
			locked.VAT = provTxn.VAT
			locked.XenditWithholdingTax = provTxn.XenditWithholdingTax
			locked.ThirdPartyWHT = provTxn.ThirdPartyWHT
			locked.EstimatedSettlementTime = provTxn.EstimatedSettlementTime

			if err := j.ledger.RecordSettlement(txCtx, locked, nil); err != nil {
				return err
			}

			return j.txnRepo.Update(txCtx, locked)
		})

		if err != nil {
			log.WithFields(logrus.Fields{
				"transaction_id":      txn.ID,
				"provider_payment_id": txn.ProviderPaymentID,
				"error":               err.Error(),
			}).Error("sync xendit settlements: failed to mark settled")
			failed++
			continue
		}

		log.WithFields(logrus.Fields{
			"transaction_id":      txn.ID,
			"tenant_id":           txn.TenantID,
			"order_number":        txn.OrderNumber,
			"provider_payment_id": txn.ProviderPaymentID,
			"amount":              txn.Amount,
			"xendit_fee":          provTxn.XenditFee,
			"vat":                 provTxn.VAT,
		}).Info("sync xendit settlements: transaction settled")
		settled++
	}

	log.WithFields(logrus.Fields{
		"settled":     settled,
		"pending":     pending,
		"failed":      failed,
		"duration_ms": time.Since(start).Milliseconds(),
	}).Info("sync xendit settlements: batch complete")
	return nil
}
