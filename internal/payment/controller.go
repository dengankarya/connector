package payment

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/dengankarya/overwatch/common"
	"github.com/dengankarya/overwatch/internal/payment/domain"
	"github.com/dengankarya/overwatch/internal/payment/provider"
	"github.com/dengankarya/overwatch/internal/payment/repository"
	paymentservice "github.com/dengankarya/overwatch/internal/payment/service"
	"github.com/dengankarya/overwatch/internal/payment/webhook"
	"github.com/dengankarya/overwatch/internal/worker"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/sirupsen/logrus"
)

// ─── XenPlatform account controller (existing, unchanged) ────────────────────

type controller struct {
	svc              *PaymentService
	webhookToken     string
	enqueuer         *asynq.Client
	paymentProcessor *webhook.Processor              // nil on legacy path
	xenditProvider   provider.PaymentProvider        // nil on legacy path
	requestLogRepo   *repository.WebhookRequestLogRepository // nil on legacy path
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

// handleWebhook is the public Xendit callback endpoint.
// It validates the signature, stores the raw event, and enqueues async processing.
func (ctrl *controller) handleWebhook(c fiber.Ctx) error {
	requestID := string(c.Request().Header.Peek("X-Request-ID"))
	if requestID == "" {
		requestID = uuid.New().String()
	}
	c.Set("X-Request-ID", requestID)

	// ── Audit log ────────────────────────────────────────────────────────────
	// Capture every request before validation — fire-and-forget so the log
	// write never delays or disrupts the webhook response.
	// Body bytes and IP are copied here; Fiber recycles the request buffer
	// after the handler returns so they must not be captured by reference.
	if ctrl.requestLogRepo != nil {
		sourceIP := c.IP()
		rawBody := make([]byte, len(c.Body()))
		copy(rawBody, c.Body())
		headers := make(map[string]string)
		c.Request().Header.VisitAll(func(k, v []byte) {
			headers[string(k)] = string(v)
		})
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := ctrl.requestLogRepo.Create(ctx, sourceIP, rawBody, headers); err != nil {
				logrus.WithFields(logrus.Fields{
					"component":  "webhook_controller",
					"request_id": requestID,
				}).WithError(err).Warn("failed to write webhook request log")
			}
		}()
	}

	log := logrus.WithFields(logrus.Fields{
		"component":  "webhook_controller",
		"request_id": requestID,
	})

	// Validate the callback token before doing anything else.
	if ctrl.webhookToken != "" {
		token := c.Get("x-callback-token")
		if token != ctrl.webhookToken {
			log.Warn("webhook: invalid callback token")
			// Return 200 to prevent Xendit from retrying; log the security event.
			return c.Status(http.StatusOK).JSON(common.Response{Status: "OK"})
		}
	}

	rawBody := c.Body()

	// Route account lifecycle events (account.*, kyc.*) to the XenPlatform handler.
	// NOTE: Xendit puts business_id in ALL webhook envelopes, including payment_session.*
	// and payment.* events — so we must check the event type, not just business_id presence.
	var baseEvent WebhookEvent
	if err := json.Unmarshal(rawBody, &baseEvent); err == nil && isAccountEvent(baseEvent.Event) {
		ctrl.enqueueAccountUpdate(baseEvent)
		return c.Status(http.StatusOK).JSON(common.Response{Status: "OK"})
	}

	// Legacy path: no payment module wired → log and return.
	if ctrl.paymentProcessor == nil {
		var event map[string]any
		_ = json.Unmarshal(rawBody, &event)
		log.WithField("data", event).Info("received xendit webhook (no processor configured)")
		return c.Status(http.StatusOK).JSON(common.Response{Status: "OK"})
	}

	// Extract all headers as a map for storage.
	headers := make(map[string]string)
	c.Request().Header.VisitAll(func(key, value []byte) {
		headers[string(key)] = string(value)
	})

	// Ingest: store raw event and get the assigned UUID.
	eventID, isDuplicate, err := ctrl.paymentProcessor.Ingest(c.Context(), rawBody, headers, ctrl.xenditProvider)
	if err != nil {
		log.WithError(err).Error("webhook: ingest failed")
		// Still return 200 — do not let Xendit retry on a storage error;
		// the retry_webhooks job will recover it.
		return c.Status(http.StatusOK).JSON(common.Response{Status: "OK"})
	}

	if isDuplicate {
		// Already ingested — acknowledge without re-enqueuing.
		return c.Status(http.StatusOK).JSON(common.Response{Status: "OK"})
	}

	// Enqueue async processing. The HTTP handler returns immediately.
	task, opts := webhook.NewTask(eventID)
	if _, err := ctrl.enqueuer.EnqueueContext(c.Context(), task, opts...); err != nil {
		log.WithFields(logrus.Fields{
			"webhook_event_id": eventID,
			"error":            err.Error(),
		}).Error("webhook: enqueue failed — event stored, will be retried by job")
	}

	return c.Status(http.StatusOK).JSON(common.Response{Status: "OK"})
}

