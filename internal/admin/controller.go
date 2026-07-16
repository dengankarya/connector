package admin

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/dengankarya/connector/common"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

type controller struct {
	svc *Service
}

// RegisterAuthHandlers mounts unauthenticated admin auth endpoints.
//
//	POST /admin/auth/login
func RegisterAuthHandlers(mux fiber.Router, svc *Service) {
	ctrl := &controller{svc: svc}
	mux.Post("/login", ctrl.login)
}

// RegisterHandlers mounts JWT-protected admin endpoints.
//
//	PUT    /admin/me/password
//	GET    /admin/transactions
//	GET    /admin/payouts
//	POST   /admin/merchants/:tenantId/topup
//	GET    /admin/roles
//	POST   /admin/roles
//	PUT    /admin/roles/:id
//	DELETE /admin/roles/:id
//	GET    /admin/users
//	POST   /admin/users
//	PUT    /admin/users/:id
//	DELETE /admin/users/:id
func RegisterHandlers(mux fiber.Router, svc *Service) {
	ctrl := &controller{svc: svc}

	// ── Self-service — any authenticated admin ─────────────────────────────────
	mux.Put("/me/password", ctrl.changePassword)

	// ── Cross-tenant data — permission-guarded ────────────────────────────────
	mux.Get("/transactions", RequirePermission("transactions", "READ"), ctrl.listTransactions)
	mux.Get("/payouts", RequirePermission("payouts", "READ"), ctrl.listPayouts)
	mux.Post("/merchants/:tenantId/topup", RequirePermission("merchants", "WRITE"), ctrl.topup)

	// ── Role management — super admin only ────────────────────────────────────
	mux.Get("/roles", ctrl.listRoles)
	mux.Post("/roles", ctrl.createRole)
	mux.Put("/roles/:id", ctrl.updateRole)
	mux.Delete("/roles/:id", ctrl.deleteRole)

	// ── User management — super admin only ────────────────────────────────────
	mux.Get("/users", ctrl.listUsers)
	mux.Post("/users", ctrl.createUser)
	mux.Put("/users/:id", ctrl.updateUser)
	mux.Delete("/users/:id", ctrl.deleteUser)
}

// login godoc
//
//	@Summary		Platform admin login
//	@Description	Validates email + password and returns a signed JWT (24-hour expiry).
//	@Tags			Admin
//	@Accept			json
//	@Produce		json
//	@Param			body	body		admin.LoginBody								true	"Credentials"
//	@Success		200		{object}	common.Response{data=admin.LoginResponse}	"JWT token"
//	@Failure		400		{object}	common.Response								"Missing or malformed body"
//	@Failure		401		{object}	common.Response								"Invalid credentials"
//	@Router			/admin/auth/login [post]
func (ctrl *controller) login(c fiber.Ctx) error {
	var body LoginBody
	if err := c.Bind().JSON(&body); err != nil {
		return badRequest(c, "invalid request body")
	}
	if body.Email == "" || body.Password == "" {
		return badRequest(c, "email and password are required")
	}

	resp, err := ctrl.svc.Login(c.Context(), body.Email, body.Password)
	if err != nil {
		if isDomainErr(err, "AU_") {
			return c.Status(http.StatusUnauthorized).JSON(common.Response{
				Status: "Unauthorized", Error: err.Error(),
			})
		}
		return internalError(c, err)
	}
	return c.JSON(common.Response{Status: "OK", Data: resp})
}

