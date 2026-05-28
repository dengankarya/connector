package jobs

import (
	"context"
	"errors"
	"time"

	"github.com/dengankarya/connector/internal/payment/domain"
	"github.com/dengankarya/connector/internal/payment/provider"
	"github.com/dengankarya/connector/internal/payment/repository"
	"github.com/sirupsen/logrus"
)

const (
	syncSettlementDefaultLookback = 30 * 24 * time.Hour // first-run: go back 30 days
	syncSettlementPageSize        = 100
)

// SyncSettlementJob reconciles settlement status and fee breakdowns by pulling from
// Xendit's GET /transactions API against the master account.
// Designed to run nightly at 1 AM WIB (18:00 UTC).
//
// It:
//  1. Fetches all Xendit MONEY_IN transactions from the master account since the last sync.
//  2. Matches each to our payment_transaction via payment_session_id.
//  3. Backfills fee columns (xendit_fee, vat, wht, etc.) and marks SETTLED transactions.
//  4. Recomputes and upserts the merchant_settlement_snapshot per tenant.
type SyncSettlementJob struct {
	txnRepo      *repository.TransactionRepository
	snapshotRepo *repository.SettlementSnapshotRepository
	syncer       provider.TransactionSyncer
	logger       *logrus.Logger
}

// NewSyncSettlementJob creates a SyncSettlementJob.
func NewSyncSettlementJob(
	txnRepo *repository.TransactionRepository,
	snapshotRepo *repository.SettlementSnapshotRepository,
	syncer provider.TransactionSyncer,
	logger *logrus.Logger,
) *SyncSettlementJob {
	return &SyncSettlementJob{
		txnRepo:      txnRepo,
		snapshotRepo: snapshotRepo,
		syncer:       syncer,
		logger:       logger,
	}
}

// Run fetches all Xendit transactions from the master account and reconciles them
// against our payment_transactions. Then it recomputes per-tenant snapshots.
func (j *SyncSettlementJob) Run(ctx context.Context) error {
	start := time.Now()
	log := j.logger.WithField("component", "sync_settlement_job")
	log.Info("settlement sync: starting")

	// ── Step 1: Fetch from Xendit master account ──────────────────────────────
	// Use the oldest last_synced_at across all tenants as the lower bound;
	// fall back to a 30-day lookback on first run.
	since := j.globalSince(ctx)
	log.WithField("since", since.Format(time.RFC3339)).Info("settlement sync: fetching xendit transactions")

	processed, err := j.fetchAndReconcile(ctx, since, log)
	if err != nil {
		log.WithError(err).Error("settlement sync: reconciliation failed")
		return err
	}

	// ── Step 2: Recompute per-tenant snapshots ────────────────────────────────
	tenantIDs, err := j.txnRepo.ListDistinctTenants(ctx)
	if err != nil {
		log.WithError(err).Error("settlement sync: failed to list tenants")
		return err
	}

	now := time.Now().UTC()
	var succeeded, failed int
	for _, tenantID := range tenantIDs {
		if err := j.updateSnapshot(ctx, tenantID, now); err != nil {
			log.WithError(err).WithField("tenant_id", tenantID).Error("settlement sync: snapshot update failed")
			failed++
		} else {
			succeeded++
		}
	}

	log.WithFields(logrus.Fields{
		"transactions_reconciled": processed,
		"tenants_ok":              succeeded,
		"tenants_err":             failed,
		"duration_ms":             time.Since(start).Milliseconds(),
	}).Info("settlement sync: complete")
	return nil
}

// globalSince returns the earliest last_synced_at across all tenant snapshots.
// If none exist, it falls back to the default lookback window.
func (j *SyncSettlementJob) globalSince(ctx context.Context) time.Time {
	fallback := time.Now().UTC().Add(-syncSettlementDefaultLookback)
	tenantIDs, err := j.txnRepo.ListDistinctTenants(ctx)
	if err != nil || len(tenantIDs) == 0 {
		return fallback
	}
	earliest := fallback
	for _, id := range tenantIDs {
		snap, err := j.snapshotRepo.GetByTenantID(ctx, id)
		if err == nil && snap.LastSyncedAt != nil && snap.LastSyncedAt.Before(earliest) {
			earliest = *snap.LastSyncedAt
		}
	}
	return earliest
}