func (ctrl *controller) enqueueAccountUpdate(event WebhookEvent) {
	var dataMap map[string]any
	if raw, err := json.Marshal(event.Data); err == nil {
		_ = json.Unmarshal(raw, &dataMap)
	}

	payload, err := json.Marshal(worker.XenplatformAccountUpdatedPayload{
		Event:      event.Event,
		BusinessID: event.BusinessID,
		Data:       dataMap,
	})
	if err != nil {
		logrus.WithError(err).Error("webhook: failed to marshal task payload")
		return
	}

	task := asynq.NewTask(worker.TaskXenplatformAccountUpdated, payload)
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

// ─── New payment module controller ───────────────────────────────────────────

type paymentController struct {
	svc       *paymentservice.PaymentService
	replaySvc *webhook.ReplayService
	logger    *logrus.Logger
}

// RegisterPaymentHandlers registers the payment transaction and webhook management endpoints.
func RegisterPaymentHandlers(
	mux fiber.Router,
	svc *paymentservice.PaymentService,
	replaySvc *webhook.ReplayService,
	logger *logrus.Logger,
) {
	ctrl := &paymentController{svc: svc, replaySvc: replaySvc, logger: logger}
	mux.Get("/", ctrl.listTransactions)
	mux.Post("/", ctrl.createPayment)
	mux.Post("/manual", ctrl.createManualPayment)
	mux.Get("/:id", ctrl.getPayment)
	mux.Post("/webhooks/:event_id/replay", ctrl.replayWebhook)
}

// RegisterWebhookHandlerV2 registers the new Xendit webhook handler that uses full payment module wiring.
func RegisterWebhookHandlerV2(
	mux fiber.Router,
	svc *PaymentService,
	webhookToken string,
	enqueuer *asynq.Client,
	processor *webhook.Processor,
	prov provider.PaymentProvider,
	requestLogRepo *repository.WebhookRequestLogRepository,
) {
	ctrl := controller{
		svc:              svc,
		webhookToken:     webhookToken,
		enqueuer:         enqueuer,
		paymentProcessor: processor,
		xenditProvider:   prov,
		requestLogRepo:   requestLogRepo,
	}
	mux.Post("/webhook/xendit", ctrl.handleWebhook)
}

func (ctrl *paymentController) createPayment(c fiber.Ctx) error {
	tenantID := mustParseIntHeader(c, "X-Tenant-ID")
	if tenantID == 0 {
		return c.Status(http.StatusBadRequest).JSON(common.Response{
			Status: "Bad Request",
			Error:  "X-Tenant-ID header is required",
		})
	}

	var body struct {
		OrderNumber            string         `json:"order_number"`
		IdempotencyKey         string         `json:"idempotency_key"`
		Amount                 int64          `json:"amount"`
		Currency               string         `json:"currency"`
		PlatformFee            int64          `json:"platform_fee"`
		AllowedPaymentChannels []string       `json:"allowed_payment_channels"`
		SuccessReturnURL       string         `json:"success_return_url"`
		CancelReturnURL        string         `json:"cancel_return_url"`
		Description            string         `json:"description"`
		CustomerEmail          string         `json:"customer_email"`
		CustomerName           string         `json:"customer_name"`
		CustomerReferenceID    string         `json:"customer_reference_id"`
		ForUserID              string         `json:"for_user_id"`
		Metadata               map[string]any `json:"metadata"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(http.StatusBadRequest).JSON(common.Response{
			Status: "Bad Request", Error: err.Error(),
		})
	}

	if body.OrderNumber == "" {
		return c.Status(http.StatusBadRequest).JSON(common.Response{
			Status: "Bad Request", Error: "order_number is required",
		})
	}

	txn, err := ctrl.svc.CreatePayment(c.Context(), paymentservice.CreatePaymentRequest{
		TenantID:               tenantID,
		ForUserID:              body.ForUserID,
		OrderNumber:            body.OrderNumber,
		IdempotencyKey:         body.IdempotencyKey,
		Amount:                 body.Amount,
		Currency:               body.Currency,
		PlatformFee:            body.PlatformFee,
		AllowedPaymentChannels: body.AllowedPaymentChannels,
		SuccessReturnURL:       body.SuccessReturnURL,
		CancelReturnURL:        body.CancelReturnURL,
		Description:            body.Description,
		CustomerEmail:          body.CustomerEmail,
		CustomerName:           body.CustomerName,
		CustomerReferenceID:    body.CustomerReferenceID,
		Metadata:               body.Metadata,
	})
	if err != nil {
		ctrl.logger.WithError(err).Error("create payment failed")
		return c.Status(http.StatusInternalServerError).JSON(common.Response{
			Status: "Internal Server Error", Error: err.Error(),
		})
	}

	return c.Status(http.StatusCreated).JSON(common.Response{
		Status: "Created",
		Data:   txn,
	})
}

func (ctrl *paymentController) getPayment(c fiber.Ctx) error {
	tenantID := mustParseIntHeader(c, "X-Tenant-ID")
	if tenantID == 0 {
		return c.Status(http.StatusBadRequest).JSON(common.Response{
			Status: "Bad Request", Error: "X-Tenant-ID header is required",
		})
	}

	txnID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return c.Status(http.StatusBadRequest).JSON(common.Response{
			Status: "Bad Request", Error: "invalid transaction id",
		})
	}

	txn, err := ctrl.svc.GetPayment(c.Context(), tenantID, txnID)
	if err != nil {
		var nf domain.ErrNotFound
		if ok := isErrNotFound(err, &nf); ok {
			return c.Status(http.StatusNotFound).JSON(common.Response{
				Status: "Not Found", Error: nf.Error(),
			})
		}
		return c.Status(http.StatusInternalServerError).JSON(common.Response{
			Status: "Internal Server Error", Error: err.Error(),
		})
	}

	return c.Status(http.StatusOK).JSON(common.Response{Status: "OK", Data: txn})
}

func (ctrl *paymentController) createManualPayment(c fiber.Ctx) error {
	tenantID := mustParseIntHeader(c, "X-Tenant-ID")
	if tenantID == 0 {
		return c.Status(http.StatusBadRequest).JSON(common.Response{
			Status: "Bad Request", Error: "X-Tenant-ID header is required",
		})
	}

	var body struct {
		OrderNumber    string         `json:"order_number"`
		IdempotencyKey string         `json:"idempotency_key"`
		Amount         int64          `json:"amount"`
		Currency       string         `json:"currency"`
		PlatformFee    int64          `json:"platform_fee"`
		PaymentMethod  string         `json:"payment_method"`
		PaymentChannel string         `json:"payment_channel"`
		Description    string         `json:"description"`
		Metadata       map[string]any `json:"metadata"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(http.StatusBadRequest).JSON(common.Response{
			Status: "Bad Request", Error: err.Error(),
		})
	}

	if body.OrderNumber == "" {
		return c.Status(http.StatusBadRequest).JSON(common.Response{
			Status: "Bad Request", Error: "order_number is required",
		})
	}
	if body.Amount <= 0 {
		return c.Status(http.StatusBadRequest).JSON(common.Response{
			Status: "Bad Request", Error: "amount must be greater than 0",
		})
	}
	if body.Currency == "" {
		return c.Status(http.StatusBadRequest).JSON(common.Response{
			Status: "Bad Request", Error: "currency is required",
		})
	}

	txn, err := ctrl.svc.CreateManualPayment(c.Context(), paymentservice.CreateManualPaymentRequest{
		TenantID:       tenantID,
		OrderNumber:    body.OrderNumber,
		IdempotencyKey: body.IdempotencyKey,
		Amount:         body.Amount,
		Currency:       body.Currency,
		PlatformFee:    body.PlatformFee,
		PaymentMethod:  body.PaymentMethod,
		PaymentChannel: body.PaymentChannel,
		Description:    body.Description,
		Metadata:       body.Metadata,
	})
	if err != nil {
		if err == domain.ErrDuplicateIdempotencyKey {
			return c.Status(http.StatusConflict).JSON(common.Response{
				Status: "Conflict", Error: "a transaction with this idempotency_key already exists",
			})
		}
		ctrl.logger.WithError(err).Error("create manual payment failed")
		return c.Status(http.StatusInternalServerError).JSON(common.Response{
			Status: "Internal Server Error", Error: err.Error(),
		})
	}

	return c.Status(http.StatusCreated).JSON(common.Response{
		Status: "Created",
		Data:   txn,
	})
}

func (ctrl *paymentController) listTransactions(c fiber.Ctx) error {
	tenantID := mustParseIntHeader(c, "X-Tenant-ID")
	if tenantID == 0 {
		return c.Status(http.StatusBadRequest).JSON(common.Response{
			Status: "Bad Request", Error: "X-Tenant-ID header is required",
		})
	}

	limit, _ := strconv.Atoi(c.Query("limit", "20"))
	cursor := c.Query("cursor")

	// Accept comma-separated statuses: ?status=paid,settled
	var statuses []domain.PaymentStatus
	if s := c.Query("status"); s != "" {
		for _, part := range strings.Split(s, ",") {
			if part = strings.TrimSpace(part); part != "" {
				statuses = append(statuses, domain.PaymentStatus(part))
			}
		}
	}

	result, err := ctrl.svc.ListTransactions(c.Context(), paymentservice.ListTransactionsRequest{
		TenantID: tenantID,
		Limit:    limit,
		Cursor:   cursor,
		Status:   statuses,
	})
	if err != nil {
		if errors.Is(err, paymentservice.ErrInvalidCursor) {
			return c.Status(http.StatusBadRequest).JSON(common.Response{
				Status: "Bad Request", Error: err.Error(),
			})
		}
		ctrl.logger.WithError(err).Error("list transactions failed")
		return c.Status(http.StatusInternalServerError).JSON(common.Response{
			Status: "Internal Server Error", Error: err.Error(),
		})
	}

	return c.Status(http.StatusOK).JSON(common.Response{
		Status: "OK",
		Data: map[string]any{
			"items":       result.Items,
			"next_cursor": result.NextCursor,
			"has_more":    result.HasMore,
		},
	})
}

func (ctrl *paymentController) replayWebhook(c fiber.Ctx) error {
	eventID, err := uuid.Parse(c.Params("event_id"))
	if err != nil {
		return c.Status(http.StatusBadRequest).JSON(common.Response{
			Status: "Bad Request", Error: "invalid event_id",
		})
	}

	var body struct {
		Force bool `json:"force"`
	}
	_ = c.Bind().JSON(&body)

	if err := ctrl.replaySvc.ReplayEvent(c.Context(), eventID, body.Force); err != nil {
		if err == domain.ErrEventAlreadyProcessed {
			return c.Status(http.StatusConflict).JSON(common.Response{
				Status: "Conflict",
				Error:  err.Error(),
			})
		}
		return c.Status(http.StatusInternalServerError).JSON(common.Response{
			Status: "Internal Server Error", Error: err.Error(),
		})
	}

	return c.Status(http.StatusAccepted).JSON(common.Response{
		Status: "Accepted",
		Data:   map[string]string{"webhook_event_id": eventID.String()},
	})
}

// ─── helpers ──────────────────────────────────────────────────────────────────

// mustParseIntHeader parses a request header as int64. Returns 0 on missing/invalid value.
func mustParseIntHeader(c fiber.Ctx, header string) int64 {
	v, _ := strconv.ParseInt(c.Get(header), 10, 64)
	return v
}

func isErrNotFound(err error, target *domain.ErrNotFound) bool {
	if err == nil {
		return false
	}
	nf, ok := err.(domain.ErrNotFound)
	if ok {
		*target = nf
	}
	return ok
}

// isAccountEvent returns true for Xendit account/KYC lifecycle events that should
// be routed to the XenPlatform account handler. Payment events (payment_session.*,
// payment.*) return false even though they also carry a business_id in the envelope.
func isAccountEvent(event string) bool {
	if len(event) == 0 {
		return false
	}
	// Account and KYC events from Xendit XenPlatform callbacks.
	prefixes := []string{"account.", "kyc.", "verification."}
	for _, p := range prefixes {
		if len(event) >= len(p) && event[:len(p)] == p {
			return true
		}
	}
	return false
}
