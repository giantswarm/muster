package testing

import (
	"fmt"
)

// TestFramework holds all components needed for testing
type TestFramework struct {
	Runner          TestRunner
	Client          MCPTestClient
	Loader          TestScenarioLoader
	Reporter        TestReporter
	InstanceManager MusterInstanceManager
	Logger          TestLogger
}

// NewTestFrameworkWithConfig creates a fully configured test framework with all configuration options
func NewTestFrameworkWithConfig(verbose, debug bool, basePort int, reportPath string, keepTempConfig bool) (*TestFramework, error) {
	return NewTestFrameworkForMode(ExecutionModeCLI, verbose, debug, basePort, reportPath, keepTempConfig)
}

// NewTestFrameworkForMode creates a fully configured test framework for the specified execution mode
//
// Execution Modes:
//   - ExecutionModeCLI: Uses standard stdio output for reporting (compatible with existing behavior)
//   - ExecutionModeMCPServer: Uses structured reporting that captures data without stdio output
//     to avoid contaminating the MCP protocol stream. Results can be retrieved programmatically.
//
// Note: In MCP server mode, verbose defaults to true and debug output is controlled by the
// silent logger to prevent stdio contamination.
func NewTestFrameworkForMode(mode ExecutionMode, verbose, debug bool, basePort int, reportPath string, keepTempConfig bool) (*TestFramework, error) {
	// Create logger based on execution mode
	var logger TestLogger
	switch mode {
	case ExecutionModeCLI:
		logger = NewStdoutLogger(verbose, debug)
	case ExecutionModeMCPServer:
		logger = NewSilentLogger(verbose, debug)
	default:
		logger = NewStdoutLogger(verbose, debug)
	}

	// Create instance manager
	instanceManager, err := NewMusterInstanceManagerWithConfig(debug, basePort, logger, keepTempConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create instance manager: %w", err)
	}

	// Create MCP client with logger
	client := NewMCPTestClientWithLogger(debug, logger)

	// Create scenario loader with logger
	loader := NewTestScenarioLoaderWithLogger(debug, logger)

	// Create reporter based on execution mode
	var reporter TestReporter
	switch mode {
	case ExecutionModeCLI:
		reporter = NewTestReporter(verbose, debug, reportPath)
	case ExecutionModeMCPServer:
		reporter = NewStructuredReporter(verbose, debug, reportPath)
	default:
		reporter = NewTestReporter(verbose, debug, reportPath)
	}

	// Create runner with logger
	runner := NewTestRunnerWithLogger(client, loader, reporter, instanceManager, debug, logger)

	return &TestFramework{
		Runner:          runner,
		Client:          client,
		Loader:          loader,
		Reporter:        reporter,
		InstanceManager: instanceManager,
		Logger:          logger,
	}, nil
}

// Cleanup cleans up resources used by the test framework
func (tf *TestFramework) Cleanup() error {
	if manager, ok := tf.InstanceManager.(*musterInstanceManager); ok {
		return manager.Cleanup()
	}
	return nil
}
