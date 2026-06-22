package webhook

import (
	"net/http"

	"github.com/dengankarya/connector/internal/payment/domain"
	"github.com/dengankarya/connector/internal/payment/provider"
	"github.com/dengankarya/connector/internal/payment/repository"
	"github.com/gofiber/fiber/v3"
	"github.com/hibiken/asynq"
	"github.com/sirupsen/logrus"
)

// RegisterIngestHandler registers a POST webhook ingestion endpoint for the given provider.
// The route must be registered before the auth middleware — webhooks are unauthenticated.
//
// Processing is async: the raw event is stored and enqueued immediately; the caller
// receives HTTP 200 as soon as the event is persisted.
func RegisterIngestHandler(
	mux fiber.Router,
	route string,
	prov provider.PaymentProvider,
	eventRepo *repository.WebhookEventRepository,
	logRepo *repository.WebhookRequestLogRepository,
	enqueuer *asynq.Client,
	logger *logrus.Logger,
) {
	mux.Post(route, func(c fiber.Ctx) error {
		ctx := c.Context()
		log := logger.WithFields(logrus.Fields{
			"component": "webhook_ingest",
			"provider":  prov.ProviderName(),
			"route":     route,
		})

		rawBody := c.Body()

		// Collect relevant headers for audit log and signature validation.
		headers := map[string]string{
			"Content-Type":     c.Get("Content-Type"),
			"X-Callback-Token": c.Get("X-Callback-Token"),
			"X-Webhook-Token":  c.Get("X-Webhook-Token"),
			"X-Request-ID":     c.Get("X-Request-ID"),
		}

		// Audit log — best-effort, never blocks processing.
		if err := logRepo.Create(ctx, c.IP(), rawBody, headers); err != nil {
			log.WithError(err).Warn("failed to write webhook request log")
		}

		// Signature validation — on failure, return 200 to prevent retry storms
		// while not processing the event.
		if err := prov.ValidateWebhookSignature(ctx, rawBody, headers); err != nil {
			log.WithError(err).Warn("webhook signature validation failed — ignoring event")
			return c.Status(http.StatusOK).JSON(map[string]string{"status": "ok"})
		}

		// Parse the raw payload into a normalized event.
		parsed, err := prov.ParseWebhookEvent(ctx, rawBody)
		if err != nil {
			log.WithError(err).Error("failed to parse webhook event")
			return c.Status(http.StatusBadRequest).JSON(map[string]string{"status": "bad_request", "error": err.Error()})
		}

		event := &domain.WebhookEvent{
			Provider:         prov.ProviderName(),
			ProviderEventID:  parsed.ProviderEventID,
			EventType:        parsed.EventType,
			RawPayload:       rawBody,
			Headers:          headers,
			SignatureValid:   true,
			ProcessingStatus: domain.WebhookStatusReceived,
		}

		if err := eventRepo.Create(ctx, event); err != nil {
			if err == domain.ErrDuplicateWebhookEvent {
				log.WithField("provider_event_id", parsed.ProviderEventID).
					Info("duplicate webhook received — idempotent")
				return c.Status(http.StatusOK).JSON(map[string]string{"status": "ok"})
			}
			log.WithError(err).Error("failed to store webhook event")
			return c.Status(http.StatusInternalServerError).JSON(map[string]string{"status": "error"})
		}

		task, opts := NewTask(event.ID)
		if _, err := enqueuer.EnqueueContext(ctx, task, opts...); err != nil {
			log.WithError(err).Error("failed to enqueue webhook processing task")
			// Event is stored — the retry job will pick it up.
		}

		log.WithFields(logrus.Fields{
			"webhook_event_id":  event.ID,
			"event_type":        event.EventType,
			"provider_event_id": parsed.ProviderEventID,
		}).Info("webhook event ingested")

		return c.Status(http.StatusOK).JSON(map[string]string{"status": "ok"})
	})
}
