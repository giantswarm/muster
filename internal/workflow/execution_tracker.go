package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"muster/internal/api"
	"muster/pkg/logging"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
)

// ExecutionTracker handles automatic workflow execution tracking.
// It wraps workflow execution with comprehensive tracking including timing,
// step-by-step results, and error handling while preserving the original
// execution behavior and results.
//
// The tracker integrates seamlessly with the existing workflow execution flow,
// providing transparent tracking without modifying workflow execution logic.
type ExecutionTracker struct {
	storage ExecutionStorage
	mu      sync.RWMutex
}

// NewExecutionTracker creates a new execution tracker with the specified storage.
// The tracker provides automatic execution tracking for all workflow executions
// while maintaining thread safety for concurrent workflow executions.
func NewExecutionTracker(storage ExecutionStorage) *ExecutionTracker {
	return &ExecutionTracker{
		storage: storage,
	}
}

// TrackExecution wraps workflow execution with automatic tracking.
// This method creates an execution record, tracks step-by-step progress,
// and persists the complete execution history while preserving the original
// execution behavior and results.
//
// Parameters:
//   - ctx: Context for the operation
//   - workflowName: Name of the workflow being executed
//   - args: Arguments passed to the workflow
//   - executeFn: Function that performs the actual workflow execution
//
// Returns:
//   - *mcp.CallToolResult: Original workflow execution result (unchanged)
//   - *api.WorkflowExecution: Complete execution record for reference
//   - error: Error if execution or tracking fails
func (et *ExecutionTracker) TrackExecution(ctx context.Context, workflowName string, args map[string]interface{}, executeFn func() (*mcp.CallToolResult, error)) (*mcp.CallToolResult, *api.WorkflowExecution, error) {
	// Generate unique execution ID
	executionID := uuid.New().String()
	startTime := time.Now()

	logging.Debug("ExecutionTracker", "Starting execution tracking for workflow %s (execution: %s)", workflowName, executionID)

	// Create initial execution record
	execution := &api.WorkflowExecution{
		ExecutionID:  executionID,
		WorkflowName: workflowName,
		Status:       api.WorkflowExecutionInProgress,
		StartedAt:    startTime,
		CompletedAt:  nil,
		DurationMs:   0,
		Input:        args,
		Result:       nil,
		Error:        nil,
		Steps:        []api.WorkflowExecutionStep{},
	}

	// Store initial execution record
	if err := et.storage.Store(ctx, execution); err != nil {
		logging.Warn("ExecutionTracker", "Failed to store initial execution record %s: %v", executionID, err)
		// Continue with execution even if initial storage fails
	}

	// Execute the workflow with step tracking
	result, err := et.executeWithStepTracking(ctx, execution, executeFn)

	// Update execution record with final results
	endTime := time.Now()
	execution.CompletedAt = &endTime
	execution.DurationMs = endTime.Sub(startTime).Milliseconds()

	if err != nil {
		execution.Status = api.WorkflowExecutionFailed
		errorStr := err.Error()
		execution.Error = &errorStr
		logging.Debug("ExecutionTracker", "Execution %s failed: %v", executionID, err)
	} else {
		execution.Status = api.WorkflowExecutionCompleted
		execution.Result = et.parseResult(result)
		logging.Debug("ExecutionTracker", "Execution %s completed successfully", executionID)
	}

	// Store final execution record
	if storageErr := et.storage.Store(ctx, execution); storageErr != nil {
		logging.Warn("ExecutionTracker", "Failed to store final execution record %s: %v", executionID, storageErr)
	}

	logging.Debug("ExecutionTracker", "Completed execution tracking for workflow %s (execution: %s, duration: %dms)",
		workflowName, executionID, execution.DurationMs)

	return result, execution, err
}

// executeWithStepTracking executes the workflow while tracking individual steps.
// This method intercepts tool calls during workflow execution to record
// step-by-step timing, arguments, results, and errors.
func (et *ExecutionTracker) executeWithStepTracking(ctx context.Context, execution *api.WorkflowExecution, executeFn func() (*mcp.CallToolResult, error)) (*mcp.CallToolResult, error) {
	// For now, execute without step-level tracking since we don't have direct access
	// to individual step execution in the current architecture.
	// This would require more invasive changes to the workflow executor.

	// TODO: In a future enhancement, we could modify the WorkflowExecutor
	// to accept a step callback for detailed step tracking.

	result, err := executeFn()

	// If execution was successful, try to extract step information from the result
	if err == nil && result != nil {
		et.extractStepInformation(execution, result)
	}

	return result, err
}

// extractStepInformation attempts to extract step information from workflow results.
// This provides basic step tracking based on the workflow result structure
// until more detailed step tracking can be implemented.
func (et *ExecutionTracker) extractStepInformation(execution *api.WorkflowExecution, result *mcp.CallToolResult) {
	if len(result.Content) == 0 {
		return
	}

	// Try to parse the result content to extract step information
	if textContent, ok := result.Content[0].(mcp.TextContent); ok {
		var resultData map[string]interface{}
		if err := json.Unmarshal([]byte(textContent.Text), &resultData); err != nil {
			return // Can't parse, skip step extraction
		}

		// Look for step metadata in the workflow result
		stepMetadataRaw, hasStepMetadata := resultData["stepMetadata"]
		resultsRaw, hasResults := resultData["results"].(map[string]interface{})

		if hasStepMetadata && hasResults {
			// Use enhanced step metadata approach
			et.extractStepsFromMetadata(execution, stepMetadataRaw, resultsRaw)
		} else {
			// Fallback to legacy approach for backwards compatibility
			et.extractStepsLegacy(execution, resultsRaw)
		}
	}
}

