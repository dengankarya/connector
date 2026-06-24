package main

import (
	"net/http"

	"github.com/dengankarya/connector/common"
	"github.com/gofiber/fiber/v3"
)

func registerHealthHandler(app *fiber.App) {
	app.Get("/health", func(c fiber.Ctx) error {
		return c.Status(http.StatusOK).JSON(common.Response{
			Status: "ok",
		})
	})
}
