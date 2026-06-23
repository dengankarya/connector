// Package webhook handles inbound payment callbacks: ingestion, processing, replay, and retry.
package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/dengankarya/connector/internal/payment/domain"
	"github.com/dengankarya/connector/internal/payment/ledger"
	"github.com/dengankarya/connector/internal/payment/repository"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)


// PaymentForwarder forwards a normalized payment status event to an upstream system
// (e.g. Tokokarya) after the DB transaction commits. Called in a fire-and-forget goroutine.
// tokokarya.Client satisfies this interface.
type PaymentForwarder interface {
	ForwardPaymentWebhook(ctx context.Context, payload []byte) error
}

// PaymentStatusEvent is the normalized payload forwarded to Tokokarya on every
// payment state transition. Uses connector-internal IDs — no provider-specific fields.
type PaymentStatusEvent struct {
	Event         string     `json:"event"`          // "payment.paid" | "payment.expired" | "payment.failed"
	TransactionID string     `json:"transaction_id"` // connector's internal UUID
	OrderNumber   string     `json:"order_number"`
	Status        string     `json:"status"`
	Amount        int64      `json:"amount"`
	Currency      string     `json:"currency"`
	Provider      string     `json:"provider"`
	CheckoutURL   string     `json:"checkout_url,omitempty"`
	PaidAt        *time.Time `json:"paid_at,omitempty"`
}

// Processor orchestrates the full webhook processing pipeline.
// Every step from lock → transition → ledger → mark-processed runs in one DB transaction.
type Processor struct {
	eventRepo        *repository.WebhookEventRepository
	txnRepo          *repository.TransactionRepository
	ledger           *ledger.Service
	txRunner  *repository.TxRunner
	forwarder PaymentForwarder // optional; if nil Tokokarya forwarding is skipped
	logger    *logrus.Logger
}

// NewProcessor creates a Processor with all required dependencies.
// forwarder may be nil.
func NewProcessor(
	eventRepo *repository.WebhookEventRepository,
	txnRepo *repository.TransactionRepository,
	ledgerSvc *ledger.Service,
	txRunner *repository.TxRunner,
	forwarder PaymentForwarder,
	logger *logrus.Logger,
) *Processor {
	return &Processor{
		eventRepo: eventRepo,
		txnRepo:   txnRepo,
		ledger:    ledgerSvc,
		txRunner:  txRunner,
		forwarder: forwarder,
		logger:    logger,
	}
}

// Process runs the full processing pipeline for a stored webhook event.
// All DB mutations (status updates, transaction update, ledger inserts) are wrapped
// in a single ACID transaction so there is no partial state on failure.
//
// Safe to call multiple times — idempotency is enforced at every step.
func (p *Processor) Process(ctx context.Context, eventID uuid.UUID) error {
	start := time.Now()
	log := p.logger.WithFields(logrus.Fields{
		"component":        "webhook_processor",
		"webhook_event_id": eventID,
	})

	var txnSnapshot *domain.PaymentTransaction // captured inside tx, used for forwarding after commit

	err := p.txRunner.RunInTx(ctx, func(txCtx context.Context) error {
		// ── Step 1: Lock the webhook event row ──────────────────────────────
		// SELECT FOR UPDATE prevents two workers from processing the same event.
		event, err := p.eventRepo.GetByIDForUpdate(txCtx, eventID)
		if err != nil {
			return fmt.Errorf("lock webhook event: %w", err)
		}

		log = log.WithFields(logrus.Fields{
			"event_type":        event.EventType,
			"provider":          event.Provider,
			"provider_event_id": event.ProviderEventID,
		})

		// ── Step 2: Idempotency check ────────────────────────────────────────
		if event.ProcessingStatus == domain.WebhookStatusProcessed {
			log.Info("webhook already processed — skipping (idempotent)")
			return nil
		}

		// ── Step 3: Increment attempts + mark processing ─────────────────────
		if err := p.eventRepo.IncrementAttempts(txCtx, eventID); err != nil {
			return fmt.Errorf("increment attempts: %w", err)
		}
		if err := p.eventRepo.UpdateStatus(txCtx, eventID, domain.WebhookStatusProcessing, ""); err != nil {
			return fmt.Errorf("mark processing: %w", err)
		}

		log.WithField("attempt", event.ProcessingAttempts+1).Info("processing webhook event")

		// ── Step 4: Find and lock the payment transaction ─────────────────────
		invoiceID := extractInvoiceID(event)
		txn, err := p.txnRepo.GetByProviderInvoiceIDForUpdate(txCtx, event.Provider, invoiceID)
		if err != nil {
			_ = p.eventRepo.UpdateStatus(txCtx, eventID, domain.WebhookStatusFailed, err.Error())
			return fmt.Errorf("lock transaction for invoice %q: %w", invoiceID, err)
		}

		log = log.WithFields(logrus.Fields{
			"transaction_id":      txn.ID,
			"tenant_id":           txn.TenantID,
			"provider_invoice_id": invoiceID,
			"current_status":      txn.Status,
		})

		// ── Step 5: Handle the event (transition + ledger) ───────────────────
		if err := p.handle(txCtx, event, txn, log); err != nil {
			_ = p.eventRepo.UpdateStatus(txCtx, eventID, domain.WebhookStatusFailed, err.Error())
			return err
		}

		// ── Step 6: Mark event as processed and link to transaction ──────────
		if err := p.eventRepo.MarkProcessed(txCtx, eventID, txn.ID); err != nil {
			return fmt.Errorf("mark processed: %w", err)
		}

		txnSnapshot = txn // capture for post-commit forwarding
		log.WithField("duration_ms", time.Since(start).Milliseconds()).
			Info("webhook event processed successfully")
		return nil
	})
	if err != nil {
		return err
	}

	// ── Post-commit: forward status to Tokokarya ────────────────────────────
	// Fire-and-forget: Tokokarya unavailability must never fail payment processing.
	if p.forwarder != nil && txnSnapshot != nil {
		go p.forwardToTokokarya(context.Background(), txnSnapshot, log)
	}

	return nil
}

