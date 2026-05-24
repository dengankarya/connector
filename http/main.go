package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dengankarya/overwatch/common"
	"github.com/dengankarya/overwatch/config"
	"github.com/dengankarya/overwatch/internal/payment"
	"github.com/dengankarya/overwatch/internal/region"
	"github.com/dengankarya/overwatch/internal/shipping"
	"github.com/dengankarya/overwatch/internal/tracking"
	"github.com/dengankarya/overwatch/internal/worker"
	"github.com/dengankarya/overwatch/pkg/biteship"
	"github.com/dengankarya/overwatch/pkg/tokokarya"
	"github.com/dengankarya/overwatch/pkg/wilayah"
	"github.com/dengankarya/overwatch/pkg/xenplatform"
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/cors"
	"github.com/hibiken/asynq"
	log "github.com/sirupsen/logrus"
)

const shutdownTimeout = 30 * time.Second

func main() {
	log.SetFormatter(&log.JSONFormatter{})
	log.Info("starting overwatch http handler...")

	cfg := config.ParseENV()

	// ----- asynq client (enqueuer) + embedded worker server
	redisOpt := asynq.RedisClientOpt{Addr: cfg.RedisAddr}
	asynqClient := asynq.NewClient(redisOpt)
	defer asynqClient.Close()

	var tokokaryaNotifier worker.TokokaryaNotifier
	if cfg.TokokaryaURL != "" && cfg.TokokaryaAPIKey != "" {
		tokokaryaNotifier = tokokarya.NewClient(cfg.TokokaryaURL, cfg.TokokaryaAPIKey)
	}

	workerServer := worker.NewServer(cfg.RedisAddr)
	workerMux := worker.NewMux(tokokaryaNotifier)
	go func() {
		if err := workerServer.Start(workerMux); err != nil {
			log.WithError(err).Fatal("asynq worker server failed")
		}
	}()

	// ----- HTTP server
	app := fiber.New()

	app.Use(cors.New(cors.ConfigDefault))
	app.Use(requestLogger())

	app.Get("/", func(c fiber.Ctx) error {
		return c.Status(http.StatusOK).JSON(common.Response{
			Status: http.StatusText(http.StatusOK),
		})
	})

	// ----- health check handler
	registerHealthHandler(app)

	// ----- xendit webhook (public — no API key required, validated by x-callback-token)
	xenplatformClient := xenplatform.NewClient(cfg.XenditAPIKey, cfg.XenditBaseURL)
	paymentSvc := payment.NewPaymentService(xenplatformClient)
	payment.RegisterWebhookHandler(app, paymentSvc, cfg.XenditWebhookToken, asynqClient)

	// ------ all incoming request after this line should contain X-API-KEY headers.
	app.Use(authenticatedRequest(cfg))

	biteshipClient := biteship.NewClient(cfg.BiteshipAPIKey, cfg.BiteshipBaseURL)
	cachedAggregator := shipping.NewCachedAggregator(biteshipClient)
	shippingSvc := shipping.NewShippingService(cachedAggregator)
	shipping.RegisterHandlers(app.Group("/api/shippings"), shippingSvc)

	trackingSvc := tracking.NewTrackingService(biteshipClient)
	tracking.RegisterHandlers(app.Group("/api/trackings"), trackingSvc)

	wilayahClient := wilayah.NewClient(cfg.WilayahBaseURL)
	cachedWilayah := region.NewCachedClient(wilayahClient)
	regionSvc := region.NewRegionService(cachedWilayah)
	region.RegisterHandlers(app.Group("/api/regions"), regionSvc)

	payment.RegisterHandlers(app.Group("/api/payments"), paymentSvc)

	go func() {
		if err := app.Listen(":" + cfg.PORT); err != nil {
			log.Fatal(err)
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info("shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	// Stop HTTP server first, then drain the worker
	if err := app.ShutdownWithContext(ctx); err != nil {
		log.Errorf("HTTP server shutdown failed: %v", err)
	}
	workerServer.Shutdown()

	log.Info("server stopped gracefully")
}
