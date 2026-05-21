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
func NewServer(redisAddr string) *asynq.Server {
	return asynq.NewServer(
		asynq.RedisClientOpt{Addr: redisAddr},
		asynq.Config{
			Concurrency: 5,
			Queues:      map[string]int{"default": 1},
			ErrorHandler: asynq.ErrorHandlerFunc(func(ctx context.Context, task *asynq.Task, err error) {
				logrus.WithFields(logrus.Fields{
					"type":  task.Type(),
					"error": err.Error(),
				}).Error("asynq task failed")
			}),
		},
	)
}

// NewMux registers all task handlers on a ServeMux and returns it.
func NewMux(notifier TokokaryaNotifier) *asynq.ServeMux {
	mux := asynq.NewServeMux()
	mux.HandleFunc(TaskXenplatformAccountUpdated, (&xenplatformHandler{notifier: notifier}).ProcessTask)
	return mux
}
