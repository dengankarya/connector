package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/cors"
	"github.com/hibiken/asynq"
	log "github.com/sirupsen/logrus"

	"github.com/dengankarya/overwatch/common"
	"github.com/dengankarya/overwatch/config"
	"github.com/dengankarya/overwatch/internal/payment"
	"github.com/dengankarya/overwatch/internal/payment/jobs"
	"github.com/dengankarya/overwatch/internal/payment/ledger"
	"github.com/dengankarya/overwatch/internal/payment/provider/xendit"
	"github.com/dengankarya/overwatch/internal/payment/repository"
	paymentservice "github.com/dengankarya/overwatch/internal/payment/service"
	"github.com/dengankarya/overwatch/internal/payment/webhook"
	"github.com/dengankarya/overwatch/internal/region"
	"github.com/dengankarya/overwatch/internal/shipping"
	"github.com/dengankarya/overwatch/internal/worker"
	"github.com/dengankarya/overwatch/pkg/biteship"
	"github.com/dengankarya/overwatch/pkg/dbconn"
	"github.com/dengankarya/overwatch/pkg/tokokarya"
	"github.com/dengankarya/overwatch/pkg/wilayah"
	"github.com/dengankarya/overwatch/pkg/xenplatform"
)

const shutdownTimeout = 30 * time.Second

