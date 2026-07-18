package payment

import (
	"errors"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/dengankarya/connector/common"
	"github.com/dengankarya/connector/internal/payment/domain"
	"github.com/dengankarya/connector/internal/payment/jobs"
	"github.com/dengankarya/connector/internal/payment/provider"
	paymentservice "github.com/dengankarya/connector/internal/payment/service"
	"github.com/dengankarya/connector/internal/payment/webhook"
	"github.com/dengankarya/connector/pkg/logger"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/sirupsen/logrus"
)

// ─── Payment module controller ───────────────────────────────────────────────

type paymentController struct {
	svc              *paymentservice.PaymentService
	providers        map[string]provider.PaymentProvider // keyed by provider name (e.g. "durianpay")
	enqueuer         *asynq.Client
	webhookProcessor *webhook.Processor // optional; used by dp-sync to confirm payment + record ledger
	logger           *logger.Logger
}

// RegisterPaymentHandlers registers the payment transaction endpoints.
// providers is a map of configured payment providers; POST / is only registered when at least one is present.
// webhookProc may be nil; when set it enables the POST /:id/dp-sync endpoint to confirm payments.
func RegisterPaymentHandlers(
	mux fiber.Router,
	svc *paymentservice.PaymentService,
	providers map[string]provider.PaymentProvider,
	enqueuer *asynq.Client,
	logger *logger.Logger,
	webhookProc ...*webhook.Processor,
) {
	var wp *webhook.Processor
	if len(webhookProc) > 0 {
		wp = webhookProc[0]
	}
	ctrl := &paymentController{svc: svc, providers: providers, enqueuer: enqueuer, webhookProcessor: wp, logger: logger}

	mux.Post("/cancel-schedule", ctrl.scheduleOrderCancellation)
	mux.Post("/manual", ctrl.createManualPayment)
	mux.Post("/manual/:id/confirm", ctrl.confirmManualPayment)
	mux.Get("/:id", ctrl.getPayment)
	mux.Post("/:id/dp-sync", ctrl.syncDurianPay)

	if len(providers) > 0 {
		mux.Post("/", ctrl.createPayment)
	}
}

