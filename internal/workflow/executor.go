package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"muster/internal/api"
	"muster/internal/template"
	"muster/pkg/logging"

	"github.com/mark3labs/mcp-go/mcp"
)

// ToolCaller interface - what we need from the aggregator
type ToolCaller interface {
	CallToolInternal(ctx context.Context, toolName string, args map[string]interface{}) (*mcp.CallToolResult, error)
}

// stepMetadata holds metadata about an executed step for tracking purposes
type stepMetadata struct {
	ID                  string      // Original step ID from workflow definition
	Tool                string      // Tool name used in the step
	Store               string      // Variable name where result was stored (if any)
	Status              string      // Step execution status: "completed", "skipped", "failed"
	ConditionEvaluation *bool       // Boolean result of condition evaluation (nil if no condition)
	ConditionResult     interface{} // Actual result from condition tool call (nil if no condition)
	ConditionTool       string      // Tool used for condition evaluation (empty if no condition)
}

// WorkflowExecutor executes workflow steps
type WorkflowExecutor struct {
	toolCaller ToolCaller
	template   *template.Engine
}

// NewWorkflowExecutor creates a new workflow executor
func NewWorkflowExecutor(toolCaller ToolCaller) *WorkflowExecutor {
	return &WorkflowExecutor{
		toolCaller: toolCaller,
		template:   template.New(),
	}
}

