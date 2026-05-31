package shipping

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/dengankarya/connector/common"
	shippingDomain "github.com/dengankarya/connector/internal/shipping/domain"
	"github.com/dengankarya/connector/internal/shipping/provider"
	"github.com/dengankarya/connector/pkg/biteship"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

// This required for swagger to create the correct schema for Shipment, since the controller returns a common.Response{Data: Shipment} and swagger needs to know the structure of Shipment for documentation purposes.
var _ shippingDomain.Shipment
var _ biteship.Courier

func RegisterHandlers(mux fiber.Router, service *ShippingService) {
	ctrl := controller{svc: service}

	mux.Get("/", ctrl.handleListShipments)
	mux.Post("/", ctrl.handleCreateShipment)
	mux.Get("/couriers", ctrl.handleGetCourierList)
	mux.Post("/rates", ctrl.handleGetCourierRates)
	mux.Get("/:id", ctrl.handleGetShipment)
	mux.Post("/:id/confirm", ctrl.handleConfirmShipment)
}

type controller struct {
	svc *ShippingService
}

// handleGetCourierList godoc
//
//	@Summary		List couriers
//	@Description	Returns a list of available shipping couriers from Biteship. Filter by comma-separated courier codes using the `only` query parameter.
//	@Tags			Shipping
//	@Produce		json
//	@Param			only	query		string										false	"Comma-separated courier codes to filter (e.g. jne,sicepat)"
//	@Success		200		{object}	common.Response{data=[]biteship.Courier}	"Courier list"
//	@Failure		500		{object}	common.Response								"Internal server error"
//	@Security		ApiKeyAuth
//	@Router			/shipments/couriers [get]
func (ctrl *controller) handleGetCourierList(c fiber.Ctx) error {
	courierQueryParams := c.Query("only")
	courierReq := strings.Split(courierQueryParams, ",")

	couriers, err := ctrl.svc.GetCourierList(c.Context(), courierReq)
	if err != nil {
		return c.Status(http.StatusInternalServerError).JSON(common.Response{
			Status: http.StatusText(http.StatusInternalServerError),
			Error:  err.Error(),
		})
	}

	return c.Status(http.StatusOK).JSON(common.Response{
		Status: http.StatusText(http.StatusOK),
		Data:   couriers,
	})
}

// handleListShipments godoc
//
//	@Summary		List shipments
//	@Description	Returns a cursor-paginated list of shipments for the tenant. Supports filtering by status.
//	@Tags			Shipping
//	@Produce		json
//	@Param			X-Tenant-ID	header		int64																		true	"Tenant ID"
//	@Param			limit		query		int																			false	"Number of results per page (default 20, max 100)"
//	@Param			cursor		query		string																		false	"Pagination cursor returned by previous response"
//	@Param			status		query		string																		false	"Comma-separated statuses to filter (e.g. in_transit,delivered)"
//	@Success		200			{object}	common.Response{data=common.PaginationResponse[shippingDomain.Shipment]}	"Paginated shipment list"
//	@Failure		400			{object}	common.Response																"Invalid request or cursor"
//	@Failure		500			{object}	common.Response																"Internal server error"
//	@Security		ApiKeyAuth
//	@Router			/shipments [get]
func (ctrl *controller) handleListShipments(c fiber.Ctx) error {
	tenantID, _ := strconv.ParseInt(c.Get("X-Tenant-ID"), 10, 64)
	if tenantID == 0 {
		return c.Status(http.StatusBadRequest).JSON(common.Response{
			Status: http.StatusText(http.StatusBadRequest),
			Error:  "X-Tenant-ID header is required",
		})
	}

	limit, _ := strconv.Atoi(c.Query("limit", "20"))
	cursor := c.Query("cursor")

	var statuses []shippingDomain.ShipmentStatus
	if s := c.Query("status"); s != "" {
		for _, part := range strings.Split(s, ",") {
			if part = strings.TrimSpace(part); part != "" {
				statuses = append(statuses, shippingDomain.ShipmentStatus(part))
			}
		}
	}

	result, err := ctrl.svc.ListShipments(c.Context(), ListShipmentsRequest{
		TenantID: tenantID,
		Limit:    limit,
		Cursor:   cursor,
		Status:   statuses,
	})
	if err != nil {
		if errors.Is(err, ErrInvalidCursor) {
			return c.Status(http.StatusBadRequest).JSON(common.Response{
				Status: http.StatusText(http.StatusBadRequest),
				Error:  err.Error(),
			})
		}
		return c.Status(http.StatusInternalServerError).JSON(common.Response{
			Status: http.StatusText(http.StatusInternalServerError),
			Error:  err.Error(),
		})
	}

	return c.Status(http.StatusOK).JSON(common.Response{
		Status: http.StatusText(http.StatusOK),
		Data: common.PaginationResponse[*shippingDomain.Shipment]{
			Items:      result.Items,
			NextCursor: result.NextCursor,
			HasMore:    result.HasMore,
		},
	})
}

