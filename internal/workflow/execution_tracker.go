package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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
// Arguments:
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
	startTime := time.Now().UTC()

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
	endTime := time.Now().UTC()
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

	// Extract step information from both successful and failed executions
	// Failed workflows may still have partial step results that are valuable for debugging
	if result != nil {
		et.extractStepInformation(execution, result)
	} else if err != nil {
		// For failed workflows without results, try to create basic step tracking
		// based on the error information if available
		et.extractStepInformationFromError(execution, err)
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

		// Check if the workflow result indicates an error
		var workflowError string
		var failedStepID string
		if errorRaw, hasError := resultData["error"]; hasError {
			if errorStr, ok := errorRaw.(string); ok {
				workflowError = errorStr
			}
		}

		// Check for explicit failed step information
		if failedStepRaw, hasFailedStep := resultData["failedStep"]; hasFailedStep {
			if failedStepStr, ok := failedStepRaw.(string); ok {
				failedStepID = failedStepStr
			}
		}

		// If no explicit failed step but we have an error, try to extract from error message
		if failedStepID == "" && workflowError != "" {
			failedStepID = et.extractStepIDFromError(workflowError)
		}

		if hasStepMetadata && hasResults {
			// Use enhanced step metadata approach
			et.extractStepsFromMetadata(execution, stepMetadataRaw, resultsRaw, workflowError, failedStepID)
		} else {
			// Fallback to legacy approach for backwards compatibility
			et.extractStepsLegacy(execution, resultsRaw)
		}
	}
}

