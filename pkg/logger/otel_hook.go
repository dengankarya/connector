package logger

import (
	"context"
	"fmt"
	"os"

	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	otellog "go.opentelemetry.io/otel/sdk/log"

	otellogapi "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/log/global"
)

// OtelLogrusHook is a Logrus hook that forwards log entries to an
// OpenTelemetry LoggerProvider (e.g. PostHog via OTLP HTTP).
type OtelLogrusHook struct {
	provider *otellog.LoggerProvider
	logger   otellogapi.Logger
}

// OtelHookConfig holds the configuration needed to set up the OTEL exporter.
type OtelHookConfig struct {
	Endpoint     string // e.g. "eu.i.posthog.com"
	ProjectToken string // PostHog project API key
	ServiceName  string // e.g. "growreach-be"
}

// NewOtelLogrusHook creates a Logrus hook that ships logs to PostHog via OTLP.
// Returns the hook and a shutdown function that must be called on app exit
// to flush pending logs.
func NewOtelLogrusHook(cfg OtelHookConfig) (*OtelLogrusHook, func(), error) {
	ctx := context.Background()

	endpoint := cfg.Endpoint
	if len(endpoint) > 0 {
		if endpoint[:7] == "http://" {
			endpoint = endpoint[7:]
		} else if endpoint[:8] == "https://" {
			endpoint = endpoint[8:]
		}
	}

	exporter, err := otlploghttp.New(ctx,
		otlploghttp.WithEndpoint(endpoint),
		otlploghttp.WithURLPath("/i/v1/logs"),
		otlploghttp.WithHeaders(map[string]string{
			"Authorization": "Bearer " + cfg.ProjectToken,
		}),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create OTLP log exporter: %w", err)
	}

	os.Setenv("OTEL_SERVICE_NAME", cfg.ServiceName)

	provider := otellog.NewLoggerProvider(
		otellog.WithProcessor(otellog.NewBatchProcessor(exporter)),
	)

	global.SetLoggerProvider(provider)

	otelLogger := provider.Logger(cfg.ServiceName)

	hook := &OtelLogrusHook{
		provider: provider,
		logger:   otelLogger,
	}

	shutdownFn := func() {
		_ = provider.Shutdown(context.Background())
	}

	return hook, shutdownFn, nil
}

// Levels returns all log levels so the hook fires on every log entry.
func (h *OtelLogrusHook) Levels() []logrus.Level {
	return logrus.AllLevels
}

// Fire is called by Logrus for each log entry. It converts the entry
// into an OTEL log record and emits it to the provider.
func (h *OtelLogrusHook) Fire(entry *logrus.Entry) error {
	var record otellogapi.Record

	// Map logrus level → OTEL severity
	record.SetSeverity(mapLogrusLevel(entry.Level))
	record.SetSeverityText(entry.Level.String())
	record.SetTimestamp(entry.Time)
	record.SetBody(otellogapi.StringValue(entry.Message))

	// Convert logrus fields to OTEL key-value pairs
	attrs := make([]otellogapi.KeyValue, 0, len(entry.Data))
	for k, v := range entry.Data {
		attrs = append(attrs, otellogapi.String(k, fmt.Sprintf("%v", v)))
	}
	record.AddAttributes(attrs...)

	h.logger.Emit(context.Background(), record)
	return nil
}

func mapLogrusLevel(level logrus.Level) otellogapi.Severity {
	switch level {
	case logrus.TraceLevel:
		return otellogapi.SeverityTrace
	case logrus.DebugLevel:
		return otellogapi.SeverityDebug
	case logrus.InfoLevel:
		return otellogapi.SeverityInfo
	case logrus.WarnLevel:
		return otellogapi.SeverityWarn
	case logrus.ErrorLevel:
		return otellogapi.SeverityError
	case logrus.FatalLevel, logrus.PanicLevel:
		return otellogapi.SeverityFatal
	default:
		return otellogapi.SeverityInfo
	}
}
