package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"muster/internal/api"
	"muster/internal/config"
	"muster/pkg/logging"
)

// ExecutionStorage defines the interface for persisting workflow executions.
// This interface abstracts the storage mechanism to enable different implementations
// while maintaining consistent behavior for execution persistence and retrieval.
//
// The storage interface follows the same patterns as other config storage
// in the muster system, enabling consistent path resolution and file management.
type ExecutionStorage interface {
	// Store persists a workflow execution record
	Store(ctx context.Context, execution *api.WorkflowExecution) error

	// Get retrieves a specific workflow execution by ID
	Get(ctx context.Context, executionID string) (*api.WorkflowExecution, error)

	// List returns paginated workflow executions with optional filtering
	List(ctx context.Context, req *api.ListWorkflowExecutionsRequest) (*api.ListWorkflowExecutionsResponse, error)

	// Delete removes a workflow execution record (for cleanup)
	Delete(ctx context.Context, executionID string) error
}

// ExecutionStorageImpl implements ExecutionStorage using the existing config.Storage
// patterns for consistent file management and path resolution.
//
// This implementation stores each execution as a separate JSON file for optimal
// performance with concurrent access and efficient individual file operations.
type ExecutionStorageImpl struct {
	storage *config.Storage
	mu      sync.RWMutex
	cache   map[string]*api.WorkflowExecutionSummary // Cache for efficient listing
}

// NewExecutionStorage creates a new execution storage instance that integrates
// with the existing configuration storage system.
//
// The storage follows the established precedence of project directory over
// user directory and supports custom configuration paths for standalone mode.
func NewExecutionStorage(configPath string) ExecutionStorage {
	if configPath == "" {
		panic("Logic error: empty execution storage configPath")
	}
	storage := config.NewStorageWithPath(configPath)

	return &ExecutionStorageImpl{
		storage: storage,
		cache:   make(map[string]*api.WorkflowExecutionSummary),
	}
}

// Store persists a workflow execution record as a JSON file.
// Each execution is stored in a separate file using the execution ID as the filename
// to enable efficient concurrent access and individual file operations.
func (es *ExecutionStorageImpl) Store(ctx context.Context, execution *api.WorkflowExecution) error {
	es.mu.Lock()
	defer es.mu.Unlock()

	// Serialize execution to JSON
	data, err := json.MarshalIndent(execution, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal execution %s: %w", execution.ExecutionID, err)
	}

	// Store using existing storage with "workflow_executions" entity type
	if err := es.storage.Save("workflow_executions", execution.ExecutionID, data); err != nil {
		return fmt.Errorf("failed to save execution %s: %w", execution.ExecutionID, err)
	}

	// Update cache with summary information
	summary := es.executionToSummary(execution)
	es.cache[execution.ExecutionID] = summary

	logging.Debug("ExecutionStorage", "Stored execution %s for workflow %s", execution.ExecutionID, execution.WorkflowName)
	return nil
}

// Get retrieves a specific workflow execution by ID from storage.
// This loads the complete execution record including all step details.
func (es *ExecutionStorageImpl) Get(ctx context.Context, executionID string) (*api.WorkflowExecution, error) {
	es.mu.RLock()
	defer es.mu.RUnlock()

	// Load from storage using existing patterns
	data, err := es.storage.Load("workflow_executions", executionID)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return nil, fmt.Errorf("execution %s not found", executionID)
		}
		return nil, fmt.Errorf("failed to load execution %s: %w", executionID, err)
	}

	// Deserialize from JSON
	var execution api.WorkflowExecution
	if err := json.Unmarshal(data, &execution); err != nil {
		return nil, fmt.Errorf("failed to unmarshal execution %s: %w", executionID, err)
	}

	logging.Debug("ExecutionStorage", "Retrieved execution %s for workflow %s", executionID, execution.WorkflowName)
	return &execution, nil
}

