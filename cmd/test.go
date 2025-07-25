package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"muster/internal/agent"
	"muster/internal/cli"
	"muster/internal/config"
	"muster/internal/testing"
	"muster/internal/testing/mock"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/spf13/cobra"
)

var (
	testTimeout    time.Duration
	testVerbose    bool
	testDebug      bool
	testCategory   string
	testConcept    string
	testScenario   string
	testConfigPath string
	testReportPath string
	testFailFast   bool
	testParallel   int
	testMCPServer  bool
	testBasePort   int
	// New flags for mock MCP server
	testMockMCPServer bool
	testConfigName    string
	testMockConfig    string
	// New flag for schema generation
	testGenerateSchema bool
	testSchemaOutput   string
	// New flag for scenario validation
	testValidateScenarios bool
	testSchemaInput       string
	// Muster configuration path flag
	testMusterConfigPath string
	// Flag to keep temporary config for debugging
	testKeepTempConfig bool
)

// completeCategoryFlag provides shell completion for the category flag
func completeCategoryFlag(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return []string{"behavioral", "integration"}, cobra.ShellCompDirectiveDefault
}

// completeConceptFlag provides shell completion for the concept flag
func completeConceptFlag(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return []string{"serviceclass", "workflow", "mcpserver", "service"}, cobra.ShellCompDirectiveDefault
}

// completeScenarioFlag provides shell completion for the scenario flag by loading available scenarios
func completeScenarioFlag(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	// Load scenarios using unified approach for completion
	scenarios, err := testing.LoadScenariosForCompletion(testConfigPath)
	if err != nil {
		// Return empty completion on error
		return []string{}, cobra.ShellCompDirectiveDefault
	}

	// Extract scenario names
	var scenarioNames []string
	for _, scenario := range scenarios {
		scenarioNames = append(scenarioNames, scenario.Name)
	}

	return scenarioNames, cobra.ShellCompDirectiveDefault
}

// testCmd represents the test command
var testCmd = &cobra.Command{
	Use:   "test",
	Short: "Execute comprehensive behavioral and integration tests for muster",
	Long: `The test command executes comprehensive behavioral and integration tests
for muster by creating clean, isolated instances of muster serve for each test scenario.

This command validates all core muster concepts including:
- ServiceClass management and templating
- Workflow execution and arg resolution
- MCPServer registration and tool aggregation

- Service lifecycle management and dependencies

Test execution modes:
1. Full Test Suite (default): Runs all behavioral and integration tests
2. Category-based: Run specific test categories (--category)
3. Concept-based: Run tests for specific concepts (--concept)
4. Scenario-based: Run individual test scenarios (--scenario)
5. MCP Server mode (--mcp-server): Runs an MCP server that exposes test functionality via stdio
6. Schema Generation (--generate-schema): Generate API schema from muster serve instance
7. Scenario Validation (--validate-scenarios): Validate test scenarios against API schema

Test Categories:
- behavioral: BDD-style scenarios validating expected behavior
- integration: Component interaction and end-to-end validation

Core Concepts:
- serviceclass: ServiceClass management and dynamic instantiation
- workflow: Workflow execution and arg templating
- mcpserver: MCP server registration and tool aggregation

- service: Service lifecycle and dependency management

Schema Generation and Validation:
The test command can generate JSON schemas from live muster serve instances and validate
existing test scenarios against these schemas. This ensures test scenarios stay in sync
with the actual API as it evolves.

Example usage:
  muster test                              # Run all tests
  muster test --category=behavioral        # Run behavioral tests only
  muster test --concept=serviceclass      # Run ServiceClass tests
  muster test --scenario=basic-create     # Run specific scenario
  muster test --verbose --debug           # Detailed output and debugging
  muster test --fail-fast                 # Stop on first failure
  muster test --parallel=50               # Run with 50 parallel workers
  muster test --base-port=19000           # Use port 19000+ for test instances
  muster test --mcp-server                # Run as MCP server (stdio transport)
  muster test --generate-schema           # Generate API schema from muster serve
  muster test --validate-scenarios        # Validate scenarios against schema

Schema Generation Examples:
  muster test --generate-schema --verbose --schema-output=api-v2.json
  muster test --validate-scenarios --schema-input=api-v2.json --verbose

In MCP Server mode:
- The test command acts as an MCP server using stdio transport
- It exposes all test functionality as MCP tools
- It's designed for integration with AI assistants like Claude or Cursor
- Configure it in your AI assistant's MCP settings

The test framework uses YAML-based test scenario definitions and automatically
creates clean, isolated muster serve instances for each test scenario.
Each scenario can specify pre-configuration including MCP servers, workflows,
capabilities, service classes, and service instances.

Test results are reported with structured output suitable for CI/CD integration.`,
	RunE: runTest,
}