// listTransactions godoc
//
//	@Summary		List all transactions (admin)
//	@Description	Returns cross-tenant transactions, newest first. Filter by tenant_id, date range, and cursor pagination.
//	@Tags			Admin
//	@Produce		json
//	@Param			tenant_id	query		int64																	false	"Filter by tenant ID"
//	@Param			date_from	query		string																	false	"Start of date range (RFC3339)"
//	@Param			date_to		query		string																	false	"End of date range (RFC3339)"
//	@Param			cursor		query		string																	false	"Pagination cursor from previous response"
//	@Param			limit		query		int																		false	"Page size (default 20, max 100)"
//	@Success		200			{object}	common.Response{data=common.PaginationResponse[admin.AdminTransaction]}	"Transaction list"
//	@Failure		400			{object}	common.Response															"Invalid query params"
//	@Failure		401			{object}	common.Response															"Unauthorized"
//	@Failure		403			{object}	common.Response															"Forbidden"
//	@Failure		500			{object}	common.Response															"Internal server error"
//	@Router			/admin/transactions [get]
func (ctrl *controller) listTransactions(c fiber.Ctx) error {
	filter := AdminTxnFilter{Cursor: c.Query("cursor")}

	if limitStr := c.Query("limit"); limitStr != "" {
		n, err := strconv.Atoi(limitStr)
		if err != nil || n <= 0 {
			return badRequest(c, "limit must be a positive integer")
		}
		filter.Limit = n
	}

	if tidStr := c.Query("tenant_id"); tidStr != "" {
		tid, err := strconv.ParseInt(tidStr, 10, 64)
		if err != nil || tid <= 0 {
			return badRequest(c, "tenant_id must be a positive integer")
		}
		filter.TenantID = &tid
	}

	if fromStr := c.Query("date_from"); fromStr != "" {
		t, err := time.Parse(time.RFC3339, fromStr)
		if err != nil {
			return badRequest(c, "date_from must be RFC3339 (e.g. 2024-01-01T00:00:00Z)")
		}
		filter.From = &t
	}

	if toStr := c.Query("date_to"); toStr != "" {
		t, err := time.Parse(time.RFC3339, toStr)
		if err != nil {
			return badRequest(c, "date_to must be RFC3339 (e.g. 2024-12-31T23:59:59Z)")
		}
		filter.To = &t
	}

	result, err := ctrl.svc.ListTransactions(c.Context(), filter)
	if err != nil {
		if isDomainErr(err, "BR_") {
			return badRequest(c, err.Error())
		}
		return internalError(c, err)
	}
	return c.JSON(common.Response{Status: "OK", Data: result})
}

// listPayouts godoc
//
//	@Summary		List all payouts (admin)
//	@Description	Returns all payout records across all tenants, newest first.
//	@Tags			Admin
//	@Produce		json
//	@Param			limit	query		int											false	"Page size (default 20, max 100)"
//	@Param			offset	query		int											false	"Offset for pagination"
//	@Success		200		{object}	common.Response{data=[]admin.AdminPayout}	"Payout list"
//	@Failure		401		{object}	common.Response								"Unauthorized"
//	@Failure		403		{object}	common.Response								"Forbidden"
//	@Failure		500		{object}	common.Response								"Internal server error"
//	@Router			/admin/payouts [get]
func (ctrl *controller) listPayouts(c fiber.Ctx) error {
	limit := 20
	offset := 0

	if s := c.Query("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n <= 0 {
			return badRequest(c, "limit must be a positive integer")
		}
		limit = n
	}
	if s := c.Query("offset"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			return badRequest(c, "offset must be a non-negative integer")
		}
		offset = n
	}

	var f AdminPayoutFilter
	if s := c.Query("tenant_id"); s != "" {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || n <= 0 {
			return badRequest(c, "tenant_id must be a positive integer")
		}
		f.TenantID = &n
	}
	if s := c.Query("status"); s != "" {
		if !ValidPayoutStatuses[s] {
			return badRequest(c, "invalid status value")
		}
		f.Status = s
	}

	payouts, err := ctrl.svc.ListPayouts(c.Context(), limit, offset, f)
	if err != nil {
		return internalError(c, err)
	}
	return c.JSON(common.Response{Status: "OK", Data: payouts})
}

