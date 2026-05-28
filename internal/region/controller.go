package region

import (
	"net/http"

	"github.com/dengankarya/connector/common"
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

// handleGetProvinces godoc
//
//	@Summary		List provinces
//	@Description	Returns a list of all Indonesian provinces.
//	@Tags			Regions
//	@Produce		json
//	@Success		200	{object}	common.Response{data=[]Area}	"Province list"
//	@Failure		500	{object}	common.Response					"Internal server error"
//	@Security		ApiKeyAuth
//	@Router			/regions/provinces [get]
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

// handleGetRegencies godoc
//
//	@Summary		List regencies
//	@Description	Returns a list of regencies/cities for the given province.
//	@Tags			Regions
//	@Produce		json
//	@Param			province_code	path		string							true	"Province code"
//	@Success		200				{object}	common.Response{data=[]Area}	"Regency list"
//	@Failure		500				{object}	common.Response					"Internal server error"
//	@Security		ApiKeyAuth
//	@Router			/regions/regencies/{province_code} [get]
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

// handleGetDistricts godoc
//
//	@Summary		List districts
//	@Description	Returns a list of districts for the given regency.
//	@Tags			Regions
//	@Produce		json
//	@Param			regency_code	path		string							true	"Regency code"
//	@Success		200				{object}	common.Response{data=[]Area}	"District list"
//	@Failure		500				{object}	common.Response					"Internal server error"
//	@Security		ApiKeyAuth
//	@Router			/regions/districts/{regency_code} [get]
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

// handleGetVillages godoc
//
//	@Summary		List villages
//	@Description	Returns a list of villages for the given district.
//	@Tags			Regions
//	@Produce		json
//	@Param			district_code	path		string							true	"District code"
//	@Success		200				{object}	common.Response{data=[]Area}	"Village list"
//	@Failure		500				{object}	common.Response					"Internal server error"
//	@Security		ApiKeyAuth
//	@Router			/regions/villages/{district_code} [get]
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
