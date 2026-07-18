package logger

import (
	"context"

	"github.com/sirupsen/logrus"
)

type contextKey string

const RequestIDKey contextKey = "requestID"

type Logger struct {
	log *logrus.Logger
}

// New wraps an existing logrus.Logger into a context-aware Logger.
func New(l *logrus.Logger) *Logger {
	return &Logger{log: l}
}

// getEntry returns a logrus.Entry with context values attached.
func (l *Logger) getEntry(ctx context.Context, fields map[string]interface{}) *logrus.Entry {
	entry := logrus.NewEntry(l.log)

	if ctx != nil {
		if reqID, ok := ctx.Value(RequestIDKey).(string); ok && reqID != "" {
			entry = entry.WithField("request_id", reqID)
		}
	}

	if len(fields) > 0 {
		entry = entry.WithFields(fields)
	}

	return entry
}

func (l *Logger) Info(ctx context.Context, msg string, fields map[string]interface{}) {
	l.getEntry(ctx, fields).Info(msg)
}

func (l *Logger) Error(ctx context.Context, msg string, fields map[string]interface{}) {
	l.getEntry(ctx, fields).Error(msg)
}

func (l *Logger) Warn(ctx context.Context, msg string, fields map[string]interface{}) {
	l.getEntry(ctx, fields).Warn(msg)
}

func (l *Logger) Debug(ctx context.Context, msg string, fields map[string]interface{}) {
	l.getEntry(ctx, fields).Debug(msg)
}

func (l *Logger) Fatal(ctx context.Context, msg string, fields map[string]interface{}) {
	l.getEntry(ctx, fields).Fatal(msg)
}

// WithError is a convenience method that returns a logger entry with an error field.
// We can simulate logrus.WithError style.
func (l *Logger) WithError(ctx context.Context, err error) *logrus.Entry {
	return l.getEntry(ctx, map[string]interface{}{"error": err})
}

// WithFields is for cases where standard context isn't available or for bridging.
func (l *Logger) WithFields(ctx context.Context, fields logrus.Fields) *logrus.Entry {
	return l.getEntry(ctx, map[string]interface{}(fields))
}

// WithField is a convenience method that returns a logger entry with a single field.
func (l *Logger) WithField(ctx context.Context, key string, value interface{}) *logrus.Entry {
	return l.getEntry(ctx, map[string]interface{}{key: value})
}
