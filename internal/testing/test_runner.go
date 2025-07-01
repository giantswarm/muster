package testing

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// testRunner implements the TestRunner interface
type testRunner struct {
	client          MCPTestClient
	loader          TestScenarioLoader
	reporter        TestReporter
	instanceManager MusterInstanceManager
	debug           bool
	logger          TestLogger
}

// NewTestRunner creates a new test runner
func NewTestRunner(client MCPTestClient, loader TestScenarioLoader, reporter TestReporter, instanceManager MusterInstanceManager, debug bool) TestRunner {
	return &testRunner{
		client:          client,
		loader:          loader,
		reporter:        reporter,
		instanceManager: instanceManager,
		debug:           debug,
		logger:          NewStdoutLogger(false, debug), // Default to stdout logger
	}
}

// NewTestRunnerWithLogger creates a new test runner with custom logger
func NewTestRunnerWithLogger(client MCPTestClient, loader TestScenarioLoader, reporter TestReporter, instanceManager MusterInstanceManager, debug bool, logger TestLogger) TestRunner {
	return &testRunner{
		client:          client,
		loader:          loader,
		reporter:        reporter,
		instanceManager: instanceManager,
		debug:           debug,
		logger:          logger,
	}
}

// Run executes test scenarios according to the configuration
func (r *testRunner) Run(ctx context.Context, config TestConfiguration, scenarios []TestScenario) (*TestSuiteResult, error) {
	// Create the test suite result
	result := &TestSuiteResult{
		StartTime:       time.Now(),
		TotalScenarios:  len(scenarios),
		ScenarioResults: make([]TestScenarioResult, 0, len(scenarios)),
		Configuration:   config,
	}

	// Report test start
	r.reporter.ReportStart(config)

	// Filter scenarios based on configuration
	filteredScenarios := r.loader.FilterScenarios(scenarios, config)
	result.TotalScenarios = len(filteredScenarios)

	if len(filteredScenarios) == 0 {
		r.reporter.ReportSuiteResult(*result)
		return result, nil
	}

	// Execute scenarios based on parallel configuration
	// Each scenario now manages its own muster instance
	if config.Parallel <= 1 {
		// Sequential execution
		r.reporter.SetParallelMode(false)
		for _, scenario := range filteredScenarios {
			scenarioResult := r.runScenario(ctx, scenario, config)
			result.ScenarioResults = append(result.ScenarioResults, scenarioResult)

			// Update counters
			r.updateCounters(result, scenarioResult)

			// Report individual scenario result
			r.reporter.ReportScenarioResult(scenarioResult)

			// Check fail-fast
			if config.FailFast && scenarioResult.Result == ResultFailed {
				break
			}
		}
	} else {
		// Parallel execution
		r.reporter.SetParallelMode(true)
		results := r.runScenariosParallel(ctx, filteredScenarios, config, result)
		result.ScenarioResults = results
	}

	// Finalize result
	result.EndTime = time.Now()
	result.Duration = result.EndTime.Sub(result.StartTime)

	// Report final suite result
	r.reporter.ReportSuiteResult(*result)

	return result, nil
}

