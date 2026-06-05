package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/dengankarya/connector/internal/worker"
	"github.com/dengankarya/connector/pkg/tokokarya"
	"github.com/hibiken/asynq"
	"github.com/sirupsen/logrus"
)

// CancelExpiredOrderPayload is the task payload for TaskCancelExpiredOrder.
type CancelExpiredOrderPayload struct {
	OrderNumber string `json:"order_number"`
}

// NewCancelExpiredOrderTask creates an asynq Task scheduled to run at processAt.
func NewCancelExpiredOrderTask(orderNumber string, processAt time.Time) (*asynq.Task, []asynq.Option) {
	payload, _ := json.Marshal(CancelExpiredOrderPayload{OrderNumber: orderNumber})
	opts := []asynq.Option{
		asynq.ProcessAt(processAt),
		asynq.MaxRetry(3),
		asynq.Queue("default"),
		asynq.Timeout(30 * 1e9),              // 30s
		asynq.Retention(7 * 24 * 3600 * 1e9), // 7 days
	}
	return asynq.NewTask(worker.TaskCancelExpiredOrder, payload), opts
}

// CancelExpiredOrderJob is the asynq handler for TaskCancelExpiredOrder.
// It calls Tokokarya's cancel-expired-orders cron endpoint for the given order.
type CancelExpiredOrderJob struct {
	tokokaryaClient *tokokarya.Client
	logger          *logrus.Logger
}

// NewCancelExpiredOrderJob creates a CancelExpiredOrderJob.
func NewCancelExpiredOrderJob(tokokaryaClient *tokokarya.Client, logger *logrus.Logger) *CancelExpiredOrderJob {
	return &CancelExpiredOrderJob{tokokaryaClient: tokokaryaClient, logger: logger}
}

// ProcessTask implements asynq.Handler.
func (j *CancelExpiredOrderJob) ProcessTask(ctx context.Context, t *asynq.Task) error {
	var p CancelExpiredOrderPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("unmarshal payload: %w", asynq.SkipRetry)
	}

	log := j.logger.WithFields(logrus.Fields{
		"component":    "cancel_expired_order_job",
		"order_number": p.OrderNumber,
	})

	if j.tokokaryaClient == nil {
		log.Warn("cancel expired order: tokokarya client not configured, skipping")
		return nil
	}

	log.Info("cancel expired order: calling tokokarya")

	if err := j.tokokaryaClient.CancelExpiredOrders(ctx, p.OrderNumber); err != nil {
		log.WithError(err).Error("cancel expired order: tokokarya call failed")
		return fmt.Errorf("cancel expired order %s: %w", p.OrderNumber, err)
	}

	log.Info("cancel expired order: done")
	return nil
}
