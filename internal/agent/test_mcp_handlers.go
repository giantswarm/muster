package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"muster/internal/testing"

	"github.com/mark3labs/mcp-go/mcp"
)

// handleRunScenarios handles the test_run_scenarios MCP tool
func (t *TestMCPServer) handleRunScenarios(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()

	// Extract and validate parameters
	var config testing.TestConfiguration
	config.Timeout = 30 * time.Second // Default timeout
	config.Parallel = 1               // Default parallel workers
	config.Verbose = true             // Always verbose for MCP
	config.Debug = t.debug            // Inherit debug setting from server
	config.BasePort = 18000           // Default base port for test instances

	// Parse optional parameters
	if category, ok := args["category"].(string); ok && category != "" {
		switch category {
		case "behavioral":
			config.Category = testing.CategoryBehavioral
		case "integration":
			config.Category = testing.CategoryIntegration
		default:
			return mcp.NewToolResultError(fmt.Sprintf("Invalid category '%s', must be 'behavioral' or 'integration'", category)), nil
		}
	}

	if concept, ok := args["concept"].(string); ok && concept != "" {
		switch concept {
		case "serviceclass":
			config.Concept = testing.ConceptServiceClass
		case "workflow":
			config.Concept = testing.ConceptWorkflow
		case "mcpserver":
			config.Concept = testing.ConceptMCPServer
		case "capability":
			config.Concept = testing.ConceptCapability
		case "service":
			config.Concept = testing.ConceptService
		default:
			return mcp.NewToolResultError(fmt.Sprintf("Invalid concept '%s', must be one of: serviceclass, workflow, mcpserver, capability, service", concept)), nil
		}
	}

	if scenario, ok := args["scenario"].(string); ok {
		config.Scenario = scenario
	}

	if configPath, ok := args["config_path"].(string); ok {
		config.ConfigPath = configPath
	} else {
		config.ConfigPath = t.configPath
	}

	if parallel, ok := args["parallel"].(float64); ok {
		if parallel < 1 || parallel > 10 {
			return mcp.NewToolResultError("parallel workers must be between 1 and 10"), nil
		}
		config.Parallel = int(parallel)
	}

	if failFast, ok := args["fail_fast"].(bool); ok {
		config.FailFast = failFast
	}

	if verbose, ok := args["verbose"].(bool); ok {
		config.Verbose = verbose
	}

	// Load and filter test scenarios using unified approach
	scenarios, err := testing.LoadAndFilterScenarios(config.ConfigPath, config, nil)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to load test scenarios: %v", err)), nil
	}

	if len(scenarios) == 0 {
		scenarioPath := testing.GetScenarioPath(config.ConfigPath)
		return mcp.NewToolResultText(fmt.Sprintf("No test scenarios found in %s", scenarioPath)), nil
	}

	// Execute test suite with timeout protection
	// Create a timeout context to prevent the MCP call from hanging
	timeoutCtx, cancel := context.WithTimeout(ctx, config.Timeout)
	defer cancel()

	// Run tests with timeout protection
	resultChan := make(chan *testing.TestSuiteResult, 1)
	errorChan := make(chan error, 1)

	go func() {
		result, err := t.testRunner.Run(timeoutCtx, config, scenarios)
		if err != nil {
			errorChan <- err
			return
		}
		resultChan <- result
	}()

	// Wait for result or timeout
	select {
	case result := <-resultChan:
		// Store result for later retrieval
		t.lastResult = result

		// Format result as JSON
		jsonData, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Failed to format test results: %v", err)), nil
		}

		return mcp.NewToolResultText(string(jsonData)), nil

	case err := <-errorChan:
		return mcp.NewToolResultError(fmt.Sprintf("Test execution failed: %v", err)), nil

	case <-timeoutCtx.Done():
		return mcp.NewToolResultError(fmt.Sprintf("Test execution timed out after %v", config.Timeout)), nil
	}
}

