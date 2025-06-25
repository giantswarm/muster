package testing

import (
	"fmt"
	"os"
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
