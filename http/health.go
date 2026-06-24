package main

import (
	"context"
	"net/http"
	"time"

	"github.com/dengankarya/connector/common"
	"github.com/gofiber/fiber/v3"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

func registerHealthHandler(app *fiber.App, pool *pgxpool.Pool, redisURL string) {
	app.Get("/health", func(c fiber.Ctx) error {
		checks := map[string]string{}
		healthy := true

		// Check PostgreSQL
		if pool != nil {
			ctx, cancel := context.WithTimeout(c.Context(), 3*time.Second)
			defer cancel()
			if err := pool.Ping(ctx); err != nil {
				checks["postgres"] = err.Error()
				healthy = false
			} else {
				checks["postgres"] = "ok"
			}
		}

		// Check Redis
		if redisURL != "" {
			redisOpt, err := redis.ParseURL(redisURL)
			if err == nil {
				redisClient := redis.NewClient(redisOpt)
				defer redisClient.Close()
				ctx, cancel := context.WithTimeout(c.Context(), 1*time.Second)
				defer cancel()
				if err := redisClient.Ping(ctx).Err(); err != nil {
					checks["redis"] = err.Error()
					healthy = false
				} else {
					checks["redis"] = "ok"
				}
			} else {
				checks["redis"] = "invalid redis url"
				healthy = false
			}
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