// handleGetCourierRates godoc
//
//	@Summary		Get courier rates
//	@Description	Returns available courier rates for a shipment before creating a draft order. Supply at least one location pair: area IDs (high accuracy), coordinates (required for instant couriers), or postal codes (easiest). Mix-and-match is supported (e.g. postal code origin + coordinate destination).
//	@Tags			Shipping
//	@Accept			json
//	@Produce		json
//	@Param			body	body		provider.GetRatesRequest						true	"Rate query request"
//	@Success		200		{object}	common.Response{data=provider.GetRatesResult}	"Available courier rates"
//	@Failure		400		{object}	common.Response									"Invalid request body"
//	@Failure		500		{object}	common.Response									"Internal server error"
//	@Security		ApiKeyAuth
//	@Router			/shipments/rates [post]
func (ctrl *controller) handleGetCourierRates(c fiber.Ctx) error {
	var req provider.GetRatesRequest
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(http.StatusBadRequest).JSON(common.Response{
			Status: http.StatusText(http.StatusBadRequest),
			Error:  err.Error(),
		})
	}

	result, err := ctrl.svc.GetCourierRates(c.Context(), req)
	if err != nil {
		return c.Status(http.StatusInternalServerError).JSON(common.Response{
			Status: http.StatusText(http.StatusInternalServerError),
			Error:  err.Error(),
		})
	}

	return c.Status(http.StatusOK).JSON(common.Response{
		Status: http.StatusText(http.StatusOK),
		Data:   result,
	})
}

// handleGetShipment godoc
//
//	@Summary		Get shipment
//	@Description	Retrieves a single shipment by its UUID for the authenticated tenant.
//	@Tags			Shipping
//	@Produce		json
//	@Param			X-Tenant-ID	header		int64											true	"Tenant ID"
//	@Param			id			path		string											true	"Shipment UUID"
//	@Success		200			{object}	common.Response{data=shippingDomain.Shipment}	"Shipment details"
//	@Failure		400			{object}	common.Response									"Invalid shipment ID"
//	@Failure		404			{object}	common.Response									"Shipment not found"
//	@Failure		500			{object}	common.Response									"Internal server error"
//	@Security		ApiKeyAuth
//	@Router			/shipments/{id} [get]
func (ctrl *controller) handleGetShipment(c fiber.Ctx) error {
	tenantID, _ := strconv.ParseInt(c.Get("X-Tenant-ID"), 10, 64)
	if tenantID == 0 {
		return c.Status(http.StatusBadRequest).JSON(common.Response{
			Status: http.StatusText(http.StatusBadRequest),
			Error:  "X-Tenant-ID header is required",
		})
	}

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return c.Status(http.StatusBadRequest).JSON(common.Response{
			Status: http.StatusText(http.StatusBadRequest),
			Error:  "invalid shipment id",
		})
	}

	shipment, err := ctrl.svc.GetShipment(c.Context(), tenantID, id)
	if err != nil {
		if errors.Is(err, shippingDomain.ErrNotFound) {
			return c.Status(http.StatusNotFound).JSON(common.Response{
				Status: http.StatusText(http.StatusNotFound),
				Error:  "shipment not found",
			})
		}
		return c.Status(http.StatusInternalServerError).JSON(common.Response{
			Status: http.StatusText(http.StatusInternalServerError),
			Error:  err.Error(),
		})
	}

	return c.Status(http.StatusOK).JSON(common.Response{
		Status: http.StatusText(http.StatusOK),
		Data:   shipment,
	})
}

