package testing

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// ScenarioValidationResults represents the results of validating multiple scenarios
type ScenarioValidationResults struct {
	TotalScenarios    int                        `json:"total_scenarios"`
	ValidScenarios    int                        `json:"valid_scenarios"`
	TotalErrors       int                        `json:"total_errors"`
	ScenarioResults   []ScenarioValidationResult `json:"scenario_results"`
	ValidationSummary map[string]int             `json:"validation_summary"`
}

// ScenarioValidationResult represents the validation result for a single scenario
type ScenarioValidationResult struct {
	ScenarioName string                 `json:"scenario_name"`
	Valid        bool                   `json:"valid"`
	Errors       []ValidationError      `json:"errors,omitempty"`
	StepResults  []StepValidationResult `json:"step_results"`
}

// StepValidationResult represents the validation result for a single step
type StepValidationResult struct {
	StepID string            `json:"step_id"`
	Tool   string            `json:"tool"`
	Valid  bool              `json:"valid"`
	Errors []ValidationError `json:"errors,omitempty"`
}

// ValidationError represents a validation error
type ValidationError struct {
	Type       string `json:"type"`
	Message    string `json:"message"`
	Field      string `json:"field,omitempty"`
	Suggestion string `json:"suggestion,omitempty"`
}

// LoadSchemaFromFile loads a JSON schema from file
func LoadSchemaFromFile(filename string) (map[string]interface{}, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read schema file: %w", err)
	}

	var schema map[string]interface{}
	if err := json.Unmarshal(data, &schema); err != nil {
		return nil, fmt.Errorf("failed to parse schema JSON: %w", err)
	}

	return schema, nil
}

// ValidateScenariosAgainstSchema validates scenarios against the API schema
func ValidateScenariosAgainstSchema(scenarios []TestScenario, schema map[string]interface{}, verbose, debug bool) *ScenarioValidationResults {
	results := &ScenarioValidationResults{
		TotalScenarios:    len(scenarios),
		ScenarioResults:   make([]ScenarioValidationResult, 0),
		ValidationSummary: make(map[string]int),
	}

	// Extract tool schemas from the main schema
	toolSchemas := extractToolSchemas(schema)

	for _, scenario := range scenarios {
		scenarioResult := validateScenario(scenario, toolSchemas, verbose, debug)
		results.ScenarioResults = append(results.ScenarioResults, scenarioResult)

		if scenarioResult.Valid {
			results.ValidScenarios++
		} else {
			results.TotalErrors += len(scenarioResult.Errors)
		}

		// Update summary statistics
		for _, stepResult := range scenarioResult.StepResults {
			if stepResult.Valid {
				results.ValidationSummary["valid_steps"]++
			} else {
				results.ValidationSummary["invalid_steps"]++
				for _, err := range stepResult.Errors {
					results.ValidationSummary[err.Type]++
				}
			}
		}
	}

	return results
}

// validateScenario validates a single scenario against tool schemas
func validateScenario(scenario TestScenario, toolSchemas map[string]interface{}, verbose, debug bool) ScenarioValidationResult {
	result := ScenarioValidationResult{
		ScenarioName: scenario.Name,
		Valid:        true,
		Errors:       make([]ValidationError, 0),
		StepResults:  make([]StepValidationResult, 0),
	}

	// Validate all steps in the scenario
	allSteps := append(scenario.Steps, scenario.Cleanup...)

	for _, step := range allSteps {
		stepResult := validateStep(step, toolSchemas, verbose, debug)
		result.StepResults = append(result.StepResults, stepResult)

		if !stepResult.Valid {
			result.Valid = false
			// Add step errors to scenario errors
			for _, err := range stepResult.Errors {
				result.Errors = append(result.Errors, ValidationError{
					Type:       err.Type,
					Message:    fmt.Sprintf("Step %s: %s", step.ID, err.Message),
					Field:      err.Field,
					Suggestion: err.Suggestion,
				})
			}
		}
	}

	return result
}

