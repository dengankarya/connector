package payment

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/dengankarya/connector/common"
	"github.com/dengankarya/connector/internal/payment/domain"
	"github.com/dengankarya/connector/internal/payment/jobs"
	"github.com/dengankarya/connector/internal/payment/provider"
	"github.com/dengankarya/connector/internal/payment/repository"
	paymentservice "github.com/dengankarya/connector/internal/payment/service"
	"github.com/dengankarya/connector/internal/payment/webhook"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/sirupsen/logrus"
)

// ─── Xendit webhook controller ────────────────────────────────────────────────

type controller struct {
	webhookToken     string
	enqueuer         *asynq.Client
	paymentProcessor *webhook.Processor
	xenditProvider   provider.PaymentProvider
	requestLogRepo   *repository.WebhookRequestLogRepository
}

// handleWebhook godoc
//
//	@Summary		Xendit webhook
//	@Description	Receives Xendit payment callback events. Validates the x-callback-token header, stores the raw event, and enqueues async processing. Always returns HTTP 200 to prevent Xendit retries on errors.
//	@Tags			Webhooks
//	@Accept			json
//	@Produce		json
//	@Param			x-callback-token	header		string			true	"Xendit callback token"
//	@Param			body				body		object			true	"Xendit webhook event payload"
//	@Success		200					{object}	common.Response	"OK"
//	@Router			/webhook/xendit [post]
//
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

// ─── Payment module controller ───────────────────────────────────────────────

type paymentController struct {
	svc       *paymentservice.PaymentService
	replaySvc *webhook.ReplayService
	enqueuer  *asynq.Client
	logger    *logrus.Logger
}

// RegisterPaymentHandlers registers the payment transaction and webhook management endpoints.
func RegisterPaymentHandlers(
	mux fiber.Router,
	svc *paymentservice.PaymentService,
	replaySvc *webhook.ReplayService,
	enqueuer *asynq.Client,
	logger *logrus.Logger,
) {
	ctrl := &paymentController{svc: svc, replaySvc: replaySvc, enqueuer: enqueuer, logger: logger}
	mux.Get("/transactions", ctrl.listTransactions)

	mux.Post("/", ctrl.createPayment)
	mux.Post("/cancel-schedule", ctrl.scheduleOrderCancellation)
	mux.Post("/manual", ctrl.createManualPayment)
	mux.Post("/manual/:id/confirm", ctrl.confirmManualPayment)
	mux.Get("/:id", ctrl.getPayment)
	mux.Post("/:id/refresh", ctrl.refreshPayment)
	mux.Post("/webhooks/:event_id/replay", ctrl.replayWebhook)
}

// RegisterWebhookHandlerV2 registers the Xendit webhook endpoint on the public router.
func RegisterWebhookHandlerV2(
	mux fiber.Router,
	webhookToken string,
	enqueuer *asynq.Client,
	processor *webhook.Processor,
	prov provider.PaymentProvider,
	requestLogRepo *repository.WebhookRequestLogRepository,
) {
	ctrl := controller{
		webhookToken:     webhookToken,
		enqueuer:         enqueuer,
		paymentProcessor: processor,
		xenditProvider:   prov,
		requestLogRepo:   requestLogRepo,
	}
	mux.Post("/webhook/xendit", ctrl.handleWebhook)
}