// runScenariosParallel executes scenarios in parallel with a worker pool
// Each scenario gets its own muster instance
func (r *testRunner) runScenariosParallel(ctx context.Context, scenarios []TestScenario, config TestConfiguration, suiteResult *TestSuiteResult) []TestScenarioResult {
	// Create channels
	scenarioChan := make(chan TestScenario, len(scenarios))
	resultChan := make(chan TestScenarioResult, len(scenarios))

	// Send scenarios to channel
	for _, scenario := range scenarios {
		scenarioChan <- scenario
	}
	close(scenarioChan)

	// Create worker pool
	var wg sync.WaitGroup
	numWorkers := config.Parallel
	if numWorkers > len(scenarios) {
		numWorkers = len(scenarios)
	}

	// Start workers
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			for scenario := range scenarioChan {
				if r.debug {
					r.logger.Debug("🔄 Worker %d executing scenario: %s\n", workerID, scenario.Name)
				}

				// Each worker runs scenario with its own muster instance
				scenarioResult := r.runScenario(ctx, scenario, config)
				resultChan <- scenarioResult
			}
		}(i)
	}

	// Wait for all workers to complete
	go func() {
		wg.Wait()
		close(resultChan)
	}()

	// Collect results and handle fail-fast in main thread
	var results []TestScenarioResult
	expectedResults := len(scenarios)

	for result := range resultChan {
		results = append(results, result)

		// 🚀 REAL-TIME: Report result immediately as it comes in
		r.updateCounters(suiteResult, result)
		r.reporter.ReportScenarioResult(result)

		// Handle fail-fast by breaking out of collection loop
		// This allows workers to finish naturally without deadlocking
		if config.FailFast && result.Result == ResultFailed {
			if r.debug {
				r.logger.Debug("🛑 Fail-fast triggered by scenario: %s\n", result.Scenario.Name)
			}
			break
		}
	}

	// If we broke early due to fail-fast, continue collecting remaining results
	// but don't process them (just let workers finish cleanly)
	if len(results) < expectedResults {
		for result := range resultChan {
			// Collect remaining results but don't report them in fail-fast mode
			results = append(results, result)
			if r.debug {
				r.logger.Debug("📋 Collected remaining result: %s (not reported due to fail-fast)\n", result.Scenario.Name)
			}
		}
	}

	return results
}

// collectInstanceLogs collects logs from an muster instance and stores them in the result
func (r *testRunner) collectInstanceLogs(instance *MusterInstance, result *TestScenarioResult) {
	if instance == nil {
		return
	}

	// Get the managed process to collect logs
	if manager, ok := r.instanceManager.(*musterInstanceManager); ok {
		manager.mu.RLock()
		if managedProc, exists := manager.processes[instance.ID]; exists && managedProc != nil && managedProc.logCapture != nil {
			// Get logs without closing the capture yet (defer will handle that)
			instance.Logs = managedProc.logCapture.getLogs()
			result.InstanceLogs = instance.Logs
			if r.debug {
				r.logger.Debug("📋 Collected instance logs for result: stdout=%d chars, stderr=%d chars\n",
					len(instance.Logs.Stdout), len(instance.Logs.Stderr))
			}
		}
		manager.mu.RUnlock()
	}
}