// topup godoc
//
//	@Summary		Top up merchant shipping balance (admin)
//	@Description	Credits the shipping wallet of the specified merchant. Requires admin JWT + merchants:WRITE permission.
//	@Tags			Admin
//	@Accept			json
//	@Produce		json
//	@Param			tenantId	path		int64										true	"Merchant tenant ID"
//	@Param			body		body		admin.TopupBody								true	"Top-up details"
//	@Success		201			{object}	common.Response{data=admin.ShippingTopup}	"Top-up recorded"
//	@Failure		400			{object}	common.Response								"Invalid request"
//	@Failure		401			{object}	common.Response								"Unauthorized"
//	@Failure		403			{object}	common.Response								"Forbidden"
//	@Failure		500			{object}	common.Response								"Internal server error"
//	@Router			/admin/merchants/{tenantId}/topup [post]
func (ctrl *controller) topup(c fiber.Ctx) error {
	tenantID, err := strconv.ParseInt(c.Params("tenantId"), 10, 64)
	if err != nil || tenantID <= 0 {
		return badRequest(c, "tenantId must be a positive integer")
	}

	var body struct {
		Amount   int64  `json:"amount"`
		Currency string `json:"currency"`
		Note     string `json:"note"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return badRequest(c, "invalid request body")
	}
	if body.Amount <= 0 {
		return badRequest(c, "amount must be greater than 0")
	}

	topup, err := ctrl.svc.Topup(c.Context(), tenantID, body.Amount, body.Currency, body.Note)
	if err != nil {
		return internalError(c, err)
	}
	return c.Status(http.StatusCreated).JSON(common.Response{Status: "Created", Data: topup})
}

// ─── Self-service ─────────────────────────────────────────────────────────────

func (ctrl *controller) changePassword(c fiber.Ctx) error {
	adminID, ok := adminIDFromCtx(c)
	if !ok {
		return badRequest(c, "missing admin identity")
	}
	var body struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return badRequest(c, "invalid request body")
	}
	if body.OldPassword == "" || body.NewPassword == "" {
		return badRequest(c, "old_password and new_password are required")
	}
	if len(body.NewPassword) < 8 {
		return badRequest(c, "new password must be at least 8 characters")
	}
	if err := ctrl.svc.ChangePassword(c.Context(), adminID, body.OldPassword, body.NewPassword); err != nil {
		if isDomainErr(err, "AU_") {
			return c.Status(http.StatusUnauthorized).JSON(common.Response{
				Status: "Unauthorized", Error: "current password is incorrect",
			})
		}
		if isDomainErr(err, "RB_") {
			return badRequest(c, err.Error())
		}
		return internalError(c, err)
	}
	return c.JSON(common.Response{Status: "OK"})
}

// ─── Role management ──────────────────────────────────────────────────────────

func (ctrl *controller) listRoles(c fiber.Ctx) error {
	if !isSuperAdmin(c) {
		return forbidden(c)
	}
	roles, err := ctrl.svc.ListRoles(c.Context())
	if err != nil {
		return internalError(c, err)
	}
	return c.JSON(common.Response{Status: "OK", Data: roles})
}

func (ctrl *controller) createRole(c fiber.Ctx) error {
	if !isSuperAdmin(c) {
		return forbidden(c)
	}
	adminID, ok := adminIDFromCtx(c)
	if !ok {
		return badRequest(c, "missing admin identity")
	}
	var body CreateRoleBody
	if err := c.Bind().JSON(&body); err != nil {
		return badRequest(c, "invalid request body")
	}
	role, err := ctrl.svc.CreateRole(c.Context(), body, adminID)
	if err != nil {
		if isDomainErr(err, "RB_") {
			return badRequest(c, err.Error())
		}
		return internalError(c, err)
	}
	return c.Status(http.StatusCreated).JSON(common.Response{Status: "Created", Data: role})
}

func (ctrl *controller) updateRole(c fiber.Ctx) error {
	if !isSuperAdmin(c) {
		return forbidden(c)
	}
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return badRequest(c, "invalid role id")
	}
	var body CreateRoleBody
	if err := c.Bind().JSON(&body); err != nil {
		return badRequest(c, "invalid request body")
	}
	role, err := ctrl.svc.UpdateRole(c.Context(), id, body)
	if err != nil {
		if isDomainErr(err, "RB_") {
			return badRequest(c, err.Error())
		}
		if strings.Contains(err.Error(), "not found") {
			return c.Status(http.StatusNotFound).JSON(common.Response{Status: "Not Found", Error: err.Error()})
		}
		return internalError(c, err)
	}
	return c.JSON(common.Response{Status: "OK", Data: role})
}

func (ctrl *controller) deleteRole(c fiber.Ctx) error {
	if !isSuperAdmin(c) {
		return forbidden(c)
	}
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return badRequest(c, "invalid role id")
	}
	if err := ctrl.svc.DeleteRole(c.Context(), id); err != nil {
		if strings.Contains(err.Error(), "not found") {
			return c.Status(http.StatusNotFound).JSON(common.Response{Status: "Not Found", Error: err.Error()})
		}
		return internalError(c, err)
	}
	return c.JSON(common.Response{Status: "OK"})
}

// ─── User management ──────────────────────────────────────────────────────────

func (ctrl *controller) listUsers(c fiber.Ctx) error {
	if !isSuperAdmin(c) {
		return forbidden(c)
	}
	users, err := ctrl.svc.ListUsers(c.Context())
	if err != nil {
		return internalError(c, err)
	}
	return c.JSON(common.Response{Status: "OK", Data: users})
}

func (ctrl *controller) createUser(c fiber.Ctx) error {
	if !isSuperAdmin(c) {
		return forbidden(c)
	}
	var body CreateAdminUserBody
	if err := c.Bind().JSON(&body); err != nil {
		return badRequest(c, "invalid request body")
	}
	user, err := ctrl.svc.CreateUser(c.Context(), body)
	if err != nil {
		if isDomainErr(err, "RB_") {
			return badRequest(c, err.Error())
		}
		return internalError(c, err)
	}
	return c.Status(http.StatusCreated).JSON(common.Response{Status: "Created", Data: user})
}

func (ctrl *controller) updateUser(c fiber.Ctx) error {
	if !isSuperAdmin(c) {
		return forbidden(c)
	}
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return badRequest(c, "invalid user id")
	}
	var body struct {
		RoleID *uuid.UUID `json:"role_id"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return badRequest(c, "invalid request body")
	}
	if err := ctrl.svc.UpdateUser(c.Context(), id, body.RoleID); err != nil {
		if strings.Contains(err.Error(), "not found") {
			return c.Status(http.StatusNotFound).JSON(common.Response{Status: "Not Found", Error: err.Error()})
		}
		return internalError(c, err)
	}
	return c.JSON(common.Response{Status: "OK"})
}

