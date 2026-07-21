package geocoding

import (
	"net/http"
	"strings"

	"github.com/dengankarya/connector/common"
	"github.com/gofiber/fiber/v3"
)

var (
	_ GeocodingResult
	_ Place
)

func RegisterHandlers(mux fiber.Router, svc *Service) {
	ctrl := &controller{svc: svc}
	mux.Get("/", ctrl.handleGeocode)
}

type controller struct {
	svc *Service
}

// handleGeocode godoc
//
//	@Summary		Geocode an address
//	@Description	Geocode an address using the provided address query
//	@Tags			geocoding
//	@Accept			json
//	@Produce		json
//	@Param			address	query		string	true	"Address to geocode"
//	@Success		200		{object}	common.Response[data=GeocodingResult]
//	@Failure		400		{object}	common.Response
//	@Failure		500		{object}	common.Response
//	@Security		ApiKeyAuth
//	@Router			/geocoding [GET]
func (c *controller) handleGeocode(ctx fiber.Ctx) error {
	addressQuery := ctx.Query("address")
	if strings.TrimSpace(addressQuery) == "" {
		return badRequest(ctx, "address query is required")
	}

	result, err := c.svc.Geocode(ctx.Context(), addressQuery)
	if err != nil {
		return internalError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(common.Response{
		Status:  http.StatusText(http.StatusOK),
		Message: "success geocoding address",
		Data:    result,
		Error:   nil,
	})

}

func (c *controller) handleAutoComplete(ctx fiber.Ctx) error {

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