// validateStep validates a single test step against tool schemas
func validateStep(step TestStep, toolSchemas map[string]interface{}, verbose, debug bool) StepValidationResult {
	result := StepValidationResult{
		StepID: step.ID,
		Tool:   step.Tool,
		Valid:  true,
		Errors: make([]ValidationError, 0),
	}

	// Check tool prefix to determine validation approach
	if strings.HasPrefix(step.Tool, "core_") {
		// Core tools can be validated against the schema
		toolSchema, exists := toolSchemas[step.Tool]
		if !exists {
			result.Valid = false
			result.Errors = append(result.Errors, ValidationError{
				Type:       "unknown_tool",
				Message:    fmt.Sprintf("Core tool '%s' not found in API schema", step.Tool),
				Suggestion: "Check if this core tool exists in the current muster serve API",
			})
			return result
		}

		// Validate step arguments against tool schema for core tools
		if schemaMap, ok := toolSchema.(map[string]interface{}); ok {
			if properties, hasProps := schemaMap["properties"].(map[string]interface{}); hasProps {
				errors := validateArguments(step.Args, properties, step.Tool)
				if len(errors) > 0 {
					result.Valid = false
					result.Errors = append(result.Errors, errors...)
				}
			}
		}
	} else if strings.HasPrefix(step.Tool, "x_") || strings.HasPrefix(step.Tool, "workflow_") || strings.HasPrefix(step.Tool, "api_") {
		// These tools are valid but can't be verified further - they're part of test scenarios
		// x_* = mock MCP server tools
		// workflow_* = workflow execution tools
		// api_* = API tools
		// We consider them valid but don't validate args since they're scenario-specific
	} else {
		// All other tools are invalid
		result.Valid = false
		result.Errors = append(result.Errors, ValidationError{
			Type:       "unknown_tool",
			Message:    fmt.Sprintf("Tool '%s' is not a valid tool type", step.Tool),
			Suggestion: "Tools must be prefixed with 'core_' (API tools), 'x_' (mock tools), 'workflow_' (workflow execution), or 'api_' (API tools)",
		})
	}

	return result
}

// validateArguments validates step arguments against expected properties
func validateArguments(args map[string]interface{}, properties map[string]interface{}, toolName string) []ValidationError {
	var errors []ValidationError

	// Check for unexpected arguments
	for argName := range args {
		if _, exists := properties[argName]; !exists {
			errors = append(errors, ValidationError{
				Type:    "unexpected_argument",
				Message: fmt.Sprintf("Argument '%s' not expected for tool '%s'", argName, toolName),
				Field:   argName,
			})
		}
	}

	return errors
}

// extractToolSchemas extracts tool schemas from the main schema
func extractToolSchemas(schema map[string]interface{}) map[string]interface{} {
	if properties, ok := schema["properties"].(map[string]interface{}); ok {
		if tools, ok := properties["tools"].(map[string]interface{}); ok {
			if toolProps, ok := tools["properties"].(map[string]interface{}); ok {
				return toolProps
			}
		}
	}
	return make(map[string]interface{})
}

// FormatValidationResults formats validation results for CLI output
func FormatValidationResults(results *ScenarioValidationResults, verbose bool) string {
	var output strings.Builder

	output.WriteString("🔍 API Schema Validation Results\n")
	output.WriteString("════════════════════════════════\n")
	output.WriteString(fmt.Sprintf("Total scenarios: %d\n", results.TotalScenarios))
	output.WriteString(fmt.Sprintf("Valid scenarios: %d\n", results.ValidScenarios))
	output.WriteString(fmt.Sprintf("Invalid scenarios: %d\n", results.TotalScenarios-results.ValidScenarios))
	output.WriteString(fmt.Sprintf("Total errors: %d\n", results.TotalErrors))

	// Summary statistics
	if len(results.ValidationSummary) > 0 {
		output.WriteString("\n📊 Validation Summary:\n")
		for errorType, count := range results.ValidationSummary {
			output.WriteString(fmt.Sprintf("  %s: %d\n", errorType, count))
		}
	}

	// Show detailed errors if verbose or if there are failures
	if verbose || results.TotalErrors > 0 {
		output.WriteString("\n📋 Scenario Details:\n")
		for _, scenarioResult := range results.ScenarioResults {
			status := "✅"
			if !scenarioResult.Valid {
				status = "❌"
			}
			output.WriteString(fmt.Sprintf("  %s %s\n", status, scenarioResult.ScenarioName))

			if !scenarioResult.Valid && len(scenarioResult.Errors) > 0 {
				for _, err := range scenarioResult.Errors {
					output.WriteString(fmt.Sprintf("    • %s: %s\n", err.Type, err.Message))
					if err.Suggestion != "" {
						output.WriteString(fmt.Sprintf("      💡 %s\n", err.Suggestion))
					}
				}
			}
		}
	}

	return output.String()
}