// handleListScenarios handles the test_list_scenarios MCP tool
func (t *TestMCPServer) handleListScenarios(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()

	// Extract config path
	configPath := t.configPath
	if path, ok := args["config_path"].(string); ok && path != "" {
		configPath = path
	}

	// Create test configuration for filtering
	testConfig := testing.TestConfiguration{
		Verbose: true, // MCP server mode defaults to verbose
		Debug:   t.debug,
	}

	// Apply category filter
	if category, ok := args["category"].(string); ok && category != "" {
		switch category {
		case "behavioral":
			testConfig.Category = testing.CategoryBehavioral
		case "integration":
			testConfig.Category = testing.CategoryIntegration
		default:
			return mcp.NewToolResultError(fmt.Sprintf("Invalid category '%s'", category)), nil
		}
	}

	// Apply concept filter
	if concept, ok := args["concept"].(string); ok && concept != "" {
		switch concept {
		case "serviceclass":
			testConfig.Concept = testing.ConceptServiceClass
		case "workflow":
			testConfig.Concept = testing.ConceptWorkflow
		case "mcpserver":
			testConfig.Concept = testing.ConceptMCPServer
		case "capability":
			testConfig.Concept = testing.ConceptCapability
		case "service":
			testConfig.Concept = testing.ConceptService
		default:
			return mcp.NewToolResultError(fmt.Sprintf("Invalid concept '%s'", concept)), nil
		}
	}

	// Load and filter scenarios using unified approach
	filteredScenarios, err := testing.LoadAndFilterScenarios(configPath, testConfig, nil)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to load scenarios: %v", err)), nil
	}

	// Format scenarios for output
	type ScenarioInfo struct {
		Name         string   `json:"name"`
		Category     string   `json:"category"`
		Concept      string   `json:"concept"`
		Description  string   `json:"description"`
		StepCount    int      `json:"step_count"`
		CleanupCount int      `json:"cleanup_count"`
		Tags         []string `json:"tags,omitempty"`
		Timeout      string   `json:"timeout,omitempty"`
	}

	scenarioList := make([]ScenarioInfo, len(filteredScenarios))
	for i, scenario := range filteredScenarios {
		info := ScenarioInfo{
			Name:         scenario.Name,
			Category:     string(scenario.Category),
			Concept:      string(scenario.Concept),
			Description:  scenario.Description,
			StepCount:    len(scenario.Steps),
			CleanupCount: len(scenario.Cleanup),
			Tags:         scenario.Tags,
		}

		if scenario.Timeout > 0 {
			info.Timeout = scenario.Timeout.String()
		}

		scenarioList[i] = info
	}

	jsonData, err := json.MarshalIndent(scenarioList, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to format scenarios: %v", err)), nil
	}

	return mcp.NewToolResultText(string(jsonData)), nil
}

// handleValidateScenario handles the test_validate_scenario MCP tool with optional API schema validation
func (t *TestMCPServer) handleValidateScenario(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	scenarioPath, err := request.RequireString("scenario_path")
	if err != nil {
		return mcp.NewToolResultError("scenario_path parameter is required"), nil
	}

	args := request.GetArguments()

	// Check if schema validation is requested
	schemaPath, hasSchema := args["schema_path"].(string)

	if hasSchema && schemaPath != "" {
		// API Schema Validation Mode
		return t.handleAPISchemaValidation(ctx, scenarioPath, schemaPath, args)
	} else {
		// YAML Structure Validation Mode (original behavior)
		return t.handleYAMLValidation(ctx, scenarioPath)
	}
}

// handleYAMLValidation performs the original YAML structure validation
func (t *TestMCPServer) handleYAMLValidation(ctx context.Context, scenarioPath string) (*mcp.CallToolResult, error) {
	// Try to load scenarios from the path with unified approach
	actualPath := testing.GetScenarioPath(scenarioPath)
	loader := testing.CreateScenarioLoaderForContext(true, nil)
	scenarios, err := loader.LoadScenarios(actualPath)
	if err != nil {
		// Return detailed validation error
		return mcp.NewToolResultError(fmt.Sprintf("YAML validation failed: %v", err)), nil
	}

	// If loading succeeded, create validation report
	type ScenarioValidation struct {
		Name         string   `json:"name"`
		Valid        bool     `json:"valid"`
		Errors       []string `json:"errors,omitempty"`
		Warnings     []string `json:"warnings,omitempty"`
		StepCount    int      `json:"step_count"`
		CleanupCount int      `json:"cleanup_count"`
	}

	type ValidationResult struct {
		ValidationType string               `json:"validation_type"`
		Valid          bool                 `json:"valid"`
		ScenarioCount  int                  `json:"scenario_count"`
		Scenarios      []ScenarioValidation `json:"scenarios"`
		Path           string               `json:"path"`
	}

	result := ValidationResult{
		ValidationType: "yaml_structure",
		Valid:          true,
		ScenarioCount:  len(scenarios),
		Path:           scenarioPath,
		Scenarios:      make([]ScenarioValidation, len(scenarios)),
	}

	for i, scenario := range scenarios {
		validation := ScenarioValidation{
			Name:         scenario.Name,
			Valid:        true,
			StepCount:    len(scenario.Steps),
			CleanupCount: len(scenario.Cleanup),
		}

		// Perform additional validations
		var errors []string
		var warnings []string

		// Check for empty description
		if scenario.Description == "" {
			warnings = append(warnings, "Missing description")
		}

		// Check for steps without descriptions
		for j, step := range scenario.Steps {
			if step.Description == "" {
				warnings = append(warnings, fmt.Sprintf("Step %d (%s) missing description", j+1, step.ID))
			}
		}

		// Check for missing timeouts on long scenarios
		if len(scenario.Steps) > 5 && scenario.Timeout == 0 {
			warnings = append(warnings, "Consider adding timeout for scenario with many steps")
		}

		validation.Errors = errors
		validation.Warnings = warnings

		if len(errors) > 0 {
			validation.Valid = false
			result.Valid = false
		}

		result.Scenarios[i] = validation
	}

	jsonData, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to format validation result: %v", err)), nil
	}

	return mcp.NewToolResultText(string(jsonData)), nil
}

