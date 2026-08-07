package address

import (
	"net/http"
	"strings"

	"github.com/dengankarya/connector/common"
	"github.com/gofiber/fiber/v3"
)

func RegisterHandlers(mux fiber.Router, svc *Service) {
	ctrl := &controller{svc: svc}
	mux.Get("/search", ctrl.handleSearch)
}

type controller struct{ svc *Service }

// handleSearch godoc
//
//	@Summary		Search for addresses
//	@Description	Returns normalized Indonesian address suggestions from a free-text query.
//	@Tags			address
//	@Produce		json
//	@Param			q	query		string	true	"Address search query"
//	@Success		200	{object}	common.Response{data=SearchResult}
//	@Failure		400	{object}	common.Response
//	@Failure		500	{object}	common.Response
//	@Security		ApiKeyAuth
//	@Router			/address/search [get]
func (c *controller) handleSearch(ctx fiber.Ctx) error {
	q := strings.TrimSpace(ctx.Query("q"))
	if q == "" {
		return ctx.Status(http.StatusBadRequest).JSON(common.Response{
			Status: "Bad Request", Error: "'q' query parameter is required",
		})
	}

	result, err := c.svc.Search(ctx.Context(), q)
	if err != nil {
		return ctx.Status(http.StatusInternalServerError).JSON(common.Response{
			Status: "Internal Server Error", Error: err.Error(),
		})
	}

	return ctx.Status(http.StatusOK).JSON(common.Response{
		Status: http.StatusText(http.StatusOK), Message: "success", Data: result,
	})
}
