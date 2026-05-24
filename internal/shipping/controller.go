package shipping

import (
	"net/http"

	"github.com/dengankarya/overwatch/common"
	"github.com/gofiber/fiber/v3"
)

func RegisterHandlers(mux fiber.Router, service *ShippingService) {
	ctrl := controller{
		svc: service,
	}

	mux.Get("/couriers", ctrl.handleGetCourierList)
	mux.Post("/rates", ctrl.handleGetRates)
}

type controller struct {
	svc *ShippingService
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

func (ctrl *controller) handleGetRates(c fiber.Ctx) error {
	var req RateRequest
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(http.StatusBadRequest).JSON(common.Response{
			Status: http.StatusText(http.StatusBadRequest),
			Error:  "invalid request body",
		})
	}

	rates, err := ctrl.svc.GetRates(c.Context(), req)
	if err != nil {
		return c.Status(http.StatusInternalServerError).JSON(common.Response{
			Status: http.StatusText(http.StatusInternalServerError),
			Error:  err.Error(),
		})
	}

	return c.Status(http.StatusOK).JSON(common.Response{
		Status: http.StatusText(http.StatusOK),
		Data:   rates,
	})
}
