package jobs

import (
	"context"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/dengankarya/overwatch/internal/payment/webhook"
)

const retryWebhooksBatchSize = 50

// RetryWebhooksJob re-enqueues failed webhook events for reprocessing.
// After max asynq retries, events land in "dead_lettered" state via MarkDeadLettered.
// Run every 10 minutes via the asynq periodic task scheduler.
type RetryWebhooksJob struct {
	replay *webhook.ReplayService
	logger *logrus.Logger
}

// NewRetryWebhooksJob creates a RetryWebhooksJob.
func NewRetryWebhooksJob(replay *webhook.ReplayService, logger *logrus.Logger) *RetryWebhooksJob {
	return &RetryWebhooksJob{replay: replay, logger: logger}
}

// Run replays up to retryWebhooksBatchSize failed webhook events.
func (j *RetryWebhooksJob) Run(ctx context.Context) error {
	start := time.Now()
	log := j.logger.WithField("component", "retry_webhooks_job")

	count, err := j.replay.ReplayFailed(ctx, retryWebhooksBatchSize)
	if err != nil {
		log.WithError(err).Error("retry webhooks: replay failed")
		return err
	}

	log.WithFields(logrus.Fields{
		"replayed":    count,
		"duration_ms": time.Since(start).Milliseconds(),
	}).Info("retry webhooks: batch complete")
	return nil
}
