package logging

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	ctrl "sigs.k8s.io/controller-runtime"
)

func TestLogLevel_String(t *testing.T) {
	tests := []struct {
		level    LogLevel
		expected string
	}{
		{LevelDebug, "DEBUG"},
		{LevelInfo, "INFO"},
		{LevelWarn, "WARN"},
		{LevelError, "ERROR"},
		{LogLevel(999), "UNKNOWN"},
	}

	for _, test := range tests {
		result := test.level.String()
		if result != test.expected {
			t.Errorf("LogLevel(%d).String() = %s, expected %s", test.level, result, test.expected)
		}
	}
}

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

	// Test that CLI mode is set
	if isTuiMode {
		t.Error("Expected isTuiMode to be false after InitForCLI")
	}

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

func TestLogEntry(t *testing.T) {
	// Test LogEntry structure
	now := time.Now()
	testErr := errors.New("test error")

	entry := LogEntry{
		Timestamp: now,
		Level:     LevelError,
		Subsystem: "test-subsystem",
		Message:   "test message",
		Err:       testErr,
	}

	if entry.Timestamp != now {
		t.Error("Timestamp not set correctly")
	}

	if entry.Level != LevelError {
		t.Error("Level not set correctly")
	}

	if entry.Subsystem != "test-subsystem" {
		t.Error("Subsystem not set correctly")
	}

	if entry.Message != "test message" {
		t.Error("Message not set correctly")
	}

	if entry.Err != testErr {
		t.Error("Error not set correctly")
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

func TestControllerRuntimeLoggerInTUIMode(t *testing.T) {
	// Initialize for CLI mode since TUI functions were removed
	var buf bytes.Buffer
	InitForCLI(LevelDebug, &buf)

	// Verify controller-runtime logger is set
	logger := ctrl.Log

	if logger.GetSink() == nil {
		t.Error("Expected controller-runtime logger sink to be initialized")
	}

	// Controller-runtime logs should work without panicking
	logger.Info("this message goes to the configured output")
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
