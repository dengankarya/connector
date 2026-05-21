package region

import (
	"net/http"

	"github.com/dengankarya/overwatch/common"
	"github.com/gofiber/fiber/v3"
)

func RegisterHandlers(mux fiber.Router, svc *RegionService) {
	ctrl := controller{svc: svc}

	mux.Get("/provinces", ctrl.handleGetProvinces)
	mux.Get("/regencies/:province_code", ctrl.handleGetRegencies)
	mux.Get("/districts/:regency_code", ctrl.handleGetDistricts)
	mux.Get("/villages/:district_code", ctrl.handleGetVillages)
}

type controller struct {
	svc *RegionService
}

func (ctrl *controller) handleGetProvinces(c fiber.Ctx) error {
	data, err := ctrl.svc.GetProvinces(c.Context())
	if err != nil {
		return c.Status(http.StatusInternalServerError).JSON(common.Response{
			Status: http.StatusText(http.StatusInternalServerError),
			Error:  err.Error(),
		})
	}
	return c.Status(http.StatusOK).JSON(common.Response{
		Status: http.StatusText(http.StatusOK),
		Data:   data,
	})
}

func (ctrl *controller) handleGetRegencies(c fiber.Ctx) error {
	data, err := ctrl.svc.GetRegencies(c.Context(), c.Params("province_code"))
	if err != nil {
		return c.Status(http.StatusInternalServerError).JSON(common.Response{
			Status: http.StatusText(http.StatusInternalServerError),
			Error:  err.Error(),
		})
	}
	return c.Status(http.StatusOK).JSON(common.Response{
		Status: http.StatusText(http.StatusOK),
		Data:   data,
	})
}

func (ctrl *controller) handleGetDistricts(c fiber.Ctx) error {
	data, err := ctrl.svc.GetDistricts(c.Context(), c.Params("regency_code"))
	if err != nil {
		return c.Status(http.StatusInternalServerError).JSON(common.Response{
			Status: http.StatusText(http.StatusInternalServerError),
			Error:  err.Error(),
		})
	}
	return c.Status(http.StatusOK).JSON(common.Response{
		Status: http.StatusText(http.StatusOK),
		Data:   data,
	})
}

func (ctrl *controller) handleGetVillages(c fiber.Ctx) error {
	data, err := ctrl.svc.GetVillages(c.Context(), c.Params("district_code"))
	if err != nil {
		return c.Status(http.StatusInternalServerError).JSON(common.Response{
			Status: http.StatusText(http.StatusInternalServerError),
			Error:  err.Error(),
		})
	}
	return c.Status(http.StatusOK).JSON(common.Response{
		Status: http.StatusText(http.StatusOK),
		Data:   data,
	})
}