// runScenario executes a single test scenario with template variable support
func (r *testRunner) runScenario(ctx context.Context, scenario TestScenario, config TestConfiguration) TestScenarioResult {
	result := TestScenarioResult{
		Scenario:    scenario,
		StartTime:   time.Now(),
		StepResults: make([]TestStepResult, 0, len(scenario.Steps)),
		Result:      ResultPassed,
	}

	// Report scenario start
	r.reporter.ReportScenarioStart(scenario)

	// Create scenario context for template variable support
	scenarioContext := NewScenarioContext()

	// Apply scenario timeout if specified
	scenarioCtx := ctx
	if scenario.Timeout > 0 {
		var cancel context.CancelFunc
		scenarioCtx, cancel = context.WithTimeout(ctx, scenario.Timeout)
		defer cancel()
	}

	// Create and start muster instance for this scenario
	var instance *MusterInstance
	var err error

	if r.debug {
		r.logger.Debug("🏗️  Creating muster instance for scenario: %s\n", scenario.Name)
	}

	instance, err = r.instanceManager.CreateInstance(scenarioCtx, scenario.Name, scenario.PreConfiguration)
	if err != nil {
		result.Result = ResultError
		result.Error = fmt.Sprintf("failed to create muster instance: %v", err)
		result.EndTime = time.Now()
		result.Duration = result.EndTime.Sub(result.StartTime)

		r.collectInstanceLogs(instance, &result)

		return result
	}

	if r.debug {
		r.logger.Debug("✅ Created muster instance %s (port: %d)\n", instance.ID, instance.Port)
	}

	// Ensure cleanup of instance
	defer func() {
		if instance != nil {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			if err := r.instanceManager.DestroyInstance(cleanupCtx, instance); err != nil {
				if r.debug {
					r.logger.Debug("⚠️  Failed to destroy muster instance %s: %v\n", instance.ID, err)
				}
			} else {
				// Final log storage - may have been updated during destruction
				if instance.Logs != nil && result.InstanceLogs == nil {
					result.InstanceLogs = instance.Logs
				}
				if r.debug {
					r.logger.Debug("✅ Cleanup complete for muster instance %s\n", instance.ID)
				}
			}
		}
	}()

	// Wait for instance to be ready
	if err := r.instanceManager.WaitForReady(scenarioCtx, instance); err != nil {
		result.Result = ResultError
		result.Error = fmt.Sprintf("muster instance not ready: %v", err)
		result.EndTime = time.Now()
		result.Duration = result.EndTime.Sub(result.StartTime)

		r.collectInstanceLogs(instance, &result)

		return result
	}

	// Create isolated MCP client for this scenario
	// This ensures each parallel scenario has its own client and context
	scenarioClient := NewMCPTestClientWithLogger(r.debug, r.logger)

	// Connect the isolated MCP client to this specific instance
	if err := scenarioClient.Connect(scenarioCtx, instance.Endpoint); err != nil {
		result.Result = ResultError
		result.Error = fmt.Sprintf("failed to connect to muster instance: %v", err)
		result.EndTime = time.Now()
		result.Duration = result.EndTime.Sub(result.StartTime)

		r.collectInstanceLogs(instance, &result)

		return result
	}

	// Ensure isolated MCP client is closed properly
	defer func() {
		if r.debug {
			r.logger.Debug("🔌 Closing isolated MCP client connection to %s\n", instance.Endpoint)
		}

		// Close with timeout to avoid hanging
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer closeCancel()

		done := make(chan struct{})
		go func() {
			scenarioClient.Close()
			close(done)
		}()

		select {
		case <-done:
			if r.debug {
				r.logger.Debug("✅ Isolated MCP client closed successfully\n")
			}
		case <-closeCtx.Done():
			if r.debug {
				r.logger.Debug("⏰ Isolated MCP client close timeout - connection may have been reset\n")
			}
		}

		// Give a small delay to ensure close request is processed
		time.Sleep(100 * time.Millisecond)
	}()

	if r.debug {
		r.logger.Debug("✅ Connected isolated MCP client to muster instance %s at %s\n", instance.ID, instance.Endpoint)
	}

	// Execute steps using the isolated client
	for _, step := range scenario.Steps {
		stepResult := r.runStep(scenarioCtx, step, config, scenarioClient, scenarioContext)
		result.StepResults = append(result.StepResults, stepResult)

		// Report step result
		r.reporter.ReportStepResult(stepResult)

		// Check if step failed
		if stepResult.Result == ResultFailed || stepResult.Result == ResultError {
			result.Result = stepResult.Result
			result.Error = stepResult.Error
			break
		}
	}

	// Execute cleanup steps regardless of main scenario outcome using the isolated client
	if len(scenario.Cleanup) > 0 {
		for _, cleanupStep := range scenario.Cleanup {
			stepResult := r.runStep(scenarioCtx, cleanupStep, config, scenarioClient, scenarioContext)
			result.StepResults = append(result.StepResults, stepResult)
			r.reporter.ReportStepResult(stepResult)

			// Cleanup step failures should also fail the scenario
			if stepResult.Result == ResultFailed || stepResult.Result == ResultError {
				// Only update if the scenario hasn't already failed
				if result.Result == ResultPassed {
					result.Result = stepResult.Result
					result.Error = stepResult.Error
				}
			}
		}
	}

	// Finalize result - collect instance logs before ending
	result.EndTime = time.Now()
	result.Duration = result.EndTime.Sub(result.StartTime)

	// Collect instance logs by triggering the destroy process early
	// The defer cleanup will handle the actual cleanup, but we need logs now
	r.collectInstanceLogs(instance, &result)

	return result
}

