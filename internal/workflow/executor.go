package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

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

// Step execution statuses recorded in step metadata and surfaced in results.
const (
	statusCompleted = "completed"
	statusSkipped   = "skipped"
	statusFailed    = "failed"
)

// stepMetadata holds metadata about an executed step for tracking purposes
type stepMetadata struct {
	ID                  string      // Original step ID from workflow definition
	Tool                string      // Tool name used in the step
	Output              bool        // Whether the step result is included in the returned document
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
	logging.Debug("WorkflowExecutor", "ExecuteWorkflow called with workflow=%s, args=%+v, required=%+v", workflow.Name, args, requiredArgs)
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

		// Dispatch by step kind: forEach loop, parallel group, or plain tool call.
		var outcome stepOutcome
		var err error
		switch {
		case step.ForEach != nil:
			outcome, err = we.runForEach(ctx, workflow.Name, step, execCtx)
		case len(step.Parallel) > 0:
			outcome, err = we.runParallel(ctx, workflow.Name, step, execCtx)
		default:
			outcome, err = we.runStep(ctx, workflow.Name, plainStepView(step), execCtx)
		}

		// A Go error (e.g. argument or condition resolution failure) is fatal;
		// run best-effort cleanup before surfacing it, mirroring step failures.
		if err != nil {
			we.runOnFailure(ctx, workflow, execCtx)
			return nil, err
		}
		if outcome.stop {
			return we.failWorkflow(ctx, workflow, execCtx, outcome)
		}
		// Only plain tool steps surface a single result to merge at the end.
		if outcome.result != nil {
			lastStepResult = outcome.result
		}
	}

	// When the workflow declares an output projection, render it once against the
	// completed step results and return it in place of the default envelope. This
	// lets a workflow return a small, shaped document instead of dumping every
	// step result. Referencing here works for every step regardless of its output
	// flag (see #873).
	if len(workflow.Output) > 0 {
		projected, err := we.renderOutputProjection(workflow.Output, execCtx)
		if err != nil {
			logging.Error("WorkflowExecutor", err, "Failed to render output projection")
			return nil, fmt.Errorf("failed to render output projection: %w", err)
		}
		projectedJSON, err := json.Marshal(projected)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal projected output: %w", err)
		}
		logging.Debug("WorkflowExecutor", "Projected output JSON: %s", string(projectedJSON))
		return &mcp.CallToolResult{
			Content: []mcp.Content{mcp.NewTextContent(string(projectedJSON))},
		}, nil
	}

	// Build clean final result with consolidated step information
	steps := we.buildStepsArray(execCtx.stepMetadata, execCtx.results, "", "")

	finalResult := map[string]interface{}{
		"execution_id":  "", // Will be filled by manager
		"workflow":      workflow.Name,
		api.FieldStatus: statusCompleted,
		"input":         execCtx.input,
		api.FieldSteps:  steps,
		"template_vars": execCtx.templateVars,
	}

	// If the last step isn't an output step, merge its result into the top level.
	// Only applies to plain tool steps; forEach/parallel steps produce no single
	// result to merge.
	if lastStepResult != nil && len(workflow.Steps) > 0 {
		lastStep := workflow.Steps[len(workflow.Steps)-1]
		if !api.OutputEnabled(lastStep.Output, lastStep.Store) && lastStep.Tool != "" {
			logging.Debug("WorkflowExecutor", "Last step %s is not an output step, merging result into top level", lastStep.ID)
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
			"id":            stepMeta.ID,
			"tool":          stepMeta.Tool,
			api.FieldStatus: stepMeta.Status,
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

		// Add result only for output steps (the returned document). Results are
		// always recorded for referencing, but only surfaced here when requested.
		if stepMeta.Output && results[stepMeta.ID] != nil {
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

// subStepView is the common shape of an executable tool step, shared by
// top-level plain steps, forEach/parallel sub-steps, and onFailure handlers.
type subStepView struct {
	ID           string
	Tool         string
	Args         map[string]interface{}
	Condition    *api.WorkflowCondition
	Output       bool
	AllowFailure bool
}

func plainStepView(step api.WorkflowStep) subStepView {
	return subStepView{
		ID:           step.ID,
		Tool:         step.Tool,
		Args:         step.Args,
		Condition:    step.Condition,
		Output:       api.OutputEnabled(step.Output, step.Store),
		AllowFailure: step.AllowFailure,
	}
}

func subStepViewFrom(ss api.WorkflowSubStep) subStepView {
	return subStepView{
		ID:           ss.ID,
		Tool:         ss.Tool,
		Args:         ss.Args,
		Condition:    ss.Condition,
		Output:       api.OutputEnabled(ss.Output, ss.Store),
		AllowFailure: ss.AllowFailure,
	}
}

// stepOutcome reports how a step (or composite step) affected the workflow.
type stepOutcome struct {
	// stop indicates the workflow must stop because a non-allowFailure step failed.
	stop bool
	// result is the surfaced result: for a plain successful step the tool result;
	// for a fatal IsError stop the offending error result; nil otherwise.
	result *mcp.CallToolResult
	// fatalErr is the Go error that caused a fatal stop (nil for an IsError stop).
	fatalErr error
	// failedStepID / errorMessage describe the failing step for partial-result reporting.
	failedStepID string
	errorMessage string
}

// templateContext builds the variable context exposed to templates: workflow
// input, user/loop variables, and previous step results.
func (we *WorkflowExecutor) templateContext(execCtx *executionContext) map[string]interface{} {
	return map[string]interface{}{
		"input":   execCtx.input,
		"vars":    execCtx.variables,
		"results": execCtx.results,
		"context": execCtx.results, // Alias for results to support .context.variable syntax
	}
}

// isConditionTrue interprets a rendered template-condition result as a boolean.
func isConditionTrue(rendered interface{}) bool {
	switch v := rendered.(type) {
	case bool:
		return v
	case string:
		return v == "true"
	default:
		return false
	}
}

// runStep executes a single tool step: condition gate, argument resolution,
// tool call, result storage, and metadata recording. It honors the step's own
// AllowFailure and reports via stepOutcome whether the workflow must stop.
func (we *WorkflowExecutor) runStep(ctx context.Context, workflowName string, s subStepView, execCtx *executionContext) (stepOutcome, error) {
	var conditionEvaluation *bool
	var conditionResult interface{}
	var conditionTool string

	if s.Condition != nil {
		passed, eval, condResult, condTool, err := we.evaluateStepCondition(ctx, workflowName, s, execCtx)
		if err != nil {
			return stepOutcome{}, err
		}
		conditionEvaluation, conditionResult, conditionTool = eval, condResult, condTool

		we.eventCallback.GenerateStepEvent(workflowName, s.ID, "condition_evaluated", map[string]interface{}{
			"tool":             s.Tool,
			"condition_result": fmt.Sprintf("%t", passed),
		})

		if !passed {
			logging.Debug("WorkflowExecutor", "Step %s condition failed, skipping step", s.ID)
			we.eventCallback.GenerateStepEvent(workflowName, s.ID, "step_skipped", map[string]interface{}{
				"tool":             s.Tool,
				"condition_result": "false",
			})
			execCtx.stepMetadata = append(execCtx.stepMetadata, stepMetadata{
				ID:                  s.ID,
				Tool:                s.Tool,
				Output:              s.Output,
				Status:              statusSkipped,
				AllowFailure:        s.AllowFailure,
				ConditionEvaluation: conditionEvaluation,
				ConditionResult:     conditionResult,
				ConditionTool:       conditionTool,
			})
			return stepOutcome{}, nil
		}
	}

	resolvedArgs, err := we.resolveArguments(s.Args, execCtx)
	if err != nil {
		return stepOutcome{}, fmt.Errorf("failed to resolve arguments for step %s: %w", s.ID, err)
	}
	logging.Debug("WorkflowExecutor", "Step %s resolved args: %+v", s.ID, resolvedArgs)

	we.eventCallback.GenerateStepEvent(workflowName, s.ID, "step_started", map[string]interface{}{"tool": s.Tool})

	stepCtx, endStepSpan := startStepSpan(ctx, workflowName, s.ID, s.Tool)
	result, err := we.toolCaller.CallToolInternal(stepCtx, s.Tool, resolvedArgs)
	endStepSpan(result != nil && result.IsError, err)

	if err != nil {
		logging.Error("WorkflowExecutor", err, "Step %s failed", s.ID)
		we.eventCallback.GenerateStepEvent(workflowName, s.ID, "step_failed", map[string]interface{}{
			"tool":          s.Tool,
			api.FieldError:  err.Error(),
			"allow_failure": s.AllowFailure,
		})
		execCtx.stepMetadata = append(execCtx.stepMetadata, stepMetadata{
			ID:                  s.ID,
			Tool:                s.Tool,
			Output:              s.Output,
			Status:              statusFailed,
			AllowFailure:        s.AllowFailure,
			ConditionEvaluation: conditionEvaluation,
			ConditionResult:     conditionResult,
			ConditionTool:       conditionTool,
		})
		// Always record a result for the failed step so later steps and the
		// output projection can reference it, regardless of the output flag.
		execCtx.results[s.ID] = map[string]interface{}{
			api.FieldError:   err.Error(),
			api.FieldSuccess: false,
			"isError":        true,
		}
		if s.AllowFailure {
			logging.Debug("WorkflowExecutor", "Step %s failed but allow_failure is true, continuing", s.ID)
			return stepOutcome{}, nil
		}
		return stepOutcome{stop: true, fatalErr: err, failedStepID: s.ID, errorMessage: err.Error()}, nil
	}

	// Always record the step result so later steps and the output projection can
	// reference it via {{ .results.<id>.<field> }}, regardless of the output
	// flag. The output flag only controls visibility in the returned document.
	var resultData interface{}
	if len(result.Content) > 0 {
		if textContent, ok := result.Content[0].(mcp.TextContent); ok {
			if err := json.Unmarshal([]byte(textContent.Text), &resultData); err != nil {
				resultData = textContent.Text
			}
		}
	}
	execCtx.results[s.ID] = resultData
	logging.Debug("WorkflowExecutor", "Recorded result from step %s: %+v", s.ID, resultData)

	we.eventCallback.GenerateStepEvent(workflowName, s.ID, "step_completed", map[string]interface{}{"tool": s.Tool})

	execCtx.stepMetadata = append(execCtx.stepMetadata, stepMetadata{
		ID:                  s.ID,
		Tool:                s.Tool,
		Output:              s.Output,
		Status:              statusCompleted,
		AllowFailure:        s.AllowFailure,
		ConditionEvaluation: conditionEvaluation,
		ConditionResult:     conditionResult,
		ConditionTool:       conditionTool,
	})

	if result.IsError {
		logging.Error("WorkflowExecutor", fmt.Errorf("step returned error"), "Step %s returned error result", s.ID)
		we.eventCallback.GenerateStepEvent(workflowName, s.ID, "step_failed", map[string]interface{}{
			"tool":          s.Tool,
			api.FieldError:  "step returned error result",
			"allow_failure": s.AllowFailure,
		})
		if s.AllowFailure {
			if len(execCtx.stepMetadata) > 0 {
				execCtx.stepMetadata[len(execCtx.stepMetadata)-1].Status = statusFailed
			}
			var errorMessage string
			if len(result.Content) > 0 {
				if textContent, ok := result.Content[0].(mcp.TextContent); ok {
					errorMessage = textContent.Text
				}
			}
			execCtx.results[s.ID] = map[string]interface{}{
				api.FieldSuccess: false,
				"isError":        true,
				api.FieldError:   errorMessage,
			}
			return stepOutcome{result: result}, nil
		}
		return stepOutcome{stop: true, result: result}, nil
	}

	return stepOutcome{result: result}, nil
}

// evaluateStepCondition resolves a step's condition and reports whether the
// step should run. It supports three forms: a boolean Go-template gate
// (Template), a reference to a previous step's result (FromStep), and an
// inline condition tool call, each validated against Expect/ExpectNot.
func (we *WorkflowExecutor) evaluateStepCondition(ctx context.Context, workflowName string, s subStepView, execCtx *executionContext) (bool, *bool, interface{}, string, error) {
	cond := s.Condition
	logging.Debug("WorkflowExecutor", "Step %s has condition, evaluating...", s.ID)

	// Boolean Go-template gate.
	if cond.Template != "" {
		rendered, err := we.template.RenderGoTemplate(cond.Template, we.templateContext(execCtx))
		if err != nil {
			return false, nil, nil, "template", fmt.Errorf("failed to evaluate condition template for step %s: %w", s.ID, err)
		}
		passed := isConditionTrue(rendered)
		logging.Debug("WorkflowExecutor", "Step %s template condition %q -> %v", s.ID, cond.Template, passed)
		return passed, &passed, rendered, "template", nil
	}

	var conditionToolResult *mcp.CallToolResult
	var conditionError error
	var conditionResult interface{}
	var conditionTool string

	if cond.FromStep != "" {
		logging.Debug("WorkflowExecutor", "Step %s condition references previous step: %s", s.ID, cond.FromStep)

		var referencedStepResult interface{}
		found := false

		for _, stepMeta := range execCtx.stepMetadata {
			if stepMeta.ID == cond.FromStep {
				if execCtx.results[stepMeta.ID] != nil {
					referencedStepResult = execCtx.results[stepMeta.ID]
					found = true
					break
				}
			}
		}

		if !found {
			for _, stepMeta := range execCtx.stepMetadata {
				if stepMeta.ID == cond.FromStep {
					if stepMeta.Status == statusFailed {
						referencedStepResult = map[string]interface{}{
							api.FieldError:   fmt.Sprintf("Step %s failed", stepMeta.ID),
							api.FieldSuccess: false,
							"isError":        true,
							api.FieldStatus:  stepMeta.Status,
						}
						found = true
						break
					} else if stepMeta.Status == statusCompleted {
						referencedStepResult = map[string]interface{}{
							api.FieldSuccess: true,
							api.FieldStatus:  stepMeta.Status,
						}
						found = true
						break
					}
				}
			}
		}

		if !found {
			logging.Error("WorkflowExecutor", fmt.Errorf("referenced step not found"), "Step %s condition references non-existent step result: %s", s.ID, cond.FromStep)
			return false, nil, nil, "", fmt.Errorf("step %s condition references non-existent step result: %s", s.ID, cond.FromStep)
		}

		resultJSON, err := json.Marshal(referencedStepResult)
		if err != nil {
			return false, nil, nil, "", fmt.Errorf("failed to marshal referenced step result for condition evaluation: %w", err)
		}

		conditionToolResult = &mcp.CallToolResult{
			Content: []mcp.Content{mcp.NewTextContent(string(resultJSON))},
			IsError: false,
		}

		if resultMap, ok := referencedStepResult.(map[string]interface{}); ok {
			if isError, exists := resultMap["isError"].(bool); exists && isError {
				conditionToolResult.IsError = true
			}
		}

		conditionTool = fmt.Sprintf("from_step:%s", cond.FromStep)
		conditionResult = referencedStepResult
	} else {
		conditionTool = cond.Tool

		resolvedConditionArgs, err := we.resolveArguments(cond.Args, execCtx)
		if err != nil {
			return false, nil, nil, "", fmt.Errorf("failed to resolve condition arguments for step %s: %w", s.ID, err)
		}

		condCtx, endCondSpan := startStepSpan(ctx, workflowName, s.ID+".condition", cond.Tool)
		conditionToolResult, conditionError = we.toolCaller.CallToolInternal(condCtx, cond.Tool, resolvedConditionArgs)
		endCondSpan(conditionToolResult != nil && conditionToolResult.IsError, conditionError)
		if conditionError != nil {
			logging.Debug("WorkflowExecutor", "Step %s condition tool failed: %v", s.ID, conditionError)
			conditionResult = false
		} else if len(conditionToolResult.Content) > 0 {
			if textContent, ok := conditionToolResult.Content[0].(mcp.TextContent); ok {
				var parsedResult interface{}
				if err := json.Unmarshal([]byte(textContent.Text), &parsedResult); err != nil {
					conditionResult = textContent.Text
				} else {
					conditionResult = parsedResult
				}
			}
		}
	}

	conditionPassed := true

	if conditionError != nil {
		conditionPassed = false
	} else {
		hasExpect := len(cond.Expect.JsonPath) > 0
		hasExpectNot := len(cond.ExpectNot.JsonPath) > 0
		if !hasExpect && !hasExpectNot {
			hasExpect = true
		}

		if hasExpect {
			expectPassed := (!conditionToolResult.IsError) == cond.Expect.Success
			if expectPassed && len(cond.Expect.JsonPath) > 0 {
				jsonPathPassed, err := we.validateJsonPath(conditionToolResult, cond.Expect.JsonPath, execCtx)
				if err != nil {
					expectPassed = false
				} else {
					expectPassed = jsonPathPassed
				}
			}
			conditionPassed = conditionPassed && expectPassed
		}

		if hasExpectNot {
			expectNotPassed := true
			if cond.ExpectNot.Success || len(cond.ExpectNot.JsonPath) == 0 {
				if len(cond.ExpectNot.JsonPath) == 0 {
					expectNotPassed = (!conditionToolResult.IsError) == cond.ExpectNot.Success
				}
			}
			if len(cond.ExpectNot.JsonPath) > 0 {
				jsonPathPassed, err := we.validateJsonPath(conditionToolResult, cond.ExpectNot.JsonPath, execCtx)
				if err != nil {
					expectNotPassed = true
				} else {
					expectNotPassed = !jsonPathPassed
				}
			}
			conditionPassed = conditionPassed && expectNotPassed
		}
	}

	return conditionPassed, &conditionPassed, conditionResult, conditionTool, nil
}

// runForEach executes a forEach step: it resolves the items list and runs the
// body sub-steps once per item, binding the current item to "{{ .vars.<as> }}".
func (we *WorkflowExecutor) runForEach(ctx context.Context, workflowName string, step api.WorkflowStep, execCtx *executionContext) (stepOutcome, error) {
	if skip, outcome, err := we.evaluateCompositeCondition(ctx, workflowName, step, execCtx); err != nil || skip {
		return outcome, err
	}

	items, err := we.resolveForEachItems(step.ForEach.Items, execCtx)
	if err != nil {
		return stepOutcome{}, fmt.Errorf("forEach step %s: %w", step.ID, err)
	}

	as := step.ForEach.As
	if as == "" {
		as = "item"
	}
	idxKey := as + "_index"

	// Each sub-step's result lives under its plain ID (so later sub-steps in the
	// same iteration can chain off it) and is also copied to an index-suffixed
	// key "<id>_<index>" after each iteration, so every iteration stays
	// addressable after the loop (the plain ID keeps the last iteration's result
	// for convenience). Results are recorded for every sub-step regardless of its
	// output flag.
	prev, hadPrev := execCtx.variables[as]
	defer func() {
		if hadPrev {
			execCtx.variables[as] = prev
		} else {
			delete(execCtx.variables, as)
		}
		delete(execCtx.variables, idxKey)
	}()

	for idx, item := range items {
		execCtx.variables[as] = item
		execCtx.variables[idxKey] = idx
		for _, ss := range step.ForEach.Steps {
			outcome, err := we.runStep(ctx, workflowName, subStepViewFrom(ss), execCtx)
			if err != nil {
				return stepOutcome{}, err
			}
			if outcome.stop {
				if step.AllowFailure {
					logging.Debug("WorkflowExecutor", "forEach step %s sub-step failed but allow_failure is true", step.ID)
					return stepOutcome{}, nil
				}
				return outcome, nil
			}
			if v, ok := execCtx.results[ss.ID]; ok {
				execCtx.results[fmt.Sprintf("%s_%d", ss.ID, idx)] = v
			}
		}
	}
	return stepOutcome{}, nil
}

// runParallel executes a parallel step: its sub-steps run concurrently against
// isolated contexts (shared read-only input/vars, a snapshot of results), and
// their effects are merged back deterministically after the group joins. This
// keeps concurrent execution race-free; siblings cannot observe one another's
// results.
func (we *WorkflowExecutor) runParallel(ctx context.Context, workflowName string, step api.WorkflowStep, execCtx *executionContext) (stepOutcome, error) {
	if skip, outcome, err := we.evaluateCompositeCondition(ctx, workflowName, step, execCtx); err != nil || skip {
		return outcome, err
	}

	type subResult struct {
		local   *executionContext
		outcome stepOutcome
		err     error
	}
	results := make([]subResult, len(step.Parallel))

	var wg sync.WaitGroup
	for i, ss := range step.Parallel {
		wg.Add(1)
		go func(i int, ss api.WorkflowSubStep) {
			defer wg.Done()
			local := &executionContext{
				input:        execCtx.input,
				variables:    execCtx.variables,
				results:      copyResults(execCtx.results),
				templateVars: make([]string, 0),
				stepMetadata: make([]stepMetadata, 0),
			}
			outcome, err := we.runStep(ctx, workflowName, subStepViewFrom(ss), local)
			results[i] = subResult{local: local, outcome: outcome, err: err}
		}(i, ss)
	}
	wg.Wait()

	var fatal *stepOutcome
	for i, ss := range step.Parallel {
		r := results[i]
		if r.err != nil {
			return stepOutcome{}, r.err
		}
		if v, ok := r.local.results[ss.ID]; ok {
			execCtx.results[ss.ID] = v
		}
		execCtx.stepMetadata = append(execCtx.stepMetadata, r.local.stepMetadata...)
		execCtx.templateVars = append(execCtx.templateVars, r.local.templateVars...)
		if r.outcome.stop && fatal == nil {
			oc := r.outcome
			fatal = &oc
		}
	}

	if fatal != nil && !step.AllowFailure {
		return *fatal, nil
	}
	return stepOutcome{}, nil
}

// evaluateCompositeCondition evaluates the optional condition on a forEach or
// parallel step, recording skipped metadata when it does not pass. It returns
// skip=true when the step should be skipped.
func (we *WorkflowExecutor) evaluateCompositeCondition(ctx context.Context, workflowName string, step api.WorkflowStep, execCtx *executionContext) (bool, stepOutcome, error) {
	if step.Condition == nil {
		return false, stepOutcome{}, nil
	}
	passed, eval, condResult, condTool, err := we.evaluateStepCondition(ctx, workflowName, plainStepView(step), execCtx)
	if err != nil {
		return false, stepOutcome{}, err
	}
	if passed {
		return false, stepOutcome{}, nil
	}
	we.eventCallback.GenerateStepEvent(workflowName, step.ID, "step_skipped", map[string]interface{}{"tool": ""})
	execCtx.stepMetadata = append(execCtx.stepMetadata, stepMetadata{
		ID:                  step.ID,
		Status:              statusSkipped,
		AllowFailure:        step.AllowFailure,
		ConditionEvaluation: eval,
		ConditionResult:     condResult,
		ConditionTool:       condTool,
	})
	return true, stepOutcome{}, nil
}

// resolveForEachItems resolves a forEach items expression to a list. The
// expression must be a reference that yields an array (e.g. "{{ .input.items }}").
func (we *WorkflowExecutor) resolveForEachItems(itemsExpr string, execCtx *executionContext) ([]interface{}, error) {
	resolved, err := we.resolveValue(itemsExpr, execCtx)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve items expression: %w", err)
	}
	switch v := resolved.(type) {
	case []interface{}:
		return v, nil
	case nil:
		return nil, fmt.Errorf("items expression %q resolved to nil", itemsExpr)
	default:
		return nil, fmt.Errorf("items expression %q resolved to %T, expected a list", itemsExpr, resolved)
	}
}

// runOnFailure runs the workflow's onFailure handlers best-effort: each is
// forced to allow failure so cleanup proceeds even if individual steps error.
func (we *WorkflowExecutor) runOnFailure(ctx context.Context, workflow *api.Workflow, execCtx *executionContext) {
	if len(workflow.OnFailure) == 0 {
		return
	}
	logging.Debug("WorkflowExecutor", "Running %d onFailure step(s) for workflow %s", len(workflow.OnFailure), workflow.Name)
	for _, ss := range workflow.OnFailure {
		view := subStepViewFrom(ss)
		view.AllowFailure = true
		if _, err := we.runStep(ctx, workflow.Name, view, execCtx); err != nil {
			logging.Error("WorkflowExecutor", err, "onFailure step %s errored", ss.ID)
		}
	}
}

// failWorkflow runs onFailure cleanup and produces the failure result. A Go
// error from a step yields a partial-result payload plus the wrapped error; a
// step that returned an error *result* surfaces that result directly.
func (we *WorkflowExecutor) failWorkflow(ctx context.Context, workflow *api.Workflow, execCtx *executionContext, outcome stepOutcome) (*mcp.CallToolResult, error) {
	we.runOnFailure(ctx, workflow, execCtx)

	if outcome.fatalErr == nil {
		return outcome.result, nil
	}

	steps := we.buildStepsArray(execCtx.stepMetadata, execCtx.results, outcome.failedStepID, outcome.errorMessage)
	partialResult := map[string]interface{}{
		"execution_id":  "",
		"workflow":      workflow.Name,
		api.FieldStatus: statusFailed,
		"input":         execCtx.input,
		api.FieldSteps:  steps,
		"template_vars": execCtx.templateVars,
	}
	partialJSON, jsonErr := json.Marshal(partialResult)
	if jsonErr != nil {
		return nil, fmt.Errorf("step %s failed: %w", outcome.failedStepID, outcome.fatalErr)
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{mcp.NewTextContent(string(partialJSON))},
		IsError: true,
	}, fmt.Errorf("step %s failed: %w", outcome.failedStepID, outcome.fatalErr)
}

// copyResults returns a shallow copy of a results map for isolated concurrent reads.
func copyResults(m map[string]interface{}) map[string]interface{} {
	c := make(map[string]interface{}, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}
