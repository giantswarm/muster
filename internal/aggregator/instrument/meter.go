package instrument

import (
	"context"
	"errors"
	"fmt"
	"os"

	"go.opentelemetry.io/contrib/exporters/autoexport"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// MeterShutdown drains pending metric data. Idempotent: the no-export
// branch returns a no-op closure and sdkmetric.MeterProvider.Shutdown
// itself only runs once.
type MeterShutdown func(ctx context.Context) error

// InitMeter installs the global OTEL MeterProvider, selecting the
// exporter via autoexport from OTEL_METRICS_EXPORTER and
// OTEL_EXPORTER_OTLP_PROTOCOL. Resource attribution mirrors
// mcp-toolkit/tracing.Init: serviceName / serviceVersion are written
// as semconv attributes when non-empty, and OTEL_RESOURCE_ATTRIBUTES
// overrides per-pod fields supplied via the downward API.
//
// If no OTLP endpoint and no OTEL_METRICS_EXPORTER are set, InitMeter
// returns a no-op shutdown — metrics are not collected and no exporter
// goroutine runs. The propagator is installed by the tracing init in
// mcp-toolkit; nothing meter-specific is needed there.
func InitMeter(ctx context.Context, serviceName, serviceVersion string) (MeterShutdown, error) {
	if !metricsConfigured() {
		return func(context.Context) error { return nil }, nil
	}

	exp, err := autoexport.NewMetricReader(ctx)
	if err != nil {
		return nil, fmt.Errorf("otlp metric reader: %w", err)
	}

	var attrs []attribute.KeyValue
	if serviceName != "" {
		attrs = append(attrs, semconv.ServiceName(serviceName))
	}
	if serviceVersion != "" {
		attrs = append(attrs, semconv.ServiceVersion(serviceVersion))
	}
	res, err := resource.New(ctx,
		resource.WithAttributes(attrs...),
		resource.WithProcess(),
		resource.WithOS(),
		resource.WithContainer(),
		resource.WithFromEnv(),
	)
	if err != nil && !errors.Is(err, resource.ErrPartialResource) {
		return nil, fmt.Errorf("otel resource: %w", err)
	}

	mp := metric.NewMeterProvider(
		metric.WithReader(exp),
		metric.WithResource(res),
	)
	otel.SetMeterProvider(mp)
	return mp.Shutdown, nil
}

// metricsConfigured mirrors mcp-toolkit/tracing.tracingConfigured: any
// of the standard OTEL metric env vars opts in.
func metricsConfigured() bool {
	return os.Getenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT") != "" ||
		os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" ||
		os.Getenv("OTEL_METRICS_EXPORTER") != ""
}
