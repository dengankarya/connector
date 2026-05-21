package main

import (
	"net/http"

	"github.com/dengankarya/overwatch/common"
	"github.com/gofiber/fiber/v3"
)

func registerHealthHandler(app *fiber.App) {
	app.Get("/health", func(c fiber.Ctx) error {

		checks := map[string]string{}
		healthy := true

		statusCode := http.StatusOK
		statusText := "ok"
		if !healthy {
			statusCode = http.StatusServiceUnavailable
			statusText = http.StatusText(http.StatusServiceUnavailable)
		}

		return c.Status(statusCode).JSON(common.Response{
			Status: statusText,
			Data:   checks,
		})
	})
}
