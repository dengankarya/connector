package account

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/dengankarya/connector/common"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

// Force Swagger to include these types in the generated schema.
var (
	_ ActivityItem
	_ ActivityDetail
	_ ShippingBalance
	_ ShippingTopup
	_ ShippingHold
	_ MerchantPaymentBalance
	_ UnifiedBalance
)

// RegisterHandlers mounts all account endpoints on mux.
// adminOnly is applied to operator-only mutations (topup); pass adminRequest(cfg) from main.
//
//	GET  /accounts/transactions          unified activity feed
//	GET  /accounts/transactions/:id      detail with ledger entries
//	GET  /accounts/balance               unified balance (shipping + payment gateway)
//	POST /accounts/balance/topup         credit available balance  [admin only]
//	GET  /accounts/balance/payments      transaction-derived payment settlement balance
//	GET  /accounts/holds                 list holds
//	POST /accounts/holds                 create a hold for a draft order
//	POST /accounts/holds/:id/confirm     confirm shipment, disburse hold
//	POST /accounts/holds/:id/release     cancel order, return hold to available
func RegisterHandlers(mux fiber.Router, svc *Service, adminOnly fiber.Handler) {
	ctrl := &controller{svc: svc}

	mux.Get("/transactions", ctrl.listTransactions)
	mux.Get("/transactions/:id", ctrl.getTransaction)
	mux.Get("/balance", ctrl.getBalance)
	mux.Post("/balance/topup", adminOnly, ctrl.topup)
	mux.Get("/balance/payments", ctrl.getPaymentBalance)
	mux.Get("/holds", ctrl.listHolds)
	mux.Post("/holds", ctrl.createHold)
	mux.Post("/holds/:id/confirm", ctrl.confirmHold)
	mux.Post("/holds/:id/release", ctrl.releaseHold)
}

type controller struct {
	svc *Service
}

// ─── Transactions ─────────────────────────────────────────────────────────────

// listTransactions godoc
//
//	@Summary		List account transactions
//	@Description	Returns a cursor-paginated unified activity feed for the tenant, sorted newest first. Supports filtering by type and date range.
//	@Tags			Account
//	@Produce		json
//	@Param			X-Tenant-ID	header		int64																	true	"Tenant ID"
//	@Param			cursor		query		string																	false	"Pagination cursor from previous response"
//	@Param			limit		query		int																		false	"Page size (default 20, max 100)"
//	@Param			type		query		string																	false	"Comma-separated activity types to include"	Enums(payment, balance_topup, shipment_hold, shipment_confirmed, shipment_released)
//	@Param			from		query		string																	false	"Start of date range (RFC3339, inclusive)"
//	@Param			to			query		string																	false	"End of date range (RFC3339, exclusive)"
//	@Success		200			{object}	common.Response{data=common.PaginationResponse[account.ActivityItem]}	"Paginated activity feed"
//	@Failure		400			{object}	common.Response															"Invalid request or cursor"
//	@Failure		500			{object}	common.Response															"Internal server error"
//	@Security		ApiKeyAuth
//	@Router			/accounts/transactions [get]
func (ctrl *controller) listTransactions(c fiber.Ctx) error {
	tenantID := mustParseTenantID(c)
	if tenantID == 0 {
		return badRequest(c, "X-Tenant-ID header is required")
	}

	filter := TransactionFilter{
		Cursor: c.Query("cursor"),
	}

	if limitStr := c.Query("limit"); limitStr != "" {
		n, err := strconv.Atoi(limitStr)
		if err != nil || n <= 0 {
			return badRequest(c, "limit must be a positive integer")
		}
		filter.Limit = n
	}

	if typeStr := c.Query("type"); typeStr != "" {
		for t := range strings.SplitSeq(typeStr, ",") {
			filter.Types = append(filter.Types, ActivityType(strings.TrimSpace(t)))
		}
	}

	if fromStr := c.Query("from"); fromStr != "" {
		t, err := time.Parse(time.RFC3339, fromStr)
		if err != nil {
			return badRequest(c, "from must be RFC3339 (e.g. 2024-01-01T00:00:00Z)")
		}
		filter.From = &t
	}

	if toStr := c.Query("to"); toStr != "" {
		t, err := time.Parse(time.RFC3339, toStr)
		if err != nil {
			return badRequest(c, "to must be RFC3339 (e.g. 2024-12-31T23:59:59Z)")
		}
		filter.To = &t
	}

	result, err := ctrl.svc.ListTransactions(c.Context(), tenantID, filter)
	if err != nil {
		if errors.Is(err, ErrInvalidCursor) {
			return badRequest(c, "invalid cursor")
		}
		return internalError(c, err)
	}
	return c.JSON(common.Response{Status: "OK", Data: result})
}