// ExecuteWorkflow executes a workflow with the given arguments
func (we *WorkflowExecutor) ExecuteWorkflow(ctx context.Context, workflow *api.Workflow, args map[string]interface{}) (*mcp.CallToolResult, error) {
	// Log required args for debugging
	var requiredArgs []string
	for name, arg := range workflow.Args {
		if arg.Required {
			requiredArgs = append(requiredArgs, name)
		}
	}
	logging.Error("WorkflowExecutor", fmt.Errorf("workflow execution started"), "ExecuteWorkflow called with workflow=%s, args=%+v, required=%+v", workflow.Name, args, requiredArgs)
	logging.Debug("WorkflowExecutor", "Executing workflow %s with %d steps", workflow.Name, len(workflow.Steps))

	// Validate inputs against args definition
	if err := we.validateInputs(workflow.Args, args); err != nil {
		logging.Error("WorkflowExecutor", err, "Input validation failed for workflow %s", workflow.Name)
		return nil, fmt.Errorf("input validation failed: %w", err)
	}

	// Create execution context with initial variables
	execCtx := &executionContext{
		input:        args,
		variables:    make(map[string]interface{}),
		results:      make(map[string]interface{}),
		templateVars: make([]string, 0),
		stepMetadata: make([]stepMetadata, 0),
	}
	logging.Debug("WorkflowExecutor", "Initial execution context: input=%+v, results=%+v", execCtx.input, execCtx.results)

	// Execute each step
	var lastStepResult *mcp.CallToolResult
	for i, step := range workflow.Steps {
		logging.Debug("WorkflowExecutor", "Executing step %d/%d: %s, tool: %s", i+1, len(workflow.Steps), step.ID, step.Tool)

		// Check if step has a condition
		var conditionEvaluation *bool
		var conditionResult interface{}
		var conditionTool string

		if step.Condition != nil {
			logging.Debug("WorkflowExecutor", "Step %s has condition, evaluating...", step.ID)
			conditionTool = step.Condition.Tool

			// Resolve template variables in condition arguments
			resolvedConditionArgs, err := we.resolveArguments(step.Condition.Args, execCtx)
			if err != nil {
				return nil, fmt.Errorf("failed to resolve condition arguments for step %s: %w", step.ID, err)
			}
			logging.Debug("WorkflowExecutor", "Step %s condition resolved args: %+v", step.ID, resolvedConditionArgs)

			// Execute the condition tool
			conditionToolResult, err := we.toolCaller.CallToolInternal(ctx, step.Condition.Tool, resolvedConditionArgs)
			if err != nil {
				// Condition tool failed - this means condition is false
				logging.Debug("WorkflowExecutor", "Step %s condition tool failed: %v", step.ID, err)
				conditionPassed := false
				conditionEvaluation = &conditionPassed
				conditionResult = false // Store boolean false as the result when tool call fails
			} else {
				// Parse the tool result for storage
				if len(conditionToolResult.Content) > 0 {
					if textContent, ok := conditionToolResult.Content[0].(mcp.TextContent); ok {
						// Try to parse as JSON first
						var parsedResult interface{}
						if err := json.Unmarshal([]byte(textContent.Text), &parsedResult); err != nil {
							// If not JSON, store as string
							conditionResult = textContent.Text
						} else {
							conditionResult = parsedResult
						}
					}
				}

				// Check if condition result matches expectation
				conditionPassed := (conditionToolResult.IsError == false) == step.Condition.Expect.Success

				// Also check JSON path expectations if provided
				if conditionPassed && len(step.Condition.Expect.JsonPath) > 0 {
					jsonPathPassed, err := we.validateJsonPath(conditionToolResult, step.Condition.Expect.JsonPath, execCtx)
					if err != nil {
						logging.Debug("WorkflowExecutor", "Step %s JSON path validation error: %v", step.ID, err)
						conditionPassed = false
					} else {
						conditionPassed = jsonPathPassed
					}
					logging.Debug("WorkflowExecutor", "Step %s JSON path validation result: %v", step.ID, jsonPathPassed)
				}

				conditionEvaluation = &conditionPassed
				logging.Debug("WorkflowExecutor", "Step %s condition result: tool_success=%v, expect_success=%v, condition_passed=%v",
					step.ID, !conditionToolResult.IsError, step.Condition.Expect.Success, conditionPassed)
			}

			// If condition failed, skip this step
			if !*conditionEvaluation {
				logging.Debug("WorkflowExecutor", "Step %s condition failed, skipping step", step.ID)

				// Record the skipped step metadata
				execCtx.stepMetadata = append(execCtx.stepMetadata, stepMetadata{
					ID:                  step.ID,
					Tool:                step.Tool,
					Store:               step.Store,
					Status:              "skipped",
					ConditionEvaluation: conditionEvaluation,
					ConditionResult:     conditionResult,
					ConditionTool:       conditionTool,
				})

				// Continue to next step
				continue
			}

			logging.Debug("WorkflowExecutor", "Step %s condition passed, executing step", step.ID)
		}

		// Resolve template variables in step arguments
		resolvedArgs, err := we.resolveArguments(step.Args, execCtx)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve arguments for step %s: %w", step.ID, err)
		}
		logging.Debug("WorkflowExecutor", "Step %s resolved args: %+v", step.ID, resolvedArgs)

		// Execute the tool
		result, err := we.toolCaller.CallToolInternal(ctx, step.Tool, resolvedArgs)
		if err != nil {
			logging.Error("WorkflowExecutor", err, "Step %s failed", step.ID)

			// Record the failed step metadata
			execCtx.stepMetadata = append(execCtx.stepMetadata, stepMetadata{
				ID:                  step.ID,
				Tool:                step.Tool,
				Store:               step.Store,
				Status:              "failed",
				ConditionEvaluation: conditionEvaluation,
				ConditionResult:     conditionResult,
				ConditionTool:       conditionTool,
			})

			// Enhance results with step-level information for partial results too
			enhancedPartialResults := make(map[string]interface{})

			// Copy existing stored results
			for k, v := range execCtx.results {
				enhancedPartialResults[k] = v
			}

			// Add step metadata to results including the failed step
			for _, stepMeta := range execCtx.stepMetadata {
				stepInfo := map[string]interface{}{
					"status": stepMeta.Status,
				}

				// Include condition information if present
				if stepMeta.ConditionEvaluation != nil {
					stepInfo["condition_evaluation"] = *stepMeta.ConditionEvaluation
				}
				if stepMeta.ConditionResult != nil {
					stepInfo["condition_result"] = stepMeta.ConditionResult
				}

				// Include condition tool if present
				if stepMeta.ConditionTool != "" {
					stepInfo["condition_tool"] = stepMeta.ConditionTool
				}

				// Include stored result if available
				if stepMeta.Store != "" && execCtx.results[stepMeta.Store] != nil {
					stepInfo["result"] = execCtx.results[stepMeta.Store]
				}

				enhancedPartialResults[stepMeta.ID] = stepInfo
			}

			// Create partial result with steps completed so far, including the failed step
			partialResult := map[string]interface{}{
				"workflow":     workflow.Name,
				"results":      enhancedPartialResults,
				"input":        execCtx.input,
				"templateVars": execCtx.templateVars,
				"stepMetadata": execCtx.stepMetadata,
				"status":       "failed",
				"error":        err.Error(),
				"failedStep":   step.ID,
			}

			// Return partial result as JSON for execution tracking
			partialJSON, jsonErr := json.Marshal(partialResult)
			if jsonErr != nil {
				// If JSON marshaling fails, return the original error without partial results
				return nil, fmt.Errorf("step %s failed: %w", step.ID, err)
			}

			// Return partial result with the original error
			// This allows execution tracking to capture successful steps before failure
			partialMCPResult := &mcp.CallToolResult{
				Content: []mcp.Content{
					mcp.NewTextContent(string(partialJSON)),
				},
				IsError: true, // Mark as error result
			}

			return partialMCPResult, fmt.Errorf("step %s failed: %w", step.ID, err)
		}
		logging.Debug("WorkflowExecutor", "Step %s result: %+v", step.ID, result)

		// Keep track of the last step result
		lastStepResult = result

		// Store result if requested
		// TODO: Implement step.Outputs processing for extracting specific fields
		if step.Store != "" {
			logging.Debug("WorkflowExecutor", "Processing result for step %s: %+v", step.ID, result)
			var resultData interface{}
			if len(result.Content) > 0 {
				logging.Debug("WorkflowExecutor", "Result content[0]: %+v (type: %T)", result.Content[0], result.Content[0])
				// Check if it's a TextContent
				if textContent, ok := result.Content[0].(mcp.TextContent); ok {
					logging.Debug("WorkflowExecutor", "TextContent.Text: %s", textContent.Text)
					// Try to parse as JSON first
					if err := json.Unmarshal([]byte(textContent.Text), &resultData); err != nil {
						logging.Debug("WorkflowExecutor", "Failed to parse as JSON: %v, storing as string", err)
						// If not JSON, store as string
						resultData = textContent.Text
					} else {
						logging.Debug("WorkflowExecutor", "Successfully parsed JSON: %+v", resultData)
					}
				}
			}
			execCtx.results[step.Store] = resultData
			logging.Debug("WorkflowExecutor", "Stored result from step %s as %s: %+v", step.ID, step.Store, resultData)
			logging.Debug("WorkflowExecutor", "Current execution context results: %+v", execCtx.results)
		}

		// Record step metadata for execution tracking
		execCtx.stepMetadata = append(execCtx.stepMetadata, stepMetadata{
			ID:                  step.ID,
			Tool:                step.Tool,
			Store:               step.Store,
			Status:              "completed",
			ConditionEvaluation: conditionEvaluation,
			ConditionResult:     conditionResult,
			ConditionTool:       conditionTool,
		})

		// Check if result indicates an error
		if result.IsError {
			logging.Error("WorkflowExecutor", fmt.Errorf("step returned error"), "Step %s returned error result", step.ID)
			// Return the error result immediately
			return result, nil
		}
	}

	// Enhance results with step-level information for BDD testing access
	enhancedResults := make(map[string]interface{})

	// Copy existing stored results
	for k, v := range execCtx.results {
		enhancedResults[k] = v
	}

	// Add step metadata to results for easy access via JSON path (e.g., results.step-id.status)
	for _, stepMeta := range execCtx.stepMetadata {
		stepInfo := map[string]interface{}{
			"status": stepMeta.Status,
		}

		// Include condition information if present
		if stepMeta.ConditionEvaluation != nil {
			stepInfo["condition_evaluation"] = *stepMeta.ConditionEvaluation
		}
		if stepMeta.ConditionResult != nil {
			stepInfo["condition_result"] = stepMeta.ConditionResult
		}

		// Include condition tool if present
		if stepMeta.ConditionTool != "" {
			stepInfo["condition_tool"] = stepMeta.ConditionTool
		}

		// Include stored result if available
		if stepMeta.Store != "" && execCtx.results[stepMeta.Store] != nil {
			stepInfo["result"] = execCtx.results[stepMeta.Store]
		}

		enhancedResults[stepMeta.ID] = stepInfo
	}

	// Return the final result
	finalResult := map[string]interface{}{
		"workflow":     workflow.Name,
		"results":      enhancedResults,      // Enhanced results with step information
		"input":        execCtx.input,        // Include input arguments
		"templateVars": execCtx.templateVars, // Include template variables used
		"stepMetadata": execCtx.stepMetadata, // Include step metadata for execution tracking
		"status":       "completed",
	}

	// If the last step wasn't stored, merge its result into the top level
	if lastStepResult != nil && len(workflow.Steps) > 0 {
		lastStep := workflow.Steps[len(workflow.Steps)-1]
		if lastStep.Store == "" {
			logging.Debug("WorkflowExecutor", "Last step %s has no store, merging result into top level", lastStep.ID)
			// Parse the last step's result and merge it
			if len(lastStepResult.Content) > 0 {
				if textContent, ok := lastStepResult.Content[0].(mcp.TextContent); ok {
					var lastResultData interface{}
					if err := json.Unmarshal([]byte(textContent.Text), &lastResultData); err == nil {
						if lastResultMap, ok := lastResultData.(map[string]interface{}); ok {
							// Merge last step result into final result
							for k, v := range lastResultMap {
								finalResult[k] = v
							}
							logging.Debug("WorkflowExecutor", "Merged last step result into final result: %+v", lastResultMap)
						}
					}
				}
			}
		}
	}

	logging.Debug("WorkflowExecutor", "Final result before JSON marshal: %+v", finalResult)

	resultJSON, err := json.Marshal(finalResult)
	if err != nil {
		logging.Error("WorkflowExecutor", err, "Failed to marshal final result")
		return nil, fmt.Errorf("failed to marshal result: %w", err)
	}
	logging.Debug("WorkflowExecutor", "Final result JSON: %s", string(resultJSON))

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.NewTextContent(string(resultJSON)),
		},
	}, nil
}

