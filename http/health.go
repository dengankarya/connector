package main

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"github.com/dengankarya/overwatch/common"
	"github.com/gofiber/fiber/v3"
	"github.com/redis/go-redis/v9"
)

func registerHealthHandler(app *fiber.App, db *sql.DB, rdb *redis.Client) {
	app.Get("/health", func(c fiber.Ctx) error {
		ctx, cancel := context.WithTimeout(c.Context(), 3*time.Second)
		defer cancel()

		checks := map[string]string{}
		healthy := true

		if err := db.PingContext(ctx); err != nil {
			checks["database"] = err.Error()
			healthy = false
		} else {
			checks["database"] = "ok"
		}

		if err := rdb.Ping(ctx).Err(); err != nil {
			checks["redis"] = err.Error()
			healthy = false
		} else {
			checks["redis"] = "ok"
		}

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
