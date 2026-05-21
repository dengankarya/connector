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
	"github.com/dengankarya/overwatch/internal/shipping"
	"github.com/dengankarya/overwatch/pkg/biteship"
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/cors"
	log "github.com/sirupsen/logrus"
)

const shutdownTimeout = 30 * time.Second

func main() {
	log.Info("starting overwatch http handler...")

	cfg := config.ParseENV()

	app := fiber.New()

	app.Use(cors.New(cors.ConfigDefault))
	app.Use(requestLogger())

	app.Get("/", func(c fiber.Ctx) error {
		return c.Status(http.StatusOK).JSON(common.Response{
			Status: http.StatusText(http.StatusOK),
		})
	})

	// ------ all incoming request after this line should contain X-API-KEY headers.
	app.Use(authenticatedRequest(cfg))

	biteshipClient := biteship.NewClient(cfg.BiteshipAPIKey, cfg.BiteshipBaseURL)
	cachedAggregator := shipping.NewCachedAggregator(biteshipClient)
	shippingSvc := shipping.NewShippingService(cachedAggregator)
	shipping.RegisterHandlers(app.Group("/api/shippings"), shippingSvc)

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

	if err := app.ShutdownWithContext(ctx); err != nil {
		log.Errorf("HTTP server shutdown failed: %v", err)
	}

	log.Info("server stopped gracefully")
}