// executionContext holds the state during workflow execution
type executionContext struct {
	input        map[string]interface{} // Original input arguments
	variables    map[string]interface{} // User-defined variables
	results      map[string]interface{} // Results from previous steps
	templateVars []string               // Track template variables used
	stepMetadata []stepMetadata         // Track step metadata
}

// validateInputs validates the input arguments against the args definition
func (we *WorkflowExecutor) validateInputs(argsDefinition map[string]api.ArgDefinition, args map[string]interface{}) error {
	logging.Debug("WorkflowExecutor", "validateInputs called with args: %+v", args)
	logging.Debug("WorkflowExecutor", "validateInputs args definition: %+v", argsDefinition)

	// Check required fields and apply defaults
	for key, argDef := range argsDefinition {
		value, exists := args[key]

		if !exists {
			if argDef.Required {
				logging.Error("WorkflowExecutor", fmt.Errorf("missing required field"), "Required field '%s' is missing from args %+v", key, args)
				return fmt.Errorf("required field '%s' is missing", key)
			}
			// Apply default value if not provided
			if argDef.Default != nil {
				logging.Debug("WorkflowExecutor", "Applying default value for %s: %v", key, argDef.Default)
				args[key] = argDef.Default
			}
			continue
		}

		// Basic type validation for provided values
		if !we.validateType(value, argDef.Type) {
			return fmt.Errorf("field '%s' has wrong type, expected %s", key, argDef.Type)
		}
	}

	// Check for unknown arguments - allow extra properties for flexibility
	// This follows the same pattern as ServiceClass.ValidateServiceArgs

	logging.Debug("WorkflowExecutor", "validateInputs final args: %+v", args)
	return nil
}