func init() {
	rootCmd.AddCommand(testCmd)

	// Test execution configuration
	testCmd.Flags().DurationVar(&testTimeout, "timeout", 10*time.Minute, "Overall test execution timeout")
	testCmd.Flags().IntVar(&testBasePort, "base-port", 18000, "Starting port number for test muster instances")

	// Output and debugging
	testCmd.Flags().BoolVar(&testVerbose, "verbose", false, "Enable verbose test output")
	testCmd.Flags().BoolVar(&testDebug, "debug", false, "Enable debug logging and MCP protocol tracing")

	// Test selection and filtering
	testCmd.Flags().StringVar(&testCategory, "category", "", "Run tests for specific category (behavioral, integration)")
	testCmd.Flags().StringVar(&testConcept, "concept", "", "Run tests for specific concept (serviceclass, workflow, mcpserver, service)")
	testCmd.Flags().StringVar(&testScenario, "scenario", "", "Run specific test scenario by name")

	// Test configuration and reporting
	testCmd.Flags().StringVar(&testConfigPath, "config", "", "Path to test configuration directory (default: internal test scenarios)")
	testCmd.Flags().StringVar(&testReportPath, "report", "", "Path to save detailed test report (default: stdout only)")

	// Test execution control
	testCmd.Flags().BoolVar(&testFailFast, "fail-fast", false, "Stop test execution on first failure")
	testCmd.Flags().IntVar(&testParallel, "parallel", 1, "Number of parallel test workers (1-20)")

	// MCP Server mode
	testCmd.Flags().BoolVar(&testMCPServer, "mcp-server", false, "Run as MCP server (stdio transport)")

	// New flags for mock MCP server
	testCmd.Flags().BoolVar(&testMockMCPServer, "mock-mcp-server", false, "Run as mock MCP server")
	testCmd.Flags().StringVar(&testConfigName, "config-name", "", "Name of the mock MCP server configuration")
	testCmd.Flags().StringVar(&testMockConfig, "mock-config", "", "Path to mock MCP server configuration file")

	// Schema generation flags
	testCmd.Flags().BoolVar(&testGenerateSchema, "generate-schema", false, "Generate API schema from muster serve instance")
	testCmd.Flags().StringVar(&testSchemaOutput, "schema-output", "schema.json", "Output file for generated schema")

	// Schema validation flags
	testCmd.Flags().BoolVar(&testValidateScenarios, "validate-scenarios", false, "Validate test scenarios against API schema")
	testCmd.Flags().StringVar(&testSchemaInput, "schema-input", "schema.json", "Input schema file for validation")

	// Muster configuration path flag
	testCmd.Flags().StringVar(&testMusterConfigPath, "config-path", config.GetDefaultConfigPathOrPanic(), "Configuration directory")

	// Flag to keep temporary config for debugging
	testCmd.Flags().BoolVar(&testKeepTempConfig, "keep-temp-config", false, "Keep temporary config directory after test execution for debugging")

	// Shell completion for test flags
	_ = testCmd.RegisterFlagCompletionFunc("category", completeCategoryFlag)
	_ = testCmd.RegisterFlagCompletionFunc("concept", completeConceptFlag)
	_ = testCmd.RegisterFlagCompletionFunc("scenario", completeScenarioFlag)

	// Mark flags as mutually exclusive with MCP server mode
	testCmd.MarkFlagsMutuallyExclusive("mcp-server", "category")
	testCmd.MarkFlagsMutuallyExclusive("mcp-server", "concept")
	testCmd.MarkFlagsMutuallyExclusive("mcp-server", "scenario")
	testCmd.MarkFlagsMutuallyExclusive("mcp-server", "fail-fast")
	testCmd.MarkFlagsMutuallyExclusive("mcp-server", "parallel")
	testCmd.MarkFlagsMutuallyExclusive("mcp-server", "generate-schema")
	testCmd.MarkFlagsMutuallyExclusive("mcp-server", "keep-temp-config")

	// Mark flags as mutually exclusive with mock MCP server mode
	testCmd.MarkFlagsMutuallyExclusive("mock-mcp-server", "category")
	testCmd.MarkFlagsMutuallyExclusive("mock-mcp-server", "concept")
	testCmd.MarkFlagsMutuallyExclusive("mock-mcp-server", "scenario")
	testCmd.MarkFlagsMutuallyExclusive("mock-mcp-server", "fail-fast")
	testCmd.MarkFlagsMutuallyExclusive("mock-mcp-server", "mcp-server")
	testCmd.MarkFlagsMutuallyExclusive("mock-mcp-server", "generate-schema")
	testCmd.MarkFlagsMutuallyExclusive("mock-mcp-server", "keep-temp-config")

	// Mark flags as mutually exclusive with schema generation mode
	testCmd.MarkFlagsMutuallyExclusive("generate-schema", "category")
	testCmd.MarkFlagsMutuallyExclusive("generate-schema", "concept")
	testCmd.MarkFlagsMutuallyExclusive("generate-schema", "scenario")
	testCmd.MarkFlagsMutuallyExclusive("generate-schema", "fail-fast")
	testCmd.MarkFlagsMutuallyExclusive("generate-schema", "parallel")
	testCmd.MarkFlagsMutuallyExclusive("generate-schema", "keep-temp-config")

	// Mark flags as mutually exclusive with scenario validation mode
	testCmd.MarkFlagsMutuallyExclusive("validate-scenarios", "category")
	testCmd.MarkFlagsMutuallyExclusive("validate-scenarios", "concept")
	testCmd.MarkFlagsMutuallyExclusive("validate-scenarios", "scenario")
	testCmd.MarkFlagsMutuallyExclusive("validate-scenarios", "fail-fast")
	testCmd.MarkFlagsMutuallyExclusive("validate-scenarios", "parallel")
	testCmd.MarkFlagsMutuallyExclusive("validate-scenarios", "generate-schema")
	testCmd.MarkFlagsMutuallyExclusive("validate-scenarios", "keep-temp-config")

	// Validate parallel flag
	testCmd.PreRunE = func(cmd *cobra.Command, args []string) error {
		if !testMCPServer && !testMockMCPServer && !testGenerateSchema && !testValidateScenarios && (testParallel < 1 || testParallel > 50) {
			return fmt.Errorf("parallel workers must be between 1 and 50, got %d", testParallel)
		}
		if testMockMCPServer && testMockConfig == "" {
			return fmt.Errorf("--mock-config is required when using --mock-mcp-server")
		}
		// No additional validation needed for --validate-scenarios since schema-input has a default value
		return nil
	}
}

