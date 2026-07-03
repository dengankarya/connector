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
	_ GatewayAccount
	_ GatewayBalance
)

// RegisterHandlers mounts all account endpoints on mux.
// adminOnly is applied to operator-only mutations (topup, payout); pass adminRequest(cfg) from main.
//
//	GET  /accounts/transactions               unified activity feed
//	GET  /accounts/transactions/:id           detail with ledger entries
//	GET  /accounts/balance                    unified balance (shipping + payment + gateway)
//	POST /accounts/balance/topup              credit available balance  [admin only]
//	GET  /accounts/balance/payments           transaction-derived payment settlement balance
//	GET  /accounts/holds                      list holds
//	POST /accounts/holds                      create a hold for a draft order
//	POST /accounts/holds/:id/confirm          confirm shipment, disburse hold
//	POST /accounts/holds/:id/release          cancel order, return hold to available
//	POST /accounts/gateway/sub-account        provision Doku sub-account for tenant
//	POST /accounts/gateway/payout             send payout via Doku  [admin only]
//	POST /accounts/gateway/xendit/sub-account provision Xendit MANAGED sub-account for tenant
//	GET  /accounts/gateway/xendit/sub-account/:id fetch live Xendit account status
func RegisterHandlers(mux fiber.Router, svc *Service, xenditClient XenditGatewayClient, adminOnly fiber.Handler) {
	ctrl := &controller{svc: svc, xenditClient: xenditClient}

	mux.Get("/transactions", ctrl.listTransactions)
	mux.Get("/transactions/:id", ctrl.getTransaction)
	mux.Get("/balance", ctrl.getBalance)
	mux.Post("/balance/topup", adminOnly, ctrl.topup)
	mux.Get("/balance/payments", ctrl.getPaymentBalance)
	mux.Get("/holds", ctrl.listHolds)
	mux.Post("/holds", ctrl.createHold)
	mux.Post("/holds/:id/confirm", ctrl.confirmHold)
	mux.Post("/holds/:id/release", ctrl.releaseHold)
	mux.Post("/gateway/sub-account", ctrl.createGatewaySubAccount)
	mux.Post("/gateway/payout", adminOnly, ctrl.sendGatewayPayout)

	if xenditClient != nil {
		mux.Post("/gateway/xendit/sub-account", ctrl.createXenditSubAccount)
		mux.Get("/gateway/xendit/sub-account/:id", ctrl.getXenditAccount)
		mux.Post("/gateway/xendit/account-holder", ctrl.createXenditAccountHolder)
	}
}