func (p *Processor) forwardToTokokarya(ctx context.Context, txn *domain.PaymentTransaction, log *logrus.Entry) {
	event := paymentEventName(txn.Status)
	if event == "" {
		return // don't forward non-terminal intermediate states
	}

	evt := PaymentStatusEvent{
		Event:         event,
		TransactionID: txn.ID.String(),
		OrderNumber:   txn.OrderNumber,
		Status:        string(txn.Status),
		Amount:        txn.Amount,
		Currency:      txn.Currency,
		Provider:      txn.Provider,
		CheckoutURL:   txn.CheckoutURL,
		PaidAt:        txn.PaidAt,
	}

	payload, err := json.Marshal(evt)
	if err != nil {
		log.WithError(err).Error("forward to tokokarya: marshal payload failed")
		return
	}

	if err := p.forwarder.ForwardPaymentWebhook(ctx, payload); err != nil {
		log.WithError(err).Warn("forward to tokokarya: delivery failed (non-fatal)")
	} else {
		log.WithField("event", event).Info("payment status forwarded to tokokarya")
	}
}

func paymentEventName(status domain.PaymentStatus) string {
	switch status {
	case domain.StatusPaid:
		return "payment.paid"
	case domain.StatusExpired:
		return "payment.expired"
	case domain.StatusFailed:
		return "payment.failed"
	default:
		return ""
	}
}

func (p *Processor) handle(ctx context.Context, event *domain.WebhookEvent, txn *domain.PaymentTransaction, log *logrus.Entry) error {
	switch event.EventType {
	case "payment_session.completed", "payment.capture":
		return p.handlePaid(ctx, event, txn, log)
	case "payment_session.expired":
		return p.handleExpired(ctx, event, txn, log)
	case "payment_session.failed":
		details := parsePaymentDetails(event.RawPayload)
		return p.handleFailed(ctx, event, txn, log, details.FailureCode)
	case "payment.authorization":
		log.Info("payment authorised — no action required for automatic capture")
		return nil
	case "payment.failure":
		details := parsePaymentDetails(event.RawPayload)
		if details.FailureCode == "PAYMENT_REQUEST_EXPIRED" {
			return p.handleExpired(ctx, event, txn, log)
		}
		return p.handleFailed(ctx, event, txn, log, details.FailureCode)

	default:
		log.WithField("event_type", event.EventType).Warn("unhandled webhook event type — marking processed without action")
		return nil
	}
}