// runStep executes a single test step using the specified MCP client with template variable support
func (r *testRunner) runStep(ctx context.Context, step TestStep, config TestConfiguration, client MCPTestClient, scenarioContext *ScenarioContext) TestStepResult {
	result := TestStepResult{
		Step:      step,
		StartTime: time.Now(),
		Result:    ResultPassed,
	}

	// Apply step timeout if specified
	stepCtx := ctx
	if step.Timeout > 0 {
		var cancel context.CancelFunc
		stepCtx, cancel = context.WithTimeout(ctx, step.Timeout)
		defer cancel()
	}

	// Resolve template variables in step arguments if scenario context is available
	resolvedArgs := step.Args
	if scenarioContext != nil {
		processor := NewTemplateProcessor(scenarioContext)

		var err error
		resolvedArgs, err = processor.ResolveArgs(step.Args)
		if err != nil {
			result.Result = ResultError
			result.Error = fmt.Sprintf("template resolution failed: %v", err)
			result.EndTime = time.Now()
			result.Duration = result.EndTime.Sub(result.StartTime)
			return result
		}

		if r.debug {
			r.logger.Debug("🔧 Step %s: Template resolution completed\n", step.ID)
		}
	}

	// Execute the tool call with resolved arguments
	response, err := client.CallTool(stepCtx, step.Tool, resolvedArgs)

	// Store response (even if there's an error)
	result.Response = response
	result.EndTime = time.Now()
	result.Duration = result.EndTime.Sub(result.StartTime)

	// Store result if requested and scenario context is available
	if step.Store != "" && scenarioContext != nil && response != nil {
		// Extract actual data from MCP CallToolResult for template variable access
		storableResult := r.extractStorableResult(response)
		scenarioContext.StoreResult(step.Store, storableResult)

		if r.debug {
			r.logger.Debug("💾 Step %s: Stored result as '%s': %v\n", step.ID, step.Store, storableResult)
		}
	}

	// Validate expectations (always check, even with errors - they might be expected)
	if !r.validateExpectationsWithClient(stepCtx, step.Expected, response, err, client, step.Tool, resolvedArgs) {
		if err != nil {
			result.Result = ResultError
			result.Error = fmt.Sprintf("tool call failed: %v", err)
		} else {
			result.Result = ResultFailed
			result.Error = "step expectations not met"
		}
		return result
	}

	// Success - expectations met, even if there was an error
	result.Result = ResultPassed

	return result
}

// validateExpectationsWithClient checks if the step response meets the expected criteria with state waiting support
func (r *testRunner) validateExpectationsWithClient(ctx context.Context, expected TestExpectation, response interface{}, err error, client MCPTestClient, stepTool string, stepArgs map[string]interface{}) bool {
	// Handle state waiting if configured
	if expected.WaitForState > 0 {
		if r.debug {
			r.logger.Debug("⏳ State waiting configured - polling for expected state\n")
		}

		// Use the configured timeout
		timeout := expected.WaitForState
		pollInterval := 1 * time.Second // Default poll interval

		// Start polling with timeout
		waitCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		pollTicker := time.NewTicker(pollInterval)
		defer pollTicker.Stop()

		if r.debug {
			r.logger.Debug("🔄 Starting state polling: tool=%s, timeout=%v, interval=%v\n", stepTool, timeout, pollInterval)
		}

		// Poll for expected state
		for {
			select {
			case <-waitCtx.Done():
				if r.debug {
					r.logger.Debug("⏰ State waiting timeout reached\n")
				}
				return false // Timeout reached without achieving expected state

			case <-pollTicker.C:
				// Make status call using the polling tool and args
				response, err := client.CallTool(waitCtx, stepTool, stepArgs)

				if r.debug {
					r.logger.Debug("📊 Status poll result: error=%v\n", err)
				}

				// Check if the status call succeeded and meets JSON path expectations
				if err == nil {
					if r.validateExpectations(expected, response, nil) {
						if r.debug {
							r.logger.Debug("✅ Expected state achieved!\n")
						}
						return true
					} else {
						if r.debug {
							r.logger.Debug("🔄 State not yet achieved, continuing to poll...\n")
						}
					}
				}
			}
		}
	}

	// Continue with normal validation using original response and error
	return r.validateExpectations(expected, response, err)
}