// handleAPISchemaValidation performs API schema validation using the shared validation logic
func (t *TestMCPServer) handleAPISchemaValidation(ctx context.Context, scenarioPath, schemaPath string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	// Load the schema using shared logic
	schema, err := testing.LoadSchemaFromFile(schemaPath)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to load schema from %s: %v", schemaPath, err)), nil
	}

	// Create test configuration for filtering
	testConfig := testing.TestConfiguration{
		Verbose: true, // MCP server mode defaults to verbose
		Debug:   t.debug,
	}

	// Apply category filter
	if category, ok := args["category"].(string); ok && category != "" {
		switch category {
		case "behavioral":
			testConfig.Category = testing.CategoryBehavioral
		case "integration":
			testConfig.Category = testing.CategoryIntegration
		default:
			return mcp.NewToolResultError(fmt.Sprintf("Invalid category '%s'", category)), nil
		}
	}

	// Apply concept filter
	if concept, ok := args["concept"].(string); ok && concept != "" {
		switch concept {
		case "serviceclass":
			testConfig.Concept = testing.ConceptServiceClass
		case "workflow":
			testConfig.Concept = testing.ConceptWorkflow
		case "mcpserver":
			testConfig.Concept = testing.ConceptMCPServer
		case "capability":
			testConfig.Concept = testing.ConceptCapability
		case "service":
			testConfig.Concept = testing.ConceptService
		default:
			return mcp.NewToolResultError(fmt.Sprintf("Invalid concept '%s'", concept)), nil
		}
	}

	// Use scenario path if provided, otherwise use config path or default
	if scenarioPath != "" {
		testConfig.ConfigPath = scenarioPath
	}

	// Load test scenarios using the existing unified approach
	scenarios, err := testing.LoadAndFilterScenarios(testConfig.ConfigPath, testConfig, nil)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to load test scenarios: %v", err)), nil
	}

	if len(scenarios) == 0 {
		return mcp.NewToolResultText("No test scenarios found matching the criteria"), nil
	}

	// Validate scenarios against the schema using shared logic
	validationResults := testing.ValidateScenariosAgainstSchema(scenarios, schema, testConfig.Verbose, testConfig.Debug)

	// Add validation type to distinguish from YAML validation
	resultMap := map[string]interface{}{
		"validation_type": "api_schema",
		"schema_path":     schemaPath,
	}

	// Convert validation results to map and merge
	jsonData, err := json.Marshal(validationResults)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to marshal validation results: %v", err)), nil
	}

	var validationMap map[string]interface{}
	if err := json.Unmarshal(jsonData, &validationMap); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to unmarshal validation results: %v", err)), nil
	}

	// Merge the validation type info
	for k, v := range validationMap {
		resultMap[k] = v
	}

	// Format final results as JSON
	finalData, err := json.MarshalIndent(resultMap, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to format validation results: %v", err)), nil
	}

	return mcp.NewToolResultText(string(finalData)), nil
}

// handleGetResults handles the test_get_results MCP tool
func (t *TestMCPServer) handleGetResults(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Try to get results from structured reporter first
	if structuredReporter, ok := t.testReporter.(interface {
		GetResultsAsJSON() (string, error)
	}); ok {
		jsonData, err := structuredReporter.GetResultsAsJSON()
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Failed to get structured results: %v", err)), nil
		}
		return mcp.NewToolResultText(jsonData), nil
	}

	// Fallback to lastResult if structured reporter is not available
	if t.lastResult == nil {
		return mcp.NewToolResultText("No test results available. Run test_run_scenarios first."), nil
	}

	// Format the last result as JSON
	jsonData, err := json.MarshalIndent(t.lastResult, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to format test results: %v", err)), nil
	}

	return mcp.NewToolResultText(string(jsonData)), nil
}
