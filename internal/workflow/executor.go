package workflow

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/template"
	"github.com/giantswarm/muster/pkg/logging"

	"github.com/mark3labs/mcp-go/mcp"
)

// ToolCaller interface - what we need from the aggregator
type ToolCaller interface {
	CallToolInternal(ctx context.Context, toolName string, args map[string]interface{}) (*mcp.CallToolResult, error)
}

// EventCallback interface for generating workflow step events
type EventCallback interface {
	// GenerateStepEvent generates an event for a workflow step operation
	GenerateStepEvent(workflowName string, stepID string, eventType string, data map[string]interface{})
}

// NoOpEventCallback provides a no-operation implementation of EventCallback
type NoOpEventCallback struct{}

func (n *NoOpEventCallback) GenerateStepEvent(workflowName string, stepID string, eventType string, data map[string]interface{}) {
	// No operation - events are disabled
}

// stepMetadata holds metadata about an executed step for tracking purposes
type stepMetadata struct {
	ID                  string      // Original step ID from workflow definition
	Tool                string      // Tool name used in the step
	Store               bool        // Whether the step result was stored in workflow results
	Status              string      // Step execution status: "completed", "skipped", "failed"
	AllowFailure        bool        // Whether this step is allowed to fail without failing the workflow
	ConditionEvaluation *bool       // Boolean result of condition evaluation (nil if no condition)
	ConditionResult     interface{} // Actual result from condition tool call (nil if no condition)
	ConditionTool       string      // Tool used for condition evaluation (empty if no condition)
}

// executionContext holds the state during workflow execution.
type executionContext struct {
	input        map[string]interface{} // Original input arguments
	variables    map[string]interface{} // User-defined variables
	results      map[string]interface{} // Results from previous steps
	templateVars []string               // Track template variables used
	stepMetadata []stepMetadata         // Track step metadata
}

// WorkflowExecutor executes workflow steps
type WorkflowExecutor struct {
	toolCaller    ToolCaller
	template      *template.Engine
	eventCallback EventCallback
}

