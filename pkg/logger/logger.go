package logger

import (
	"context"

	"github.com/sirupsen/logrus"
)

type contextKey string

const RequestIDKey contextKey = "requestID"

// Logger is a context-aware structured logger. Call one of the With* methods
// to get a logrus.Entry pre-populated with the request_id from ctx.
type Logger interface {
	WithField(ctx context.Context, key string, value any) *logrus.Entry
	WithFields(ctx context.Context, fields logrus.Fields) *logrus.Entry
	WithError(ctx context.Context, err error) *logrus.Entry
}

type logrusLogger struct {
	l *logrus.Logger
}

// New wraps l in a context-aware Logger.
func New(l *logrus.Logger) Logger {
	return &logrusLogger{l: l}
}

func (ll *logrusLogger) entry(ctx context.Context) *logrus.Entry {
	e := ll.l.WithContext(ctx)
	if ctx != nil {
		if reqID, ok := ctx.Value(RequestIDKey).(string); ok && reqID != "" {
			e = e.WithField("request_id", reqID)
		}
	}
	return e
}

func (ll *logrusLogger) WithField(ctx context.Context, key string, value any) *logrus.Entry {
	return ll.entry(ctx).WithField(key, value)
}

func (ll *logrusLogger) WithFields(ctx context.Context, fields logrus.Fields) *logrus.Entry {
	return ll.entry(ctx).WithFields(fields)
}

func (ll *logrusLogger) WithError(ctx context.Context, err error) *logrus.Entry {
	return ll.entry(ctx).WithError(err)
}