// handleConfirmShipment godoc
//
//	@Summary		Confirm draft shipment
//	@Description	Promotes a draft shipment to a live order at Biteship. The shipment must belong to the authenticated tenant and have a valid provider draft order ID. Returns 402 if the merchant's shipping balance is insufficient.
//	@Tags			Shipping
//	@Produce		json
//	@Param			X-Tenant-ID	header		int64											true	"Tenant ID"
//	@Param			id			path		string											true	"Shipment UUID"
//	@Success		200			{object}	common.Response{data=shippingDomain.Shipment}	"Confirmed shipment"
//	@Failure		400			{object}	common.Response{error=common.ErrorDetail}		"Invalid request (e.g. missing tenant ID, invalid UUID)"
//	@Failure		402			{object}	common.Response{error=common.ErrorDetail}		"Insufficient shipping balance"
//	@Failure		404			{object}	common.Response{error=common.ErrorDetail}		"Shipment not found"
//	@Failure		500			{object}	common.Response{error=common.ErrorDetail}		"Internal server error"
//	@Security		ApiKeyAuth
//	@Router			/shipments/{id}/confirm [post]
func (ctrl *controller) handleConfirmShipment(c fiber.Ctx) error {
	tenantID, _ := strconv.ParseInt(c.Get("X-Tenant-ID"), 10, 64)
	if tenantID == 0 {
		return c.Status(http.StatusBadRequest).JSON(
			common.Err(http.StatusText(http.StatusBadRequest), "BR_MISSING_TENANT_ID", "X-Tenant-ID header is required"),
		)
	}

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return c.Status(http.StatusBadRequest).JSON(
			common.Err(http.StatusText(http.StatusBadRequest), "BR_INVALID_SHIPMENT_ID", "invalid shipment id"),
		)
	}

	shipment, err := ctrl.svc.ConfirmShipment(c.Context(), tenantID, id)
	if err != nil {
		var de *common.DomainError
		if errors.As(err, &de) {
			status := de.HTTPStatus()
			return c.Status(status).JSON(common.Err(http.StatusText(status), de.Code, de.Message))
		}
		return c.Status(http.StatusInternalServerError).JSON(
			common.Err(http.StatusText(http.StatusInternalServerError), "IN_INTERNAL_ERROR", err.Error()),
		)
	}

	return c.Status(http.StatusOK).JSON(common.Response{
		Status: http.StatusText(http.StatusOK),
		Data:   shipment,
	})
}

// handleCreateShipment godoc
//
//	@Summary		Create shipment
//	@Description	Creates a new draft shipment order via Biteship.
//	@Tags			Shipping
//	@Accept			json
//	@Produce		json
//	@Param			X-Tenant-ID	header		int64											true	"Tenant ID"
//	@Param			body		body		provider.CreateShipmentRequest					true	"Shipment creation request"
//	@Success		201			{object}	common.Response{data=shippingDomain.Shipment}	"Shipment created"
//	@Failure		400			{object}	common.Response									"Invalid request body"
//	@Failure		500			{object}	common.Response									"Internal server error"
//	@Security		ApiKeyAuth
//	@Router			/shipments [post]
func (ctrl *controller) handleCreateShipment(c fiber.Ctx) error {
	tenantID, _ := strconv.ParseInt(c.Get("X-Tenant-ID"), 10, 64)

	var req provider.CreateShipmentRequest
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(http.StatusBadRequest).JSON(common.Response{
			Status: http.StatusText(http.StatusBadRequest),
			Error:  err.Error(),
		})
	}

	shipment, err := ctrl.svc.CreateShipment(c.Context(), tenantID, req)
	if err != nil {
		return c.Status(http.StatusInternalServerError).JSON(common.Response{
			Status: http.StatusText(http.StatusInternalServerError),
			Error:  err.Error(),
		})
	}

	return c.Status(http.StatusCreated).JSON(common.Response{
		Status: http.StatusText(http.StatusCreated),
		Data:   shipment,
	})
}