// createPayment godoc
//
//	@Summary		Create payment
//	@Description	Creates a payment session via the configured provider and returns the checkout URL or VA/QRIS details.
//	@Tags			Payments
//	@Accept			json
//	@Produce		json
//	@Param			X-Tenant-ID	header		int64											true	"Tenant ID"
//	@Param			body		body		CreatePaymentBody								true	"Payment request"
//	@Success		201			{object}	common.Response{data=domain.PaymentTransaction}	"Payment created"
//	@Failure		400			{object}	common.Response									"Invalid request"
//	@Failure		409			{object}	common.Response									"Duplicate idempotency key"
//	@Failure		500			{object}	common.Response									"Internal server error"
//	@Security		ApiKeyAuth
//	@Router			/payments [post]
func (ctrl *paymentController) createPayment(c fiber.Ctx) error {
	tenantID := mustParseIntHeader(c, "X-Tenant-ID")
	if tenantID == 0 {
		return c.Status(http.StatusBadRequest).JSON(common.Response{
			Status: "Bad Request", Error: "X-Tenant-ID header is required",
		})
	}

	var body CreatePaymentBody
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
	if body.CustomerName == "" {
		return c.Status(http.StatusBadRequest).JSON(common.Response{
			Status: "Bad Request", Error: "customer_name is required",
		})
	}
	if body.CustomerEmail == "" {
		return c.Status(http.StatusBadRequest).JSON(common.Response{
			Status: "Bad Request", Error: "customer_email is required",
		})
	}

	// Provider is determined by ENV config (which API keys are set at startup).
	// If exactly one provider is configured, use it. If multiple, pick the first alphabetically.
	var prov provider.PaymentProvider
	if len(ctrl.providers) == 1 {
		for _, p := range ctrl.providers {
			prov = p
		}
	} else if len(ctrl.providers) > 1 {
		names := make([]string, 0, len(ctrl.providers))
		for n := range ctrl.providers {
			names = append(names, n)
		}
		sort.Strings(names)
		prov = ctrl.providers[names[0]]
	}
	if prov == nil {
		return c.Status(http.StatusServiceUnavailable).JSON(common.Response{
			Status: "Service Unavailable", Error: "no payment provider configured",
		})
	}

	txn, err := ctrl.svc.CreateProviderPayment(c.Context(), prov, paymentservice.CreateProviderPaymentRequest{
		TenantID:           tenantID,
		OrderNumber:        body.OrderNumber,
		IdempotencyKey:     body.IdempotencyKey,
		Amount:             body.Amount,
		Currency:           body.Currency,
		PlatformFee:        body.PlatformFee,
		ShippingFee:        body.ShippingFee,
		CustomerName:       body.CustomerName,
		CustomerEmail:      body.CustomerEmail,
		CustomerMobile:     body.CustomerMobile,
		Description:        body.Description,
		SuccessReturnURL:   body.SuccessReturnURL,
		CancelReturnURL:    body.CancelReturnURL,
		ExpiresAt:          body.ExpiresAt,
		Metadata:           body.Metadata,
		PaymentType:        body.PaymentType,
		PaymentMethodTypes: body.PaymentMethodTypes,
		ChannelProperties:  body.ChannelProperties,
		ResultURL:          body.ResultURL,
	})
	if err != nil {
		if err == domain.ErrDuplicateIdempotencyKey {
			return c.Status(http.StatusConflict).JSON(common.Response{
				Status: "Conflict", Error: "a transaction with this idempotency_key already exists",
			})
		}
		ctrl.logger.WithError(c.Context(), err).Error("create payment failed")
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
		ctrl.logger.WithError(c.Context(), err).Error("create manual payment failed")
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
		ctrl.logger.WithError(c.Context(), err).Error("confirm manual payment failed")
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
		ctrl.logger.WithFields(c.Context(), logrus.Fields{
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

// syncDurianPay godoc
//
//	@Summary		Sync DurianPay payment status
//	@Description	Fetches the live payment status from DurianPay for the given transaction and returns both the connector DB status and the live DurianPay status. Use this as a fallback when a webhook delivery is delayed — the caller can act on dp_status="paid" even before the connector processes the webhook.
//	@Tags			Payments
//	@Produce		json
//	@Param			X-Tenant-ID	header		int64			true	"Tenant ID"
//	@Param			id			path		string			true	"Transaction UUID"
//	@Success		200			{object}	common.Response	"Status response"
//	@Failure		400			{object}	common.Response	"Invalid request"
//	@Failure		404			{object}	common.Response	"Transaction not found"
//	@Failure		503			{object}	common.Response	"DurianPay provider not configured"
//	@Security		ApiKeyAuth
//	@Router			/payments/{id}/dp-sync [post]
func (ctrl *paymentController) syncDurianPay(c fiber.Ctx) error {
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

	// Only DurianPay transactions can be synced this way.
	if txn.Provider != "durianpay" {
		return c.Status(http.StatusBadRequest).JSON(common.Response{
			Status: "Bad Request", Error: "transaction is not a DurianPay payment",
		})
	}

	prov, ok := ctrl.providers["durianpay"]
	if !ok {
		return c.Status(http.StatusServiceUnavailable).JSON(common.Response{
			Status: "Service Unavailable", Error: "DurianPay provider not configured",
		})
	}

	inv, err := prov.GetInvoice(c.Context(), txn.ProviderInvoiceID)
	if err != nil {
		ctrl.logger.WithError(c.Context(), err).WithField("provider_invoice_id", txn.ProviderInvoiceID).
			Error("dp-sync: GetInvoice failed")
		return c.Status(http.StatusInternalServerError).JSON(common.Response{
			Status: "Internal Server Error", Error: "failed to fetch payment status from DurianPay",
		})
	}

	// If DurianPay says paid but the connector DB hasn't been updated yet (webhook delay),
	// confirm the payment now: transition status, record ledger entries, notify Tokokarya.
	if inv.Status == "paid" && txn.Status == domain.StatusAwaitingPayment && ctrl.webhookProcessor != nil {
		updated, cerr := ctrl.webhookProcessor.ConfirmGatewayPayment(c.Context(), tenantID, txn.ID, inv.PaidAt)
		if cerr != nil {
			ctrl.logger.WithError(c.Context(), cerr).Error("dp-sync: ConfirmGatewayPayment failed")
			// Non-fatal: return the dp status so the frontend can still react.
		} else if updated != nil {
			txn = updated
		}
	}

	return c.Status(http.StatusOK).JSON(common.Response{
		Status: "OK",
		Data: map[string]any{
			"connector_status": txn.Status,
			"dp_status":        inv.Status, // "paid" | "awaiting_payment" | "expired" | "failed"
			"paid_at":          inv.PaidAt,
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
	return errors.As(err, target)
}
