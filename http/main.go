package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hibiken/asynqmon"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/adaptor"
	"github.com/gofiber/fiber/v3/middleware/cors"
	"github.com/hibiken/asynq"
	log "github.com/sirupsen/logrus"

	"github.com/dengankarya/connector/common"
	"github.com/dengankarya/connector/config"
	"github.com/dengankarya/connector/internal/account"
	"github.com/dengankarya/connector/internal/geocoding"
	"github.com/dengankarya/connector/internal/payment"
	"github.com/dengankarya/connector/internal/payment/jobs"
	"github.com/dengankarya/connector/internal/payment/provider"
	"github.com/dengankarya/connector/internal/payment/webhook"
	"github.com/dengankarya/connector/internal/region"
	"github.com/dengankarya/connector/internal/shipping"
	"github.com/dengankarya/connector/internal/worker"
	"github.com/dengankarya/connector/pkg/biteship"
	"github.com/dengankarya/connector/pkg/dbconn"
	"github.com/dengankarya/connector/pkg/doku"
	"github.com/dengankarya/connector/pkg/geoapify"
	"github.com/dengankarya/connector/pkg/tokokarya"
	"github.com/dengankarya/connector/pkg/wilayah"
	"github.com/dengankarya/connector/pkg/xendit"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5/pgxpool"
)

const shutdownTimeout = 30 * time.Second

//	@title			Connector API Docs
//	@version		1.0
//	@description	This is the API documentation for the Connector service, which handles any kind of external integrations for Dengankarya.

//	@contact.name	Dengan Karya Support
//	@contact.url	https://dengankarya.com/contact
//	@contact.email	dukungan@dengankarya.com

//	@BasePath	/api/v1
//	@accept		json
//	@produce	json
//	@schemes	http https

//	@securityDefinitions.apikey	ApiKeyAuth
//	@in							header
//	@name						X-API-KEY
//	@description				Regular API key (ALLOWED_API_KEYS)

//	@securityDefinitions.apikey	AdminApiKeyAuth
//	@in							header
//	@name						X-API-KEY
//	@description				Admin API key (ADMIN_API_KEYS) — required for operator-only endpoints such as balance topup

