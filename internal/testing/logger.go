package testing

import (
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
	"sync"
)

// stdoutLogger implements TestLogger for CLI mode, outputting to stdout/stderr
type stdoutLogger struct {
	verbose bool
	debug   bool
}

// NewStdoutLogger creates a logger that outputs to stdout/stderr
func NewStdoutLogger(verbose, debug bool) TestLogger {
	return &stdoutLogger{
		verbose: verbose,
		debug:   debug,
	}
}

func (l *stdoutLogger) Debug(format string, args ...interface{}) {
	if l.debug {
		fmt.Printf(format, args...)
	}
}

func (l *stdoutLogger) Info(format string, args ...interface{}) {
	if l.verbose || l.debug {
		fmt.Printf(format, args...)
	}
}

func (l *stdoutLogger) Error(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format, args...)
}

func (l *stdoutLogger) IsDebugEnabled() bool {
	return l.debug
}

func (l *stdoutLogger) IsVerboseEnabled() bool {
	return l.verbose
}

// prefixedLogger wraps a TestLogger and adds a scenario-specific prefix to all log messages.
// This is particularly useful for parallel test execution where logs from multiple
// scenarios are interleaved and need to be distinguished.
type prefixedLogger struct {
	base   TestLogger
	prefix string
	mu     sync.Mutex
}

// NewPrefixedLogger creates a new logger that wraps the base logger and adds
// a prefix to all log messages. The prefix is typically a short unique identifier
// for the scenario being executed.
func NewPrefixedLogger(base TestLogger, prefix string) TestLogger {
	return &prefixedLogger{
		base:   base,
		prefix: prefix,
	}
}

// GenerateScenarioPrefix generates a short unique prefix for a scenario.
// The prefix is formatted as [XXXXXX] where XXXXXX is derived from the scenario name.
// For readability, it combines:
// - First 3 chars of the cleaned scenario name
// - First 3 chars of a SHA256 hash of the full name (for uniqueness)
func GenerateScenarioPrefix(scenarioName string) string {
	// Clean up scenario name - remove common prefixes
	name := scenarioName
	name = strings.TrimPrefix(name, "mcpserver-")
	name = strings.TrimPrefix(name, "serviceclass-")
	name = strings.TrimPrefix(name, "workflow-")
	name = strings.TrimPrefix(name, "service-")

	// Get first 3 chars of the cleaned name (or pad with -)
	slug := strings.ToUpper(name)
	if len(slug) > 3 {
		slug = slug[:3]
	}
	for len(slug) < 3 {
		slug += "-"
	}

	// Generate a short hash suffix for uniqueness (3 hex chars from SHA256)
	hash := sha256.Sum256([]byte(scenarioName))
	hashHex := fmt.Sprintf("%x", hash[:2]) // First 2 bytes = 4 hex chars, we use first 3

	return fmt.Sprintf("[%s%s]", slug, strings.ToUpper(hashHex[:3]))
}

func (l *prefixedLogger) Debug(format string, args ...interface{}) {
	// Check enabled state before acquiring lock to avoid unnecessary contention
	if !l.base.IsDebugEnabled() {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	msg := fmt.Sprintf(format, args...)
	l.base.Debug("%s %s", l.prefix, msg)
}

func (l *prefixedLogger) Info(format string, args ...interface{}) {
	// Check enabled state before acquiring lock to avoid unnecessary contention
	// Info only checks verbose (same as base stdoutLogger.Info behavior)
	if !l.base.IsVerboseEnabled() && !l.base.IsDebugEnabled() {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	msg := fmt.Sprintf(format, args...)
	l.base.Info("%s %s", l.prefix, msg)
}

func (l *prefixedLogger) Error(format string, args ...interface{}) {
	// Errors are always logged, but still need synchronization for prefix
	l.mu.Lock()
	defer l.mu.Unlock()
	msg := fmt.Sprintf(format, args...)
	l.base.Error("%s %s", l.prefix, msg)
}

func (l *prefixedLogger) IsDebugEnabled() bool {
	return l.base.IsDebugEnabled()
}

func (l *prefixedLogger) IsVerboseEnabled() bool {
	return l.base.IsVerboseEnabled()
}

// silentLogger implements TestLogger for MCP server mode, suppressing all output
type silentLogger struct {
	verbose bool
	debug   bool
}

// NewSilentLogger creates a logger that suppresses all output (for MCP server mode)
func NewSilentLogger(verbose, debug bool) TestLogger {
	return &silentLogger{
		verbose: verbose,
		debug:   debug,
	}
}

func (l *silentLogger) Debug(format string, args ...interface{}) {
	// Silent - no output to avoid contaminating stdio
}

func (l *silentLogger) Info(format string, args ...interface{}) {
	// Silent - no output to avoid contaminating stdio
}

func (l *silentLogger) Error(format string, args ...interface{}) {
	// Silent - no output to avoid contaminating stdio
}

func (l *silentLogger) IsDebugEnabled() bool {
	return l.debug
}

func (l *silentLogger) IsVerboseEnabled() bool {
	return l.verbose
}
