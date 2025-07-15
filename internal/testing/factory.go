package testing

import (
	"fmt"
	"time"
)

// DefaultTestConfiguration returns a default test configuration
func DefaultTestConfiguration() TestConfiguration {
	return TestConfiguration{
		Timeout:        5 * time.Minute,
		Parallel:       1,
		FailFast:       false,
		Verbose:        false,
		Debug:          true,
		ConfigPath:     GetDefaultScenarioPath(),
		BasePort:       18000, // Start from port 18000 for test instances
		KeepTempConfig: false,
	}
}

// TestFramework holds all components needed for testing
type TestFramework struct {
	Runner          TestRunner
	Client          MCPTestClient
	Loader          TestScenarioLoader
	Reporter        TestReporter
	InstanceManager MusterInstanceManager
	Logger          TestLogger
}

// NewTestFramework creates a fully configured test framework
func NewTestFramework(debug bool, basePort int) (*TestFramework, error) {
	return NewTestFrameworkForMode(ExecutionModeCLI, false, debug, basePort, "", false)
}

// NewTestFrameworkWithVerbose creates a fully configured test framework with verbose and debug control
func NewTestFrameworkWithVerbose(verbose, debug bool, basePort int, reportPath string) (*TestFramework, error) {
	return NewTestFrameworkForMode(ExecutionModeCLI, verbose, debug, basePort, reportPath, false)
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

// ValidateConfiguration validates a test configuration
func ValidateConfiguration(config TestConfiguration) error {
	if config.Timeout <= 0 {
		return fmt.Errorf("timeout must be positive")
	}

	if config.Parallel < 1 {
		return fmt.Errorf("parallel workers must be at least 1")
	}

	if config.BasePort < 1024 || config.BasePort > 65535 {
		return fmt.Errorf("base port must be between 1024 and 65535")
	}

	return nil
}

// NewTestConfigurationFromFile loads test configuration from a file
func NewTestConfigurationFromFile(configPath string) (TestConfiguration, error) {
	// This would load configuration from a YAML file
	// For now, return default configuration
	config := DefaultTestConfiguration()
	config.ConfigPath = configPath
	return config, nil
}