// @externalDocs.description	OpenAPI
// @externalDocs.url			https://swagger.io/resources/open-api/
func main() {
	log.SetFormatter(&log.JSONFormatter{})
	log.Info("starting overwatch http handler...")

	cfg := config.ParseENV()

	// ── Tokokarya client — payment webhook forwarding ────────────────────────
	var tokokaryaClient *tokokarya.Client
	if cfg.TokokaryaURL != "" && cfg.TokokaryaAPIKey != "" {
		tokokaryaClient = tokokarya.NewClient(cfg.TokokaryaURL, cfg.TokokaryaAPIKey)
	}

	// ── PostgreSQL ──────────────────────────────────────────────────────────
	var (
		pool             *pgxpool.Pool
		paymentMod       *payment.Module
		accountMod       *account.Module
		shippingMod      *shipping.Module
		cancelExpiredJob *jobs.CancelExpiredOrderJob
		dokuClient       *doku.Client   // shared: account gateway + payment provider
		xenditClient     *xendit.Client // shared: account gateway + payment provider
	)

	if cfg.DatabaseDSN != "" {
		runMigrations(cfg.DatabaseDSN)

		var err error
		pool, err = dbconn.ConnectPgx(cfg.DatabaseDSN)
		if err != nil {
			log.WithError(err).Fatal("failed to connect to postgres")
		}
		defer pool.Close()

		// Initialize Doku client if configured
		if cfg.DokuClientID != "" {
			dokuClient = doku.NewClient(cfg.DokuClientID, cfg.DokuSecretKey, cfg.DokuBaseURL, cfg.WebhookBaseURL, log.StandardLogger())
		}

		// Initialize Xendit client if configured
		if cfg.XenditAPIKey != "" {
			xenditClient = xendit.NewClient(cfg.XenditAPIKey, cfg.XenditBaseURL, cfg.XenditCallbackToken, cfg.WebhookBaseURL, log.StandardLogger())
		}
	}

	// ── asynq client (enqueuer) + embedded worker server ───────────────────
	redisOpt, err := asynq.ParseRedisURI(cfg.RedisURL)
	if err != nil {
		log.WithError(err).Fatal("invalid REDIS_URL")
	}
	asynqClient := asynq.NewClient(redisOpt)
	defer asynqClient.Close()

	// Build provider map — constructed before module wiring so both payment and account share the same set.
	paymentProviders := map[string]provider.PaymentProvider{}
	if dokuClient != nil {
		paymentProviders["doku"] = dokuClient
	}
	if xenditClient != nil {
		paymentProviders["xendit"] = xenditClient
	}

	// Wire modules (payment, account, shipping) with all their components
	paymentMod = payment.NewModule(pool, asynqClient, paymentProviders, nil, tokokaryaClient, log.StandardLogger())
	accountMod = account.NewModule(pool, paymentMod.TxRunner, dokuClient, xenditClient, log.StandardLogger())

	// Biteship client setup for shipping
	var biteshipClient *biteship.Client
	if cfg.BiteshipAPIKey != "" {
		biteshipClient = biteship.NewClient(cfg.BiteshipAPIKey, cfg.BiteshipBaseURL)
	}
	shippingMod = shipping.NewModule(pool, biteshipClient, biteshipClient, accountMod.Service, log.StandardLogger())

	workerServer := worker.NewServer(cfg.RedisURL)
	workerMux := worker.NewMux(worker.MuxOptions{
		WebhookEventHandler: paymentMod.WebhookHandler, // nil-safe: NewMux checks for nil
	})
	cancelExpiredJob = jobs.NewCancelExpiredOrderJob(tokokaryaClient, log.StandardLogger())
	workerMux.HandleFunc(worker.TaskCancelExpiredOrder, cancelExpiredJob.ProcessTask)

	// Xendit settlement sync job — created directly here (needs xenditClient which lives in main).
	var syncXenditJob *jobs.SyncXenditSettlementsJob
	if xenditClient != nil && paymentMod.TransactionRepo != nil {
		syncXenditJob = jobs.NewSyncXenditSettlementsJob(paymentMod.TransactionRepo, paymentMod.TxRunner, paymentMod.LedgerService, xenditClient, log.StandardLogger())
	}
	go func() {
		if err := workerServer.Start(workerMux); err != nil {
			log.WithError(err).Fatal("asynq worker server failed")
		}
	}()

	// Register periodic asynq jobs (cron-style).
	if paymentMod.ExpireJob != nil {
		scheduler := asynq.NewScheduler(redisOpt, nil)
		_, _ = scheduler.Register("*/5 * * * *", asynq.NewTask(worker.TaskExpirePayments, nil))
		_, _ = scheduler.Register("*/10 * * * *", asynq.NewTask(worker.TaskRetryWebhooks, nil))

		// Register job handlers on the worker mux.
		workerMux.HandleFunc(worker.TaskExpirePayments, func(ctx context.Context, t *asynq.Task) error {
			return paymentMod.ExpireJob.Run(ctx)
		})
		workerMux.HandleFunc(worker.TaskRetryWebhooks, func(ctx context.Context, t *asynq.Task) error {
			return paymentMod.RetryJob.Run(ctx)
		})

		if syncXenditJob != nil {
			_, _ = scheduler.Register("0 */6 * * *", asynq.NewTask(worker.TaskSyncXenditSettlements, nil))
			workerMux.HandleFunc(worker.TaskSyncXenditSettlements, func(ctx context.Context, t *asynq.Task) error {
				return syncXenditJob.Run(ctx)
			})
		}

		go func() {
			if err := scheduler.Run(); err != nil {
				log.WithError(err).Fatal("asynq scheduler failed")
			}
		}()
	}

	// ── HTTP server ─────────────────────────────────────────────────────────
	app := fiber.New()
	app.Get("/swagger/*", serveSwaggerUI)
	app.Use(cors.New(cors.ConfigDefault))
	app.Use(requestLogger())

	// Asynq queue monitor dashboard — no API key (protected at infra level via Cloudflare Access)
	monFiberHandler := adaptor.HTTPHandler(asynqmon.New(asynqmon.Options{RootPath: "/monitor", RedisConnOpt: redisOpt}))
	app.All("/monitor", monFiberHandler)
	app.All("/monitor/*", monFiberHandler)

	apiRootGroup := app.Group("/api/v1")

	app.Get("/", func(c fiber.Ctx) error {
		return c.Status(http.StatusOK).JSON(common.Response{Status: http.StatusText(http.StatusOK)})
	})

	registerHealthHandler(app)

	// ── Biteship webhook — public, no API key check ─────────────────────────
	if shippingMod.Repository != nil {
		shipping.RegisterWebhookHandler(apiRootGroup, cfg.BiteshipWebhookSignatureKey, cfg.BiteshipWebhookSignatureValue, shippingMod.Repository, tokokaryaClient, accountMod.Service, log.StandardLogger())
	}

	// ── DOKU payment webhook — public, no API key check ──────────────────────
	if dokuClient != nil && paymentMod.EventRepo != nil {
		webhook.RegisterIngestHandler(apiRootGroup, "/webhook/doku", dokuClient, paymentMod.EventRepo, paymentMod.WebhookLogRepo, asynqClient, log.StandardLogger(), nil)
	}

	// ── Xendit payment + account webhooks — public, no API key check ───────
	if xenditClient != nil && paymentMod.EventRepo != nil {
		var xenditAccountHandler webhook.AccountEventHandler
		if accountMod.Repository != nil {
			xenditAccountHandler = account.NewXenditAccountWebhookHandler(accountMod.Repository, log.StandardLogger())
		}
		webhook.RegisterIngestHandler(apiRootGroup, "/webhook/xendit", xenditClient, paymentMod.EventRepo, paymentMod.WebhookLogRepo, asynqClient, log.StandardLogger(), xenditAccountHandler)
	}

	// ── Authenticated routes — scoped to a group so /monitor stays public ────
	authGroup := apiRootGroup.Group("", authenticatedRequest(cfg))

	shipping.RegisterHandlers(authGroup.Group("/shipments"), shippingMod.Service)

	if accountMod.Service != nil {
		account.RegisterHandlers(authGroup.Group("/accounts"), accountMod.Service, accountMod.XenditClient, adminRequest(cfg))
	}

	wilayahClient := wilayah.NewClient(cfg.WilayahBaseURL)
	cachedWilayah := region.NewCachedClient(wilayahClient)
	regionSvc := region.NewRegionService(cachedWilayah)
	region.RegisterHandlers(authGroup.Group("/regions"), regionSvc)

	geoapifyClient := geoapify.NewClient(cfg.GeoapifyAPIKey, cfg.GeoapifyBaseURL)
	cachedGeocoder := geocoding.NewCachedGeocoder(geoapifyClient)
	geocodingService := geocoding.NewService(cachedGeocoder, log.StandardLogger())
	geocoding.RegisterHandlers(authGroup.Group("/geocoding"), geocodingService)

	// Payment module endpoints (requires DB).
	if paymentMod.PaymentService != nil {
		payment.RegisterPaymentHandlers(
			authGroup.Group("/payments"),
			paymentMod.PaymentService,
			paymentMod.Providers,
			asynqClient,
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

// runMigrations applies all pending migrations from db/migrations/.
// It is called at startup before any repositories are initialised.
// The binary must be run from the project root so that the relative
// path "db/migrations" resolves correctly (true for both `make run`
// and the Docker image whose WORKDIR is set to the project root).
func runMigrations(dsn string) {
	log.Info("running database migrations...")
	m, err := migrate.New("file://db/migrations", dsn)
	if err != nil {
		log.WithError(err).Fatal("migrate: failed to initialise")
	}
	defer func() {
		srcErr, dbErr := m.Close()
		if srcErr != nil {
			log.WithError(srcErr).Warn("migrate: source close error")
		}
		if dbErr != nil {
			log.WithError(dbErr).Warn("migrate: db close error")
		}
	}()

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		log.WithError(err).Fatal("migrate: up failed")
	}

	v, _, _ := m.Version()
	log.WithField("version", v).Info("migrate: schema up to date")
}
