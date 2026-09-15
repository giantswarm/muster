package testing

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// responseView is the shape-independent view of a step response that
// expectation checking operates on.
//
// The BDD framework has two response shapes: MCP tool steps return an
// *mcp.CallToolResult, and test_* steps return a plain map (or a
// *TestToolResult). Each shape used to carry its own copy of the expectation
// checks, and the two copies drifted: json_path, not_contains and
// wait_for_state were each implemented on one path and silently ignored on the
// other, so every scenario that declared one on the wrong kind of step passed
// vacuously (#1036, #1038).
//
// Both paths now adapt their own response into this view and share the single
// checkExpectations implementation below. Adapting a shape is the only
// per-shape work left, so an expectation kind cannot be honoured for one step
// kind and dropped for the other.
type responseView struct {
	// present is false when the step produced no response at all, in which
	// case there is nothing for the content checks to match against.
	present bool
	// hasError is the tool- or transport-level failure signal, however the
	// underlying shape reports it.
	hasError bool
	// text is what contains and not_contains match against.
	text string
	// errorText is what error_contains matches against.
	errorText string
	// json is the object json_path resolves against, and the object the
	// response-body success field is read from. nil when the response is not
	// a JSON object.
	json map[string]interface{}
}

// viewOfMCPResponse adapts an MCP tool response into a responseView.
//
// Text matching uses the concatenated text content blocks rather than the Go
// rendering of the struct, so a scenario matches what the tool actually
// returned rather than mcp-go's field layout.
func (r *testRunner) viewOfMCPResponse(response interface{}, err error, logger TestLogger) responseView {
	view := responseView{present: response != nil, hasError: err != nil}

	mcpResult, isMCPResult := response.(*mcp.CallToolResult)
	if isMCPResult && mcpResult.IsError {
		view.hasError = true
	}

	if response != nil {
		if isMCPResult {
			var textParts []string
			for _, content := range mcpResult.Content {
				if textContent, ok := mcp.AsTextContent(content); ok {
					textParts = append(textParts, textContent.Text)
				}
			}
			view.text = strings.Join(textParts, " ")
		} else {
			view.text = fmt.Sprintf("%v", response)
		}

		view.json = r.extractJSONFromMCPResponse(response, logger)
	}

	switch {
	case err != nil:
		view.errorText = err.Error()
	case view.hasError:
		view.errorText = strings.TrimSpace(view.text)
	}

	return view
}

// viewOfTestToolResponse adapts a test_* tool response into a responseView.
//
// Test tools return their result as a plain map, so the JSON object is the
// response itself and text matching uses its Go rendering -- there is no
// content-block envelope to unwrap.
func (r *testRunner) viewOfTestToolResponse(response interface{}, err error) responseView {
	view := responseView{present: response != nil, hasError: err != nil}

	if response != nil {
		if respMap, ok := response.(map[string]interface{}); ok {
			view.json = respMap
			// Test tools report a tool-level failure with an isError field
			// rather than with the MCP result flag.
			if isErr, ok := respMap["isError"].(bool); ok && isErr {
				view.hasError = true
			}
		}
		if result, ok := response.(*TestToolResult); ok {
			view.hasError = view.hasError || result.IsError
		}

		view.text = fmt.Sprintf("%v", response)
	}

	switch {
	case err != nil:
		view.errorText = err.Error()
	case view.hasError:
		view.errorText = view.text
	}

	return view
}

// checkExpectations is the single implementation of every expectation kind
// declared in TestExpectation, for every step kind.
//
// Two kinds are deliberately not handled here, and both are enforced
// elsewhere rather than ignored:
//
//   - wait_for_state is a retry policy, not an assertion. It is applied by the
//     polling wrappers (validateExpectationsWithClient for MCP steps,
//     callTestToolWithWait for test_* steps), which re-invoke the tool until
//     this function returns true or the timeout elapses.
//   - status_code has no meaning for either response shape and is rejected at
//     load time by validateStep, so a scenario declaring it fails loudly
//     instead of passing vacuously.
//
// TestEveryExpectationKindIsEnforced asserts that accounting stays complete as
// TestExpectation grows.
func (r *testRunner) checkExpectations(expected TestExpectation, view responseView, logger TestLogger) bool {
	reason := r.expectationFailure(expected, view)
	if r.debug {
		if reason == "" {
			logger.Debug("✅ All expectations met for step\n")
		} else {
			logger.Debug("❌ %s\n", reason)
		}
	}
	return reason == ""
}

