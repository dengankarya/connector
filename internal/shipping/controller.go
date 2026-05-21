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