func runTest(cmd *cobra.Command, args []string) error {
	// Create context with signal handling
	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()

	// Handle interrupts gracefully
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		if !testMCPServer && !testMockMCPServer && !testGenerateSchema && !testValidateScenarios {
			fmt.Println("\nReceived interrupt signal, stopping tests gracefully...")
		}
		cancel()
	}()

	// Run in schema generation mode if requested
	if testGenerateSchema {
		return runSchemaGeneration(ctx, cmd, args)
	}

	// Run in scenario validation mode if requested
	if testValidateScenarios {
		return runScenarioValidation(ctx, cmd, args)
	}

	// Run in MCP Server mode if requested
	if testMCPServer {
		// Create logger for MCP server
		logger := agent.NewLogger(testVerbose, true, testDebug)

		// For MCP server mode, we still need an endpoint for existing functionality
		config, err := config.LoadConfig(testMusterConfigPath)
		endpoint := cli.GetAggregatorEndpoint(&config)
		if err != nil {
			logger.Info("Warning: Could not detect endpoint (%v), using default: %s\n", err, endpoint)
		}

		// Create test MCP server
		server, err := agent.NewTestMCPServer(endpoint, logger, testConfigPath, testDebug)
		if err != nil {
			return fmt.Errorf("failed to create test MCP server: %w", err)
		}

		logger.Info("Starting muster test MCP server (stdio transport)...")
		logger.Info("Connecting to aggregator at: %s", endpoint)

		if err := server.Start(ctx); err != nil {
			return fmt.Errorf("test MCP server error: %w", err)
		}
		return nil
	}

	// Run in Mock MCP Server mode if requested
	if testMockMCPServer {
		// Create mock MCP server using the provided config file
		mockServer, err := mock.NewServerFromFile(testMockConfig, testDebug)
		if err != nil {
			return fmt.Errorf("failed to create mock MCP server: %w", err)
		}

		if testDebug {
			fmt.Printf("🔧 Starting mock MCP server with config '%s' (stdio transport)...\n", testMockConfig)
		}

		if err := mockServer.Start(ctx); err != nil {
			return fmt.Errorf("mock MCP server error: %w", err)
		}
		return nil
	}

	// Create timeout context for normal test execution
	timeoutCtx, timeoutCancel := context.WithTimeout(ctx, testTimeout)
	defer timeoutCancel()

	// Create test configuration
	testConfig := testing.TestConfiguration{
		Timeout:        testTimeout,
		Parallel:       testParallel,
		FailFast:       testFailFast,
		Verbose:        testVerbose,
		Debug:          testDebug,
		ConfigPath:     testConfigPath,
		ReportPath:     testReportPath,
		BasePort:       testBasePort,
		KeepTempConfig: testKeepTempConfig,
	}

	// Parse category filter
	if testCategory != "" {
		switch testCategory {
		case "behavioral":
			testConfig.Category = testing.CategoryBehavioral
		case "integration":
			testConfig.Category = testing.CategoryIntegration
		default:
			return fmt.Errorf("invalid category '%s', must be 'behavioral' or 'integration'", testCategory)
		}
	}

	// Parse concept filter
	if testConcept != "" {
		switch testConcept {
		case "serviceclass":
			testConfig.Concept = testing.ConceptServiceClass
		case "workflow":
			testConfig.Concept = testing.ConceptWorkflow
		case "mcpserver":
			testConfig.Concept = testing.ConceptMCPServer
		case "service":
			testConfig.Concept = testing.ConceptService
		default:
			return fmt.Errorf("invalid concept '%s', must be one of: serviceclass, workflow, mcpserver, service", testConcept)
		}
	}

	// Set scenario filter
	testConfig.Scenario = testScenario

	// Create test framework with proper verbose and debug flags
	framework, err := testing.NewTestFrameworkWithConfig(testVerbose, testDebug, testBasePort, testReportPath, testKeepTempConfig)
	if err != nil {
		return fmt.Errorf("failed to create test framework: %w", err)
	}
	defer framework.Cleanup()

	// Load test scenarios using unified path determination
	scenarioPath := testing.GetScenarioPath(testConfigPath)
	scenarios, err := framework.Loader.LoadScenarios(scenarioPath)
	if err != nil {
		return fmt.Errorf("failed to load test scenarios: %w", err)
	}

	if len(scenarios) == 0 {
		fmt.Printf("⚠️  No test scenarios found in %s\n", scenarioPath)
		fmt.Printf("💡 Available test scenario files:\n")
		fmt.Printf("   • internal/testing/scenarios/serviceclass_basic.yaml\n")
		fmt.Printf("   • internal/testing/scenarios/workflow_basic.yaml\n")
		fmt.Printf("\n")
		fmt.Printf("📚 For more information, see:\n")
		fmt.Printf("   • docs/behavioral-scenarios/\n")
		return nil
	}

	// Execute test suite
	result, err := framework.Runner.Run(timeoutCtx, testConfig, scenarios)
	if err != nil {
		return fmt.Errorf("test execution failed: %w", err)
	}

	// Set exit code based on results
	if result.FailedScenarios > 0 || result.ErrorScenarios > 0 {
		os.Exit(1)
	}

	return nil
}