// List returns paginated workflow executions with optional filtering.
// This method efficiently scans execution files and applies filtering and pagination.
func (es *ExecutionStorageImpl) List(ctx context.Context, req *api.ListWorkflowExecutionsRequest) (*api.ListWorkflowExecutionsResponse, error) {
	es.mu.RLock()
	defer es.mu.RUnlock()

	// Set defaults
	limit := req.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 1000 {
		limit = 1000
	}

	offset := req.Offset
	if offset < 0 {
		offset = 0
	}

	// Scan execution files to build/refresh cache
	if err := es.refreshCache(); err != nil {
		return nil, fmt.Errorf("failed to refresh execution cache: %w", err)
	}

	// Convert cache to slice and apply filtering
	var filteredSummaries []*api.WorkflowExecutionSummary
	for _, summary := range es.cache {
		// Apply workflow name filter
		if req.WorkflowName != "" && summary.WorkflowName != req.WorkflowName {
			continue
		}

		// Apply status filter
		if req.Status != "" && summary.Status != req.Status {
			continue
		}

		filteredSummaries = append(filteredSummaries, summary)
	}

	// Sort by StartedAt descending (most recent first)
	sort.Slice(filteredSummaries, func(i, j int) bool {
		return filteredSummaries[i].StartedAt.After(filteredSummaries[j].StartedAt)
	})

	total := len(filteredSummaries)

	// Apply pagination
	var pagedSummaries []api.WorkflowExecutionSummary
	start := offset
	if start < total {
		end := start + limit
		if end > total {
			end = total
		}

		for i := start; i < end; i++ {
			pagedSummaries = append(pagedSummaries, *filteredSummaries[i])
		}
	}

	hasMore := offset+len(pagedSummaries) < total

	logging.Debug("ExecutionStorage", "Listed %d executions (total: %d, offset: %d, limit: %d)",
		len(pagedSummaries), total, offset, limit)

	return &api.ListWorkflowExecutionsResponse{
		Executions: pagedSummaries,
		Total:      total,
		Limit:      limit,
		Offset:     offset,
		HasMore:    hasMore,
	}, nil
}

// Delete removes a workflow execution record from storage.
// This is used for cleanup operations and maintenance.
func (es *ExecutionStorageImpl) Delete(ctx context.Context, executionID string) error {
	es.mu.Lock()
	defer es.mu.Unlock()

	// Delete from storage using existing patterns
	if err := es.storage.Delete("workflow_executions", executionID); err != nil {
		if strings.Contains(err.Error(), "not found") {
			return fmt.Errorf("execution %s not found", executionID)
		}
		return fmt.Errorf("failed to delete execution %s: %w", executionID, err)
	}

	// Remove from cache
	delete(es.cache, executionID)

	logging.Debug("ExecutionStorage", "Deleted execution %s", executionID)
	return nil
}

// refreshCache scans the workflow_executions directory and updates the in-memory cache
// with summary information for efficient listing operations.
func (es *ExecutionStorageImpl) refreshCache() error {
	// Get list of execution files
	executionNames, err := es.storage.List("workflow_executions")
	if err != nil {
		return fmt.Errorf("failed to list executions: %w", err)
	}

	// Load summary information for executions not in cache
	for _, executionID := range executionNames {
		if _, exists := es.cache[executionID]; exists {
			continue // Already cached
		}

		// Load execution to create summary
		data, err := es.storage.Load("workflow_executions", executionID)
		if err != nil {
			logging.Warn("ExecutionStorage", "Failed to load execution %s for caching: %v", executionID, err)
			continue
		}

		var execution api.WorkflowExecution
		if err := json.Unmarshal(data, &execution); err != nil {
			logging.Warn("ExecutionStorage", "Failed to unmarshal execution %s for caching: %v", executionID, err)
			continue
		}

		// Add to cache
		summary := es.executionToSummary(&execution)
		es.cache[executionID] = summary
	}

	// Remove cache entries for files that no longer exist
	existingFiles := make(map[string]bool)
	for _, name := range executionNames {
		existingFiles[name] = true
	}

	for executionID := range es.cache {
		if !existingFiles[executionID] {
			delete(es.cache, executionID)
		}
	}

	return nil
}

// executionToSummary converts a full WorkflowExecution to a WorkflowExecutionSummary
// for efficient listing operations.
func (es *ExecutionStorageImpl) executionToSummary(execution *api.WorkflowExecution) *api.WorkflowExecutionSummary {
	return &api.WorkflowExecutionSummary{
		ExecutionID:  execution.ExecutionID,
		WorkflowName: execution.WorkflowName,
		Status:       execution.Status,
		StartedAt:    execution.StartedAt,
		CompletedAt:  execution.CompletedAt,
		DurationMs:   execution.DurationMs,
		StepCount:    len(execution.Steps),
		Error:        execution.Error,
	}
}
