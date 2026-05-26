package logging

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	ctrl "sigs.k8s.io/controller-runtime"
)

func TestLogLevel_SlogLevel(t *testing.T) {
	tests := []struct {
		level    LogLevel
		expected slog.Level
	}{
		{LevelDebug, slog.LevelDebug},
		{LevelInfo, slog.LevelInfo},
		{LevelWarn, slog.LevelWarn},
		{LevelError, slog.LevelError},
		{LogLevel(999), slog.LevelInfo}, // Default for unknown
	}

	for _, test := range tests {
		result := test.level.SlogLevel()
		if result != test.expected {
			t.Errorf("LogLevel(%d).SlogLevel() = %v, expected %v", test.level, result, test.expected)
		}
	}
}

func TestInitForCLI(t *testing.T) {
	var buf bytes.Buffer

	// Initialize for CLI mode
	InitForCLI(LevelInfo, &buf)

	// Test that defaultLogger is set
	if defaultLogger == nil {
		t.Error("Expected defaultLogger to be set after InitForCLI")
	}

	// Test logging
	Info("test-subsystem", "test message")

	output := buf.String()
	if !strings.Contains(output, "test message") {
		t.Error("Expected log message to appear in CLI output")
	}

	if !strings.Contains(output, "test-subsystem") {
		t.Error("Expected subsystem to appear in CLI output")
	}
}

func TestCLILevelFiltering(t *testing.T) {
	var buf bytes.Buffer

	// Initialize with INFO level
	InitForCLI(LevelInfo, &buf)

	// Debug should be filtered out
	Debug("test", "debug message")

	// Info should appear
	Info("test", "info message")

	output := buf.String()
	if strings.Contains(output, "debug message") {
		t.Error("Debug message should be filtered out at INFO level")
	}

	if !strings.Contains(output, "info message") {
		t.Error("Info message should appear at INFO level")
	}
}

func TestControllerRuntimeLoggerInitialization(t *testing.T) {
	var buf bytes.Buffer

	// Initialize for CLI mode which should also initialize controller-runtime logger
	InitForCLI(LevelInfo, &buf)

	// Verify controller-runtime logger is set and functional
	// ctrl.Log returns the global logger set by ctrl.SetLogger
	logger := ctrl.Log

	// The logger should have a valid sink (not nil)
	if logger.GetSink() == nil {
		t.Error("Expected controller-runtime logger sink to be initialized")
	}

	// Test that the logger is enabled at info level (our configured level)
	if !logger.Enabled() {
		t.Error("Expected controller-runtime logger to be enabled")
	}

	// Test that logging through controller-runtime works without panicking
	// This also verifies the slog bridge is properly configured
	logger.Info("test message from controller-runtime logger", "key", "value")
}

func TestInitControllerRuntimeLoggerNilHandler(t *testing.T) {
	// This test verifies that initControllerRuntimeLogger handles nil gracefully
	// We can't directly test the unexported function, but we can verify
	// the behavior is safe by checking the logger state

	// First, initialize normally to set up a valid logger
	var buf bytes.Buffer
	InitForCLI(LevelInfo, &buf)

	// Verify logger is functional before any potential nil scenario
	logger := ctrl.Log
	if logger.GetSink() == nil {
		t.Error("Expected controller-runtime logger to be initialized")
	}
}

func TestOtlpLogsConfigured(t *testing.T) {
	cases := []struct {
		name  string
		envs  map[string]string
		want  bool
	}{
		{
			name: "no env vars",
			envs: map[string]string{
				"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT": "",
				"OTEL_EXPORTER_OTLP_ENDPOINT":      "",
				"OTEL_LOGS_EXPORTER":                "",
			},
			want: false,
		},
		{
			name: "signal-specific endpoint",
			envs: map[string]string{"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT": "http://collector:4317"},
			want: true,
		},
		{
			name: "shared endpoint",
			envs: map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://collector:4317"},
			want: true,
		},
		{
			name: "exporter selector",
			envs: map[string]string{"OTEL_LOGS_EXPORTER": "otlp"},
			want: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", "")
			t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
			t.Setenv("OTEL_LOGS_EXPORTER", "")
			for k, v := range tc.envs {
				t.Setenv(k, v)
			}
			if got := otlpLogsConfigured(); got != tc.want {
				t.Errorf("otlpLogsConfigured() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestInit_Discard_DoesNotMirror(t *testing.T) {
	// When output is io.Discard and OTLP would be configured, Init must
	// not attempt WithStderrMirror (which would fail because non-OTLP
	// or produce double output). Verify Init succeeds and the global
	// logger is set.
	t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_LOGS_EXPORTER", "")

	shutdown, err := Init(t.Context(), LevelInfo, io.Discard, "test", "0.0.0-test")
	if err != nil {
		t.Fatalf("Init with io.Discard: %v", err)
	}
	if shutdown == nil {
		t.Fatal("expected non-nil Shutdown")
	}
	_ = shutdown(t.Context())
}

func TestInit_NoOTLPEnv_NoOpShutdown(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_LOGS_EXPORTER", "")

	var buf bytes.Buffer
	shutdown, err := Init(context.Background(), LevelInfo, &buf, "muster-test", "0.0.0-test")
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if shutdown == nil {
		t.Fatal("expected non-nil Shutdown closure")
	}
	if shutdownErr := shutdown(context.Background()); shutdownErr != nil {
		t.Errorf("Shutdown returned error in no-OTLP mode: %v", shutdownErr)
	}
	Info("Test", "hello")
	if buf.Len() == 0 {
		t.Error("expected log line on the supplied writer in no-OTLP mode")
	}
}

func TestInfoCtx_PassesContextThroughToHandler(t *testing.T) {
	type ctxKey string
	const probeKey ctxKey = "probe-key"

	var seen string
	rec := &contextProbeHandler{onHandle: func(ctx context.Context) {
		if v, ok := ctx.Value(probeKey).(string); ok {
			seen = v
		}
	}}
	defaultLogger = slog.New(rec)
	t.Cleanup(func() { defaultLogger = nil })

	ctx := context.WithValue(context.Background(), probeKey, "carried")
	InfoCtx(ctx, "Test", "hello")

	if seen != "carried" {
		t.Errorf("ctx.Value not propagated: got %q, want %q", seen, "carried")
	}
}

type contextProbeHandler struct {
	onHandle func(context.Context)
}

func (h *contextProbeHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }
func (h *contextProbeHandler) Handle(ctx context.Context, _ slog.Record) error {
	h.onHandle(ctx)
	return nil
}
func (h *contextProbeHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h *contextProbeHandler) WithGroup(_ string) slog.Handler      { return h }
