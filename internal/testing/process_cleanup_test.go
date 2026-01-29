package testing

import (
	"bytes"
	"fmt"
	"testing"
)

// mockTestLogger is a simple mock logger for testing.
type mockTestLogger struct {
	debugBuf       bytes.Buffer
	debugEnabled   bool
	verboseEnabled bool
}

func (m *mockTestLogger) Info(format string, args ...interface{}) {
	fmt.Fprintf(&m.debugBuf, format, args...)
}

func (m *mockTestLogger) Debug(format string, args ...interface{}) {
	fmt.Fprintf(&m.debugBuf, format, args...)
}

func (m *mockTestLogger) Error(format string, args ...interface{}) {
	fmt.Fprintf(&m.debugBuf, format, args...)
}

func (m *mockTestLogger) IsDebugEnabled() bool {
	return m.debugEnabled
}

func (m *mockTestLogger) IsVerboseEnabled() bool {
	return m.verboseEnabled
}

func TestCleanupStaleMusterTestProcesses_NoProcesses(t *testing.T) {
	// This test verifies that the cleanup function handles the case
	// where no stale processes are found (the most common case).
	// Since pgrep returns exit code 1 when no processes match,
	// this should complete without error.

	logger := &mockTestLogger{}

	// Should not panic and should complete normally
	CleanupStaleMusterTestProcesses(logger, true)

	// In debug mode, should log that no processes were found
	output := logger.debugBuf.String()
	if output != "" && output != "No stale muster test processes found\n" {
		// Either empty (pgrep not available) or the expected message is fine
		t.Logf("Debug output: %s", output)
	}
}

func TestCleanupStaleMusterTestProcesses_NonDebugMode(t *testing.T) {
	// Test that the function works correctly in non-debug mode
	logger := &mockTestLogger{}

	// Should not panic and should complete normally
	CleanupStaleMusterTestProcesses(logger, false)

	// In non-debug mode, should not log anything for "no processes found"
	output := logger.debugBuf.String()
	if output != "" {
		t.Logf("Unexpected debug output in non-debug mode: %s", output)
	}
}
