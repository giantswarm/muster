package testing

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// testReporter implements the TestReporter interface
type testReporter struct {
	verbose         bool
	debug           bool
	reportPath      string
	parallelMode    bool              // NEW: Track if we're in parallel mode
	scenarioBuffers map[string]string // NEW: Buffer scenario start messages for parallel execution
	bufferMutex     sync.RWMutex      // NEW: Protect scenarioBuffers from concurrent access
}

// NewTestReporter creates a new test reporter
func NewTestReporter(verbose, debug bool, reportPath string) TestReporter {
	return &testReporter{
		verbose:         verbose,
		debug:           debug,
		reportPath:      reportPath,
		parallelMode:    false,
		scenarioBuffers: make(map[string]string),
	}
}

// SetParallelMode enables or disables parallel output buffering
func (r *testReporter) SetParallelMode(parallel bool) {
	r.bufferMutex.Lock()
	defer r.bufferMutex.Unlock()

	r.parallelMode = parallel
	if parallel {
		r.scenarioBuffers = make(map[string]string)
	}
}

// ReportStart is called when test execution begins
func (r *testReporter) ReportStart(config TestConfiguration) {
	fmt.Printf("🧪 Starting muster Test Framework\n")
	fmt.Printf("🏗️  Using managed muster instances (base port: %d)\n", config.BasePort)

	if r.verbose {
		fmt.Printf("\n⚙️  Configuration:\n")
		fmt.Printf("   • Category: %s\n", r.stringOrDefault(string(config.Category), "all"))
		fmt.Printf("   • Concept: %s\n", r.stringOrDefault(string(config.Concept), "all"))
		fmt.Printf("   • Scenario: %s\n", r.stringOrDefault(config.Scenario, "all"))
		fmt.Printf("   • Parallel workers: %d\n", config.Parallel)
		fmt.Printf("   • Fail fast: %t\n", config.FailFast)
		fmt.Printf("   • Debug mode: %t\n", r.debug)
		fmt.Printf("   • Verbose mode: %t\n", r.verbose)
		fmt.Printf("   • Timeout: %v\n", config.Timeout)
		fmt.Printf("   • Base port: %d\n", config.BasePort)
		if config.ConfigPath != "" {
			fmt.Printf("   • Config path: %s\n", config.ConfigPath)
		}
		if config.ReportPath != "" {
			fmt.Printf("   • Report path: %s\n", config.ReportPath)
		}
		fmt.Printf("\n")
	}
}