// createPayment godoc
//
//	@Summary		Create payment session
//	@Description	Creates a new Xendit payment session (invoice) for a tenant order. Returns a checkout_url the customer visits to complete payment.
//	@Tags			Payments
//	@Accept			json
//	@Produce		json
//	@Param			X-Tenant-ID	header		int64											true	"Tenant ID"
//	@Param			body		body		CreatePaymentBody								true	"Payment creation request"
//	@Success		201			{object}	common.Response{data=domain.PaymentTransaction}	"Payment session created"
//	@Failure		400			{object}	common.Response									"Invalid request"
//	@Failure		500			{object}	common.Response									"Internal server error"
//	@Security		ApiKeyAuth
//	@Router			/payments [post]
func (ctrl *paymentController) createPayment(c fiber.Ctx) error {
	tenantID := mustParseIntHeader(c, "X-Tenant-ID")
	if tenantID == 0 {
		return c.Status(http.StatusBadRequest).JSON(common.Response{
			Status: "Bad Request",
			Error:  "X-Tenant-ID header is required",
		})
	}

	var body struct {
		OrderNumber            string            `json:"order_number"`
		IdempotencyKey         string            `json:"idempotency_key"`
		Amount                 int64             `json:"amount"`
		Currency               string            `json:"currency"`
		PlatformFee            int64             `json:"platform_fee"`
		ShippingFee            int64             `json:"shipping_fee"`
		AllowedPaymentChannels []string          `json:"allowed_payment_channels"`
		SuccessReturnURL       string            `json:"success_return_url"`
		CancelReturnURL        string            `json:"cancel_return_url"`
		Description            string            `json:"description"`
		CustomerEmail          string            `json:"customer_email"`
		CustomerName           string            `json:"customer_name"`
		CustomerReferenceID    string            `json:"customer_reference_id"`
		Metadata               map[string]string `json:"metadata"`
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
		OrderNumber:            body.OrderNumber,
		IdempotencyKey:         body.IdempotencyKey,
		Amount:                 body.Amount,
		Currency:               body.Currency,
		PlatformFee:            body.PlatformFee,
		ShippingFee:            body.ShippingFee,
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

// getPayment godoc
//
//	@Summary		Get payment transaction
//	@Description	Retrieves a payment transaction by its UUID for the authenticated tenant.
//	@Tags			Payments
//	@Produce		json
//	@Param			X-Tenant-ID	header		int64											true	"Tenant ID"
//	@Param			id			path		string											true	"Transaction UUID"
//	@Success		200			{object}	common.Response{data=domain.PaymentTransaction}	"Transaction details"
//	@Failure		400			{object}	common.Response									"Invalid transaction ID"
//	@Failure		404			{object}	common.Response									"Transaction not found"
//	@Failure		500			{object}	common.Response									"Internal server error"
//	@Security		ApiKeyAuth
//	@Router			/payments/{id} [get]
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

// createManualPayment godoc
//
//	@Summary		Create manual payment
//	@Description	Records a manual (off-platform) payment without calling Xendit. Returns a provider_invoice_id prefixed with "manual-" that Tokokarya uses as the payment_session_id.
//	@Tags			Payments
//	@Accept			json
//	@Produce		json
//	@Param			X-Tenant-ID	header		int64											true	"Tenant ID"
//	@Param			body		body		CreateManualPaymentBody							true	"Manual payment request"
//	@Success		201			{object}	common.Response{data=domain.PaymentTransaction}	"Manual payment created"
//	@Failure		400			{object}	common.Response									"Invalid request"
//	@Failure		409			{object}	common.Response									"Duplicate idempotency key"
//	@Failure		500			{object}	common.Response									"Internal server error"
//	@Security		ApiKeyAuth
//	@Router			/payments/manual [post]
func (ctrl *paymentController) createManualPayment(c fiber.Ctx) error {
	tenantID := mustParseIntHeader(c, "X-Tenant-ID")
	if tenantID == 0 {
		return c.Status(http.StatusBadRequest).JSON(common.Response{
			Status: "Bad Request", Error: "X-Tenant-ID header is required",
		})
	}

	var body CreateManualPaymentBody
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(http.StatusBadRequest).JSON(common.Response{
			Status: "Bad Request", Error: err.Error(),
			Message: "failed to decode response body",
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
		ShippingFee:    body.ShippingFee,
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

// confirmManualPayment godoc
//
//	@Summary		Confirm manual payment
//	@Description	Marks a manual payment as paid, writes ledger entries, and returns the updated transaction. Idempotent — safe to call again if already confirmed.
//	@Tags			Payments
//	@Accept			json
//	@Produce		json
//	@Param			X-Tenant-ID	header		int64											true	"Tenant ID"
//	@Param			id			path		string											true	"Transaction UUID"
//	@Param			body		body		ConfirmManualPaymentBody						false	"Optional payment channel override"
//	@Success		200			{object}	common.Response{data=domain.PaymentTransaction}	"Payment confirmed"
//	@Failure		400			{object}	common.Response									"Invalid transaction ID or not a manual payment"
//	@Failure		404			{object}	common.Response									"Transaction not found"
//	@Failure		500			{object}	common.Response									"Internal server error"
//	@Security		ApiKeyAuth
//	@Router			/payments/manual/{id}/confirm [post]
func (ctrl *paymentController) confirmManualPayment(c fiber.Ctx) error {
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

	var body ConfirmManualPaymentBody
	_ = c.Bind().JSON(&body) // body is optional

	txn, err := ctrl.svc.ConfirmManualPayment(c.Context(), paymentservice.ConfirmManualPaymentRequest{
		TenantID:       tenantID,
		TransactionID:  txnID,
		PaymentChannel: body.PaymentChannel,
	})
	if err != nil {
		var nf domain.ErrNotFound
		if ok := isErrNotFound(err, &nf); ok {
			return c.Status(http.StatusNotFound).JSON(common.Response{
				Status: "Not Found", Error: nf.Error(),
			})
		}
		ctrl.logger.WithError(err).Error("confirm manual payment failed")
		return c.Status(http.StatusInternalServerError).JSON(common.Response{
			Status: "Internal Server Error", Error: err.Error(),
		})
	}

	return c.Status(http.StatusOK).JSON(common.Response{Status: "OK", Data: txn})
}

// scheduleOrderCancellation godoc
//
//	@Summary		Schedule order cancellation
//	@Description	Schedules a one-shot job that calls Tokokarya's cancel-expired-orders endpoint at the given Unix timestamp. Used by the frontend after a manual payment is created to trigger automatic order cancellation if payment is not confirmed.
//	@Tags			Payments
//	@Accept			json
//	@Produce		json
//	@Param			X-Tenant-ID	header		int64							true	"Tenant ID"
//	@Param			body		body		ScheduleOrderCancellationBody	true	"Cancellation schedule request"
//	@Success		202			{object}	common.Response					"Job scheduled"
//	@Failure		400			{object}	common.Response					"Invalid request"
//	@Failure		500			{object}	common.Response					"Internal server error"
//	@Security		ApiKeyAuth
//	@Router			/payments/cancel-schedule [post]
func (ctrl *paymentController) scheduleOrderCancellation(c fiber.Ctx) error {
	var body ScheduleOrderCancellationBody
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
	if body.ShouldExpireAt <= 0 {
		return c.Status(http.StatusBadRequest).JSON(common.Response{
			Status: "Bad Request", Error: "should_expired_at must be a valid Unix timestamp",
		})
	}

	processAt := time.Unix(body.ShouldExpireAt, 0)
	task, opts := jobs.NewCancelExpiredOrderTask(body.OrderNumber, processAt)

	if _, err := ctrl.enqueuer.EnqueueContext(c.Context(), task, opts...); err != nil {
		ctrl.logger.WithFields(logrus.Fields{
			"order_number":     body.OrderNumber,
			"should_expire_at": body.ShouldExpireAt,
			"error":            err.Error(),
		}).Error("schedule order cancellation: enqueue failed")
		return c.Status(http.StatusInternalServerError).JSON(common.Response{
			Status: "Internal Server Error", Error: "failed to schedule order cancellation",
		})
	}

	return c.Status(http.StatusAccepted).JSON(common.Response{
		Status: "Accepted",
		Data: map[string]any{
			"order_number":     body.OrderNumber,
			"should_expire_at": body.ShouldExpireAt,
		},
	})
}

// refreshPayment godoc
//
//	@Summary		Refresh payment transaction
//	@Description	Fetches the latest state of a payment session directly from Xendit and updates our records if the status changed. Safe to call multiple times. Returns immediately for manual payments or transactions already in a final state.
//	@Tags			Payments
//	@Produce		json
//	@Param			X-Tenant-ID	header		int64											true	"Tenant ID"
//	@Param			id			path		string											true	"Transaction UUID"
//	@Success		200			{object}	common.Response{data=domain.PaymentTransaction}	"Latest transaction state"
//	@Failure		400			{object}	common.Response									"Invalid transaction ID"
//	@Failure		404			{object}	common.Response									"Transaction not found"
//	@Failure		500			{object}	common.Response									"Internal server error"
//	@Security		ApiKeyAuth
//	@Router			/payments/{id}/refresh [post]
func (ctrl *paymentController) refreshPayment(c fiber.Ctx) error {
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

	txn, err := ctrl.svc.RefreshPayment(c.Context(), tenantID, txnID)
	if err != nil {
		var nf domain.ErrNotFound
		if isErrNotFound(err, &nf) {
			return c.Status(http.StatusNotFound).JSON(common.Response{
				Status: "Not Found", Error: nf.Error(),
			})
		}
		ctrl.logger.WithError(err).Error("refresh payment failed")
		return c.Status(http.StatusInternalServerError).JSON(common.Response{
			Status: "Internal Server Error", Error: err.Error(),
		})
	}

	return c.Status(http.StatusOK).JSON(common.Response{Status: "OK", Data: txn})
}

// listTransactions godoc
//
//	@Summary		List payment transactions
//	@Description	Returns a cursor-paginated list of payment transactions for the tenant. Supports filtering by status.
//	@Tags			Payments
//	@Produce		json
//	@Param			X-Tenant-ID	header		int64																		true	"Tenant ID"
//	@Param			limit		query		int																			false	"Number of results per page (default 20, max 100)"
//	@Param			cursor		query		string																		false	"Pagination cursor returned by previous response"
//	@Param			status		query		string																		false	"Comma-separated statuses to filter"	Enums(pending,awaiting_payment,paid,settled,refunding,refunded,expired,failed,voided)
//	@Param			date_from	query		string																		false	"Start date inclusive, format YYYY-MM-DD (e.g. 2026-05-01)"
//	@Param			date_to		query		string																		false	"End date inclusive, format YYYY-MM-DD (e.g. 2026-05-31)"
//	@Param			provider	query		string																		false	"Provider to filter by (e.g. 'xendit', 'manual_transfer')"
//	@Success		200			{object}	common.Response{data=common.PaginationResponse[domain.PaymentTransaction]}	"Paginated transaction list"
//	@Failure		400			{object}	common.Response																"Invalid request or cursor"
//	@Failure		500			{object}	common.Response																"Internal server error"
//	@Security		ApiKeyAuth
//	@Router			/payments/transactions [get]
func (ctrl *paymentController) listTransactions(c fiber.Ctx) error {
	tenantID := mustParseIntHeader(c, "X-Tenant-ID")
	if tenantID == 0 {
		return c.Status(http.StatusBadRequest).JSON(common.Response{
			Status: "Bad Request", Error: "X-Tenant-ID header is required",
		})
	}

	limit, _ := strconv.Atoi(c.Query("limit", "20"))
	cursor := c.Query("cursor")
	paymentProvider := c.Query("provider")
	// Optional filter by provider (e.g. "xendit", "manual_transfer")
	if paymentProvider != "" {
		// Validate provider value if necessary (e.g. against a list of known providers)
		// For now, we just pass it through to the service layer for filtering.
		if paymentProvider != "xendit" && paymentProvider != "manual_transfer" {
			return c.Status(http.StatusBadRequest).JSON(common.Response{
				Status: "Bad Request", Error: "invalid provider value",
			})
		}
	}

	// Accept comma-separated statuses: ?status=paid,settled
	var statuses []domain.PaymentStatus
	if s := c.Query("status"); s != "" {
		for part := range strings.SplitSeq(s, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			ps := domain.PaymentStatus(part)
			if !ps.IsValid() {
				return c.Status(http.StatusBadRequest).JSON(common.Response{
					Status: "Bad Request",
					Error:  "invalid status value: " + part,
				})
			}
			statuses = append(statuses, ps)
		}
	}

	// Date range: accept YYYY-MM-DD. date_from is inclusive (start of day UTC);
	// date_to is inclusive (end of day UTC, stored as start of next day).
	var dateFrom, dateTo *time.Time
	if s := c.Query("date_from"); s != "" {
		if t, err := time.Parse("2006-01-02", s); err == nil {
			t = t.UTC()
			dateFrom = &t
		}
	}
	if s := c.Query("date_to"); s != "" {
		if t, err := time.Parse("2006-01-02", s); err == nil {
			t = t.UTC().AddDate(0, 0, 1) // exclusive upper bound = start of next day
			dateTo = &t
		}
	}

	result, err := ctrl.svc.ListTransactions(c.Context(), paymentservice.ListTransactionsRequest{
		TenantID: tenantID,
		Limit:    limit,
		Cursor:   cursor,
		Status:   statuses,
		DateFrom: dateFrom,
		DateTo:   dateTo,
		Provider: paymentProvider,
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
		Data: common.PaginationResponse[*domain.PaymentTransaction]{
			Items:      result.Items,
			NextCursor: result.NextCursor,
			HasMore:    result.HasMore,
		},
	})
}

// replayWebhook godoc
//
//	@Summary		Replay webhook event
//	@Description	Re-enqueues a previously received Xendit webhook event for reprocessing. Use force=true to re-enqueue already-processed events.
//	@Tags			Payments
//	@Accept			json
//	@Produce		json
//	@Param			event_id	path		string				true	"Webhook event UUID"
//	@Param			body		body		ReplayWebhookBody	false	"Replay options"
//	@Success		202			{object}	common.Response		"Event enqueued for replay"
//	@Failure		400			{object}	common.Response		"Invalid event ID"
//	@Failure		409			{object}	common.Response		"Event already processed; use force=true to override"
//	@Failure		500			{object}	common.Response		"Internal server error"
//	@Security		ApiKeyAuth
//	@Router			/payments/webhooks/{event_id}/replay [post]
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
