package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"text/template"

	"muster/internal/api"
	"muster/pkg/logging"

	"github.com/mark3labs/mcp-go/mcp"
)

// ToolCaller interface - what we need from the aggregator
type ToolCaller interface {
	CallToolInternal(ctx context.Context, toolName string, args map[string]interface{}) (*mcp.CallToolResult, error)
}

// stepMetadata holds metadata about an executed step for tracking purposes
type stepMetadata struct {
	ID    string // Original step ID from workflow definition
	Tool  string // Tool name used in the step
	Store string // Variable name where result was stored (if any)
}

// WorkflowExecutor executes workflow steps
type WorkflowExecutor struct {
	toolCaller ToolCaller
}

// NewWorkflowExecutor creates a new workflow executor
func NewWorkflowExecutor(toolCaller ToolCaller) *WorkflowExecutor {
	return &WorkflowExecutor{
		toolCaller: toolCaller,
	}
}

// ExecuteWorkflow executes a workflow with the given arguments
func (we *WorkflowExecutor) ExecuteWorkflow(ctx context.Context, workflow *api.Workflow, args map[string]interface{}) (*mcp.CallToolResult, error) {
	logging.Error("WorkflowExecutor", fmt.Errorf("workflow execution started"), "ExecuteWorkflow called with workflow=%s, args=%+v, required=%+v", workflow.Name, args, workflow.InputSchema.Required)
	logging.Debug("WorkflowExecutor", "Executing workflow %s with %d steps", workflow.Name, len(workflow.Steps))

	// Validate inputs against schema
	if err := we.validateInputs(workflow.InputSchema, args); err != nil {
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

		// Resolve template variables in arguments
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
				ID:    step.ID,
				Tool:  step.Tool,
				Store: step.Store,
			})

			// Create partial result with steps completed so far, including the failed step
			partialResult := map[string]interface{}{
				"workflow":     workflow.Name,
				"results":      execCtx.results,
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
			ID:    step.ID,
			Tool:  step.Tool,
			Store: step.Store,
		})

		// Check if result indicates an error
		if result.IsError {
			logging.Error("WorkflowExecutor", fmt.Errorf("step returned error"), "Step %s returned error result", step.ID)
			// Return the error result immediately
			return result, nil
		}
	}

	// Return the final result
	finalResult := map[string]interface{}{
		"workflow":     workflow.Name,
		"results":      execCtx.results,
		"input":        execCtx.input,        // Include input parameters
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
	input        map[string]interface{} // Original input parameters
	variables    map[string]interface{} // User-defined variables
	results      map[string]interface{} // Results from previous steps
	templateVars []string               // Track template variables used
	stepMetadata []stepMetadata         // Track step metadata
}

// validateInputs validates the input arguments against the schema
func (we *WorkflowExecutor) validateInputs(schema api.WorkflowInputSchema, args map[string]interface{}) error {
	logging.Debug("WorkflowExecutor", "validateInputs called with args: %+v", args)
	logging.Debug("WorkflowExecutor", "validateInputs schema properties: %+v", schema.Properties)

	// Check required fields
	logging.Debug("WorkflowExecutor", "Checking required fields: %+v", schema.Required)
	for _, required := range schema.Required {
		if _, exists := args[required]; !exists {
			logging.Error("WorkflowExecutor", fmt.Errorf("missing required field"), "Required field '%s' is missing from args %+v", required, args)
			return fmt.Errorf("required field '%s' is missing", required)
		}
	}

	// Validate each provided argument
	for key, value := range args {
		prop, exists := schema.Properties[key]
		if !exists {
			// Allow extra properties for flexibility
			continue
		}

		// Basic type validation
		if !we.validateType(value, prop.Type) {
			return fmt.Errorf("field '%s' has wrong type, expected %s", key, prop.Type)
		}
	}

	// Apply defaults for missing optional fields
	for key, prop := range schema.Properties {
		logging.Debug("WorkflowExecutor", "Checking property %s: exists=%v, default=%+v", key, args[key] != nil, prop.Default)
		if _, exists := args[key]; !exists && prop.Default != nil {
			logging.Debug("WorkflowExecutor", "Applying default value for %s: %v", key, prop.Default)
			args[key] = prop.Default
		}
	}

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

	// Parse and execute template with strict mode options
	tmpl, err := template.New("arg").Option("missingkey=error").Parse(templateStr)
	if err != nil {
		return nil, fmt.Errorf("invalid template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, templateCtx); err != nil {
		// Check for missing key errors and provide more context
		if strings.Contains(err.Error(), "executing") && strings.Contains(err.Error(), "no such key") {
			return nil, fmt.Errorf("failed to render arguments: template variable not found: %w", err)
		}
		return nil, fmt.Errorf("failed to render arguments: %w", err)
	}

	result := buf.String()
	logging.Debug("WorkflowExecutor", "Template result: %s", result)

	// Try to parse as JSON first
	var jsonResult interface{}
	if err := json.Unmarshal([]byte(result), &jsonResult); err == nil {
		return jsonResult, nil
	}

	// If not valid JSON, return as string
	return result, nil
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