// ReportScenarioStart is called when a scenario begins
func (r *testReporter) ReportScenarioStart(scenario TestScenario) {
	if r.verbose {
		fmt.Printf("🎯 Starting scenario: %s (%s/%s)\n",
			scenario.Name, scenario.Category, scenario.Concept)
		if scenario.Description != "" {
			fmt.Printf("   📝 Description: %s\n", scenario.Description)
		}
		if len(scenario.Tags) > 0 {
			fmt.Printf("   🏷️  Tags: %s\n", strings.Join(scenario.Tags, ", "))
		}
		fmt.Printf("   📋 Steps: %d\n", len(scenario.Steps))
		if len(scenario.Cleanup) > 0 {
			fmt.Printf("   🧹 Cleanup steps: %d\n", len(scenario.Cleanup))
		}
		if scenario.Timeout > 0 {
			fmt.Printf("   ⏱️  Timeout: %v\n", scenario.Timeout)
		}

		// Show pre-configuration details
		if scenario.PreConfiguration != nil {
			fmt.Printf("   🔧 Pre-configuration:\n")

			if len(scenario.PreConfiguration.MCPServers) > 0 {
				fmt.Printf("      📡 MCP Servers (%d):\n", len(scenario.PreConfiguration.MCPServers))
				for _, server := range scenario.PreConfiguration.MCPServers {
					fmt.Printf("         • %s\n", server.Name)
					// Check for tools in config map (for mock servers)
					if server.Config != nil {
						if tools, exists := server.Config["tools"]; exists {
							if toolsList, ok := tools.([]interface{}); ok {
								fmt.Printf("           🔧 Tools (%d): ", len(toolsList))
								var toolNames []string
								for _, tool := range toolsList {
									if toolMap, ok := tool.(map[string]interface{}); ok {
										if name, exists := toolMap["name"]; exists {
											if nameStr, ok := name.(string); ok {
												toolNames = append(toolNames, nameStr)
											}
										}
									}
								}
								fmt.Printf("%s\n", strings.Join(toolNames, ", "))
							}
						}
					}
				}
			}

			if len(scenario.PreConfiguration.ServiceClasses) > 0 {
				fmt.Printf("      🏗️  Service Classes (%d):\n", len(scenario.PreConfiguration.ServiceClasses))
				for _, sc := range scenario.PreConfiguration.ServiceClasses {
					fmt.Printf("         • %s", sc.Name)
					if sc.Config != nil {
						if version, exists := sc.Config["version"]; exists {
							if versionStr, ok := version.(string); ok && versionStr != "" {
								fmt.Printf(" (v%s)", versionStr)
							}
						}
						if scType, exists := sc.Config["type"]; exists {
							if typeStr, ok := scType.(string); ok && typeStr != "" {
								fmt.Printf(" [%s]", typeStr)
							}
						}
					}
					fmt.Printf("\n")
				}
			}

			if len(scenario.PreConfiguration.Workflows) > 0 {
				fmt.Printf("      🔄 Workflows (%d):\n", len(scenario.PreConfiguration.Workflows))
				for _, wf := range scenario.PreConfiguration.Workflows {
					fmt.Printf("         • %s\n", wf.Name)
				}
			}

			if len(scenario.PreConfiguration.Capabilities) > 0 {
				fmt.Printf("      ⚡ Capabilities (%d):\n", len(scenario.PreConfiguration.Capabilities))
				for _, cap := range scenario.PreConfiguration.Capabilities {
					fmt.Printf("         • %s\n", cap.Name)
				}
			}

			if len(scenario.PreConfiguration.Services) > 0 {
				fmt.Printf("      📦 Services (%d):\n", len(scenario.PreConfiguration.Services))
				for _, svc := range scenario.PreConfiguration.Services {
					fmt.Printf("         • %s\n", svc.Name)
				}
			}
		}

		fmt.Printf("\n")
	} else {
		// In parallel mode, buffer the start message instead of printing immediately
		if r.parallelMode {
			r.bufferMutex.Lock()
			r.scenarioBuffers[scenario.Name] = fmt.Sprintf("🎯 %s... ", scenario.Name)
			r.bufferMutex.Unlock()
		} else {
			fmt.Printf("🎯 %s... ", scenario.Name)
		}
	}
}

// ReportStepResult is called when a step completes
func (r *testReporter) ReportStepResult(stepResult TestStepResult) {
	if r.verbose {
		symbol := r.getResultSymbol(stepResult.Result)
		fmt.Printf("   %s Step: %s (%v)\n",
			symbol, stepResult.Step.ID, stepResult.Duration)

		// Show step description if available
		if stepResult.Step.Description != "" {
			fmt.Printf("      📝 Description: %s\n", stepResult.Step.Description)
		}

		// Show tool call details
		fmt.Printf("      🔧 Tool: %s\n", stepResult.Step.Tool)

		// Show arguments if provided
		if stepResult.Step.Args != nil && len(stepResult.Step.Args) > 0 {
			fmt.Printf("      📥 Arguments:\n")
			for key, value := range stepResult.Step.Args {
				// Pretty print complex values
				if valueStr := r.formatValue(value); valueStr != "" {
					fmt.Printf("         • %s: %s\n", key, valueStr)
				}
			}
		}

		// Show retry information
		if stepResult.RetryCount > 0 {
			fmt.Printf("      🔄 Retries: %d\n", stepResult.RetryCount)
		}

		// Show timeout if set
		if stepResult.Step.Timeout > 0 {
			fmt.Printf("      ⏱️  Timeout: %v\n", stepResult.Step.Timeout)
		}

		// Show response details
		if stepResult.Response != nil {
			fmt.Printf("      📤 Response:\n")
			responseStr := r.formatResponse(stepResult.Response)
			if responseStr != "" {
				// Indent the response
				indentedResponse := r.indentText(responseStr, "         ")
				fmt.Printf("%s\n", indentedResponse)
			}
		}

		// Show expectations
		if r.hasExpectations(stepResult.Step.Expected) {
			fmt.Printf("      🎯 Expectations:\n")
			fmt.Printf("         • Success: %t\n", stepResult.Step.Expected.Success)
			if len(stepResult.Step.Expected.Contains) > 0 {
				fmt.Printf("         • Contains: %s\n", strings.Join(stepResult.Step.Expected.Contains, ", "))
			}
			if len(stepResult.Step.Expected.ErrorContains) > 0 {
				fmt.Printf("         • Error contains: %s\n", strings.Join(stepResult.Step.Expected.ErrorContains, ", "))
			}
			if len(stepResult.Step.Expected.NotContains) > 0 {
				fmt.Printf("         • Not contains: %s\n", strings.Join(stepResult.Step.Expected.NotContains, ", "))
			}
			if stepResult.Step.Expected.StatusCode > 0 {
				fmt.Printf("         • Status code: %d\n", stepResult.Step.Expected.StatusCode)
			}
			if len(stepResult.Step.Expected.JSONPath) > 0 {
				fmt.Printf("         • JSON path checks: %d\n", len(stepResult.Step.Expected.JSONPath))
			}
		}

		// Show error details
		if stepResult.Error != "" {
			fmt.Printf("      ❌ Error: %s\n", stepResult.Error)
		}

		fmt.Printf("\n")
	}
}

