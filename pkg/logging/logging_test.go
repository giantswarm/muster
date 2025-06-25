package logging

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
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

func TestInitForTUI(t *testing.T) {
	// Initialize for TUI mode
	channel := InitForTUI(LevelDebug)

	// Test that TUI mode is set
	if !isTuiMode {
		t.Error("Expected isTuiMode to be true after InitForTUI")
	}

	// Test that channel is returned
	if channel == nil {
		t.Error("Expected channel to be returned from InitForTUI")
	}

	// Test that tuiLogChannel is set
	if tuiLogChannel == nil {
		t.Error("Expected tuiLogChannel to be set after InitForTUI")
	}

	// Clean up
	CloseTUIChannel()
}

func TestTUILogging(t *testing.T) {
	// Initialize for TUI mode
	channel := InitForTUI(LevelDebug)
	defer CloseTUIChannel()

	// Test logging to channel
	testMessage := "test TUI message"
	testSubsystem := "test-tui-subsystem"

	Info(testSubsystem, "%s", testMessage)

	// Read from channel with timeout
	select {
	case entry := <-channel:
		if entry.Message != testMessage {
			t.Errorf("Expected message %s, got %s", testMessage, entry.Message)
		}
		if entry.Subsystem != testSubsystem {
			t.Errorf("Expected subsystem %s, got %s", testSubsystem, entry.Subsystem)
		}
		if entry.Level != LevelInfo {
			t.Errorf("Expected level %v, got %v", LevelInfo, entry.Level)
		}
		if entry.Err != nil {
			t.Errorf("Expected no error, got %v", entry.Err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("Timeout waiting for log entry")
	}
}

func TestDebugLogging(t *testing.T) {
	channel := InitForTUI(LevelDebug)
	defer CloseTUIChannel()

	Debug("debug-test", "debug message with %s", "formatting")

	select {
	case entry := <-channel:
		if entry.Level != LevelDebug {
			t.Errorf("Expected debug level, got %v", entry.Level)
		}
		if !strings.Contains(entry.Message, "debug message with formatting") {
			t.Errorf("Expected formatted message, got %s", entry.Message)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("Timeout waiting for debug log entry")
	}
}

func TestWarnLogging(t *testing.T) {
	channel := InitForTUI(LevelDebug)
	defer CloseTUIChannel()

	Warn("warn-test", "warning message")

	select {
	case entry := <-channel:
		if entry.Level != LevelWarn {
			t.Errorf("Expected warn level, got %v", entry.Level)
		}
		if entry.Message != "warning message" {
			t.Errorf("Expected 'warning message', got %s", entry.Message)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("Timeout waiting for warn log entry")
	}
}

func TestErrorLogging(t *testing.T) {
	channel := InitForTUI(LevelDebug)
	defer CloseTUIChannel()

	testErr := errors.New("test error")
	Error("error-test", testErr, "error message")

	select {
	case entry := <-channel:
		if entry.Level != LevelError {
			t.Errorf("Expected error level, got %v", entry.Level)
		}
		if entry.Message != "error message" {
			t.Errorf("Expected 'error message', got %s", entry.Message)
		}
		if entry.Err != testErr {
			t.Errorf("Expected error %v, got %v", testErr, entry.Err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("Timeout waiting for error log entry")
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

func TestTUIChannelBuffering(t *testing.T) {
	// Test with small buffer size
	channel := Initcommon("tui", LevelDebug, nil, 2)
	defer CloseTUIChannel()

	// Fill the buffer
	Info("test", "message 1")
	Info("test", "message 2")

	// This should not block but might be dropped
	Info("test", "message 3")

	// Read messages
	count := 0
	for i := 0; i < 3; i++ {
		select {
		case <-channel:
			count++
		case <-time.After(10 * time.Millisecond):
			break
		}
	}

	// Should have at least 2 messages (buffer size)
	if count < 2 {
		t.Errorf("Expected at least 2 messages, got %d", count)
	}
}

func TestCloseTUIChannel(t *testing.T) {
	channel := InitForTUI(LevelDebug)

	// Channel should be open
	if tuiLogChannel == nil {
		t.Error("Expected tuiLogChannel to be set")
	}

	// Close the channel
	CloseTUIChannel()

	// Channel should be nil after closing
	if tuiLogChannel != nil {
		t.Error("Expected tuiLogChannel to be nil after closing")
	}

	// Reading from the returned channel should indicate it's closed
	select {
	case _, ok := <-channel:
		if ok {
			t.Error("Expected channel to be closed")
		}
	case <-time.After(10 * time.Millisecond):
		// This is also acceptable - the channel might not immediately show as closed
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

func TestMessageFormatting(t *testing.T) {
	channel := InitForTUI(LevelDebug)
	defer CloseTUIChannel()

	// Test message formatting
	Info("test", "formatted message: %s %d", "hello", 42)

	select {
	case entry := <-channel:
		expected := "formatted message: hello 42"
		if entry.Message != expected {
			t.Errorf("Expected formatted message %s, got %s", expected, entry.Message)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("Timeout waiting for formatted log entry")
	}
}