// validateExpectations checks if the step response meets the expected criteria
func (r *testRunner) validateExpectations(expected TestExpectation, response interface{}, err error) bool {
	// Check if response indicates an error (for MCP responses)
	isResponseError := false
	if response != nil {
		// Check if this is a CallToolResult
		mcpResult, ok := response.(*mcp.CallToolResult)
		if ok {
			isResponseError = mcpResult.IsError
		}
	}

	// Check success expectation
	if expected.Success && (err != nil || isResponseError) {
		if r.debug {
			if err != nil {
				r.logger.Debug("❌ Expected success but got error: %v\n", err)
			} else {
				r.logger.Debug("❌ Expected success but response indicates error\n")
			}
		}
		return false
	}

	if !expected.Success && err == nil && !isResponseError {
		if r.debug {
			r.logger.Debug("❌ Expected failure but got success (no error and no response error flag)\n")
		}
		return false
	}

	// Check error content expectations
	if len(expected.ErrorContains) > 0 {
		var errorText string

		// First, try to get error text from Go error
		if err != nil {
			errorText = err.Error()
		} else if isResponseError && response != nil {
			// If no Go error but response indicates error, extract error text from response
			if mcpResult, ok := response.(*mcp.CallToolResult); ok {
				// Extract text from MCP result content
				for _, content := range mcpResult.Content {
					if textContent, ok := mcp.AsTextContent(content); ok {
						errorText += textContent.Text + " "
					}
				}
				errorText = strings.TrimSpace(errorText)
			} else {
				responseStr := fmt.Sprintf("%v", response)
				errorText = responseStr
			}
		}

		if errorText == "" {
			if r.debug {
				r.logger.Debug("❌ Expected error containing text but got no error text (err=%v, isResponseError=%v)", err, isResponseError)
			}
			return false
		}

		for _, expectedText := range expected.ErrorContains {
			if !containsText(errorText, expectedText) {
				if r.debug {
					r.logger.Debug("❌ Error text '%s' does not contain expected text '%s'", errorText, expectedText)
				}
				return false
			}
		}

		if r.debug {
			r.logger.Debug("✅ Error expectations met: found all expected text in '%s'", errorText)
		}
	}

	// Check response content expectations
	if response != nil {
		var responseStr string
		if mcpResult, ok := response.(*mcp.CallToolResult); ok {
			// Extract text content from MCP result for text matching
			var textParts []string
			for _, content := range mcpResult.Content {
				if textContent, ok := mcp.AsTextContent(content); ok {
					textParts = append(textParts, textContent.Text)
				}
			}
			responseStr = strings.Join(textParts, " ")
		} else {
			responseStr = fmt.Sprintf("%v", response)
		}

		// Check contains expectations
		for _, expectedText := range expected.Contains {
			if !containsText(responseStr, expectedText) {
				if r.debug {
					r.logger.Debug("❌ Response does not contain expected text '%s'\n", expectedText)
				}
				return false
			}
		}

		// Check not contains expectations
		for _, unexpectedText := range expected.NotContains {
			if containsText(responseStr, unexpectedText) {
				if r.debug {
					r.logger.Debug("❌ Response contains unexpected text '%s'\n", unexpectedText)
				}
				return false
			}
		}

		// Check JSON path expectations
		if len(expected.JSONPath) > 0 {
			// Parse response as JSON-like map
			var responseMap map[string]interface{}

			// Handle different response types
			if respMap, ok := response.(map[string]interface{}); ok {
				responseMap = respMap
			} else {
				// Try to extract JSON from MCP CallToolResult structure
				responseMap = r.extractJSONFromMCPResponse(response)
				if responseMap == nil {
					if r.debug {
						r.logger.Debug("❌ JSON path validation failed: could not extract JSON from response type %T\n", response)
					}
					return false
				}
			}

			// Check each JSON path expectation
			for jsonPath, expectedValue := range expected.JSONPath {
				actualValue, exists := responseMap[jsonPath]
				if !exists {
					if r.debug {
						r.logger.Debug("❌ JSON path '%s' not found in response\n", jsonPath)
					}
					return false
				}

				// Compare values
				if !compareValues(actualValue, expectedValue) {
					if r.debug {
						r.logger.Debug("❌ JSON path '%s': expected %v, got %v\n", jsonPath, expectedValue, actualValue)
					}
					return false
				}

				if r.debug {
					r.logger.Debug("✅ JSON path '%s': expected %v, got %v ✓\n", jsonPath, expectedValue, actualValue)
				}
			}
		}
	}

	if r.debug {
		r.logger.Debug("✅ All expectations met for step\n")
	}

	return true
}

