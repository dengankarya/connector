// Package ledger implements double-entry bookkeeping for payment events.
// All methods must be called within a DB transaction — they only write entries,
// never manage their own transaction boundaries.
package ledger

import (
	"context"
	"errors"
	"fmt"

	"github.com/dengankarya/connector/internal/payment/domain"
	"github.com/dengankarya/connector/internal/payment/repository"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)

// Service writes balanced double-entry journals to the ledger.
type Service struct {
	repo   *repository.LedgerRepository
	logger *logrus.Logger
}

// New creates a ledger Service.
func New(repo *repository.LedgerRepository, logger *logrus.Logger) *Service {
	return &Service{repo: repo, logger: logger}
}

// RecordPayment creates ledger entries for a payment received event (invoice.paid).
//
// Journal:
//
//	DR  escrow              amount (gross funds received)
//	CR  merchant_payable    merchant_amount (owed to merchant)
//	CR  platform_fee        platform_fee (if > 0, platform revenue)
//
// All amounts must be positive. The journal is validated before writing.
func (s *Service) RecordPayment(ctx context.Context, txn *domain.PaymentTransaction, eventID uuid.UUID) error {
	journal, err := buildPaymentJournal(txn, &eventID)
	if err != nil {
		return err
	}
	return s.write(ctx, journal, txn.ID, "record_payment")
}

// RecordManualPayment creates ledger entries for a manually-confirmed payment.
//
// Unlike Xendit payments, manual payments (cash / direct bank transfer) never flow
// through platform escrow — the merchant already holds the funds. We therefore do NOT
// debit escrow or credit merchant_payable (which would imply a platform custody
// obligation that doesn't exist).
//
// Journal:
//
//	DR  merchant_direct    amount          (off-platform funds held by merchant)
//	CR  merchant_direct    merchant_amount (net amount retained by merchant)
//	CR  platform_fee       platform_fee    (if > 0, platform revenue earned)
//	CR  shipping_balance   shipping_fee    (if > 0, shipping credit topped up)
func (s *Service) RecordManualPayment(ctx context.Context, txn *domain.PaymentTransaction) error {
	journal, err := buildManualPaymentJournal(txn)
	if err != nil {
		return err
	}
	return s.write(ctx, journal, txn.ID, "record_manual_payment")
}

// RecordSettlement creates ledger entries when a payment is settled.
// eventID is nil when settlement is detected via polling (no webhook event).
//
// If the transaction has provider fees (XenditFee / XenditWithholdingTax), this also
// writes a deduction entry so merchants can see exactly why their payable decreased:
//
//	DR  merchant_payable    total_provider_fee   (fee deducted from merchant balance)
//	CR  provider_fee        total_provider_fee   (cost of payment processing)
//
// When there are no fees, a no-op audit pair is written to mark settlement in the log:
//
//	DR  merchant_payable    merchant_amount
//	CR  merchant_payable    merchant_amount
func (s *Service) RecordSettlement(ctx context.Context, txn *domain.PaymentTransaction, eventID *uuid.UUID) error {
	ref := fmt.Sprintf("txn:%s:settled", txn.ID)

	totalProviderFee := txn.XenditFee + txn.XenditWithholdingTax

	if totalProviderFee > 0 {
		// Write separate entries for each fee type so merchants see the breakdown.
		entries := []domain.LedgerEntry{}

		if txn.XenditFee > 0 {
			entries = append(entries,
				domain.LedgerEntry{
					TenantID:       txn.TenantID,
					TransactionID:  txn.ID,
					WebhookEventID: eventID,
					AccountType:    domain.AccountMerchantPayable,
					Direction:      domain.DirectionDebit,
					Amount:         txn.XenditFee,
					Currency:       txn.Currency,
					ReferenceID:    ref + ":xendit_fee_debit",
					Description:    "Payment gateway fee (Xendit)",
				},
				domain.LedgerEntry{
					TenantID:       txn.TenantID,
					TransactionID:  txn.ID,
					WebhookEventID: eventID,
					AccountType:    domain.AccountProviderFee,
					Direction:      domain.DirectionCredit,
					Amount:         txn.XenditFee,
					Currency:       txn.Currency,
					ReferenceID:    ref + ":xendit_fee_credit",
					Description:    "Payment gateway fee (Xendit)",
				},
			)
		}

		if txn.XenditWithholdingTax > 0 {
			entries = append(entries,
				domain.LedgerEntry{
					TenantID:       txn.TenantID,
					TransactionID:  txn.ID,
					WebhookEventID: eventID,
					AccountType:    domain.AccountMerchantPayable,
					Direction:      domain.DirectionDebit,
					Amount:         txn.XenditWithholdingTax,
					Currency:       txn.Currency,
					ReferenceID:    ref + ":mayar_fee_debit",
					Description:    "Platform fee (Mayar)",
				},
				domain.LedgerEntry{
					TenantID:       txn.TenantID,
					TransactionID:  txn.ID,
					WebhookEventID: eventID,
					AccountType:    domain.AccountProviderFee,
					Direction:      domain.DirectionCredit,
					Amount:         txn.XenditWithholdingTax,
					Currency:       txn.Currency,
					ReferenceID:    ref + ":mayar_fee_credit",
					Description:    "Platform fee (Mayar)",
				},
			)
		}

		return s.write(ctx, &domain.LedgerJournal{Entries: entries}, txn.ID, "record_settlement")
	}

	// No fees known yet — write an informational audit pair.
	journal := &domain.LedgerJournal{
		Entries: []domain.LedgerEntry{
			{
				TenantID:       txn.TenantID,
				TransactionID:  txn.ID,
				WebhookEventID: eventID,
				AccountType:    domain.AccountMerchantPayable,
				Direction:      domain.DirectionDebit,
				Amount:         txn.MerchantAmount,
				Currency:       txn.Currency,
				ReferenceID:    ref + ":settled_debit",
				Description:    "Settlement confirmed by provider",
			},
			{
				TenantID:       txn.TenantID,
				TransactionID:  txn.ID,
				WebhookEventID: eventID,
				AccountType:    domain.AccountMerchantPayable,
				Direction:      domain.DirectionCredit,
				Amount:         txn.MerchantAmount,
				Currency:       txn.Currency,
				ReferenceID:    ref + ":settled_credit",
				Description:    "Settlement cleared — amount ready for payout",
			},
		},
	}
	return s.write(ctx, journal, txn.ID, "record_settlement")
}