// expectationFailure reports why a response does not meet its expectations,
// or "" when it does. The reason names what was expected and what the
// response carried, so a failed step is legible without a debug run.
func (r *testRunner) expectationFailure(expected TestExpectation, view responseView) string {
	// Success or failure, from whichever signal the shape reports it with.
	if expected.Success && view.hasError {
		return fmt.Sprintf("expected success but the step reported an error: %s", view.errorText)
	}
	if !expected.Success && !view.hasError {
		return "expected failure but the step succeeded"
	}

	// error_contains, whenever it is declared. A step that asks for error text
	// and produced none has not met its expectation, so an empty errorText is
	// a failure rather than a vacuous pass.
	if len(expected.ErrorContains) > 0 {
		if view.errorText == "" {
			return "expected error text but the step produced none"
		}
		for _, expectedText := range expected.ErrorContains {
			if !containsText(view.errorText, expectedText) {
				return fmt.Sprintf("error text %q does not contain %q", view.errorText, expectedText)
			}
		}
	}

	// Without a response there is nothing left to match against.
	if !view.present {
		return ""
	}

	// A tool can report failure in its own payload while the transport call
	// itself succeeded.
	if expected.Success && view.json != nil {
		if success, ok := view.json["success"].(bool); ok && !success {
			return "response payload reports failure (success=false)"
		}
	}

	for _, expectedText := range expected.Contains {
		if !containsText(view.text, expectedText) {
			return fmt.Sprintf("response does not contain %q", expectedText)
		}
	}
	for _, unexpectedText := range expected.NotContains {
		if containsText(view.text, unexpectedText) {
			return fmt.Sprintf("response contains forbidden %q", unexpectedText)
		}
	}

	if len(expected.JSONPath) > 0 || len(expected.JSONPathMax) > 0 {
		if view.json == nil {
			return "json_path validation failed: response is not a JSON object"
		}
	}
	// A budget breach is the headline of a step that states one: the
	// json_path_max bounds are checked before the json_path values.
	for _, jsonPath := range sortedPaths(expected.JSONPathMax) {
		limit := expected.JSONPathMax[jsonPath]
		actualValue, exists := r.resolveJSONPath(view.json, jsonPath)
		if !exists {
			return fmt.Sprintf("json_path_max %q not found in response", jsonPath)
		}
		measured, ok := numericValue(actualValue)
		if !ok {
			return fmt.Sprintf("json_path_max %q: %v is not a number", jsonPath, actualValue)
		}
		if measured > limit {
			return fmt.Sprintf("json_path_max %q: measured %v, budget %v", jsonPath, formatNumber(measured), formatNumber(limit))
		}
	}
	for _, jsonPath := range sortedPaths(expected.JSONPath) {
		expectedValue := expected.JSONPath[jsonPath]
		actualValue, exists := r.resolveJSONPath(view.json, jsonPath)
		if !exists {
			return fmt.Sprintf("json_path %q not found in response", jsonPath)
		}
		if !r.compareValuesEnhanced(actualValue, expectedValue) {
			return fmt.Sprintf("json_path %q: expected %v, got %v", jsonPath, expectedValue, actualValue)
		}
	}
	return ""
}

// sortedPaths returns a map's keys in order, so a failure reason is the same
// on every run.
func sortedPaths[V any](m map[string]V) []string {
	paths := make([]string, 0, len(m))
	for p := range m {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}

// numericValue reads a JSON number in any of the shapes a decoded response or
// a test tool's payload carries it in.
func numericValue(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case int32:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

// formatNumber prints a whole number without a decimal point and any other
// with the digits it needs.
func formatNumber(f float64) string {
	if f == math.Trunc(f) && math.Abs(f) < 1e15 {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}