// getTransaction godoc
//
//	@Summary		Get transaction detail
//	@Description	Returns the full detail for a single activity item. For payment type items, metadata and ledger entries are also included. The `type` query param is required to identify the source table.
//	@Tags			Account
//	@Produce		json
//	@Param			X-Tenant-ID	header		int64											true	"Tenant ID"
//	@Param			id			path		string											true	"Transaction UUID"
//	@Param			type		query		string											true	"Activity type"	Enums(payment, balance_topup, shipment_hold, shipment_confirmed, shipment_released)
//	@Success		200			{object}	common.Response{data=account.ActivityDetail}	"Transaction detail"
//	@Failure		400			{object}	common.Response									"Invalid ID or missing type"
//	@Failure		404			{object}	common.Response									"Transaction not found"
//	@Failure		500			{object}	common.Response									"Internal server error"
//	@Security		ApiKeyAuth
//	@Router			/accounts/transactions/{id} [get]
func (ctrl *controller) getTransaction(c fiber.Ctx) error {
	tenantID := mustParseTenantID(c)
	if tenantID == 0 {
		return badRequest(c, "X-Tenant-ID header is required")
	}

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return badRequest(c, "invalid transaction id")
	}

	activityType := ActivityType(c.Query("type"))
	if activityType == "" {
		return badRequest(c, "type query param required: payment | balance_topup | shipment_hold | shipment_confirmed | shipment_released")
	}

	detail, err := ctrl.svc.GetTransaction(c.Context(), tenantID, id, activityType)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return c.Status(http.StatusNotFound).JSON(common.Response{
				Status: "Not Found", Error: "transaction not found",
			})
		}
		return internalError(c, err)
	}
	return c.JSON(common.Response{Status: "OK", Data: detail})
}

// ─── Balance ──────────────────────────────────────────────────────────────────

// getPaymentBalance godoc
//
//	@Summary		Get payment settlement balance
//	@Description	Returns transaction-derived settled/pending/paid-out balance. Settled = SUM(merchant_amount) where status='settled'; PendingSettlement = status='paid'; PaidOut = completed payouts. AvailableToPayout = Settled - PaidOut.
//	@Tags			Account
//	@Produce		json
//	@Param			X-Tenant-ID	header		int64													true	"Tenant ID"
//	@Success		200			{object}	common.Response{data=account.MerchantPaymentBalance}	"Payment settlement balance"
//	@Failure		400			{object}	common.Response											"Missing X-Tenant-ID"
//	@Failure		500			{object}	common.Response											"Internal server error"
//	@Security		ApiKeyAuth
//	@Router			/accounts/balance/payments [get]
func (ctrl *controller) getPaymentBalance(c fiber.Ctx) error {
	tenantID := mustParseTenantID(c)
	if tenantID == 0 {
		return badRequest(c, "X-Tenant-ID header is required")
	}
	bal, err := ctrl.svc.GetPaymentBalance(c.Context(), tenantID)
	if err != nil {
		return internalError(c, err)
	}
	return c.JSON(common.Response{Status: "OK", Data: bal})
}

// getBalance godoc
//
//	@Summary		Get unified balance
//	@Description	Returns the merchant's combined balance: shipping wallet (available/on-hold) and payment settlement balance (settled/pending/paid-out). Both are computed from local DB — no external API call.
//	@Tags			Account
//	@Produce		json
//	@Param			X-Tenant-ID	header		int64											true	"Tenant ID"
//	@Success		200			{object}	common.Response{data=account.UnifiedBalance}	"Unified balance"
//	@Failure		400			{object}	common.Response									"Missing X-Tenant-ID"
//	@Failure		500			{object}	common.Response									"Internal server error"
//	@Security		ApiKeyAuth
//	@Router			/accounts/balance [get]
func (ctrl *controller) getBalance(c fiber.Ctx) error {
	tenantID := mustParseTenantID(c)
	if tenantID == 0 {
		return badRequest(c, "X-Tenant-ID header is required")
	}
	bal, err := ctrl.svc.GetUnifiedBalance(c.Context(), tenantID)
	if err != nil {
		return internalError(c, err)
	}
	return c.JSON(common.Response{Status: "OK", Data: bal})
}

