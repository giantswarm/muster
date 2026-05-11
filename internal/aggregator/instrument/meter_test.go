package instrument

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInitMeterNoEnv_ReturnsNoopShutdown(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_METRICS_EXPORTER", "")

	shutdown, err := InitMeter(context.Background(), "muster", "0.0.0-test")
	require.NoError(t, err)
	require.NotNil(t, shutdown)
	require.NoError(t, shutdown(context.Background()))
	require.NoError(t, shutdown(context.Background()), "shutdown must be idempotent")
}

func TestInitMeter_ConsoleExporter(t *testing.T) {
	t.Setenv("OTEL_METRICS_EXPORTER", "console")

	shutdown, err := InitMeter(context.Background(), "muster", "0.0.0-test")
	require.NoError(t, err)
	require.NotNil(t, shutdown)
	t.Cleanup(func() { _ = shutdown(context.Background()) })
}

func TestMetricsConfigured(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_METRICS_EXPORTER", "")
	require.False(t, metricsConfigured())

	t.Setenv("OTEL_METRICS_EXPORTER", "otlp")
	require.True(t, metricsConfigured())
}