// NewWorkflowExecutor creates a new workflow executor
func NewWorkflowExecutor(toolCaller ToolCaller, eventCallback EventCallback) *WorkflowExecutor {
	if eventCallback == nil {
		eventCallback = &NoOpEventCallback{}
	}
	return &WorkflowExecutor{
		toolCaller:    toolCaller,
		template:      template.New(),
		eventCallback: eventCallback,
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

	// Validate inputs against args definition (this applies default values to args)
	if err := we.validateInputs(workflow.Args, args); err != nil {
		logging.Error("WorkflowExecutor", err, "Input validation failed for workflow %s", workflow.Name)
		return nil, fmt.Errorf("input validation failed: %w", err)
	}

	// Create execution context with validated input (including default values)
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

			var conditionToolResult *mcp.CallToolResult
			var conditionError error

			// Check if condition references a previous step result
			if step.Condition.FromStep != "" {
				logging.Debug("WorkflowExecutor", "Step %s condition references previous step: %s", step.ID, step.Condition.FromStep)

				// Find the referenced step result
				var referencedStepResult interface{}
				found := false

				// Look for the step result in stored results first
				for _, stepMeta := range execCtx.stepMetadata {
					if stepMeta.ID == step.Condition.FromStep {
						if stepMeta.Store && execCtx.results[stepMeta.ID] != nil {
							referencedStepResult = execCtx.results[stepMeta.ID]
							found = true
							logging.Debug("WorkflowExecutor", "Found stored result for step %s: %+v", step.Condition.FromStep, referencedStepResult)
							break
						}
					}
				}

				// If not found in stored results, look for it in step metadata directly
				if !found {
					for _, stepMeta := range execCtx.stepMetadata {
						if stepMeta.ID == step.Condition.FromStep {
							// For failed steps, create a result structure
							if stepMeta.Status == "failed" { //nolint:goconst
								referencedStepResult = map[string]interface{}{
									"error":   fmt.Sprintf("Step %s failed", stepMeta.ID),
									"success": false,
									"isError": true,
									"status":  stepMeta.Status,
								}
								found = true
								logging.Debug("WorkflowExecutor", "Created error result for failed step %s", step.Condition.FromStep)
								break
							} else if stepMeta.Status == "completed" { //nolint:goconst
								// For completed steps without stored results, create a basic success structure
								referencedStepResult = map[string]interface{}{
									"success": true,
									"status":  stepMeta.Status,
								}
								found = true
								logging.Debug("WorkflowExecutor", "Created basic success result for completed step %s", step.Condition.FromStep)
								break
							}
						}
					}
				}

				if !found {
					logging.Error("WorkflowExecutor", fmt.Errorf("referenced step not found"), "Step %s condition references non-existent step result: %s. Available steps: %+v", step.ID, step.Condition.FromStep, execCtx.stepMetadata)
					return nil, fmt.Errorf("step %s condition references non-existent step result: %s", step.ID, step.Condition.FromStep)
				}

				// Create a mock CallToolResult from the referenced step result
				resultJSON, err := json.Marshal(referencedStepResult)
				if err != nil {
					return nil, fmt.Errorf("failed to marshal referenced step result for condition evaluation: %w", err)
				}

				conditionToolResult = &mcp.CallToolResult{
					Content: []mcp.Content{
						mcp.NewTextContent(string(resultJSON)),
					},
					IsError: false,
				}

				// Check if this was a failed step (error result)
				if resultMap, ok := referencedStepResult.(map[string]interface{}); ok {
					if isError, exists := resultMap["isError"].(bool); exists && isError {
						conditionToolResult.IsError = true
					}
				}

				conditionTool = fmt.Sprintf("from_step:%s", step.Condition.FromStep)
				conditionResult = referencedStepResult
			} else {
				// Execute the condition tool as before
				conditionTool = step.Condition.Tool

				// Resolve template variables in condition arguments
				resolvedConditionArgs, err := we.resolveArguments(step.Condition.Args, execCtx)
				if err != nil {
					return nil, fmt.Errorf("failed to resolve condition arguments for step %s: %w", step.ID, err)
				}
				logging.Debug("WorkflowExecutor", "Step %s condition resolved args: %+v", step.ID, resolvedConditionArgs)

				// Execute the condition tool
				condCtx, endCondSpan := startStepSpan(ctx, workflow.Name, step.ID+".condition", step.Condition.Tool)
				conditionToolResult, conditionError = we.toolCaller.CallToolInternal(condCtx, step.Condition.Tool, resolvedConditionArgs)
				endCondSpan(conditionToolResult != nil && conditionToolResult.IsError, conditionError)
				if conditionError != nil {
					// Condition tool failed - this means condition is false
					logging.Debug("WorkflowExecutor", "Step %s condition tool failed: %v", step.ID, conditionError)
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
				}
			}

			// Evaluate condition expectations
			var conditionPassed = true

			if conditionError != nil {
				// Condition tool failed
				conditionPassed = false
				logging.Debug("WorkflowExecutor", "Step %s condition failed due to tool error", step.ID)
			} else {
				// Check if we have an expect condition to evaluate
				// Since validation ensures at least one expectation is provided, we always
				// try to evaluate both expect and expect_not sections if they might be present.
				// An expect condition is present if there are JsonPath conditions OR
				// if the condition passed validation (meaning expect was provided)
				hasExpect := len(step.Condition.Expect.JsonPath) > 0
				// Check if expect_not condition is present
				hasExpectNot := len(step.Condition.ExpectNot.JsonPath) > 0

				// If neither has JsonPath, then at least one must have a success condition
				// since validation ensures at least one expectation section exists
				if !hasExpect && !hasExpectNot {
					hasExpect = true // Default to checking expect first
				}

				if hasExpect {
					// Evaluate expect condition
					expectPassed := (!conditionToolResult.IsError) == step.Condition.Expect.Success

					// Also check JSON path expectations if provided
					if expectPassed && len(step.Condition.Expect.JsonPath) > 0 {
						jsonPathPassed, err := we.validateJsonPath(conditionToolResult, step.Condition.Expect.JsonPath, execCtx)
						if err != nil {
							logging.Debug("WorkflowExecutor", "Step %s expect JSON path validation error: %v", step.ID, err)
							expectPassed = false
						} else {
							expectPassed = jsonPathPassed
						}
						logging.Debug("WorkflowExecutor", "Step %s expect JSON path validation result: %v", step.ID, jsonPathPassed)
					}

					conditionPassed = conditionPassed && expectPassed
					logging.Debug("WorkflowExecutor", "Step %s expect condition result: %v", step.ID, expectPassed)
				}

				if hasExpectNot {
					// Evaluate expect_not condition
					expectNotPassed := true

					// Only check success condition if ExpectNot.Success is explicitly set
					// Check if the Success field was actually set in the configuration
					if step.Condition.ExpectNot.Success || len(step.Condition.ExpectNot.JsonPath) == 0 {
						// This means Success was explicitly set to true, or no JsonPath is provided
						if len(step.Condition.ExpectNot.JsonPath) == 0 {
							// Only success condition specified in expect_not
							expectNotPassed = (!conditionToolResult.IsError) == step.Condition.ExpectNot.Success
						}
					}

					// Also check JSON path expectations if provided
					if len(step.Condition.ExpectNot.JsonPath) > 0 {
						jsonPathPassed, err := we.validateJsonPath(conditionToolResult, step.Condition.ExpectNot.JsonPath, execCtx)
						if err != nil {
							logging.Debug("WorkflowExecutor", "Step %s expect_not JSON path validation error: %v", step.ID, err)
							// If JSON path validation fails, it means the path doesn't exist or there's an error
							// For expect_not, this means the condition is satisfied (the expected value is not present)
							expectNotPassed = true
						} else {
							// If JSON path validation succeeds, it means the path exists and matches the expected value
							// For expect_not, this means the condition is NOT satisfied (the expected value IS present)
							expectNotPassed = !jsonPathPassed
						}
						logging.Debug("WorkflowExecutor", "Step %s expect_not JSON path validation result: %v, expectNotPassed: %v", step.ID, jsonPathPassed, expectNotPassed)
					}

					conditionPassed = conditionPassed && expectNotPassed
					logging.Debug("WorkflowExecutor", "Step %s expect_not condition result: %v", step.ID, expectNotPassed)
				}
			}

			conditionEvaluation = &conditionPassed
			logging.Debug("WorkflowExecutor", "Step %s final condition result: %v", step.ID, conditionPassed)

			// Generate step condition evaluation event
			we.eventCallback.GenerateStepEvent(workflow.Name, step.ID, "condition_evaluated", map[string]interface{}{
				"tool":             step.Tool,
				"condition_result": fmt.Sprintf("%t", conditionPassed),
			})

			// If condition failed, skip this step
			if !*conditionEvaluation {
				logging.Debug("WorkflowExecutor", "Step %s condition failed, skipping step", step.ID)

				// Generate step skipped event
				we.eventCallback.GenerateStepEvent(workflow.Name, step.ID, "step_skipped", map[string]interface{}{
					"tool":             step.Tool,
					"condition_result": "false",
				})

				// Record the skipped step metadata
				execCtx.stepMetadata = append(execCtx.stepMetadata, stepMetadata{
					ID:                  step.ID,
					Tool:                step.Tool,
					Store:               step.Store,
					Status:              "skipped",
					AllowFailure:        step.AllowFailure,
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

		// Generate step started event
		we.eventCallback.GenerateStepEvent(workflow.Name, step.ID, "step_started", map[string]interface{}{
			"tool": step.Tool,
		})

		stepCtx, endStepSpan := startStepSpan(ctx, workflow.Name, step.ID, step.Tool)
		result, err := we.toolCaller.CallToolInternal(stepCtx, step.Tool, resolvedArgs)
		endStepSpan(result != nil && result.IsError, err)
		if err != nil {
			logging.Error("WorkflowExecutor", err, "Step %s failed", step.ID)

			// Generate step failed event
			we.eventCallback.GenerateStepEvent(workflow.Name, step.ID, "step_failed", map[string]interface{}{
				"tool":          step.Tool,
				"error":         err.Error(),
				"allow_failure": step.AllowFailure,
			})

			// Record the failed step metadata
			execCtx.stepMetadata = append(execCtx.stepMetadata, stepMetadata{
				ID:                  step.ID,
				Tool:                step.Tool,
				Store:               step.Store,
				Status:              "failed",
				AllowFailure:        step.AllowFailure,
				ConditionEvaluation: conditionEvaluation,
				ConditionResult:     conditionResult,
				ConditionTool:       conditionTool,
			})

			// If step allows failure, continue execution
			if step.AllowFailure {
				logging.Debug("WorkflowExecutor", "Step %s failed but allow_failure is true, continuing execution", step.ID)

				// Store the error result for subsequent steps to reference
				if step.Store {
					errorResult := map[string]interface{}{
						"error":   err.Error(),
						"success": false,
						"isError": true,
					}
					execCtx.results[step.ID] = errorResult
					logging.Debug("WorkflowExecutor", "Stored error result from step %s as %s: %+v", step.ID, step.ID, errorResult)
				}

				// Continue to next step
				continue
			}

			// Build clean partial result for failed workflows
			steps := we.buildStepsArray(execCtx.stepMetadata, execCtx.results, step.ID, err.Error())

			partialResult := map[string]interface{}{
				"execution_id":  "", // Will be filled by manager
				"workflow":      workflow.Name,
				"status":        "failed",
				"input":         execCtx.input,
				"steps":         steps,
				"template_vars": execCtx.templateVars,
			}

			// Return partial result as JSON for execution tracking
			partialJSON, jsonErr := json.Marshal(partialResult)
			if jsonErr != nil {
				// If JSON marshaling fails, return the original error without partial results
				return nil, fmt.Errorf("step %s failed: %w", step.ID, err)
			}

			// Return partial result with the original error
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
		if step.Store {
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
			execCtx.results[step.ID] = resultData
			logging.Debug("WorkflowExecutor", "Stored result from step %s as %s: %+v", step.ID, step.ID, resultData)
			logging.Debug("WorkflowExecutor", "Current execution context results: %+v", execCtx.results)
		}

		// Generate step completed event (for now, will be updated if error is detected)
		we.eventCallback.GenerateStepEvent(workflow.Name, step.ID, "step_completed", map[string]interface{}{
			"tool": step.Tool,
		})

		// Record step metadata for execution tracking
		execCtx.stepMetadata = append(execCtx.stepMetadata, stepMetadata{
			ID:                  step.ID,
			Tool:                step.Tool,
			Store:               step.Store,
			Status:              "completed",
			AllowFailure:        step.AllowFailure,
			ConditionEvaluation: conditionEvaluation,
			ConditionResult:     conditionResult,
			ConditionTool:       conditionTool,
		})

		// Check if result indicates an error
		if result.IsError {
			logging.Error("WorkflowExecutor", fmt.Errorf("step returned error"), "Step %s returned error result", step.ID)

			// Generate step failed event for error result case
			we.eventCallback.GenerateStepEvent(workflow.Name, step.ID, "step_failed", map[string]interface{}{
				"tool":          step.Tool,
				"error":         "step returned error result",
				"allow_failure": step.AllowFailure,
			})

			// If step allows failure, treat as a normal step failure and continue
			if step.AllowFailure {
				logging.Debug("WorkflowExecutor", "Step %s returned error result but allow_failure is true, continuing execution", step.ID)

				// Update the step metadata status to failed
				if len(execCtx.stepMetadata) > 0 {
					execCtx.stepMetadata[len(execCtx.stepMetadata)-1].Status = "failed"
				}

				// Store error result for from_step conditions if step.Store is true
				if step.Store {
					// Create a structured error result that conditions can evaluate
					var errorMessage string
					if len(result.Content) > 0 {
						if textContent, ok := result.Content[0].(mcp.TextContent); ok {
							errorMessage = textContent.Text
						}
					}

					errorResult := map[string]interface{}{
						"success": false,
						"isError": true,
						"error":   errorMessage,
					}
					execCtx.results[step.ID] = errorResult
					logging.Debug("WorkflowExecutor", "Stored error result from failed step %s: %+v", step.ID, errorResult)
				}

				// Continue to next step
				continue
			}

			// Return the error result immediately
			return result, nil
		}
	}

	// Build clean final result with consolidated step information
	steps := we.buildStepsArray(execCtx.stepMetadata, execCtx.results, "", "")

	finalResult := map[string]interface{}{
		"execution_id":  "", // Will be filled by manager
		"workflow":      workflow.Name,
		"status":        "completed",
		"input":         execCtx.input,
		"steps":         steps,
		"template_vars": execCtx.templateVars,
	}

	// If the last step wasn't stored, merge its result into the top level
	if lastStepResult != nil && len(workflow.Steps) > 0 {
		lastStep := workflow.Steps[len(workflow.Steps)-1]
		if !lastStep.Store {
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

// buildStepsArray creates a consolidated steps array from step metadata and results
func (we *WorkflowExecutor) buildStepsArray(stepMetadata []stepMetadata, results map[string]interface{}, failedStepID string, errorMessage string) []map[string]interface{} {
	var steps []map[string]interface{}

	for _, stepMeta := range stepMetadata {
		step := map[string]interface{}{
			"id":     stepMeta.ID,
			"tool":   stepMeta.Tool,
			"status": stepMeta.Status,
		}

		// Add condition information if present
		if stepMeta.ConditionEvaluation != nil {
			step["condition_evaluation"] = *stepMeta.ConditionEvaluation
		}
		if stepMeta.ConditionResult != nil {
			step["condition_result"] = stepMeta.ConditionResult
		}
		if stepMeta.ConditionTool != "" {
			step["condition_tool"] = stepMeta.ConditionTool
		}

		// Add allow_failure flag if true
		if stepMeta.AllowFailure {
			step["allow_failure"] = stepMeta.AllowFailure
		}

		// Add result if available
		if stepMeta.Store && results[stepMeta.ID] != nil {
			step["result"] = results[stepMeta.ID]
		}

		// Add error if this is the failed step
		if failedStepID != "" && stepMeta.ID == failedStepID {
			step["error"] = errorMessage
		}

		steps = append(steps, step)
	}

	return steps
}