func (p *Processor) handlePaid(ctx context.Context, event *domain.WebhookEvent, txn *domain.PaymentTransaction, log *logrus.Entry) error {
	prevStatus := txn.Status

	// Capture payment details from the parsed webhook payload regardless of whether
	// we transition state — payment_session.completed has no channel_code, but
	// payment.capture (fired shortly after) does. We must update payment_method even
	// when the transaction is already paid (idempotent second event).
	details := parsePaymentDetails(event.RawPayload)

	alreadyPaid := false
	if err := txn.TransitionTo(domain.StatusPaid); err != nil {
		if errors.As(err, new(domain.ErrAlreadyInState)) {
			alreadyPaid = true
		} else {
			return fmt.Errorf("transition to paid: %w", err)
		}
	}

	if alreadyPaid {
		// Only update if this event adds information we don't already have.
		if details.ChannelCode == "" && details.PaymentID == "" {
			log.WithField("status", txn.Status).Info("transaction already paid — idempotent")
			return nil
		}
		// Backfill payment_channel / provider_payment_id from the richer event.
		if details.ChannelCode != "" && txn.PaymentChannel == "" {
			txn.PaymentChannel = details.ChannelCode
		}
		if details.PaymentID != "" && txn.ProviderPaymentID == "" {
			txn.ProviderPaymentID = details.PaymentID
		}
		txn.Version++ // increment version so Update WHERE version = expected passes
		if err := p.txnRepo.Update(ctx, txn); err != nil {
			return fmt.Errorf("backfill payment details: %w", err)
		}
		log.WithFields(logrus.Fields{
			"channel_code":        txn.PaymentMethod,
			"provider_payment_id": txn.ProviderPaymentID,
		}).Info("transaction already paid — backfilled payment details")
		return nil
	}

	now := time.Now().UTC()
	txn.PaidAt = &now
	if details.ChannelCode != "" {
		txn.PaymentChannel = details.ChannelCode
	}
	if details.PaymentID != "" {
		txn.ProviderPaymentID = details.PaymentID
	}

	if err := p.txnRepo.Update(ctx, txn); err != nil {
		return fmt.Errorf("update transaction to paid: %w", err)
	}

	if err := p.ledger.RecordPayment(ctx, txn, event.ID); err != nil {
		return fmt.Errorf("record payment ledger: %w", err)
	}


	log.WithFields(logrus.Fields{
		"prev_status":         prevStatus,
		"status":              txn.Status,
		"amount":              txn.Amount,
		"shipping_fee":        txn.ShippingFee,
		"currency":            txn.Currency,
		"channel_code":        txn.PaymentMethod,
		"provider_payment_id": txn.ProviderPaymentID,
	}).Info("transaction marked paid, ledger updated")
	return nil
}

func (p *Processor) handleExpired(ctx context.Context, _ *domain.WebhookEvent, txn *domain.PaymentTransaction, log *logrus.Entry) error {
	if err := txn.TransitionTo(domain.StatusExpired); err != nil {
		if errors.As(err, new(domain.ErrAlreadyInState)) {
			log.Info("transaction already expired — idempotent")
			return nil
		}
		return fmt.Errorf("transition to expired: %w", err)
	}

	if err := p.txnRepo.Update(ctx, txn); err != nil {
		return fmt.Errorf("update transaction to expired: %w", err)
	}

	log.WithField("status", txn.Status).Info("transaction marked expired")
	return nil
}

func (p *Processor) handleFailed(ctx context.Context, _ *domain.WebhookEvent, txn *domain.PaymentTransaction, log *logrus.Entry, failureCode string) error {
	if err := txn.TransitionTo(domain.StatusFailed); err != nil {
		if errors.As(err, new(domain.ErrAlreadyInState)) {
			log.Info("transaction already failed — idempotent")
			return nil
		}
		return fmt.Errorf("transition to failed: %w", err)
	}

	if err := p.txnRepo.Update(ctx, txn); err != nil {
		return fmt.Errorf("update transaction to failed: %w", err)
	}

	log.WithFields(logrus.Fields{
		"status":       txn.Status,
		"failure_code": failureCode,
	}).Info("transaction marked failed")
	return nil
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func extractInvoiceID(event *domain.WebhookEvent) string {
	// Prefer payment_session_id from raw payload.
	details := parsePaymentDetails(event.RawPayload)
	if details.PaymentSessionID != "" {
		return details.PaymentSessionID
	}
	// Fallback: extract from composite ProviderEventID ("event_type:session_id").
	for i := len(event.ProviderEventID) - 1; i >= 0; i-- {
		if event.ProviderEventID[i] == ':' {
			return event.ProviderEventID[i+1:]
		}
	}
	return ""
}
