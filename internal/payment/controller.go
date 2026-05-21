package payment

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/dengankarya/overwatch/common"
	"github.com/dengankarya/overwatch/internal/worker"
	"github.com/gofiber/fiber/v3"
	"github.com/hibiken/asynq"
	"github.com/sirupsen/logrus"
)

type controller struct {
	svc          *PaymentService
	webhookToken string
	enqueuer     *asynq.Client
}

// RegisterWebhookHandler registers the Xendit webhook endpoint on a public (unauthenticated) router.
func RegisterWebhookHandler(mux fiber.Router, svc *PaymentService, webhookToken string, enqueuer *asynq.Client) {
	ctrl := controller{svc: svc, webhookToken: webhookToken, enqueuer: enqueuer}
	mux.Post("/webhook/xendit", ctrl.handleWebhook)
}

// RegisterHandlers registers authenticated XenPlatform account endpoints.
func RegisterHandlers(mux fiber.Router, svc *PaymentService) {
	ctrl := controller{svc: svc}
	mux.Post("/accounts", ctrl.handleCreateAccount)
	mux.Get("/accounts/:id", ctrl.handleGetAccount)
}

func (ctrl *controller) handleCreateAccount(c fiber.Ctx) error {
	var req CreateAccountRequest
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(http.StatusBadRequest).JSON(common.Response{
			Status: http.StatusText(http.StatusBadRequest),
			Error:  err.Error(),
		})
	}

	account, err := ctrl.svc.CreateAccount(c.Context(), req)
	if err != nil {
		return c.Status(http.StatusInternalServerError).JSON(common.Response{
			Status: http.StatusText(http.StatusInternalServerError),
			Error:  err.Error(),
		})
	}

	return c.Status(http.StatusCreated).JSON(common.Response{
		Status: http.StatusText(http.StatusCreated),
		Data:   account,
	})
}

func (ctrl *controller) handleGetAccount(c fiber.Ctx) error {
	id := c.Params("id")

	account, err := ctrl.svc.GetAccount(c.Context(), id)
	if err != nil {
		return c.Status(http.StatusInternalServerError).JSON(common.Response{
			Status: http.StatusText(http.StatusInternalServerError),
			Error:  err.Error(),
		})
	}

	return c.Status(http.StatusOK).JSON(common.Response{
		Status: http.StatusText(http.StatusOK),
		Data:   account,
	})
}

func (ctrl *controller) handleWebhook(c fiber.Ctx) error {
	token := c.Get("x-callback-token")
	if ctrl.webhookToken != "" && token != ctrl.webhookToken {
		return c.Status(http.StatusUnauthorized).JSON(common.Response{
			Status: http.StatusText(http.StatusUnauthorized),
			Error:  "INVALID_WEBHOOK_TOKEN",
		})
	}

	var event WebhookEvent
	if err := c.Bind().JSON(&event); err != nil {
		return c.Status(http.StatusBadRequest).JSON(common.Response{
			Status: http.StatusText(http.StatusBadRequest),
			Error:  err.Error(),
		})
	}

	logrus.WithFields(logrus.Fields{
		"event":       event.Event,
		"business_id": event.BusinessID,
	}).Info("received xendit webhook")

	if ctrl.enqueuer != nil && strings.HasPrefix(event.Event, "account") {
		ctrl.enqueueAccountUpdate(event)
	}

	return c.Status(http.StatusOK).JSON(common.Response{
		Status: http.StatusText(http.StatusOK),
		Data:   event,
	})
}

func (ctrl *controller) enqueueAccountUpdate(event WebhookEvent) {
	// Coerce event.Data (any) to map[string]any so it marshals predictably
	var dataMap map[string]any
	if raw, err := json.Marshal(event.Data); err == nil {
		_ = json.Unmarshal(raw, &dataMap)
	}

	payload := worker.XenplatformAccountUpdatedPayload{
		Event:      event.Event,
		BusinessID: event.BusinessID,
		Data:       dataMap,
	}

	taskPayload, err := json.Marshal(payload)
	if err != nil {
		logrus.WithError(err).Error("webhook: failed to marshal task payload")
		return
	}

	task := asynq.NewTask(worker.TaskXenplatformAccountUpdated, taskPayload)
	info, err := ctrl.enqueuer.Enqueue(task, asynq.MaxRetry(5))
	if err != nil {
		logrus.WithError(err).WithField("event", event.Event).Error("webhook: failed to enqueue task")
		return
	}

	logrus.WithFields(logrus.Fields{
		"task_id":    info.ID,
		"event":      event.Event,
		"account_id": dataMap["id"],
	}).Info("webhook: enqueued xenplatform task")
}
