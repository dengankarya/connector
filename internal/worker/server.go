package worker

import (
	"context"

	"github.com/hibiken/asynq"
	"github.com/sirupsen/logrus"
)

// TokokaryaNotifier is the outbound call the worker makes after processing a task.
// pkg/tokokarya.Client satisfies this interface.
type TokokaryaNotifier interface {
	UpdateAccountStatus(ctx context.Context, accountID, status string) error
}

// NewServer creates an asynq worker server connected to Redis.
// redisURL must be a valid Redis URI, e.g. "redis://localhost:6379" or "redis://:pass@host:6379/0".
// Two queues: "webhooks" (payment events, higher priority) and "default" (everything else).
func NewServer(redisURL string) *asynq.Server {
	redisOpt, err := asynq.ParseRedisURI(redisURL)
	if err != nil {
		logrus.WithError(err).Fatal("invalid REDIS_URL")
	}
	return asynq.NewServer(
		redisOpt,
		asynq.Config{
			Concurrency: 10,
			Queues: map[string]int{
				"webhooks": 6, // higher weight — payment events must be timely
				"default":  4,
			},
			ErrorHandler: asynq.ErrorHandlerFunc(func(ctx context.Context, task *asynq.Task, err error) {
				logrus.WithFields(logrus.Fields{
					"task_type": task.Type(),
					"error":     err.Error(),
				}).Error("asynq task failed")
			}),
		},
	)
}

// WebhookEventHandler is the interface the payment webhook asynq handler must satisfy.
type WebhookEventHandler interface {
	ProcessTask(ctx context.Context, t *asynq.Task) error
}

// MuxOptions holds all handlers needed to build the asynq ServeMux.
type MuxOptions struct {
	TokokaryaNotifier   TokokaryaNotifier
	WebhookEventHandler WebhookEventHandler // optional; if nil the payment webhook task is not registered
}

// NewMux registers all task handlers and returns the ServeMux.
func NewMux(opts MuxOptions) *asynq.ServeMux {
	mux := asynq.NewServeMux()
	mux.HandleFunc(TaskXenplatformAccountUpdated, (&xenplatformHandler{notifier: opts.TokokaryaNotifier}).ProcessTask)

	if opts.WebhookEventHandler != nil {
		// Import path intentionally kept out of this package; main.go wires it in.
		mux.Handle("payment:webhook:process", asynq.HandlerFunc(opts.WebhookEventHandler.ProcessTask))
	}

	return mux
}