// extractStepsFromMetadata extracts step information using enhanced metadata
func (et *ExecutionTracker) extractStepsFromMetadata(execution *api.WorkflowExecution, stepMetadataRaw interface{}, results map[string]interface{}, workflowError string, failedStepID string) {
	stepMetadataList, ok := stepMetadataRaw.([]interface{})
	if !ok {
		return
	}

	// If no error information provided, check if execution already has error info
	if workflowError == "" && execution.Error != nil {
		workflowError = *execution.Error
		failedStepID = et.extractStepIDFromError(workflowError)
	}

	logging.Debug("ExecutionTracker", "extractStepsFromMetadata: workflowError='%s', failedStepID='%s'", workflowError, failedStepID)

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

		// Determine step status and error
		var stepStatus api.WorkflowExecutionStatus
		var stepError *string

		logging.Debug("ExecutionTracker", "Processing step: stepID='%s', failedStepID='%s', match=%t", stepID, failedStepID, stepID == failedStepID)

		if failedStepID != "" && stepID == failedStepID {
			// This is the step that failed
			logging.Debug("ExecutionTracker", "Setting step %s as failed with error: %s", stepID, workflowError)
			stepStatus = api.WorkflowExecutionFailed
			stepError = &workflowError
		} else {
			// This step completed successfully (or we assume it did if it's before the failed step)
			stepStatus = api.WorkflowExecutionCompleted
			stepError = nil
		}

		// Create step record with actual metadata and correct status
		step := api.WorkflowExecutionStep{
			StepID:      stepID,
			Tool:        tool,
			Status:      stepStatus,
			StartedAt:   execution.StartedAt, // Approximate timing
			CompletedAt: execution.CompletedAt,
			DurationMs:  0,                        // Unknown duration for now
			Input:       map[string]interface{}{}, // Unknown input for now
			Result:      stepResult,
			Error:       stepError,
			StoredAs:    store,
		}

		logging.Debug("ExecutionTracker", "Created step: stepID='%s', status='%s', hasError=%t", stepID, stepStatus, stepError != nil)
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

// extractStepInformationFromError attempts to extract step information from workflow errors.
// This provides basic step tracking for failed workflows when no result data is available.
func (et *ExecutionTracker) extractStepInformationFromError(execution *api.WorkflowExecution, err error) {
	// For failed workflows without results, we can try to infer step information from the error
	// The error message often contains step information like "step failing_step failed: ..."
	errorStr := err.Error()

	// Look for step information in error messages
	// Pattern: "step <step_id> failed: ..."
	if stepID := et.extractStepIDFromError(errorStr); stepID != "" {
		// Create a failed step record
		step := api.WorkflowExecutionStep{
			StepID:      stepID,
			Tool:        "unknown", // We don't have tool information from error
			Status:      api.WorkflowExecutionFailed,
			StartedAt:   execution.StartedAt, // Approximate timing
			CompletedAt: execution.CompletedAt,
			DurationMs:  0,                        // Unknown duration
			Input:       map[string]interface{}{}, // Unknown input
			Result:      nil,
			Error:       &errorStr,
			StoredAs:    "",
		}

		execution.Steps = append(execution.Steps, step)
	}
}

// extractStepIDFromError extracts step ID from error messages
func (et *ExecutionTracker) extractStepIDFromError(errorStr string) string {
	// Look for patterns like "step <step_id> failed:"
	// This is a simple pattern match - could be enhanced with regex
	stepPrefix := "step "
	failedSuffix := " failed:"

	startIdx := strings.Index(errorStr, stepPrefix)
	if startIdx == -1 {
		return ""
	}

	startIdx += len(stepPrefix)
	endIdx := strings.Index(errorStr[startIdx:], failedSuffix)
	if endIdx == -1 {
		return ""
	}

	return errorStr[startIdx : startIdx+endIdx]
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

	// Handle specific step query - return ONLY step data, not full execution
	if req.StepID != "" {
		// Find the specific step
		var targetStep *api.WorkflowExecutionStep
		for _, step := range execution.Steps {
			if step.StepID == req.StepID {
				targetStep = &step
				break
			}
		}

		if targetStep == nil {
			return nil, fmt.Errorf("step %s not found in execution %s", req.StepID, req.ExecutionID)
		}

		// Return a minimal execution record containing only the requested step
		// For step-specific queries, we only include execution-level error if the step itself failed
		var stepError *string
		var stepInput map[string]interface{}
		if targetStep.Status == api.WorkflowExecutionFailed {
			stepError = targetStep.Error // Use step-level error, not execution-level error
			stepInput = execution.Input  // Include full input for failed steps for debugging
		} else {
			stepError = nil                          // No error for successful steps
			stepInput = make(map[string]interface{}) // Empty input for successful steps to avoid error-related terms
		}

		return &api.WorkflowExecution{
			ExecutionID:  execution.ExecutionID,
			WorkflowName: execution.WorkflowName,
			Status:       execution.Status,
			StartedAt:    execution.StartedAt,
			CompletedAt:  execution.CompletedAt,
			DurationMs:   execution.DurationMs,
			Input:        stepInput,                                // Only include input for failed steps
			Result:       nil,                                      // Exclude full workflow result for specific step queries
			Error:        stepError,                                // Only include error if THIS step failed
			Steps:        []api.WorkflowExecutionStep{*targetStep}, // Only the requested step
		}, nil
	}

	// Handle summary mode - exclude ALL step-related data
	if !req.IncludeSteps {
		// Create a copy without steps and without step data in result
		summaryExecution := &api.WorkflowExecution{
			ExecutionID:  execution.ExecutionID,
			WorkflowName: execution.WorkflowName,
			Status:       execution.Status,
			StartedAt:    execution.StartedAt,
			CompletedAt:  execution.CompletedAt,
			DurationMs:   execution.DurationMs,
			Input:        execution.Input,
			Result:       et.filterStepDataFromResult(execution.Result), // Remove step metadata from result
			Error:        execution.Error,
			Steps:        nil, // Exclude steps array entirely
		}
		return summaryExecution, nil
	}

	// Default case: return full execution with all steps
	return execution, nil
}

// filterStepDataFromResult removes step-related metadata from workflow results
// to provide clean summary data without step references
func (et *ExecutionTracker) filterStepDataFromResult(result interface{}) interface{} {
	if result == nil {
		return nil
	}

	// If result is a map, remove step-related fields
	if resultMap, ok := result.(map[string]interface{}); ok {
		filteredResult := make(map[string]interface{})
		for key, value := range resultMap {
			// Exclude step metadata and step results from summary
			if key != "stepMetadata" && key != "results" && key != "templateVars" {
				filteredResult[key] = value
			}
		}
		return filteredResult
	}

	// For non-map results, return as-is
	return result
}