func runSchemaGeneration(ctx context.Context, cmd *cobra.Command, args []string) error {
	if testVerbose || testDebug {
		fmt.Printf("🔧 Starting API schema generation for muster serve...\n")
	}

	// Create timeout context for schema generation
	timeoutCtx, timeoutCancel := context.WithTimeout(ctx, testTimeout)
	defer timeoutCancel()

	// Create an muster instance manager
	manager, err := testing.NewMusterInstanceManagerWithLogger(testDebug, testBasePort, testing.NewStdoutLogger(testVerbose, testDebug))
	if err != nil {
		return fmt.Errorf("failed to create muster instance manager: %w", err)
	}

	if testVerbose || testDebug {
		fmt.Printf("🚀 Creating muster serve instance for schema generation...\n")
	}

	// Create the muster serve instance
	instance, err := manager.CreateInstance(timeoutCtx, "schema-generation", nil)
	if err != nil {
		return fmt.Errorf("failed to create muster instance: %w", err)
	}
	defer manager.DestroyInstance(timeoutCtx, instance)

	// Wait for the instance to be ready
	if err := manager.WaitForReady(timeoutCtx, instance); err != nil {
		return fmt.Errorf("muster instance not ready: %w", err)
	}

	if testVerbose || testDebug {
		fmt.Printf("✅ muster serve instance ready at %s\n", instance.Endpoint)
	}

	// Create MCP client to connect to the instance
	mcpClient := testing.NewMCPTestClientWithLogger(testDebug, testing.NewStdoutLogger(testVerbose, testDebug))
	defer mcpClient.Close()

	// Connect to the instance
	if err := mcpClient.Connect(timeoutCtx, instance.Endpoint); err != nil {
		return fmt.Errorf("failed to connect to muster instance: %w", err)
	}

	if testVerbose || testDebug {
		fmt.Printf("🔗 Connected to muster serve instance\n")
	}

	// Generate the schema
	schema, err := generateAPISchema(timeoutCtx, mcpClient, testVerbose, testDebug)
	if err != nil {
		return fmt.Errorf("failed to generate API schema: %w", err)
	}

	// Write schema to file
	if err := writeSchemaToFile(schema, testSchemaOutput); err != nil {
		return fmt.Errorf("failed to write schema to file: %w", err)
	}

	if testVerbose || testDebug {
		fmt.Printf("✅ API schema generated successfully and saved to: %s\n", testSchemaOutput)
	} else {
		fmt.Printf("Schema generated: %s\n", testSchemaOutput)
	}

	return nil
}

