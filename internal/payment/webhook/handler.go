package webhook

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dengankarya/connector/pkg/logger"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/sirupsen/logrus"
)

// TaskProcessWebhookEvent is the asynq task type for async webhook processing.
const TaskProcessWebhookEvent = "payment:webhook:process"

// ProcessWebhookPayload is the task payload stored in Redis.
// We store only the event UUID — the full payload is already persisted in the DB.
type ProcessWebhookPayload struct {
	WebhookEventID string `json:"webhook_event_id"`
}

// AsynqHandler is the asynq task handler for TaskProcessWebhookEvent.
type AsynqHandler struct {
	processor *Processor
	logger    *logger.Logger
}

// NewAsynqHandler creates an AsynqHandler.
func NewAsynqHandler(processor *Processor, logger *logger.Logger) *AsynqHandler {
	return &AsynqHandler{processor: processor, logger: logger}
}

// ProcessTask implements asynq.Handler. It is called by the asynq worker server.
func (h *AsynqHandler) ProcessTask(ctx context.Context, t *asynq.Task) error {
	var p ProcessWebhookPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		// Non-retryable: malformed payload will never recover.
		h.logger.WithError(ctx, err).Error("webhook task: unmarshal payload failed")
		return fmt.Errorf("unmarshal payload: %w", asynq.SkipRetry)
	}

	eventID, err := uuid.Parse(p.WebhookEventID)
	if err != nil {
		h.logger.WithField(ctx, "raw_id", p.WebhookEventID).Error("webhook task: invalid event UUID")
		return fmt.Errorf("invalid event id %q: %w", p.WebhookEventID, asynq.SkipRetry)
	}

	h.logger.WithField(ctx, "webhook_event_id", eventID).Info("webhook task: processing")

	if err := h.processor.Process(ctx, eventID); err != nil {
		h.logger.WithFields(ctx, logrus.Fields{
			"webhook_event_id": eventID,
			"error":            err.Error(),
		}).Error("webhook task: processing failed")
		return fmt.Errorf("process webhook event %s: %w", eventID, err)
	}
	return nil
}

// NewTask creates an asynq Task for processing a stored webhook event.
// Retry count and timeout are set here so the policy lives next to the handler.
func NewTask(eventID uuid.UUID) (*asynq.Task, []asynq.Option) {
	payload, _ := json.Marshal(ProcessWebhookPayload{WebhookEventID: eventID.String()})
	opts := []asynq.Option{
		asynq.MaxRetry(5),
		asynq.Queue("webhooks"),
		asynq.Timeout(30 * 1e9),              // 30s in nanoseconds (time.Duration)
		asynq.Retention(7 * 24 * 3600 * 1e9), // 7 days
	}
	return asynq.NewTask(TaskProcessWebhookEvent, payload), opts
}
