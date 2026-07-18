package main

import (
	"context"
	"net/http"
	"strings"

	"github.com/dengankarya/connector/common"
	"github.com/dengankarya/connector/config"
	_ "github.com/dengankarya/connector/docs"
	"github.com/dengankarya/connector/internal/admin"
	"github.com/dengankarya/connector/pkg/logger"
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/adaptor"
	fiblog "github.com/gofiber/fiber/v3/middleware/logger"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	httpSwagger "github.com/swaggo/http-swagger/v2"
)

// swaggerHandler wraps the swaggo http-swagger handler for use with fiber v3.
var swaggerHandler = adaptor.HTTPHandler(httpSwagger.Handler(
	httpSwagger.URL("/swagger/doc.json"),
))

func serveSwaggerUI(c fiber.Ctx) error {
	return swaggerHandler(c)
}

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

// adminRequest rejects requests whose X-API-KEY is not in ADMIN_API_KEYS.
// Must be used after authenticatedRequest (which validates the key is in ALLOWED_API_KEYS).
func adminRequest(cfg *config.Configuration) fiber.Handler {
	return func(c fiber.Ctx) error {
		xAPIKeyValue := string(c.Request().Header.Peek("X-API-KEY"))
		if !cfg.IsAdminRequest(xAPIKeyValue) {
			return c.Status(http.StatusForbidden).JSON(common.Response{
				Status: http.StatusText(http.StatusForbidden),
				Error:  "FORBIDDEN",
			})
		}
		return c.Next()
	}
}

// adminJWTAuth validates the Bearer token in Authorization header using the admin JWT secret.
// Stores the full *admin.AdminClaims (including permissions) in fiber.Ctx locals under "admin_claims".
func adminJWTAuth(secret string) fiber.Handler {
	return func(c fiber.Ctx) error {
		authHeader := c.Get("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(authHeader, prefix) {
			return c.Status(http.StatusUnauthorized).JSON(common.Response{
				Status: "Unauthorized", Error: "missing or invalid Authorization header",
			})
		}
		claims, err := admin.VerifyToken(strings.TrimPrefix(authHeader, prefix), secret)
		if err != nil {
			return c.Status(http.StatusUnauthorized).JSON(common.Response{
				Status: "Unauthorized", Error: err.Error(),
			})
		}
		c.Locals("admin_claims", claims)
		return c.Next()
	}
}

func requestIDMiddleware() fiber.Handler {
	return func(c fiber.Ctx) error {
		reqID := string(c.Request().Header.Peek("X-Request-ID"))
		if reqID == "" {
			reqID = uuid.New().String()
		}

		c.Set("X-Request-ID", reqID)

		ctx := context.WithValue(c.Context(), logger.RequestIDKey, reqID)
		c.SetContext(ctx)

		return c.Next()
	}
}

func requestLogger() fiber.Handler {
	return fiblog.New(fiblog.Config{
		LoggerFunc: func(c fiber.Ctx, data *fiblog.Data, _ *fiblog.Config) error {
			fields := log.Fields{
				"status":  c.Response().StatusCode(),
				"method":  c.Method(),
				"path":    c.Path(),
				"ip":      c.IP(),
				"latency": data.Stop.Sub(data.Start).String(),
			}

			if reqID, ok := c.Context().Value(logger.RequestIDKey).(string); ok && reqID != "" {
				fields["request_id"] = reqID
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
	// return func(c fiber.Ctx) error {
	// 	start := time.Now()

	// 	tp := c.Get("traceparent")
	// 	if tp == "" {
	// 		tp = trace.Generate()
	// 	}
	// 	c.SetContext(trace.StoreInContext(c.Context(), tp))

	// 	err := c.Next()

	// 	fields := log.Fields{
	// 		"status":      c.Response().StatusCode(),
	// 		"method":      c.Method(),
	// 		"path":        c.Path(),
	// 		"ip":          c.IP(),
	// 		"latency":     time.Since(start).String(),
	// 		"traceparent": tp,
	// 	}
	// 	if err != nil {
	// 		fields["error"] = err.Error()
	// 		log.WithFields(fields).Error("request")
	// 	} else {
	// 		log.WithFields(fields).Info("request")
	// 	}
	// 	return err
	// }
}