// containsText checks if text contains the expected substring (case-insensitive)
func containsText(text, expected string) bool {
	// Simple case-insensitive contains check
	// In production, this could be more sophisticated
	return len(text) >= len(expected) &&
		containsSubstring(text, expected)
}

// containsSubstring performs case-insensitive substring search
func containsSubstring(text, substr string) bool {
	if len(substr) == 0 {
		return true
	}
	if len(text) < len(substr) {
		return false
	}

	// Convert to lowercase for case-insensitive comparison
	textLower := toLower(text)
	substrLower := toLower(substr)

	for i := 0; i <= len(textLower)-len(substrLower); i++ {
		if textLower[i:i+len(substrLower)] == substrLower {
			return true
		}
	}
	return false
}

// toLower converts string to lowercase
func toLower(s string) string {
	result := make([]byte, len(s))
	for i, b := range []byte(s) {
		if b >= 'A' && b <= 'Z' {
			result[i] = b + 32
		} else {
			result[i] = b
		}
	}
	return string(result)
}

// extractJSONFromMCPResponse attempts to extract JSON from MCP CallToolResult
func (r *testRunner) extractJSONFromMCPResponse(response interface{}) map[string]interface{} {
	// Handle MCP CallToolResult structure properly
	if mcpResult, ok := response.(*mcp.CallToolResult); ok {
		// Extract text content from MCP result
		for _, content := range mcpResult.Content {
			if textContent, ok := mcp.AsTextContent(content); ok {
				// Try to parse the text content as JSON
				var result map[string]interface{}
				if err := json.Unmarshal([]byte(textContent.Text), &result); err == nil {
					if r.debug {
						r.logger.Debug("🔍 Successfully extracted JSON from MCP response: %+v\n", result)
					}
					return result
				} else {
					if r.debug {
						r.logger.Debug("🔍 Failed to parse MCP text content as JSON: %v\n", err)
						r.logger.Debug("🔍 Text content was: %s\n", textContent.Text)
					}
				}
			}
		}
	}

	// Try to handle other response types
	if respMap, ok := response.(map[string]interface{}); ok {
		return respMap
	}

	if r.debug {
		r.logger.Debug("🔍 Could not extract JSON from response type %T\n", response)
	}
	return nil
}

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// compareValues compares two values for equality, handling type conversions
func compareValues(actual, expected interface{}) bool {
	// Handle nil cases first
	if actual == nil || expected == nil {
		return actual == expected
	}

	// Handle slice/array comparisons to prevent panic
	actualVal := reflect.ValueOf(actual)
	expectedVal := reflect.ValueOf(expected)

	// Check if both are slices or arrays
	if actualVal.Kind() == reflect.Slice || actualVal.Kind() == reflect.Array {
		if expectedVal.Kind() == reflect.Slice || expectedVal.Kind() == reflect.Array {
			// Compare lengths first
			if actualVal.Len() != expectedVal.Len() {
				return false
			}

			// Compare each element
			for i := 0; i < actualVal.Len(); i++ {
				actualItem := actualVal.Index(i).Interface()
				expectedItem := expectedVal.Index(i).Interface()
				if !compareValues(actualItem, expectedItem) {
					return false
				}
			}
			return true
		}
		// One is slice/array, other is not - not equal
		return false
	}

	// Handle map comparisons
	if actualVal.Kind() == reflect.Map && expectedVal.Kind() == reflect.Map {
		actualKeys := actualVal.MapKeys()
		expectedKeys := expectedVal.MapKeys()

		// Compare number of keys
		if len(actualKeys) != len(expectedKeys) {
			return false
		}

		// Check each key-value pair
		for _, key := range expectedKeys {
			actualValue := actualVal.MapIndex(key)
			expectedValue := expectedVal.MapIndex(key)

			if !actualValue.IsValid() {
				return false // Key doesn't exist in actual
			}

			if !compareValues(actualValue.Interface(), expectedValue.Interface()) {
				return false
			}
		}
		return true
	}

	// Direct equality check for comparable types (but not slices/arrays)
	if actualVal.Type().Comparable() && expectedVal.Type().Comparable() {
		if actual == expected {
			return true
		}
	}

	// Handle boolean comparisons
	if expectedBool, ok := expected.(bool); ok {
		if actualBool, ok := actual.(bool); ok {
			return actualBool == expectedBool
		}
		// Convert string to bool if needed
		if actualStr, ok := actual.(string); ok {
			if actualStr == "true" {
				return expectedBool == true
			}
			if actualStr == "false" {
				return expectedBool == false
			}
		}
	}

	// Handle string comparisons
	if expectedStr, ok := expected.(string); ok {
		if actualStr, ok := actual.(string); ok {
			return actualStr == expectedStr
		}
		// Convert other types to string for comparison
		actualStr := fmt.Sprintf("%v", actual)
		return actualStr == expectedStr
	}

	// Handle numeric comparisons (int, float64, etc.)
	if expectedFloat, ok := expected.(float64); ok {
		if actualFloat, ok := actual.(float64); ok {
			return actualFloat == expectedFloat
		}
		if actualInt, ok := actual.(int); ok {
			return float64(actualInt) == expectedFloat
		}
	}

	if expectedInt, ok := expected.(int); ok {
		if actualInt, ok := actual.(int); ok {
			return actualInt == expectedInt
		}
		if actualFloat, ok := actual.(float64); ok {
			return actualFloat == float64(expectedInt)
		}
	}

	// For other types, convert both to strings and compare
	actualStr := fmt.Sprintf("%v", actual)
	expectedStr := fmt.Sprintf("%v", expected)
	return actualStr == expectedStr
}