type controller struct {
	svc          *Service
	xenditClient XenditGatewayClient // nil when Xendit not configured
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
//	@Description	Returns the merchant's combined balance: shipping wallet (available/on-hold), payment settlement balance (settled/pending/paid-out), and live gateway balance (available/pending) fetched from the tenant's DOKU sub-account when configured.
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

// ─── Gateway ──────────────────────────────────────────────────────────────────

// createGatewaySubAccount godoc
//
//	@Summary		Provision Doku gateway sub-account
//	@Description	Creates a Doku payment gateway sub-account for the tenant and stores the account ID. Called by Tokokarya when onboarding a new merchant. Idempotent — returns 409 if an account already exists for this tenant.
//	@Tags			Account
//	@Accept			json
//	@Produce		json
//	@Param			X-Tenant-ID	header		int64											true	"Tenant ID"
//	@Param			body		body		account.CreateGatewaySubAccountBody				true	"Sub-account details"
//	@Success		201			{object}	common.Response{data=account.GatewayAccount}	"Sub-account created"
//	@Failure		400			{object}	common.Response									"Invalid request"
//	@Failure		409			{object}	common.Response									"Gateway account already exists"
//	@Failure		503			{object}	common.Response									"Gateway not configured"
//	@Failure		500			{object}	common.Response									"Internal server error"
//	@Security		ApiKeyAuth
//	@Router			/accounts/gateway/sub-account [post]
func (ctrl *controller) createGatewaySubAccount(c fiber.Ctx) error {
	tenantID := mustParseTenantID(c)
	if tenantID == 0 {
		return badRequest(c, "X-Tenant-ID header is required")
	}

	var body CreateGatewaySubAccountBody
	if err := c.Bind().JSON(&body); err != nil {
		return badRequest(c, err.Error())
	}
	if body.Email == "" {
		return badRequest(c, "email is required")
	}
	if body.Name == "" {
		return badRequest(c, "name is required")
	}

	acct, err := ctrl.svc.CreateGatewaySubAccount(c.Context(), CreateGatewaySubAccountRequest{
		TenantID: tenantID,
		Email:    body.Email,
		Name:     body.Name,
	})
	if err != nil {
		if errors.Is(err, ErrGatewayNotConfigured) {
			return c.Status(http.StatusServiceUnavailable).JSON(common.Response{
				Status: "Service Unavailable", Error: err.Error(),
			})
		}
		if errors.Is(err, ErrGatewayAccountExists) {
			return c.Status(http.StatusConflict).JSON(common.Response{
				Status: "Conflict", Error: err.Error(),
			})
		}
		return internalError(c, err)
	}
	return c.Status(http.StatusCreated).JSON(common.Response{Status: "Created", Data: acct})
}

// sendGatewayPayout godoc
//
//	@Summary		Send payout via Doku (admin only)
//	@Description	Initiates a bank transfer payout from the tenant's Doku sub-account to the specified bank account. Restricted to admin API keys.
//	@Tags			Account
//	@Accept			json
//	@Produce		json
//	@Param			X-Tenant-ID	header		int64									true	"Tenant ID"
//	@Param			body		body		account.SendGatewayPayoutBody			true	"Payout details"
//	@Success		200			{object}	common.Response{data=map[string]string}	"Payout status"
//	@Failure		400			{object}	common.Response							"Invalid request"
//	@Failure		403			{object}	common.Response							"Forbidden — admin API key required"
//	@Failure		404			{object}	common.Response							"No gateway account for this tenant"
//	@Failure		503			{object}	common.Response							"Gateway not configured"
//	@Failure		500			{object}	common.Response							"Internal server error"
//	@Security		ApiKeyAuth
//	@Security		AdminApiKeyAuth
//	@Router			/accounts/gateway/payout [post]
func (ctrl *controller) sendGatewayPayout(c fiber.Ctx) error {
	tenantID := mustParseTenantID(c)
	if tenantID == 0 {
		return badRequest(c, "X-Tenant-ID header is required")
	}

	var body SendGatewayPayoutBody
	if err := c.Bind().JSON(&body); err != nil {
		return badRequest(c, err.Error())
	}
	if body.Amount <= 0 {
		return badRequest(c, "amount must be greater than 0")
	}
	if body.InvoiceNumber == "" {
		return badRequest(c, "invoice_number is required")
	}
	if body.BankCode == "" {
		return badRequest(c, "bank_code is required")
	}
	if body.BankAccountNumber == "" {
		return badRequest(c, "bank_account_number is required")
	}
	if body.BankAccountName == "" {
		return badRequest(c, "bank_account_name is required")
	}

	status, err := ctrl.svc.SendGatewayPayout(c.Context(), SendGatewayPayoutRequest{
		TenantID:          tenantID,
		Amount:            body.Amount,
		InvoiceNumber:     body.InvoiceNumber,
		BankCode:          body.BankCode,
		BankAccountNumber: body.BankAccountNumber,
		BankAccountName:   body.BankAccountName,
	})
	if err != nil {
		if errors.Is(err, ErrGatewayNotConfigured) {
			return c.Status(http.StatusServiceUnavailable).JSON(common.Response{
				Status: "Service Unavailable", Error: err.Error(),
			})
		}
		if errors.Is(err, ErrGatewayAccountNotFound) {
			return c.Status(http.StatusNotFound).JSON(common.Response{
				Status: "Not Found", Error: err.Error(),
			})
		}
		return internalError(c, err)
	}
	return c.JSON(common.Response{Status: "OK", Data: map[string]string{"status": status}})
}

// ─── Xendit gateway ───────────────────────────────────────────────────────────

// createXenditSubAccount godoc
//
//	@Summary		Provision Xendit MANAGED sub-account
//	@Description	Creates a Xendit MANAGED sub-account for the tenant and registers our payment webhook URL on it. The merchant receives an invitation email from Xendit to complete sign-up. Idempotent — returns 409 if an account already exists for this tenant.
//	@Tags			Account
//	@Accept			json
//	@Produce		json
//	@Param			X-Tenant-ID	header		int64											true	"Tenant ID"
//	@Param			body		body		account.CreateGatewaySubAccountBody				true	"Sub-account details"
//	@Success		201			{object}	common.Response{data=account.GatewayAccount}	"Sub-account created"
//	@Failure		400			{object}	common.Response									"Invalid request"
//	@Failure		409			{object}	common.Response									"Xendit account already exists for this tenant"
//	@Failure		503			{object}	common.Response									"Xendit not configured"
//	@Failure		500			{object}	common.Response									"Internal server error"
//	@Security		ApiKeyAuth
//	@Router			/accounts/gateway/xendit/sub-account [post]
func (ctrl *controller) createXenditSubAccount(c fiber.Ctx) error {
	tenantID := mustParseTenantID(c)
	if tenantID == 0 {
		return badRequest(c, "X-Tenant-ID header is required")
	}

	var body CreateGatewaySubAccountBody
	if err := c.Bind().JSON(&body); err != nil {
		return badRequest(c, err.Error())
	}
	if body.Email == "" {
		return badRequest(c, "email is required")
	}
	if body.Name == "" {
		return badRequest(c, "name is required")
	}

	acct, err := ctrl.svc.CreateXenditSubAccount(c.Context(), CreateXenditSubAccountRequest{
		TenantID: tenantID,
		Email:    body.Email,
		Name:     body.Name,
	})
	if err != nil {
		if errors.Is(err, ErrGatewayNotConfigured) {
			return c.Status(http.StatusServiceUnavailable).JSON(common.Response{
				Status: "Service Unavailable", Error: err.Error(),
			})
		}
		if errors.Is(err, ErrGatewayAccountExists) {
			return c.Status(http.StatusConflict).JSON(common.Response{
				Status: "Conflict", Error: err.Error(),
			})
		}
		return internalError(c, err)
	}
	return c.Status(http.StatusCreated).JSON(common.Response{Status: "Created", Data: acct})
}

// getXenditAccount godoc
//
//	@Summary		Get Xendit sub-account status
//	@Description	Fetches the live account status and public profile from Xendit by account ID. Useful for tracking merchant onboarding progress (INVITED → REGISTERED → AWAITING_DOCS → PENDING_VERIFICATION → LIVE).
//	@Tags			Account
//	@Produce		json
//	@Param			X-Tenant-ID	header		int64											true	"Tenant ID"
//	@Param			id			path		string											true	"Xendit account ID"
//	@Success		200			{object}	common.Response{data=account.XenditAccountInfo}	"Account info"
//	@Failure		400			{object}	common.Response									"Missing tenant ID"
//	@Failure		500			{object}	common.Response									"Internal server error"
//	@Security		ApiKeyAuth
//	@Router			/accounts/gateway/xendit/sub-account/{id} [get]
func (ctrl *controller) getXenditAccount(c fiber.Ctx) error {
	tenantID := mustParseTenantID(c)
	if tenantID == 0 {
		return badRequest(c, "X-Tenant-ID header is required")
	}
	accountID := c.Params("id")
	if accountID == "" {
		return badRequest(c, "account id is required")
	}

	info, err := ctrl.xenditClient.GetAccount(c.Context(), accountID)
	if err != nil {
		return internalError(c, err)
	}
	return c.JSON(common.Response{Status: "OK", Data: info})
}

// createXenditAccountHolder godoc
//
//	@Summary		Create and link Xendit account holder
//	@Description	Submits KYC business details for the tenant's Xendit sub-account and links them. This starts the verification flow (REGISTERED → AWAITING_DOCS → PENDING_VERIFICATION → LIVE). Call this after creating the sub-account.
//	@Tags			Account
//	@Accept			json
//	@Produce		json
//	@Param			X-Tenant-ID	header		int64								true	"Tenant ID"
//	@Param			body		body		account.CreateAccountHolderRequest	true	"KYC business details"
//	@Success		200			{object}	common.Response						"Account holder created and linked"
//	@Failure		400			{object}	common.Response						"Invalid request"
//	@Failure		404			{object}	common.Response						"No Xendit sub-account found for this tenant"
//	@Failure		503			{object}	common.Response						"Xendit not configured"
//	@Failure		500			{object}	common.Response						"Internal server error"
//	@Security		ApiKeyAuth
//	@Router			/accounts/gateway/xendit/account-holder [post]
func (ctrl *controller) createXenditAccountHolder(c fiber.Ctx) error {
	tenantID := mustParseTenantID(c)
	if tenantID == 0 {
		return badRequest(c, "X-Tenant-ID header is required")
	}

	var req CreateAccountHolderRequest
	if err := c.Bind().JSON(&req); err != nil {
		return badRequest(c, err.Error())
	}
	if req.BusinessDetail.Type == "" {
		return badRequest(c, "business_detail.type is required")
	}
	if req.BusinessDetail.LegalName == "" {
		return badRequest(c, "business_detail.legal_name is required")
	}
	if req.BusinessDetail.IndustryCategory == "" {
		return badRequest(c, "business_detail.industry_category is required")
	}
	if req.BusinessDetail.CountryOfOperation == "" {
		req.BusinessDetail.CountryOfOperation = "ID"
	}
	if req.Email == "" {
		return badRequest(c, "email is required")
	}
	if req.PhoneNumber == "" {
		return badRequest(c, "phone_number is required")
	}
	if req.Address.StreetLine1 == "" {
		return badRequest(c, "address.street_line1 is required")
	}

	if err := ctrl.svc.CreateAndLinkAccountHolder(c.Context(), tenantID, req); err != nil {
		switch {
		case errors.Is(err, ErrGatewayNotConfigured):
			return c.Status(http.StatusServiceUnavailable).JSON(common.Response{
				Status: "Service Unavailable", Error: err.Error(),
			})
		case errors.Is(err, ErrGatewayAccountNotFound):
			return c.Status(http.StatusNotFound).JSON(common.Response{
				Status: "Not Found", Error: "no Xendit sub-account found for this tenant — create one first",
			})
		default:
			return internalError(c, err)
		}
	}
	return c.JSON(common.Response{Status: "OK", Data: map[string]string{"message": "account holder created and linked — verification flow started"}})
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