// extractStepsFromMetadata extracts step information using enhanced metadata
func (et *ExecutionTracker) extractStepsFromMetadata(execution *api.WorkflowExecution, stepMetadataRaw interface{}, results map[string]interface{}) {
	stepMetadataList, ok := stepMetadataRaw.([]interface{})
	if !ok {
		return
	}

	for _, stepMetaRaw := range stepMetadataList {
		stepMeta, ok := stepMetaRaw.(map[string]interface{})
		if !ok {
			continue
		}

		stepID, _ := stepMeta["ID"].(string)
		tool, _ := stepMeta["Tool"].(string)
		store, _ := stepMeta["Store"].(string)

		// Get the result if it was stored
		var stepResult interface{}
		if store != "" {
			stepResult = results[store]
		}

		// Create step record with actual metadata
		step := api.WorkflowExecutionStep{
			StepID:      stepID,
			Tool:        tool,
			Status:      api.WorkflowExecutionCompleted,
			StartedAt:   execution.StartedAt, // Approximate timing
			CompletedAt: execution.CompletedAt,
			DurationMs:  0,                        // Unknown duration for now
			Input:       map[string]interface{}{}, // Unknown input for now
			Result:      stepResult,
			Error:       nil,
			StoredAs:    store,
		}

		execution.Steps = append(execution.Steps, step)
	}
}

// extractStepsLegacy provides backwards compatibility for results without step metadata
func (et *ExecutionTracker) extractStepsLegacy(execution *api.WorkflowExecution, results map[string]interface{}) {
	if results == nil {
		return
	}

	stepCount := 0
	for stepVar, stepResult := range results {
		stepCount++

		// Create a basic step record (legacy approach)
		step := api.WorkflowExecutionStep{
			StepID:      fmt.Sprintf("step_%d", stepCount),
			Tool:        "unknown", // We don't have tool information from result
			Status:      api.WorkflowExecutionCompleted,
			StartedAt:   execution.StartedAt, // Approximate timing
			CompletedAt: execution.CompletedAt,
			DurationMs:  0,                        // Unknown duration
			Input:       map[string]interface{}{}, // Unknown input
			Result:      stepResult,
			Error:       nil,
			StoredAs:    stepVar,
		}

		execution.Steps = append(execution.Steps, step)
	}
}

// parseResult converts the MCP result into a JSON-serializable format
// for storage in the execution record.
func (et *ExecutionTracker) parseResult(result *mcp.CallToolResult) interface{} {
	if result == nil || len(result.Content) == 0 {
		return nil
	}

	// Convert MCP content to JSON-serializable format
	var resultContent []interface{}
	for _, content := range result.Content {
		if textContent, ok := content.(mcp.TextContent); ok {
			// Try to parse as JSON first
			var jsonData interface{}
			if err := json.Unmarshal([]byte(textContent.Text), &jsonData); err == nil {
				resultContent = append(resultContent, jsonData)
			} else {
				// Store as string if not valid JSON
				resultContent = append(resultContent, textContent.Text)
			}
		} else {
			// Store other content types as-is
			resultContent = append(resultContent, content)
		}
	}

	// Return the parsed content
	if len(resultContent) == 1 {
		return resultContent[0]
	}
	return resultContent
}

// ListExecutions returns paginated workflow executions with optional filtering.
// This provides a convenient way to access execution history through the tracker.
func (et *ExecutionTracker) ListExecutions(ctx context.Context, req *api.ListWorkflowExecutionsRequest) (*api.ListWorkflowExecutionsResponse, error) {
	et.mu.RLock()
	defer et.mu.RUnlock()

	return et.storage.List(ctx, req)
}

// GetExecution returns detailed information about a specific workflow execution.
// This provides a convenient way to access individual execution records through the tracker.
func (et *ExecutionTracker) GetExecution(ctx context.Context, req *api.GetWorkflowExecutionRequest) (*api.WorkflowExecution, error) {
	et.mu.RLock()
	defer et.mu.RUnlock()

	execution, err := et.storage.Get(ctx, req.ExecutionID)
	if err != nil {
		return nil, err
	}

	// Apply filtering based on request parameters
	if req.StepID != "" {
		// Filter to only include the specified step
		var filteredSteps []api.WorkflowExecutionStep
		for _, step := range execution.Steps {
			if step.StepID == req.StepID {
				filteredSteps = append(filteredSteps, step)
				break
			}
		}

		if len(filteredSteps) == 0 {
			return nil, fmt.Errorf("step %s not found in execution %s", req.StepID, req.ExecutionID)
		}

		execution.Steps = filteredSteps
	} else if !req.IncludeSteps {
		// Exclude step details if requested
		execution.Steps = nil
	}

	return execution, nil
}