func main() {
	log.SetFormatter(&log.JSONFormatter{})
	log.Info("starting overwatch http handler...")

	cfg := config.ParseENV()

	// ── Tokokarya client — initialised early so the DB block can reference it ──
	// Satisfies both worker.TokokaryaNotifier (account updates) and
	// webhook.WebhookForwarder (payment webhook forwarding).
	var tokokaryaClient *tokokarya.Client
	var tokokaryaNotifier worker.TokokaryaNotifier
	if cfg.TokokaryaURL != "" && cfg.TokokaryaAPIKey != "" {
		tokokaryaClient = tokokarya.NewClient(cfg.TokokaryaURL, cfg.TokokaryaAPIKey)
		tokokaryaNotifier = tokokaryaClient
	}

	// ── PostgreSQL (pgx pool for payment module) ────────────────────────────
	var (
		txnRepo        *repository.TransactionRepository
		eventRepo      *repository.WebhookEventRepository
		ledgerRepo     *repository.LedgerRepository
		payoutRepo     *repository.PayoutRepository
		txRunner       *repository.TxRunner
		ledgerSvc      *ledger.Service
		webhookProc    *webhook.Processor
		webhookHandler *webhook.AsynqHandler
		replaySvc      *webhook.ReplayService
		paymentSvc     *paymentservice.PaymentService
		expireJob      *jobs.ExpirePaymentsJob
		retryJob       *jobs.RetryWebhooksJob
	)

	var requestLogRepo *repository.WebhookRequestLogRepository

	if cfg.DatabaseDSN != "" {
		pool, err := dbconn.ConnectPgx(cfg.DatabaseDSN)
		if err != nil {
			log.WithError(err).Fatal("failed to connect to postgres")
		}
		defer pool.Close()

		// Repositories
		txnRepo = repository.NewTransactionRepository(pool)
		eventRepo = repository.NewWebhookEventRepository(pool)
		ledgerRepo = repository.NewLedgerRepository(pool)
		payoutRepo = repository.NewPayoutRepository(pool)
		txRunner = repository.NewTxRunner(pool)
		requestLogRepo = repository.NewWebhookRequestLogRepository(pool)

		// Business layer
		ledgerSvc = ledger.New(ledgerRepo, log.StandardLogger())

		xenditProv := xendit.New(cfg.XenditAPIKey, cfg.XenditWebhookToken, cfg.XenditBaseURL)

		webhookProc = webhook.NewProcessor(eventRepo, txnRepo, ledgerSvc, txRunner, xenditProv, tokokaryaClient, log.StandardLogger())
		webhookHandler = webhook.NewAsynqHandler(webhookProc, log.StandardLogger())

		// Asynq client needed for ReplayService — created before the section below.
		_ = payoutRepo // used by PayoutService; wired separately if needed

		paymentSvc = paymentservice.NewPaymentService(txnRepo, xenditProv, txRunner, log.StandardLogger())

		// Jobs
		expireJob = jobs.NewExpirePaymentsJob(txnRepo, txRunner, log.StandardLogger())
		retryJob = jobs.NewRetryWebhooksJob(nil, log.StandardLogger()) // replay wired after enqueuer init below
		_ = expireJob
		_ = retryJob
	}

	// ── asynq client (enqueuer) + embedded worker server ───────────────────
	redisOpt, err := asynq.ParseRedisURI(cfg.RedisURL)
	if err != nil {
		log.WithError(err).Fatal("invalid REDIS_URL")
	}
	asynqClient := asynq.NewClient(redisOpt)
	defer asynqClient.Close()

	// Wire ReplayService now that enqueuer exists.
	if eventRepo != nil {
		replaySvc = webhook.NewReplayService(eventRepo, asynqClient, log.StandardLogger())
		retryJob = jobs.NewRetryWebhooksJob(replaySvc, log.StandardLogger())
	}

	workerServer := worker.NewServer(cfg.RedisURL)
	workerMux := worker.NewMux(worker.MuxOptions{
		TokokaryaNotifier:   tokokaryaNotifier,
		WebhookEventHandler: webhookHandler, // nil-safe: NewMux checks for nil
	})
	go func() {
		if err := workerServer.Start(workerMux); err != nil {
			log.WithError(err).Fatal("asynq worker server failed")
		}
	}()

	// Register periodic asynq jobs (cron-style).
	if cfg.DatabaseDSN != "" {
		scheduler := asynq.NewScheduler(redisOpt, nil)
		_, _ = scheduler.Register("*/5 * * * *", asynq.NewTask(worker.TaskExpirePayments, nil))
		_, _ = scheduler.Register("*/10 * * * *", asynq.NewTask(worker.TaskRetryWebhooks, nil))

		// Register job handlers on the worker mux.
		workerMux.HandleFunc(worker.TaskExpirePayments, func(ctx context.Context, t *asynq.Task) error {
			return expireJob.Run(ctx)
		})
		workerMux.HandleFunc(worker.TaskRetryWebhooks, func(ctx context.Context, t *asynq.Task) error {
			return retryJob.Run(ctx)
		})

		go func() {
			if err := scheduler.Run(); err != nil {
				log.WithError(err).Fatal("asynq scheduler failed")
			}
		}()
	}

	// ── HTTP server ─────────────────────────────────────────────────────────
	app := fiber.New()
	app.Use(cors.New(cors.ConfigDefault))
	app.Use(requestLogger())

	app.Get("/", func(c fiber.Ctx) error {
		return c.Status(http.StatusOK).JSON(common.Response{Status: http.StatusText(http.StatusOK)})
	})

	registerHealthHandler(app)

	// ── Xendit webhook — public, no API key check ───────────────────────────
	xenplatformClient := xenplatform.NewClient(cfg.XenditAPIKey, cfg.XenditBaseURL)
	legacyPaymentSvc := payment.NewPaymentService(xenplatformClient)

	if cfg.DatabaseDSN != "" {
		xenditProv := xendit.New(cfg.XenditAPIKey, cfg.XenditWebhookToken, cfg.XenditBaseURL)
		payment.RegisterWebhookHandlerV2(app, legacyPaymentSvc, cfg.XenditWebhookToken, asynqClient, webhookProc, xenditProv, requestLogRepo)
	} else {
		// Fall back to legacy handler when database is not configured.
		payment.RegisterWebhookHandler(app, legacyPaymentSvc, cfg.XenditWebhookToken, asynqClient)
	}

	// ── Authenticated routes ─────────────────────────────────────────────────
	app.Use(authenticatedRequest(cfg))

	biteshipClient := biteship.NewClient(cfg.BiteshipAPIKey, cfg.BiteshipBaseURL)
	cachedAggregator := shipping.NewCachedAggregator(biteshipClient)
	shippingSvc := shipping.NewShippingService(cachedAggregator)
	shipping.RegisterHandlers(app.Group("/api/shippings"), shippingSvc)

	wilayahClient := wilayah.NewClient(cfg.WilayahBaseURL)
	cachedWilayah := region.NewCachedClient(wilayahClient)
	regionSvc := region.NewRegionService(cachedWilayah)
	region.RegisterHandlers(app.Group("/api/regions"), regionSvc)

	// XenPlatform account endpoints (existing).
	payment.RegisterHandlers(app.Group("/api/payments"), legacyPaymentSvc)

	// New payment module endpoints (requires DB).
	if paymentSvc != nil {
		payment.RegisterPaymentHandlers(
			app.Group("/api/payments"),
			paymentSvc,
			replaySvc,
			log.StandardLogger(),
		)
	}

	go func() {
		if err := app.Listen(":" + cfg.PORT); err != nil {
			log.Fatal(err)
		}
	}()

	// ── Graceful shutdown ────────────────────────────────────────────────────
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info("shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := app.ShutdownWithContext(ctx); err != nil {
		log.Errorf("HTTP server shutdown failed: %v", err)
	}
	workerServer.Shutdown()

	log.Info("server stopped gracefully")
}
