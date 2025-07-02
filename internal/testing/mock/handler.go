package mock

import (
	"fmt"
	"os"
	"reflect"
	"time"

	"muster/internal/template"
)

// ToolHandler handles mock tool calls with configurable responses
type ToolHandler struct {
	config         ToolConfig
	templateEngine *template.Engine
	debug          bool
}

// NewToolHandler creates a new mock tool handler
func NewToolHandler(config ToolConfig, templateEngine *template.Engine, debug bool) *ToolHandler {
	return &ToolHandler{
		config:         config,
		templateEngine: templateEngine,
		debug:          debug,
	}
}

// HandleCall processes a tool call and returns the configured response
func (h *ToolHandler) HandleCall(args map[string]interface{}) (interface{}, error) {
	if h.debug {
		fmt.Fprintf(os.Stderr, "🔧 Mock tool '%s' called with args: %v\n", h.config.Name, args)
	}

	// Merge args with default values from input schema
	mergedArgs := h.mergeWithDefaults(args)

	if h.debug && len(mergedArgs) != len(args) {
		fmt.Fprintf(os.Stderr, "🔧 Mock tool '%s' merged with defaults: %v\n", h.config.Name, mergedArgs)
	}

	// Find the first matching response
	var selectedResponse *ToolResponse
	for _, response := range h.config.Responses {
		if h.matchesCondition(response.Condition, mergedArgs) {
			selectedResponse = &response
			break
		}
	}

	// If no specific response matched, use the first one as fallback
	if selectedResponse == nil && len(h.config.Responses) > 0 {
		selectedResponse = &h.config.Responses[0]
	}

	if selectedResponse == nil {
		return nil, fmt.Errorf("no response configured for tool %s", h.config.Name)
	}

	// Handle delay if specified
	if selectedResponse.Delay != "" {
		if duration, err := time.ParseDuration(selectedResponse.Delay); err == nil {
			if h.debug {
				fmt.Fprintf(os.Stderr, "⏳ Simulating delay of %s for tool '%s'\n", selectedResponse.Delay, h.config.Name)
			}
			time.Sleep(duration)
		}
	}

	// Handle error response
	if selectedResponse.Error != "" {
		errorMessage, err := h.templateEngine.Replace(selectedResponse.Error, mergedArgs)
		if err != nil {
			if h.debug {
				fmt.Fprintf(os.Stderr, "❌ Failed to render error message for tool '%s': %v\n", h.config.Name, err)
			}
			return nil, fmt.Errorf("failed to render error message: %w", err)
		}
		errorStr, ok := errorMessage.(string)
		if !ok {
			errorStr = fmt.Sprintf("%v", errorMessage)
		}
		if h.debug {
			fmt.Fprintf(os.Stderr, "❌ Mock tool '%s' returning error: %s\n", h.config.Name, errorStr)
		}
		return nil, fmt.Errorf("%s", errorStr)
	}

	// Render the response using the template engine with merged args
	renderedResponse, err := h.templateEngine.Replace(selectedResponse.Response, mergedArgs)
	if err != nil {
		if h.debug {
			fmt.Fprintf(os.Stderr, "❌ Failed to render response for tool '%s': %v\n", h.config.Name, err)
		}
		return nil, fmt.Errorf("failed to render response: %w", err)
	}

	if h.debug {
		fmt.Fprintf(os.Stderr, "✅ Mock tool '%s' returning response: %v\n", h.config.Name, renderedResponse)
	}

	return renderedResponse, nil
}

// mergeWithDefaults merges provided args with default values from input schema
func (h *ToolHandler) mergeWithDefaults(args map[string]interface{}) map[string]interface{} {
	merged := make(map[string]interface{})

	// First, add default values from input schema
	if h.config.InputSchema != nil {
		if properties, ok := h.config.InputSchema["properties"].(map[string]interface{}); ok {
			for propName, propDef := range properties {
				if propDefMap, ok := propDef.(map[string]interface{}); ok {
					if defaultValue, hasDefault := propDefMap["default"]; hasDefault {
						merged[propName] = defaultValue
					}
				}
			}
		}
	}

	// Then, override with provided args
	for key, value := range args {
		merged[key] = value
	}

	return merged
}

// matchesCondition checks if the given args match the response condition
func (h *ToolHandler) matchesCondition(condition map[string]interface{}, args map[string]interface{}) bool {
	if len(condition) == 0 {
		return true // No condition means it matches everything
	}

	for key, expectedValue := range condition {
		actualValue, exists := args[key]
		if !exists || !h.valuesEqual(expectedValue, actualValue) {
			return false
		}
	}

	return true
}

// valuesEqual compares two values for equality, handling type conversions
func (h *ToolHandler) valuesEqual(expected, actual interface{}) bool {
	// Direct equality check first
	if reflect.DeepEqual(expected, actual) {
		return true
	}

	// Handle string comparisons
	expectedStr := fmt.Sprintf("%v", expected)
	actualStr := fmt.Sprintf("%v", actual)
	if expectedStr == actualStr {
		return true
	}

	return false
}
