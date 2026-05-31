package main

import (
	"net/http"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/gofiber/fiber/v3"
	log "github.com/sirupsen/logrus"
)

// initSentry initialises the Sentry SDK. It is a no-op when dsn is empty.
func initSentry(dsn, env string) {
	if dsn == "" {
		return
	}
	err := sentry.Init(sentry.ClientOptions{
		Dsn:              dsn,
		Environment:      env,
		AttachStacktrace: true,
		// Capture 100 % of traces; tune this down in production if needed.
		TracesSampleRate: 1.0,
	})
	if err != nil {
		log.WithError(err).Warn("sentry: failed to initialise — error reporting disabled")
		return
	}
	log.WithField("env", env).Info("sentry: initialised")
}

// sentryRecovery is a Fiber middleware that recovers from panics, captures the
// exception to Sentry, and returns a 500 response so the server keeps running.
func sentryRecovery() fiber.Handler {
	return func(c fiber.Ctx) (err error) {
		defer func() {
			if r := recover(); r != nil {
				hub := sentry.CurrentHub().Clone()
				hub.WithScope(func(scope *sentry.Scope) {
					scope.SetTag("method", c.Method())
					scope.SetTag("path", c.Path())
					scope.SetTag("url", c.OriginalURL())
				})

				var eventErr error
				switch v := r.(type) {
				case error:
					eventErr = v
				default:
					eventErr = fiber.NewError(http.StatusInternalServerError, "panic")
				}

				hub.RecoverWithContext(c.Context(), r)
				sentry.Flush(2 * time.Second)

				log.WithError(eventErr).Error("sentry: recovered panic")
				err = c.Status(http.StatusInternalServerError).JSON(map[string]string{
					"status":  "Internal Server Error",
					"message": "an unexpected error occurred",
				})
			}
		}()
		return c.Next()
	}
}
