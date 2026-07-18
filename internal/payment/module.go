package payment

import (
	"github.com/dengankarya/connector/internal/payment/jobs"
	"github.com/dengankarya/connector/internal/payment/ledger"
	"github.com/dengankarya/connector/internal/payment/provider"
	"github.com/dengankarya/connector/internal/payment/repository"
	paymentservice "github.com/dengankarya/connector/internal/payment/service"
	"github.com/dengankarya/connector/internal/payment/webhook"
	"github.com/dengankarya/connector/pkg/logger"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Module contains all payment-related services and repositories.
type Module struct {
	TransactionRepo  *repository.TransactionRepository
	EventRepo        *repository.WebhookEventRepository
	WebhookLogRepo   *repository.WebhookRequestLogRepository
	LedgerRepo       *repository.LedgerRepository
	PayoutRepo       *repository.PayoutRepository
	TxRunner         *repository.TxRunner
	LedgerService    *ledger.Service
	WebhookProcessor *webhook.Processor
	WebhookHandler   *webhook.AsynqHandler
	ReplayService    *webhook.ReplayService
	PaymentService   *paymentservice.PaymentService
	ExpireJob        *jobs.ExpirePaymentsJob
	RetryJob         *jobs.RetryWebhooksJob
	Providers        map[string]provider.PaymentProvider // keyed by provider name ("doku", "xendit")
}

// NewModule constructs the payment module (excluding jobs that need external clients).
// enqueuer can be nil when Redis is not available.
func NewModule(
	pool *pgxpool.Pool,
	enqueuer *asynq.Client,
	providers map[string]provider.PaymentProvider,
	accountFinder paymentservice.GatewayAccountFinder,
	forwarder webhook.PaymentForwarder,
	logger *logger.Logger,
) *Module {
	if pool == nil {
		return &Module{Providers: providers}
	}

	// Repositories
	txnRepo := repository.NewTransactionRepository(pool)
	eventRepo := repository.NewWebhookEventRepository(pool)
	webhookLogRepo := repository.NewWebhookRequestLogRepository(pool)
	ledgerRepo := repository.NewLedgerRepository(pool)
	payoutRepo := repository.NewPayoutRepository(pool)
	txRunner := repository.NewTxRunner(pool)

	// Business layer
	ledgerSvc := ledger.New(ledgerRepo, logger)
	webhookProc := webhook.NewProcessor(eventRepo, txnRepo, ledgerSvc, txRunner, forwarder, logger)
	webhookHandler := webhook.NewAsynqHandler(webhookProc, logger)
	paymentSvc := paymentservice.NewPaymentService(txnRepo, txRunner, ledgerSvc, accountFinder, logger)

	// Jobs
	expireJob := jobs.NewExpirePaymentsJob(txnRepo, txRunner, logger)
	retryJob := jobs.NewRetryWebhooksJob(nil, logger) // wired after enqueuer init

	// Wire ReplayService now that enqueuer exists
	var replaySvc *webhook.ReplayService
	if enqueuer != nil {
		replaySvc = webhook.NewReplayService(eventRepo, enqueuer, logger)
		retryJob = jobs.NewRetryWebhooksJob(replaySvc, logger)
	}

	return &Module{
		TransactionRepo:  txnRepo,
		EventRepo:        eventRepo,
		WebhookLogRepo:   webhookLogRepo,
		LedgerRepo:       ledgerRepo,
		PayoutRepo:       payoutRepo,
		TxRunner:         txRunner,
		LedgerService:    ledgerSvc,
		WebhookProcessor: webhookProc,
		WebhookHandler:   webhookHandler,
		ReplayService:    replaySvc,
		PaymentService:   paymentSvc,
		ExpireJob:        expireJob,
		RetryJob:         retryJob,
		Providers:        providers,
	}
}