func (ctrl *controller) deleteUser(c fiber.Ctx) error {
	if !isSuperAdmin(c) {
		return forbidden(c)
	}
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return badRequest(c, "invalid user id")
	}
	if err := ctrl.svc.DeleteUser(c.Context(), id); err != nil {
		if strings.Contains(err.Error(), "not found") {
			return c.Status(http.StatusNotFound).JSON(common.Response{Status: "Not Found", Error: err.Error()})
		}
		return internalError(c, err)
	}
	return c.JSON(common.Response{Status: "OK"})
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func badRequest(c fiber.Ctx, msg string) error {
	return c.Status(http.StatusBadRequest).JSON(common.Response{
		Status: "Bad Request", Error: msg,
	})
}

func forbidden(c fiber.Ctx) error {
	return c.Status(http.StatusForbidden).JSON(common.Response{
		Status: "Forbidden", Error: "super admin access required",
	})
}

func internalError(c fiber.Ctx, err error) error {
	return c.Status(http.StatusInternalServerError).JSON(common.Response{
		Status: "Internal Server Error", Error: err.Error(),
	})
}

func isDomainErr(err error, prefix string) bool {
	de, ok := err.(*common.DomainError)
	if !ok {
		return false
	}
	return len(de.Code) >= len(prefix) && de.Code[:len(prefix)] == prefix
}