// generateAPISchema generates a JSON schema for the core API tools
func generateAPISchema(ctx context.Context, client testing.MCPTestClient, verbose, debug bool) (map[string]interface{}, error) {
	if verbose || debug {
		fmt.Printf("🔍 Discovering available tools with schemas...\n")
	}

	// Get all available tools with their full schemas
	allTools, err := client.ListToolsWithSchemas(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list tools with schemas: %w", err)
	}

	if verbose || debug {
		fmt.Printf("📋 Found %d tools with schemas\n", len(allTools))
	}

	// Filter for core_* tools and build schemas
	coreToolSchemas := make(map[string]interface{})
	for _, tool := range allTools {
		if strings.HasPrefix(tool.Name, "core_") {
			if verbose || debug {
				fmt.Printf("🔧 Processing tool: %s\n", tool.Name)
			}

			schema := convertMCPToolToSchema(tool, verbose, debug)
			coreToolSchemas[tool.Name] = schema
		}
	}

	if verbose || debug {
		fmt.Printf("✅ Generated schema for %d core tools\n", len(coreToolSchemas))
	}

	// Create the overall schema structure
	apiSchema := map[string]interface{}{
		"$schema":     "http://json-schema.org/draft-07/schema#",
		"title":       "muster Core API Schema",
		"description": "Generated schema for muster core API tools for test scenario validation",
		"type":        "object",
		"properties": map[string]interface{}{
			"tools": map[string]interface{}{
				"type":        "object",
				"description": "Core API tools available in muster serve",
				"properties":  coreToolSchemas,
			},
		},
		"generated_at": time.Now().Format(time.RFC3339),
		"version":      "1.0.0",
	}

	return apiSchema, nil
}

