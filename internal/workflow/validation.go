package workflow

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/pkg/logging"

	"github.com/mark3labs/mcp-go/mcp"
)

// validateInputs validates the input arguments against the args definition,
// applying defaults for missing optional fields. Extra args are tolerated.
func (we *WorkflowExecutor) validateInputs(argsDefinition map[string]api.ArgDefinition, args map[string]interface{}) error {
	logging.Debug("WorkflowExecutor", "validateInputs called with args: %+v", args)
	logging.Debug("WorkflowExecutor", "validateInputs args definition: %+v", argsDefinition)

	for key, argDef := range argsDefinition {
		value, exists := args[key]

		if !exists {
			if argDef.Required {
				logging.Error("WorkflowExecutor", fmt.Errorf("missing required field"), "Required field '%s' is missing from args %+v", key, args)
				return fmt.Errorf("required field '%s' is missing", key)
			}
			if argDef.Default != nil {
				logging.Debug("WorkflowExecutor", "Applying default value for %s: %v", key, argDef.Default)
				args[key] = argDef.Default
			}
			continue
		}

		if !we.validateType(value, argDef.Type) {
			return fmt.Errorf("field '%s' has wrong type, expected %s", key, argDef.Type)
		}
	}

	logging.Debug("WorkflowExecutor", "validateInputs final args: %+v", args)
	return nil
}

// validateType performs basic type validation. Unknown types pass — the
// engine doesn't have a rich type system; this catches obvious shape errors
// only.
func (we *WorkflowExecutor) validateType(value interface{}, expectedType string) bool {
	switch api.ArgType(expectedType) {
	case api.ArgTypeString:
		_, ok := value.(string)
		return ok
	case api.ArgTypeNumber, api.ArgTypeInteger:
		switch value.(type) {
		case int, int32, int64, float32, float64:
			return true
		default:
			return false
		}
	case api.ArgTypeBoolean:
		_, ok := value.(bool)
		return ok
	case api.ArgTypeArray:
		switch value.(type) {
		case []interface{}, []string, []int, []float64:
			return true
		default:
			return false
		}
	case api.ArgTypeObject:
		_, ok := value.(map[string]interface{})
		return ok
	default:
		return true
	}
}

// validateJsonPath validates JSON path expectations against a tool result.
// Each expectation may itself be a template string, resolved before
// comparison so step-result chaining works inside expectations.
func (we *WorkflowExecutor) validateJsonPath(toolResult *mcp.CallToolResult, jsonPathExpectations map[string]interface{}, execCtx *executionContext) (bool, error) {
	var resultData interface{}
	if len(toolResult.Content) == 0 {
		return false, fmt.Errorf("tool result has no content")
	}

	if textContent, ok := toolResult.Content[0].(mcp.TextContent); ok {
		if err := json.Unmarshal([]byte(textContent.Text), &resultData); err != nil {
			return false, fmt.Errorf("failed to parse tool result as JSON: %w", err)
		}
	} else {
		resultData = toolResult.Content[0]
	}

	for jsonPath, expectedValue := range jsonPathExpectations {
		resolvedExpectedValue, err := we.resolveValue(expectedValue, execCtx)
		if err != nil {
			return false, fmt.Errorf("failed to resolve expected value template for path %s: %w", jsonPath, err)
		}

		actualValue, err := we.resolveJsonPathValue(resultData, jsonPath, execCtx)
		if err != nil {
			logging.Debug("WorkflowExecutor", "Failed to get value from path %s: %v", jsonPath, err)
			return false, nil
		}

		if !we.valuesEqual(actualValue, resolvedExpectedValue) {
			logging.Debug("WorkflowExecutor", "JSON path validation failed: path=%s, expected=%v, actual=%v",
				jsonPath, resolvedExpectedValue, actualValue)
			return false, nil
		}

		logging.Debug("WorkflowExecutor", "JSON path validation passed: path=%s, expected=%v, actual=%v",
			jsonPath, resolvedExpectedValue, actualValue)
	}

	return true, nil
}

// resolveJsonPathValue extracts the value a jsonPath expectation asserts on from
// a condition tool result. It accepts two interchangeable forms that share the
// workflow's single expression language:
//
//   - a bare dotted/bracketed path navigated from the result object, e.g.
//     "data.field" or "items[0].name" (back-compatible with legacy dotted paths,
//     now also supporting array indexing); and
//   - a full Go-template expression with the same capabilities as step args,
//     where the condition result is exposed as ".result" alongside the usual
//     ".input" / ".results" / ".vars" roots, e.g.
//     "{{ (index .result.items 0).name }}".
func (we *WorkflowExecutor) resolveJsonPathValue(resultData interface{}, path string, execCtx *executionContext) (interface{}, error) {
	if strings.Contains(path, "{{") {
		tctx := we.templateContext(execCtx)
		tctx["result"] = resultData
		return we.renderTypedTemplate(path, tctx)
	}
	return we.template.ResolvePath(resultData, path)
}

// valuesEqual compares two values, falling back to string comparison when
// types differ — JSON numbers come back as float64 while declared expectations
// may be ints, so a stringified compare avoids spurious mismatches.
func (we *WorkflowExecutor) valuesEqual(actual, expected interface{}) bool {
	if actual == expected {
		return true
	}
	return fmt.Sprintf("%v", actual) == fmt.Sprintf("%v", expected)
}
