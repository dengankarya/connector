package tracking

import (
	"net/http"

	"github.com/dengankarya/overwatch/common"
	"github.com/gofiber/fiber/v3"
)

func RegisterHandlers(mux fiber.Router, service *TrackingService) {
	ctrl := controller{
		svc: service,
	}

	mux.Get("/couriers", ctrl.handleGetCourierList)
	mux.Get("/:waybill_id/couriers/:courier_code", ctrl.handleGetPublicTracking)
}

type controller struct {
	svc *TrackingService
}

func (ctrl *controller) handleGetPublicTracking(c fiber.Ctx) error {
	waybillID := c.Params("waybill_id")
	courierCode := c.Params("courier_code")

	if waybillID == "" || courierCode == "" {
		return c.Status(http.StatusBadRequest).JSON(common.Response{
			Status: http.StatusText(http.StatusBadRequest),
			Error:  "waybill_id and courier_code are required",
		})
	}

	trackingData, err := ctrl.svc.GetPublicTracking(c.Context(), waybillID, courierCode)
	if err != nil {
		return c.Status(http.StatusInternalServerError).JSON(common.Response{
			Status: http.StatusText(http.StatusInternalServerError),
			Error:  err.Error(),
		})
	}

	return c.Status(http.StatusOK).JSON(common.Response{
		Status: http.StatusText(http.StatusOK),
		Data:   trackingData,
	})
}

func (ctrl *controller) handleGetCourierList(c fiber.Ctx) error {
	couriers, err := ctrl.svc.GetCourierList(c.Context())
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
