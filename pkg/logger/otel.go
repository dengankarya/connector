package logger

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	otellogapi "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

type OtelLogrusHook struct {
	logger otellogapi.Logger
}

func NewOtelLogrusHook(logger otellogapi.Logger) *OtelLogrusHook {
	return &OtelLogrusHook{logger: logger}
}

func (h *OtelLogrusHook) Levels() []logrus.Level {
	return logrus.AllLevels
}

func (h *OtelLogrusHook) Fire(entry *logrus.Entry) error {
	var record otellogapi.Record

	// Map logrus level to OTEL severity
	record.SetSeverityText(entry.Level.String())
	record.SetTimestamp(entry.Time)
	record.SetBody(otellogapi.StringValue(entry.Message))

	// Convert logrus fields to OTEL attributes
	var attrs []otellogapi.KeyValue
	for k, v := range entry.Data {
		attrs = append(attrs, otellogapi.String(k, fmt.Sprintf("%v", v)))
	}
	record.AddAttributes(attrs...)

	h.logger.Emit(context.Background(), record)
	return nil
}

// InitOtelProvider creates and returns a configured LoggerProvider
func InitOtelProvider(ctx context.Context, endpoint, token, serviceName string) (*log.LoggerProvider, error) {
	// PostHog expects logs at /v1/logs
	url := endpoint
	if url[len(url)-1] == '/' {
		url = url[:len(url)-1]
	}
	url = url + "/v1/logs"

	exporter, err := otlploghttp.New(ctx,
		otlploghttp.WithEndpointURL(url),
		otlploghttp.WithHeaders(map[string]string{
			"Authorization": "Bearer " + token,
		}),
	)
	if err != nil {
		return nil, err
	}

	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceNameKey.String(serviceName),
		),
	)
	if err != nil {
		return nil, err
	}

	provider := log.NewLoggerProvider(
		log.WithProcessor(log.NewBatchProcessor(exporter)),
		log.WithResource(res),
	)

	return provider, nil
}