// validateType performs basic type validation
func (we *WorkflowExecutor) validateType(value interface{}, expectedType string) bool {
	switch expectedType {
	case "string":
		_, ok := value.(string)
		return ok
	case "number":
		switch value.(type) {
		case int, int32, int64, float32, float64:
			return true
		default:
			return false
		}
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "array":
		switch value.(type) {
		case []interface{}, []string, []int, []float64:
			return true
		default:
			return false
		}
	case "object":
		_, ok := value.(map[string]interface{})
		return ok
	default:
		return true // Unknown types pass validation
	}
}

// resolveArguments resolves template variables in step arguments
func (we *WorkflowExecutor) resolveArguments(args map[string]interface{}, ctx *executionContext) (map[string]interface{}, error) {
	resolved := make(map[string]interface{})

	for key, value := range args {
		resolvedValue, err := we.resolveValue(value, ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to render arguments for argument '%s': %w", key, err)
		}
		resolved[key] = resolvedValue
	}

	return resolved, nil
}

// resolveValue recursively resolves template variables in a value
func (we *WorkflowExecutor) resolveValue(value interface{}, ctx *executionContext) (interface{}, error) {
	switch v := value.(type) {
	case string:
		// Check if it contains a template pattern
		if strings.Contains(v, "{{") && strings.Contains(v, "}}") {
			return we.resolveTemplate(v, ctx)
		}
		return v, nil

	case map[string]interface{}:
		// Recursively resolve map values
		resolved := make(map[string]interface{})
		for k, val := range v {
			resolvedVal, err := we.resolveValue(val, ctx)
			if err != nil {
				return nil, err
			}
			resolved[k] = resolvedVal
		}
		return resolved, nil

	case []interface{}:
		// Recursively resolve array elements
		resolved := make([]interface{}, len(v))
		for i, val := range v {
			resolvedVal, err := we.resolveValue(val, ctx)
			if err != nil {
				return nil, err
			}
			resolved[i] = resolvedVal
		}
		return resolved, nil

	default:
		// Return other types as-is
		return value, nil
	}
}