// updateCounters updates the result counters based on a scenario result
func (r *testRunner) updateCounters(suiteResult *TestSuiteResult, scenarioResult TestScenarioResult) {
	switch scenarioResult.Result {
	case ResultPassed:
		suiteResult.PassedScenarios++
	case ResultFailed:
		suiteResult.FailedScenarios++
	case ResultSkipped:
		suiteResult.SkippedScenarios++
	case ResultError:
		suiteResult.ErrorScenarios++
	}
}

// extractStorableResult extracts a storable result from a response
func (r *testRunner) extractStorableResult(response interface{}) interface{} {
	// Handle different response types
	switch resp := response.(type) {
	case *mcp.CallToolResult:
		// Extract text content from MCP result
		var textParts []string
		for _, content := range resp.Content {
			if textContent, ok := mcp.AsTextContent(content); ok {
				textParts = append(textParts, textContent.Text)
			}
		}

		if len(textParts) == 0 {
			return response // Return original if no text content
		}

		// Join all text parts
		combinedText := strings.Join(textParts, " ")

		// Try to parse as JSON first
		var jsonResult interface{}
		if err := json.Unmarshal([]byte(combinedText), &jsonResult); err == nil {
			// Successfully parsed as JSON, return the structured data
			if r.debug {
				r.logger.Debug("🔍 Extracted JSON result for template variables: %v\n", jsonResult)
			}
			return jsonResult
		}

		// If not JSON, return as string
		if r.debug {
			r.logger.Debug("🔍 Extracted text result for template variables: %s\n", combinedText)
		}
		return combinedText
	default:
		// For other response types, return as-is
		return response
	}
}
