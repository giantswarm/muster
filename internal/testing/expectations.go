package testing

import (
	"fmt"
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
	// Success or failure, from whichever signal the shape reports it with.
	if expected.Success && view.hasError {
		if r.debug {
			logger.Debug("❌ Expected success but the step reported an error: %s\n", view.errorText)
		}
		return false
	}

	if !expected.Success && !view.hasError {
		if r.debug {
			logger.Debug("❌ Expected failure but the step succeeded\n")
		}
		return false
	}

	// error_contains, whenever it is declared. A step that asks for error text
	// and produced none has not met its expectation, so an empty errorText is
	// a failure rather than a vacuous pass.
	if len(expected.ErrorContains) > 0 {
		if view.errorText == "" {
			if r.debug {
				logger.Debug("❌ Expected error text but the step produced none\n")
			}
			return false
		}
		for _, expectedText := range expected.ErrorContains {
			if !containsText(view.errorText, expectedText) {
				if r.debug {
					logger.Debug("❌ Error text '%s' does not contain expected text '%s'\n", view.errorText, expectedText)
				}
				return false
			}
		}
	}

	// Without a response there is nothing left to match against.
	if !view.present {
		return true
	}

	// A tool can report failure in its own payload while the transport call
	// itself succeeded.
	if expected.Success && view.json != nil {
		if success, ok := view.json["success"].(bool); ok && !success {
			if r.debug {
				logger.Debug("❌ Response payload reports failure (success=false)\n")
			}
			return false
		}
	}

	for _, expectedText := range expected.Contains {
		if !containsText(view.text, expectedText) {
			if r.debug {
				logger.Debug("❌ Response does not contain expected text '%s'\n", expectedText)
			}
			return false
		}
	}

	for _, unexpectedText := range expected.NotContains {
		if containsText(view.text, unexpectedText) {
			if r.debug {
				logger.Debug("❌ Response contains unexpected text '%s'\n", unexpectedText)
			}
			return false
		}
	}

	if len(expected.JSONPath) > 0 {
		if view.json == nil {
			if r.debug {
				logger.Debug("❌ JSON path validation failed: response is not a JSON object\n")
			}
			return false
		}

		for jsonPath, expectedValue := range expected.JSONPath {
			actualValue, exists := r.resolveJSONPath(view.json, jsonPath)
			if !exists {
				if r.debug {
					logger.Debug("❌ JSON path '%s' not found in response\n", jsonPath)
				}
				return false
			}

			if !r.compareValuesEnhanced(actualValue, expectedValue) {
				if r.debug {
					logger.Debug("❌ JSON path '%s': expected %v, got %v\n", jsonPath, expectedValue, actualValue)
				}
				return false
			}
		}
	}

	if r.debug {
		logger.Debug("✅ All expectations met for step\n")
	}

	return true
}