// ReportScenarioResult is called when a scenario completes
func (r *testReporter) ReportScenarioResult(scenarioResult TestScenarioResult) {
	symbol := r.getResultSymbol(scenarioResult.Result)

	if r.verbose {
		fmt.Printf("%s Scenario completed: %s (%v)\n",
			symbol, scenarioResult.Scenario.Name, scenarioResult.Duration)

		if scenarioResult.Error != "" {
			fmt.Printf("   ❌ Scenario Error: %s\n", scenarioResult.Error)
		}

		// Show detailed step summary
		passed := 0
		failed := 0
		errors := 0
		totalSteps := len(scenarioResult.StepResults)

		for _, stepResult := range scenarioResult.StepResults {
			switch stepResult.Result {
			case ResultPassed:
				passed++
			case ResultFailed:
				failed++
			case ResultError:
				errors++
			}
		}

		fmt.Printf("   📊 Step Summary: %d total", totalSteps)
		if passed > 0 {
			fmt.Printf(", %d ✅ passed", passed)
		}
		if failed > 0 {
			fmt.Printf(", %d ❌ failed", failed)
		}
		if errors > 0 {
			fmt.Printf(", %d 💥 errors", errors)
		}
		fmt.Printf("\n")

		// Show failed steps details
		if failed > 0 || errors > 0 {
			fmt.Printf("   🔍 Failed Steps:\n")
			for _, stepResult := range scenarioResult.StepResults {
				if stepResult.Result == ResultFailed || stepResult.Result == ResultError {
					stepSymbol := r.getResultSymbol(stepResult.Result)
					fmt.Printf("      %s %s: %s\n", stepSymbol, stepResult.Step.ID, stepResult.Error)
				}
			}
		}

		// Show instance logs if available and there were failures
		if r.debug && scenarioResult.InstanceLogs != nil {
			// Show logs in debug mode even for successful scenarios
			fmt.Printf("   📄 Instance Logs (debug mode):\n")
			if scenarioResult.InstanceLogs.Stdout != "" {
				stdout := scenarioResult.InstanceLogs.Stdout
				fmt.Printf("   📤 STDOUT:\n%s\n", r.indentText(stdout, "      "))
			}
			if scenarioResult.InstanceLogs.Stderr != "" {
				stderr := scenarioResult.InstanceLogs.Stderr
				fmt.Printf("   📥 STDERR:\n%s\n", r.indentText(stderr, "      "))
			}
		} else if (failed > 0 || errors > 0) && scenarioResult.InstanceLogs != nil {
			fmt.Printf("   📄 Instance Logs (last execution):\n")
			if scenarioResult.InstanceLogs.Stdout != "" {
				fmt.Printf("   📤 STDOUT:\n")
				stdout := r.trimLogs(scenarioResult.InstanceLogs.Stdout, 1000)
				fmt.Printf("%s\n", r.indentText(stdout, "      "))
			}
			if scenarioResult.InstanceLogs.Stderr != "" {
				fmt.Printf("   📥 STDERR:\n")
				stderr := r.trimLogs(scenarioResult.InstanceLogs.Stderr, 1000)
				fmt.Printf("%s\n", r.indentText(stderr, "      "))
			}
		}

		fmt.Printf("\n")
	} else {
		// In parallel mode, print the complete buffered line
		if r.parallelMode {
			r.bufferMutex.Lock()
			bufferedStart, exists := r.scenarioBuffers[scenarioResult.Scenario.Name]
			if exists {
				// Clean up the buffer entry
				delete(r.scenarioBuffers, scenarioResult.Scenario.Name)
			}
			r.bufferMutex.Unlock()

			if exists {
				fmt.Printf("%s%s (%v)\n", bufferedStart, symbol, scenarioResult.Duration)
			} else {
				// Fallback if buffer missing (shouldn't happen)
				fmt.Printf("🎯 %s... %s (%v)\n", scenarioResult.Scenario.Name, symbol, scenarioResult.Duration)
			}
		} else {
			// Sequential mode - just print the result (start was already printed)
			fmt.Printf("%s (%v)\n", symbol, scenarioResult.Duration)
		}
	}
}