// RecordRefund creates ledger entries for a refund event.
//
// Journal:
//
//	DR  refund    amount
//	CR  escrow    amount (funds returned from escrow)
func (s *Service) RecordRefund(ctx context.Context, txn *domain.PaymentTransaction, eventID uuid.UUID, refundAmount int64) error {
	ref := fmt.Sprintf("txn:%s:refunded", txn.ID)
	journal := &domain.LedgerJournal{
		Entries: []domain.LedgerEntry{
			{
				TenantID:       txn.TenantID,
				TransactionID:  txn.ID,
				WebhookEventID: &eventID,
				AccountType:    domain.AccountRefund,
				Direction:      domain.DirectionDebit,
				Amount:         refundAmount,
				Currency:       txn.Currency,
				ReferenceID:    ref + ":refund_debit",
				Description:    "Refund issued to customer",
			},
			{
				TenantID:       txn.TenantID,
				TransactionID:  txn.ID,
				WebhookEventID: &eventID,
				AccountType:    domain.AccountEscrow,
				Direction:      domain.DirectionCredit,
				Amount:         refundAmount,
				Currency:       txn.Currency,
				ReferenceID:    ref + ":escrow_credit",
				Description:    "Escrow reduced by refund amount",
			},
		},
	}
	return s.write(ctx, journal, txn.ID, "record_refund")
}

// RecordPayout creates ledger entries when a payout is dispatched to a merchant.
//
// Journal:
//
//	DR  merchant_payable    amount (liability reduced)
//	CR  payout              amount (disbursement recorded)
func (s *Service) RecordPayout(ctx context.Context, tenantID int64, transactionID uuid.UUID, payoutID uuid.UUID, amount int64, currency string) error {
	ref := fmt.Sprintf("payout:%s", payoutID)
	journal := &domain.LedgerJournal{
		Entries: []domain.LedgerEntry{
			{
				TenantID:      tenantID,
				TransactionID: transactionID,
				AccountType:   domain.AccountMerchantPayable,
				Direction:     domain.DirectionDebit,
				Amount:        amount,
				Currency:      currency,
				ReferenceID:   ref + ":payable_debit",
				Description:   "Merchant payable reduced on payout",
			},
			{
				TenantID:      tenantID,
				TransactionID: transactionID,
				AccountType:   domain.AccountPayout,
				Direction:     domain.DirectionCredit,
				Amount:        amount,
				Currency:      currency,
				ReferenceID:   ref + ":payout_credit",
				Description:   "Payout disbursed to merchant",
			},
		},
	}
	return s.write(ctx, journal, transactionID, "record_payout")
}

// ─── internal ─────────────────────────────────────────────────────────────────

func (s *Service) write(ctx context.Context, j *domain.LedgerJournal, txnID uuid.UUID, op string) error {
	if err := j.Validate(); err != nil {
		// Imbalanced journal is a programming error — do not persist.
		return fmt.Errorf("ledger: %s: %w", op, err)
	}

	log := s.logger.WithFields(logrus.Fields{
		"component":      "ledger",
		"operation":      op,
		"transaction_id": txnID,
		"entry_count":    len(j.Entries),
	})

	if err := s.repo.CreateEntries(ctx, j.Entries); err != nil {
		if errors.Is(err, domain.ErrDuplicateLedgerEntry) {
			log.Warn("ledger entries already exist (idempotent replay)")
			return nil
		}
		return fmt.Errorf("ledger: %s: write entries: %w", op, err)
	}

	log.Info("ledger journal written")
	return nil
}

