package main

import (
	"encoding/base64"
	"net/http"
	"strings"

	"github.com/dengankarya/connector/common"
	"github.com/dengankarya/connector/config"
	_ "github.com/dengankarya/connector/docs"
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/adaptor"
	"github.com/gofiber/fiber/v3/middleware/logger"
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

// basicAuth returns a handler that enforces HTTP Basic Auth when both username
// and password are configured. If either is empty, the route is left open.
func basicAuth(username, password string) fiber.Handler {
	return func(c fiber.Ctx) error {
		if username == "" || password == "" {
			return c.Next()
		}
		user, pass, ok := parseBasicAuth(c.Get("Authorization"))
		if !ok || user != username || pass != password {
			c.Set("WWW-Authenticate", `Basic realm="Swagger UI"`)
			return c.Status(http.StatusUnauthorized).SendString("Unauthorized")
		}
		return c.Next()
	}
}

func parseBasicAuth(header string) (username, password string, ok bool) {
	const prefix = "Basic "
	if !strings.HasPrefix(header, prefix) {
		return "", "", false
	}
	decoded, err := base64.StdEncoding.DecodeString(header[len(prefix):])
	if err != nil {
		return "", "", false
	}
	user, pass, found := strings.Cut(string(decoded), ":")
	if !found {
		return "", "", false
	}
	return user, pass, true
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