// topup godoc
//
//	@Summary		Top up shipping balance (admin only)
//	@Description	Credits the merchant's available shipping balance. Restricted to admin API keys (ADMIN_API_KEYS). Called by the platform operator after the merchant has manually transferred funds.
//	@Tags			Account
//	@Accept			json
//	@Produce		json
//	@Param			X-Tenant-ID	header		int64										true	"Tenant ID"
//	@Param			body		body		account.TopupBody							true	"Top-up request"
//	@Success		201			{object}	common.Response{data=account.ShippingTopup}	"Top-up recorded"
//	@Failure		400			{object}	common.Response								"Invalid request"
//	@Failure		403			{object}	common.Response								"Forbidden — admin API key required"
//	@Failure		500			{object}	common.Response								"Internal server error"
//	@Security		ApiKeyAuth
//	@Security		AdminApiKeyAuth
//	@Router			/accounts/balance/topup [post]
func (ctrl *controller) topup(c fiber.Ctx) error {
	tenantID := mustParseTenantID(c)
	if tenantID == 0 {
		return badRequest(c, "X-Tenant-ID header is required")
	}

	var body TopupBody
	if err := c.Bind().JSON(&body); err != nil {
		return badRequest(c, err.Error())
	}
	if body.Amount <= 0 {
		return badRequest(c, "amount must be greater than 0")
	}
	if body.Currency == "" {
		body.Currency = "IDR"
	}

	topup, err := ctrl.svc.Topup(c.Context(), TopupRequest{
		TenantID: tenantID,
		Amount:   body.Amount,
		Currency: body.Currency,
		Note:     body.Note,
	})
	if err != nil {
		return internalError(c, err)
	}
	return c.Status(http.StatusCreated).JSON(common.Response{Status: "Created", Data: topup})
}

// ─── Holds ────────────────────────────────────────────────────────────────────

// listHolds godoc
//
//	@Summary		List shipment holds
//	@Description	Returns all shipment holds for the tenant, newest first.
//	@Tags			Account
//	@Produce		json
//	@Param			X-Tenant-ID	header		int64											true	"Tenant ID"
//	@Success		200			{object}	common.Response{data=[]account.ShippingHold}	"Hold list"
//	@Failure		400			{object}	common.Response									"Missing X-Tenant-ID"
//	@Failure		500			{object}	common.Response									"Internal server error"
//	@Security		ApiKeyAuth
//	@Router			/accounts/holds [get]
func (ctrl *controller) listHolds(c fiber.Ctx) error {
	tenantID := mustParseTenantID(c)
	if tenantID == 0 {
		return badRequest(c, "X-Tenant-ID header is required")
	}
	holds, err := ctrl.svc.ListHolds(c.Context(), tenantID)
	if err != nil {
		return internalError(c, err)
	}
	return c.JSON(common.Response{Status: "OK", Data: holds})
}

// createHold godoc
//
//	@Summary		Create shipment hold
//	@Description	Reserves shipping funds for a draft order. Deducts from available balance and adds to on_hold. Returns 422 when available balance is insufficient, 409 when an active hold already exists for the order.
//	@Tags			Account
//	@Accept			json
//	@Produce		json
//	@Param			X-Tenant-ID	header		int64										true	"Tenant ID"
//	@Param			body		body		account.CreateHoldBody						true	"Hold request"
//	@Success		201			{object}	common.Response{data=account.ShippingHold}	"Hold created"
//	@Failure		400			{object}	common.Response								"Invalid request"
//	@Failure		409			{object}	common.Response								"Active hold already exists for this order"
//	@Failure		422			{object}	common.Response								"Insufficient shipping balance"
//	@Failure		500			{object}	common.Response								"Internal server error"
//	@Security		ApiKeyAuth
//	@Router			/accounts/holds [post]
func (ctrl *controller) createHold(c fiber.Ctx) error {
	tenantID := mustParseTenantID(c)
	if tenantID == 0 {
		return badRequest(c, "X-Tenant-ID header is required")
	}

	var body CreateHoldBody
	if err := c.Bind().JSON(&body); err != nil {
		return badRequest(c, err.Error())
	}
	if body.OrderNumber == "" {
		return badRequest(c, "order_number is required")
	}
	if body.Amount <= 0 {
		return badRequest(c, "amount must be greater than 0")
	}
	if body.Currency == "" {
		body.Currency = "IDR"
	}

	hold, err := ctrl.svc.CreateHold(c.Context(), CreateHoldRequest{
		TenantID:    tenantID,
		OrderNumber: body.OrderNumber,
		Amount:      body.Amount,
		Currency:    body.Currency,
	})
	if err != nil {
		if errors.Is(err, ErrInsufficientBalance) {
			return c.Status(http.StatusUnprocessableEntity).JSON(common.Response{
				Status: "Unprocessable Entity", Error: err.Error(),
			})
		}
		if errors.Is(err, ErrDuplicateHold) {
			return c.Status(http.StatusConflict).JSON(common.Response{
				Status: "Conflict", Error: err.Error(),
			})
		}
		return internalError(c, err)
	}
	return c.Status(http.StatusCreated).JSON(common.Response{Status: "Created", Data: hold})
}