// ─── journal builders ─────────────────────────────────────────────────────────

// buildManualPaymentJournal constructs the ledger journal for a manual payment.
// Funds never touched platform escrow, so we use merchant_direct instead of escrow/merchant_payable.
func buildManualPaymentJournal(txn *domain.PaymentTransaction) (*domain.LedgerJournal, error) {
	if err := txn.Validate(); err != nil {
		return nil, fmt.Errorf("build manual payment journal: %w", err)
	}

	baseRef := fmt.Sprintf("txn:%s:manual_paid", txn.ID)

	entries := []domain.LedgerEntry{
		{
			TenantID:      txn.TenantID,
			TransactionID: txn.ID,
			AccountType:   domain.AccountMerchantDirect,
			Direction:     domain.DirectionDebit, // acknowledges that merchant received this gross amount
			Amount:        txn.Amount,
			Currency:      txn.Currency,
			ReferenceID:   baseRef + ":direct_debit",
			Description:   "Gross payment received directly by merchant (off-platform)",
		},
		{
			TenantID:      txn.TenantID,
			TransactionID: txn.ID,
			AccountType:   domain.AccountMerchantDirect,
			Direction:     domain.DirectionCredit,
			Amount:        txn.MerchantAmount,
			Currency:      txn.Currency,
			ReferenceID:   baseRef + ":direct_credit",
			Description:   "Net amount retained by merchant",
		},
	}

	if txn.PlatformFee > 0 {
		entries = append(entries, domain.LedgerEntry{
			TenantID:      txn.TenantID,
			TransactionID: txn.ID,
			AccountType:   domain.AccountPlatformFee,
			Direction:     domain.DirectionCredit,
			Amount:        txn.PlatformFee,
			Currency:      txn.Currency,
			ReferenceID:   baseRef + ":platform_fee",
			Description:   "Platform fee revenue (manual payment)",
		})
	}

	if txn.ShippingFee > 0 {
		entries = append(entries, domain.LedgerEntry{
			TenantID:      txn.TenantID,
			TransactionID: txn.ID,
			AccountType:   domain.AccountShippingBalance,
			Direction:     domain.DirectionCredit,
			Amount:        txn.ShippingFee,
			Currency:      txn.Currency,
			ReferenceID:   baseRef + ":shipping_balance",
			Description:   "Shipping balance topped up for merchant (manual payment)",
		})
	}

	return &domain.LedgerJournal{Entries: entries}, nil
}

func buildPaymentJournal(txn *domain.PaymentTransaction, eventID *uuid.UUID) (*domain.LedgerJournal, error) {
	if err := txn.Validate(); err != nil {
		return nil, fmt.Errorf("build payment journal: %w", err)
	}

	baseRef := fmt.Sprintf("txn:%s:paid", txn.ID)

	entries := []domain.LedgerEntry{
		{
			TenantID:       txn.TenantID,
			TransactionID:  txn.ID,
			WebhookEventID: eventID,
			AccountType:    domain.AccountEscrow,
			Direction:      domain.DirectionDebit, // asset: we hold these funds
			Amount:         txn.Amount,
			Currency:       txn.Currency,
			ReferenceID:    baseRef + ":escrow",
			Description:    "Gross payment received into escrow",
		},
		{
			TenantID:       txn.TenantID,
			TransactionID:  txn.ID,
			WebhookEventID: eventID,
			AccountType:    domain.AccountMerchantPayable,
			Direction:      domain.DirectionCredit, // liability: we owe merchant
			Amount:         txn.MerchantAmount,
			Currency:       txn.Currency,
			ReferenceID:    baseRef + ":merchant_payable",
			Description:    "Net amount payable to merchant",
		},
	}

	if txn.PlatformFee > 0 {
		entries = append(entries, domain.LedgerEntry{
			TenantID:       txn.TenantID,
			TransactionID:  txn.ID,
			WebhookEventID: eventID,
			AccountType:    domain.AccountPlatformFee,
			Direction:      domain.DirectionCredit, // revenue earned
			Amount:         txn.PlatformFee,
			Currency:       txn.Currency,
			ReferenceID:    baseRef + ":platform_fee",
			Description:    "Platform fee revenue",
		})
	}

	if txn.ShippingFee > 0 {
		entries = append(entries, domain.LedgerEntry{
			TenantID:       txn.TenantID,
			TransactionID:  txn.ID,
			WebhookEventID: eventID,
			AccountType:    domain.AccountShippingBalance,
			Direction:      domain.DirectionCredit, // liability: we owe merchant shipping credits
			Amount:         txn.ShippingFee,
			Currency:       txn.Currency,
			ReferenceID:    baseRef + ":shipping_balance",
			Description:    "Shipping balance topped up for merchant",
		})
	}

	return &domain.LedgerJournal{Entries: entries}, nil
}
