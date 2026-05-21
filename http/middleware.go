package main

import (
	"net/http"

	"github.com/dengankarya/overwatch/common"
	"github.com/dengankarya/overwatch/config"
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/logger"
	log "github.com/sirupsen/logrus"
)

func authenticatedRequest(cfg *config.Configuration) fiber.Handler {
	return func(c fiber.Ctx) error {
		xAPIKeyValue := string(c.Request().Header.Peek("X-API-KEY"))
		if !cfg.IsThisRequestAuthenticated(xAPIKeyValue) {
			return c.Status(http.StatusUnauthorized).JSON(common.Response{
				Status: http.StatusText(http.StatusUnauthorized),
				Error:  "INVALID_API_KEY",
			})
		}

		return c.Next()
	}
}

func requestLogger() fiber.Handler {
	return logger.New(logger.Config{
		LoggerFunc: func(c fiber.Ctx, data *logger.Data, _ *logger.Config) error {
			fields := log.Fields{
				"status":  c.Response().StatusCode(),
				"method":  c.Method(),
				"path":    c.Path(),
				"ip":      c.IP(),
				"latency": data.Stop.Sub(data.Start).String(),
			}
			if data.ChainErr != nil {
				fields["error"] = data.ChainErr.Error()
				log.WithFields(fields).Error("request")
			} else {
				log.WithFields(fields).Info("request")
			}
			return nil
		},
	})
}