// fetchAndReconcile pages through all Xendit master-account transactions and
// backfills fees / settlement status in our DB. Returns the count processed.
func (j *SyncSettlementJob) fetchAndReconcile(ctx context.Context, since time.Time, log *logrus.Entry) (int, error) {
	var afterID string
	var processed int
	for {
		result, err := j.syncer.ListTransactions(ctx, provider.ListTransactionsRequest{
			CreatedGTE: since,
			AfterID:    afterID,
			Limit:      syncSettlementPageSize,
		})
		if err != nil {
			return processed, err
		}

		for _, xt := range result.Transactions {
			if err := j.reconcileTransaction(ctx, xt, log); err != nil {
				log.WithError(err).WithField("xendit_txn_id", xt.ID).
					Warn("settlement sync: failed to process transaction")
			}
			processed++
		}

		if !result.HasMore {
			break
		}
		afterID = result.LastID
	}
	return processed, nil
}

// reconcileTransaction matches a Xendit transaction to our DB record and backfills it.
func (j *SyncSettlementJob) reconcileTransaction(
	ctx context.Context,
	xt provider.ProviderTransaction,
	log *logrus.Entry,
) error {
	if xt.PaymentSessionID == "" {
		return nil // not a payment session transaction — skip
	}

	txn, err := j.txnRepo.GetByProviderInvoiceID(ctx, "xendit", xt.PaymentSessionID)
	if err != nil {
		var nf domain.ErrNotFound
		if errors.As(err, &nf) {
			return nil // transaction created by a different system or not yet recorded
		}
		return err
	}

	// Backfill fee breakdown from Xendit — always overwrite as Xendit is authoritative.
	txn.XenditFee = xt.XenditFee
	txn.VAT = xt.VAT
	txn.XenditWithholdingTax = xt.XenditWithholdingTax
	txn.ThirdPartyWHT = xt.ThirdPartyWHT
	txn.EstimatedSettlementTime = xt.EstimatedSettlementTime

	// Transition to settled if Xendit confirms and we haven't done so already.
	if xt.SettlementStatus == "SETTLED" && txn.Status == domain.StatusPaid {
		if err := txn.TransitionTo(domain.StatusSettled); err == nil {
			now := time.Now().UTC()
			txn.SettledAt = &now
			log.WithFields(logrus.Fields{
				"transaction_id":      txn.ID,
				"tenant_id":           txn.TenantID,
				"provider_invoice_id": txn.ProviderInvoiceID,
			}).Info("settlement sync: transaction marked settled")
		}
	}

	if err := j.txnRepo.Update(ctx, txn); err != nil {
		if errors.Is(err, domain.ErrVersionConflict) {
			log.WithField("transaction_id", txn.ID).Warn("settlement sync: version conflict, skipping")
			return nil
		}
		return err
	}

	return nil
}

// updateSnapshot recomputes and persists the balance snapshot for a single tenant.
func (j *SyncSettlementJob) updateSnapshot(ctx context.Context, tenantID int64, now time.Time) error {
	pending, err := j.txnRepo.SumPendingSettlement(ctx, tenantID)
	if err != nil {
		return err
	}

	settled, err := j.txnRepo.SumSettled(ctx, tenantID)
	if err != nil {
		return err
	}

	upsert := &domain.MerchantSettlementSnapshot{
		TenantID:           tenantID,
		PendingBalance:     pending.MerchantAmount,
		PendingPlatformFee: pending.PlatformFee,
		PendingXenditFee:   pending.XenditFee,
		PendingVAT:         pending.VAT,
		PendingWithholding: pending.Withholding,
		SettledBalance:     settled.MerchantAmount,
		SettledPlatformFee: settled.PlatformFee,
		SettledXenditFee:   settled.XenditFee,
		SettledVAT:         settled.VAT,
		SettledWithholding: settled.Withholding,
		LastSyncedAt:       &now,
	}
	return j.snapshotRepo.Upsert(ctx, upsert)
}