// resolveTemplate resolves a template string
func (we *WorkflowExecutor) resolveTemplate(templateStr string, ctx *executionContext) (interface{}, error) {
	logging.Debug("WorkflowExecutor", "Resolving template: %s", templateStr)
	logging.Debug("WorkflowExecutor", "Original results: %v", ctx.results)

	// Track template variables used (extract from template string)
	if strings.Contains(templateStr, ".input.") {
		// Find all .input.variable_name patterns
		words := strings.Fields(templateStr)
		for _, word := range words {
			if strings.Contains(word, ".input.") {
				// Extract variable names like "input.service_name", "input.message"
				if start := strings.Index(word, ".input."); start != -1 {
					remaining := word[start+1:] // Remove the leading dot
					if end := strings.IndexAny(remaining, " }"); end != -1 {
						varName := remaining[:end]
						if varName != "" && !contains(ctx.templateVars, varName) {
							ctx.templateVars = append(ctx.templateVars, varName)
						}
					} else {
						// Take everything until end of string, removing trailing }}
						varName := strings.TrimSuffix(remaining, "}}")
						varName = strings.TrimSuffix(varName, "}")
						if varName != "" && !contains(ctx.templateVars, varName) {
							ctx.templateVars = append(ctx.templateVars, varName)
						}
					}
				}
			}
		}
	}

	// Create template context with original objects (no preprocessing)
	templateCtx := map[string]interface{}{
		"input":   ctx.input,
		"vars":    ctx.variables,
		"results": ctx.results,
		"context": ctx.results, // Alias for results to support .context.variable syntax
	}

	logging.Debug("WorkflowExecutor", "Template context results (raw): %v", templateCtx["results"])

	// Check if this is a simple variable access pattern ({{ .input.key }} or {{ .results.key }})
	// If so, try to preserve the original type
	if we.isSimpleVariableAccess(templateStr) {
		originalValue := we.getOriginalValue(templateStr, ctx)
		if originalValue != nil {
			// For simple variable access, return the original value with its type preserved
			return originalValue, nil
		}
	}

	// Parse and execute template with strict mode options
	result, err := we.template.RenderGoTemplate(templateStr, templateCtx)
	if err != nil {
		return nil, fmt.Errorf("failed to render arguments: %w", err)
	}

	logging.Debug("WorkflowExecutor", "Template result: %v", result)
	return result, nil
}