// confirmHold godoc
//
//	@Summary		Confirm shipment hold
//	@Description	Transitions a hold from holding → confirmed. Removes the amount from on_hold (funds are considered disbursed to the shipping provider). Returns 409 if the hold is already confirmed or released.
//	@Tags			Account
//	@Produce		json
//	@Param			X-Tenant-ID	header		int64										true	"Tenant ID"
//	@Param			id			path		string										true	"Hold UUID"
//	@Success		200			{object}	common.Response{data=account.ShippingHold}	"Hold confirmed"
//	@Failure		400			{object}	common.Response								"Invalid hold ID"
//	@Failure		404			{object}	common.Response								"Hold not found"
//	@Failure		409			{object}	common.Response								"Hold already confirmed or released"
//	@Failure		500			{object}	common.Response								"Internal server error"
//	@Security		ApiKeyAuth
//	@Router			/accounts/holds/{id}/confirm [post]
func (ctrl *controller) confirmHold(c fiber.Ctx) error {
	tenantID, holdID, err := parseTenantAndHoldID(c)
	if err != nil {
		return badRequest(c, err.Error())
	}
	hold, err := ctrl.svc.ConfirmHold(c.Context(), tenantID, holdID)
	if err != nil {
		return holdActionError(c, err)
	}
	return c.JSON(common.Response{Status: "OK", Data: hold})
}

// releaseHold godoc
//
//	@Summary		Release shipment hold
//	@Description	Transitions a hold from holding → released and returns the amount to available balance. Use when an order is cancelled before shipment. Returns 409 if the hold is already confirmed or released.
//	@Tags			Account
//	@Produce		json
//	@Param			X-Tenant-ID	header		int64										true	"Tenant ID"
//	@Param			id			path		string										true	"Hold UUID"
//	@Success		200			{object}	common.Response{data=account.ShippingHold}	"Hold released, funds returned to available"
//	@Failure		400			{object}	common.Response								"Invalid hold ID"
//	@Failure		404			{object}	common.Response								"Hold not found"
//	@Failure		409			{object}	common.Response								"Hold already confirmed or released"
//	@Failure		500			{object}	common.Response								"Internal server error"
//	@Security		ApiKeyAuth
//	@Router			/accounts/holds/{id}/release [post]
func (ctrl *controller) releaseHold(c fiber.Ctx) error {
	tenantID, holdID, err := parseTenantAndHoldID(c)
	if err != nil {
		return badRequest(c, err.Error())
	}
	hold, err := ctrl.svc.ReleaseHold(c.Context(), tenantID, holdID)
	if err != nil {
		return holdActionError(c, err)
	}
	return c.JSON(common.Response{Status: "OK", Data: hold})
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func mustParseTenantID(c fiber.Ctx) int64 {
	id, _ := strconv.ParseInt(c.Get("X-Tenant-ID"), 10, 64)
	return id
}

func parseTenantAndHoldID(c fiber.Ctx) (int64, uuid.UUID, error) {
	tenantID := mustParseTenantID(c)
	if tenantID == 0 {
		return 0, uuid.Nil, errors.New("X-Tenant-ID header is required")
	}
	holdID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return 0, uuid.Nil, errors.New("invalid hold id")
	}
	return tenantID, holdID, nil
}

func badRequest(c fiber.Ctx, msg string) error {
	return c.Status(http.StatusBadRequest).JSON(common.Response{
		Status: "Bad Request", Error: msg,
	})
}

func internalError(c fiber.Ctx, err error) error {
	return c.Status(http.StatusInternalServerError).JSON(common.Response{
		Status: "Internal Server Error", Error: err.Error(),
	})
}

func holdActionError(c fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, ErrHoldNotFound):
		return c.Status(http.StatusNotFound).JSON(common.Response{
			Status: "Not Found", Error: err.Error(),
		})
	case errors.Is(err, ErrHoldAlreadyActioned):
		return c.Status(http.StatusConflict).JSON(common.Response{
			Status: "Conflict", Error: err.Error(),
		})
	default:
		return internalError(c, err)
	}
}