// convertMCPToolToSchema converts an MCP tool to our schema format
func convertMCPToolToSchema(tool mcp.Tool, verbose, debug bool) map[string]interface{} {
	schema := map[string]interface{}{
		"type":        "object",
		"description": fmt.Sprintf("Arguments for %s tool", tool.Name),
		"properties":  make(map[string]interface{}),
	}

	// Handle both old InputSchema format and new args format
	if tool.InputSchema.Properties != nil {
		// Copy properties from the tool's input schema
		schema["properties"] = tool.InputSchema.Properties
	}

	if len(tool.InputSchema.Required) > 0 {
		schema["required"] = tool.InputSchema.Required
	}

	if verbose || debug {
		var propertiesCount int
		if tool.InputSchema.Properties != nil {
			propertiesCount = len(tool.InputSchema.Properties)
		}
		fmt.Printf("  📝 Tool %s has %d properties\n", tool.Name, propertiesCount)
	}

	return schema
}

// writeSchemaToFile writes the generated schema to a JSON file
func writeSchemaToFile(schema map[string]interface{}, filename string) error {
	// Convert schema to pretty-printed JSON
	jsonData, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal schema to JSON: %w", err)
	}

	// Write to file
	if err := os.WriteFile(filename, jsonData, 0644); err != nil {
		return fmt.Errorf("failed to write schema file: %w", err)
	}

	return nil
}

// getValueOrDefault returns the value if not empty, otherwise returns the default
func getValueOrDefault(value, defaultValue string) string {
	if value == "" {
		return defaultValue
	}
	return value
}

// runScenarioValidation validates test scenarios against the API schema
func runScenarioValidation(ctx context.Context, cmd *cobra.Command, args []string) error {
	if testVerbose || testDebug {
		fmt.Printf("🔍 Starting test scenario validation against API schema...\n")
	}

	// Load the schema first to validate it exists
	schema, err := testing.LoadSchemaFromFile(testSchemaInput)
	if err != nil {
		return fmt.Errorf("failed to load schema from %s: %w", testSchemaInput, err)
	}

	if testVerbose || testDebug {
		fmt.Printf("✅ Loaded schema from: %s\n", testSchemaInput)
	}

	// Use the existing unified loading approach for scenarios
	testConfig := testing.TestConfiguration{
		ConfigPath: testConfigPath,
		Verbose:    testVerbose,
		Debug:      testDebug,
	}

	scenarios, err := testing.LoadAndFilterScenarios(testConfigPath, testConfig, testing.NewStdoutLogger(testVerbose, testDebug))
	if err != nil {
		return fmt.Errorf("failed to load test scenarios: %w", err)
	}

	if len(scenarios) == 0 {
		fmt.Printf("⚠️  No test scenarios found in %s\n", testing.GetScenarioPath(testConfigPath))
		return nil
	}

	if testVerbose || testDebug {
		fmt.Printf("📋 Found %d test scenarios to validate\n", len(scenarios))
	}

	// Perform detailed validation using shared logic
	results := testing.ValidateScenariosAgainstSchema(scenarios, schema, testVerbose, testDebug)

	// Format and display results
	output := testing.FormatValidationResults(results, testVerbose)
	fmt.Print(output)

	// Exit with error code if validation failed
	if results.TotalErrors > 0 {
		fmt.Printf("\n❌ Validation failed with %d errors\n", results.TotalErrors)
		return fmt.Errorf("validation failed")
	}

	fmt.Printf("\n✅ All scenarios passed validation!\n")
	return nil
}
