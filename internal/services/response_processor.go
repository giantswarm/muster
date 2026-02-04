package services

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/pkg/logging"
)

// ExtractFromResponse extracts a value from response using a JSON path
// Currently supports simple dot notation paths like "result.sessionID" or direct field names
func ExtractFromResponse(response map[string]interface{}, path string) interface{} {
	if path == "" {
		return nil
	}

	// Handle direct field access
	if !strings.Contains(path, ".") {
		if value, exists := response[path]; exists {
			return value
		}
		return nil
	}

	// Handle dot notation paths like "result.sessionID"
	parts := strings.Split(path, ".")
	current := response

	for i, part := range parts {
		if current == nil {
			return nil
		}

		value, exists := current[part]
		if !exists {
			return nil
		}

		// If this is the last part, return the value
		if i == len(parts)-1 {
			return value
		}

		// Otherwise, continue traversing if it's a map
		if nextMap, ok := value.(map[string]interface{}); ok {
			current = nextMap
		} else {
			// Can't traverse further
			return nil
		}
	}

	return nil
}

// ProcessToolOutputs extracts outputs from a tool response based on the output configuration
func ProcessToolOutputs(response map[string]interface{}, outputs map[string]string) map[string]interface{} {
	if outputs == nil || len(outputs) == 0 {
		return nil
	}

	extractedOutputs := make(map[string]interface{})

	// First, try to get the actual data from MCP response format
	var dataToExtractFrom map[string]interface{}

	// Check if response has a "text" field with JSON content (common MCP pattern)
	if textField, exists := response["text"]; exists {
		if textStr, ok := textField.(string); ok {
			// Try to parse the text field as JSON
			var jsonData map[string]interface{}
			if err := json.Unmarshal([]byte(textStr), &jsonData); err == nil {
				logging.Debug("ProcessToolOutputs", "Successfully parsed text field as JSON")
				dataToExtractFrom = jsonData
			}
		}
	}
	// If no text field or text parsing failed, use the top-level response
	if dataToExtractFrom == nil {
		dataToExtractFrom = response
	}

	// Extract outputs using JSON path
	for outputName, jsonPath := range outputs {
		value := extractValueFromJSONPath(dataToExtractFrom, jsonPath)
		if value != nil {
			extractedOutputs[outputName] = value
			logging.Debug("ProcessToolOutputs", "Successfully extracted output %s=%v from JSON path %s", outputName, value, jsonPath)
		} else {
			logging.Debug("ProcessToolOutputs", "Failed to extract output %s from path %s", outputName, jsonPath)
		}
	}

	if len(extractedOutputs) == 0 {
		return nil
	}

	return extractedOutputs
}

// extractValueFromJSONPath extracts a value from a JSON object using a simple path
func extractValueFromJSONPath(data map[string]interface{}, jsonPath string) interface{} {
	// Handle simple field access (e.g., "sessionID", "status")
	if value, exists := data[jsonPath]; exists {
		return value
	}

	// Handle nested paths (e.g., "result.sessionID")
	parts := strings.Split(jsonPath, ".")
	current := data
	for _, part := range parts {
		if nextLevel, exists := current[part]; exists {
			if nextMap, ok := nextLevel.(map[string]interface{}); ok {
				current = nextMap
			} else {
				// This is the final value
				return nextLevel
			}
		} else {
			return nil
		}
	}

	return current
}

// EvaluateHealthCheckExpectation evaluates health check conditions against tool response
func EvaluateHealthCheckExpectation(response map[string]interface{}, expectation *api.HealthCheckExpectation) (bool, error) {
	if expectation == nil {
		// If no expectation is provided, consider it healthy if the tool call succeeded
		return true, nil
	}

	// Check success condition first (default is true)
	expectedSuccess := true
	if expectation.Success != nil {
		expectedSuccess = *expectation.Success
	}

	// If we expect success=false but the tool didn't indicate failure, that's unhealthy
	if !expectedSuccess {
		// Tool is expected to fail, but it succeeded, so this is unhealthy
		return false, nil
	}

	// Check JSON path conditions
	if expectation.JsonPath != nil {
		for path, expectedValue := range expectation.JsonPath {
			actualValue := ExtractFromResponse(response, path)

			// If the field doesn't exist or doesn't match expected value, it's unhealthy
			if !compareValues(actualValue, expectedValue) {
				return false, nil
			}
		}
	}

	// All conditions passed, service is healthy
	return true, nil
}

// compareValues compares two values for equality, handling different types
func compareValues(actual, expected interface{}) bool {
	if actual == nil && expected == nil {
		return true
	}
	if actual == nil || expected == nil {
		return false
	}

	// Convert both to strings for comparison to handle type mismatches
	actualStr := fmt.Sprintf("%v", actual)
	expectedStr := fmt.Sprintf("%v", expected)

	return actualStr == expectedStr
}