// isSimpleVariableAccess checks if the template is a simple variable access pattern
func (we *WorkflowExecutor) isSimpleVariableAccess(templateStr string) bool {
	// Remove whitespace and check if it matches {{ .input.key }} or {{ .results.key }} pattern
	trimmed := strings.TrimSpace(templateStr)
	if !strings.HasPrefix(trimmed, "{{") || !strings.HasSuffix(trimmed, "}}") {
		return false
	}

	// Extract the inner part
	inner := strings.TrimSpace(trimmed[2 : len(trimmed)-2])

	// Check if it's a simple dot notation access
	return strings.HasPrefix(inner, ".input.") || strings.HasPrefix(inner, ".results.") || strings.HasPrefix(inner, ".vars.")
}

// getOriginalValue extracts the original value from the context based on the template path
func (we *WorkflowExecutor) getOriginalValue(templateStr string, ctx *executionContext) interface{} {
	// Remove whitespace and extract the path
	trimmed := strings.TrimSpace(templateStr)
	if !strings.HasPrefix(trimmed, "{{") || !strings.HasSuffix(trimmed, "}}") {
		return nil
	}

	// Extract the inner part
	inner := strings.TrimSpace(trimmed[2 : len(trimmed)-2])

	// Parse the path and get the value
	if strings.HasPrefix(inner, ".input.") {
		key := inner[7:] // Remove ".input."
		return ctx.input[key]
	} else if strings.HasPrefix(inner, ".results.") {
		key := inner[9:] // Remove ".results."
		return ctx.results[key]
	} else if strings.HasPrefix(inner, ".vars.") {
		key := inner[6:] // Remove ".vars."
		return ctx.variables[key]
	}

	return nil
}

// contains checks if a string slice contains a specific string
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// validateJsonPath validates JSON path expectations against tool result
func (we *WorkflowExecutor) validateJsonPath(toolResult *mcp.CallToolResult, jsonPathExpectations map[string]interface{}, execCtx *executionContext) (bool, error) {
	// Parse the tool result as JSON
	var resultData interface{}
	if len(toolResult.Content) == 0 {
		return false, fmt.Errorf("tool result has no content")
	}

	// Get the first content item and try to parse as JSON
	if textContent, ok := toolResult.Content[0].(mcp.TextContent); ok {
		if err := json.Unmarshal([]byte(textContent.Text), &resultData); err != nil {
			return false, fmt.Errorf("failed to parse tool result as JSON: %w", err)
		}
	} else {
		// If it's not text content, try to use it directly
		resultData = toolResult.Content[0]
	}

	// Validate each JSON path expectation
	for jsonPath, expectedValue := range jsonPathExpectations {
		// Resolve the expected value template if it contains templating
		resolvedExpectedValue, err := we.resolveValue(expectedValue, execCtx)
		if err != nil {
			return false, fmt.Errorf("failed to resolve expected value template for path %s: %w", jsonPath, err)
		}

		// Get the actual value from the result using simple path navigation
		actualValue, err := we.getValueFromPath(resultData, jsonPath)
		if err != nil {
			logging.Debug("WorkflowExecutor", "Failed to get value from path %s: %v", jsonPath, err)
			return false, nil // Path not found = condition fails
		}

		// Compare the values
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

// getValueFromPath extracts a value from nested data using a simple path (e.g., "name", "data.field")
func (we *WorkflowExecutor) getValueFromPath(data interface{}, path string) (interface{}, error) {
	parts := strings.Split(path, ".")
	current := data

	for _, part := range parts {
		switch v := current.(type) {
		case map[string]interface{}:
			if value, exists := v[part]; exists {
				current = value
			} else {
				return nil, fmt.Errorf("path '%s' not found", path)
			}
		default:
			return nil, fmt.Errorf("cannot navigate path '%s': not an object", path)
		}
	}

	return current, nil
}

// valuesEqual compares two values for equality, handling type conversions
func (we *WorkflowExecutor) valuesEqual(actual, expected interface{}) bool {
	// Direct equality check first
	if actual == expected {
		return true
	}

	// Convert both to strings for comparison if they're different types
	actualStr := fmt.Sprintf("%v", actual)
	expectedStr := fmt.Sprintf("%v", expected)

	return actualStr == expectedStr
}