// trimLogs trims logs to a reasonable length for display
func (r *testReporter) trimLogs(logs string, maxChars int) string {
	if len(logs) <= maxChars {
		return logs
	}

	// Try to break at a reasonable line boundary
	truncated := logs[:maxChars]
	lastNewline := strings.LastIndex(truncated, "\n")
	if lastNewline > maxChars/2 {
		truncated = logs[:lastNewline]
	}

	return truncated + "\n... (truncated, see full report for complete logs)"
}

// indentText adds indentation to each line of text
func (r *testReporter) indentText(text string, indent string) string {
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	var indented []string
	for _, line := range lines {
		indented = append(indented, indent+line)
	}
	return strings.Join(indented, "\n")
}

// formatValue formats a value for display in arguments
func (r *testReporter) formatValue(value interface{}) string {
	if value == nil {
		return "null"
	}

	switch v := value.(type) {
	case string:
		return fmt.Sprintf("\"%s\"", v)
	case bool, int, int64, float64:
		return fmt.Sprintf("%v", v)
	case map[string]interface{}, []interface{}:
		if jsonBytes, err := json.Marshal(v); err == nil {
			return string(jsonBytes)
		}
		return fmt.Sprintf("%v", v)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// hasExpectations checks if a TestExpectation has any meaningful values set
func (r *testReporter) hasExpectations(expected TestExpectation) bool {
	return len(expected.Contains) > 0 ||
		len(expected.ErrorContains) > 0 ||
		len(expected.NotContains) > 0 ||
		len(expected.JSONPath) > 0 ||
		expected.StatusCode > 0 ||
		!expected.Success // Show if expecting failure
}

// ReportSuiteResult is called when all tests complete
func (r *testReporter) ReportSuiteResult(suiteResult TestSuiteResult) {
	fmt.Printf("\n🏁 Test Suite Complete\n")
	fmt.Printf("⏱️  Duration: %v\n", suiteResult.Duration)
	fmt.Printf("📊 Results:\n")
	fmt.Printf("   ✅ Passed: %d\n", suiteResult.PassedScenarios)

	if suiteResult.FailedScenarios > 0 {
		fmt.Printf("   ❌ Failed: %d\n", suiteResult.FailedScenarios)
	}

	if suiteResult.ErrorScenarios > 0 {
		fmt.Printf("   💥 Errors: %d\n", suiteResult.ErrorScenarios)
	}

	if suiteResult.SkippedScenarios > 0 {
		fmt.Printf("   ⏭️  Skipped: %d\n", suiteResult.SkippedScenarios)
	}

	fmt.Printf("   📈 Total: %d\n", suiteResult.TotalScenarios)

	// Calculate success rate
	successRate := 0.0
	if suiteResult.TotalScenarios > 0 {
		successRate = float64(suiteResult.PassedScenarios) / float64(suiteResult.TotalScenarios) * 100
	}
	fmt.Printf("   📏 Success Rate: %.1f%%\n", successRate)

	// Overall result
	if suiteResult.FailedScenarios == 0 && suiteResult.ErrorScenarios == 0 {
		fmt.Printf("\n🎉 All tests passed!\n")
	} else {
		fmt.Printf("\n💔 Some tests failed\n")
	}

	// Save detailed report if requested
	if r.reportPath != "" {
		if err := r.saveDetailedReport(suiteResult); err != nil {
			fmt.Printf("⚠️  Failed to save detailed report: %v\n", err)
		} else {
			fmt.Printf("📄 Detailed report saved to: %s\n", r.reportPath)
		}
	}
}

// saveDetailedReport saves a detailed JSON report to file
func (r *testReporter) saveDetailedReport(suiteResult TestSuiteResult) error {
	// Create report directory if it doesn't exist
	if err := os.MkdirAll(r.reportPath, 0755); err != nil {
		return fmt.Errorf("failed to create report directory: %w", err)
	}

	// Generate filename with timestamp
	timestamp := time.Now().Format("20060102-150405")
	filename := fmt.Sprintf("muster-test-report-%s.json", timestamp)
	fullPath := fmt.Sprintf("%s/%s", r.reportPath, filename)

	// Convert to JSON
	jsonData, err := json.MarshalIndent(suiteResult, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal report to JSON: %w", err)
	}

	// Write to file
	if err := os.WriteFile(fullPath, jsonData, 0644); err != nil {
		return fmt.Errorf("failed to write report file: %w", err)
	}

	return nil
}

// getResultSymbol returns an appropriate symbol for the test result
func (r *testReporter) getResultSymbol(result TestResult) string {
	switch result {
	case ResultPassed:
		return "✅"
	case ResultFailed:
		return "❌"
	case ResultSkipped:
		return "⏭️"
	case ResultError:
		return "💥"
	default:
		return "❓"
	}
}

// formatResponse formats response data for display
func (r *testReporter) formatResponse(response interface{}) string {
	if response == nil {
		return ""
	}

	// Try to extract meaningful content from MCP response structures
	if extractedContent := r.extractMCPContent(response); extractedContent != "" {
		return extractedContent
	}

	// Try to format as JSON if it's a map or slice
	switch v := response.(type) {
	case map[string]interface{}, []interface{}:
		if jsonBytes, err := json.MarshalIndent(v, "", "  "); err == nil {
			return string(jsonBytes)
		}
	}

	// Fallback to string representation
	responseStr := fmt.Sprintf("%v", response)

	// Truncate very long responses
	const maxLength = 200
	if len(responseStr) > maxLength {
		return responseStr[:maxLength] + "..."
	}

	return responseStr
}

// extractMCPContent tries to extract meaningful content from MCP response structures
func (r *testReporter) extractMCPContent(response interface{}) string {
	// Convert to string to analyze the structure
	responseStr := fmt.Sprintf("%+v", response)

	// Try to extract JSON content from MCP text responses
	// Look for patterns like: text {"key":"value"}
	if strings.Contains(responseStr, "text ") {
		// Find all JSON-like content after "text "
		var jsonContents []string

		// Split by "text " and process each part
		parts := strings.Split(responseStr, "text ")
		for i := 1; i < len(parts); i++ { // Skip first part before "text "
			part := parts[i]

			// Find potential JSON content
			if jsonStart := strings.Index(part, "{"); jsonStart != -1 {
				// Find the matching closing brace
				braceCount := 0
				var jsonEnd int
				for j, char := range part[jsonStart:] {
					if char == '{' {
						braceCount++
					} else if char == '}' {
						braceCount--
						if braceCount == 0 {
							jsonEnd = jsonStart + j + 1
							break
						}
					}
				}

				if jsonEnd > jsonStart {
					jsonCandidate := part[jsonStart:jsonEnd]
					// Try to parse as JSON to validate
					var jsonData interface{}
					if err := json.Unmarshal([]byte(jsonCandidate), &jsonData); err == nil {
						// Pretty print the JSON
						if prettyJson, err := json.MarshalIndent(jsonData, "", "  "); err == nil {
							jsonContents = append(jsonContents, string(prettyJson))
						} else {
							jsonContents = append(jsonContents, jsonCandidate)
						}
					}
				}
			}
		}

		if len(jsonContents) > 0 {
			if len(jsonContents) == 1 {
				return jsonContents[0]
			}
			return strings.Join(jsonContents, "\n---\n")
		}
	}

	// Try to extract other structured content
	if strings.Contains(responseStr, "IsError:true") || strings.Contains(responseStr, "isError:true") {
		// This is an error response, try to extract error text
		if errorText := r.extractErrorText(responseStr); errorText != "" {
			return "❌ Error: " + errorText
		}
	}

	// Look for simple text content patterns
	if textContent := r.extractSimpleText(responseStr); textContent != "" {
		return textContent
	}

	return ""
}

// extractErrorText extracts error messages from response strings
func (r *testReporter) extractErrorText(responseStr string) string {
	// Look for common error patterns
	patterns := []string{
		"text ",
		"error:",
		"Error:",
		"message:",
		"Message:",
	}

	for _, pattern := range patterns {
		if idx := strings.Index(responseStr, pattern); idx != -1 {
			afterPattern := responseStr[idx+len(pattern):]
			// Extract until next structural element
			endPatterns := []string{"}", "]", "IsError:", "Content:"}
			endIdx := len(afterPattern)
			for _, endPattern := range endPatterns {
				if pos := strings.Index(afterPattern, endPattern); pos != -1 && pos < endIdx {
					endIdx = pos
				}
			}

			extracted := strings.TrimSpace(afterPattern[:endIdx])
			if extracted != "" {
				return extracted
			}
		}
	}

	return ""
}

// extractSimpleText extracts simple text content from structured responses
func (r *testReporter) extractSimpleText(responseStr string) string {
	// Look for text content that's not JSON
	if idx := strings.Index(responseStr, "text "); idx != -1 {
		afterText := responseStr[idx+5:] // Skip "text "

		// If it doesn't start with {, it might be simple text
		if !strings.HasPrefix(strings.TrimSpace(afterText), "{") {
			// Extract until next structural element
			endPatterns := []string{"}", "]", " IsError:", " Content:"}
			endIdx := len(afterText)
			for _, endPattern := range endPatterns {
				if pos := strings.Index(afterText, endPattern); pos != -1 && pos < endIdx {
					endIdx = pos
				}
			}

			extracted := strings.TrimSpace(afterText[:endIdx])
			if extracted != "" && !strings.HasPrefix(extracted, "&") {
				return extracted
			}
		}
	}

	return ""
}

// stringOrDefault returns the string if not empty, otherwise returns the default
func (r *testReporter) stringOrDefault(s, defaultValue string) string {
	if s == "" {
		return defaultValue
	}
	return s
}

// NewQuietReporter creates a reporter that only outputs essential information
func NewQuietReporter() TestReporter {
	return &quietReporter{}
}

// quietReporter implements minimal output for CI/CD integration
type quietReporter struct{}

func (r *quietReporter) ReportStart(config TestConfiguration) {
	// Silent start
}

func (r *quietReporter) ReportScenarioStart(scenario TestScenario) {
	// Silent scenario start
}

func (r *quietReporter) ReportStepResult(stepResult TestStepResult) {
	// Silent step reporting
}

func (r *quietReporter) ReportScenarioResult(scenarioResult TestScenarioResult) {
	// Only report failures
	if scenarioResult.Result == ResultFailed || scenarioResult.Result == ResultError {
		symbol := "❌"
		if scenarioResult.Result == ResultError {
			symbol = "💥"
		}
		fmt.Printf("%s %s: %s\n", symbol, scenarioResult.Scenario.Name, scenarioResult.Error)
	}
}

func (r *quietReporter) ReportSuiteResult(suiteResult TestSuiteResult) {
	// Print just the final summary
	if suiteResult.FailedScenarios == 0 && suiteResult.ErrorScenarios == 0 {
		fmt.Printf("✅ All %d tests passed (%v)\n", suiteResult.TotalScenarios, suiteResult.Duration)
	} else {
		fmt.Printf("❌ %d/%d tests failed (%v)\n",
			suiteResult.FailedScenarios+suiteResult.ErrorScenarios,
			suiteResult.TotalScenarios,
			suiteResult.Duration)
	}
}

func (r *quietReporter) SetParallelMode(parallel bool) {
	// Quiet reporter doesn't need special parallel handling
}

// NewJSONReporter creates a reporter that outputs JSON for CI/CD integration
func NewJSONReporter() TestReporter {
	return &jsonReporter{}
}

// jsonReporter implements JSON output for machine consumption
type jsonReporter struct {
	results []TestScenarioResult
	config  TestConfiguration
}

func (r *jsonReporter) ReportStart(config TestConfiguration) {
	r.config = config
	r.results = make([]TestScenarioResult, 0)
}

func (r *jsonReporter) ReportScenarioStart(scenario TestScenario) {
	// Silent
}

func (r *jsonReporter) ReportStepResult(stepResult TestStepResult) {
	// Silent
}

func (r *jsonReporter) ReportScenarioResult(scenarioResult TestScenarioResult) {
	r.results = append(r.results, scenarioResult)
}

func (r *jsonReporter) ReportSuiteResult(suiteResult TestSuiteResult) {
	output := map[string]interface{}{
		"configuration": r.config,
		"results":       r.results,
		"summary":       suiteResult,
	}
	jsonBytes, _ := json.MarshalIndent(output, "", "  ")
	fmt.Println(string(jsonBytes))
}

func (r *jsonReporter) SetParallelMode(parallel bool) {
	// JSON reporter doesn't need special parallel handling - it outputs structured data at the end
}
