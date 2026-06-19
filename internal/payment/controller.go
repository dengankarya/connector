package payment

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/dengankarya/connector/common"
	"github.com/dengankarya/connector/internal/payment/domain"
	"github.com/dengankarya/connector/internal/payment/jobs"
	paymentservice "github.com/dengankarya/connector/internal/payment/service"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/sirupsen/logrus"
)

// ─── Payment module controller ───────────────────────────────────────────────

type paymentController struct {
	svc      *paymentservice.PaymentService
	enqueuer *asynq.Client
	logger   *logrus.Logger
}

// RegisterPaymentHandlers registers the payment transaction endpoints.
func RegisterPaymentHandlers(
	mux fiber.Router,
	svc *paymentservice.PaymentService,
	enqueuer *asynq.Client,
	logger *logrus.Logger,
) {
	ctrl := &paymentController{svc: svc, enqueuer: enqueuer, logger: logger}
	mux.Get("/transactions", ctrl.listTransactions)

	mux.Post("/cancel-schedule", ctrl.scheduleOrderCancellation)
	mux.Post("/manual", ctrl.createManualPayment)
	mux.Post("/manual/:id/confirm", ctrl.confirmManualPayment)
	mux.Get("/:id", ctrl.getPayment)
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
//	@Description	Records a manual (off-platform) payment. Returns a provider_invoice_id prefixed with "manual-". Confirm via POST /payments/manual/:id/confirm.
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
//	@Param			provider	query		string																		false	"Provider to filter by (e.g. 'manual_transfer')"
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
	if paymentProvider != "" && paymentProvider != "manual_transfer" {
		return c.Status(http.StatusBadRequest).JSON(common.Response{
			Status: "Bad Request", Error: "invalid provider value",
		})
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
