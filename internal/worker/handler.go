package worker

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hibiken/asynq"
	"github.com/sirupsen/logrus"
)

type xenplatformHandler struct {
	notifier TokokaryaNotifier
}

func (h *xenplatformHandler) ProcessTask(ctx context.Context, t *asynq.Task) error {
	var p XenplatformAccountUpdatedPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("xenplatform handler: unmarshal payload: %w", err)
	}

	accountID, _ := p.Data["id"].(string)
	status, _ := p.Data["status"].(string)

	if accountID == "" || status == "" {
		// Non-retryable: skip events without expected fields
		logrus.WithField("event", p.Event).Warn("xenplatform task: missing account id or status in data, skipping")
		return nil
	}

	if h.notifier == nil {
		logrus.Warn("xenplatform task: tokokarya notifier not configured, skipping")
		return nil
	}

	logrus.WithFields(logrus.Fields{
		"account_id": accountID,
		"status":     status,
		"event":      p.Event,
	}).Info("xenplatform task: updating account status")

	if err := h.notifier.UpdateAccountStatus(ctx, accountID, status); err != nil {
		return fmt.Errorf("xenplatform task: notify tokokarya: %w", err)
	}

	return nil
}
