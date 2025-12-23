package reconciler

import (
	"context"
	"time"
)

// ResourceType represents the type of resource being reconciled.
type ResourceType string

const (
	// ResourceTypeMCPServer represents MCPServer CRD/YAML resources.
	ResourceTypeMCPServer ResourceType = "MCPServer"

	// ResourceTypeServiceClass represents ServiceClass CRD/YAML resources.
	ResourceTypeServiceClass ResourceType = "ServiceClass"

	// ResourceTypeWorkflow represents Workflow CRD/YAML resources.
	ResourceTypeWorkflow ResourceType = "Workflow"
)

// ChangeEvent represents a detected change in a resource.
type ChangeEvent struct {
	// Type is the type of resource that changed.
	Type ResourceType

	// Name is the name of the resource that changed.
	Name string

	// Namespace is the Kubernetes namespace (empty for filesystem mode).
	Namespace string

	// Operation describes what kind of change occurred.
	Operation ChangeOperation

	// Timestamp is when the change was detected.
	Timestamp time.Time

	// Source indicates where the change came from.
	Source ChangeSource

	// FilePath is the path to the file that changed (filesystem mode only).
	FilePath string
}

// ChangeOperation represents the type of change detected.
type ChangeOperation string

const (
	// OperationCreate indicates a new resource was created.
	OperationCreate ChangeOperation = "Create"

	// OperationUpdate indicates an existing resource was modified.
	OperationUpdate ChangeOperation = "Update"

	// OperationDelete indicates a resource was deleted.
	OperationDelete ChangeOperation = "Delete"
)

// ChangeSource indicates where a change originated.
type ChangeSource string

const (
	// SourceFilesystem indicates the change came from filesystem watching.
	SourceFilesystem ChangeSource = "Filesystem"

	// SourceKubernetes indicates the change came from Kubernetes informers.
	SourceKubernetes ChangeSource = "Kubernetes"

	// SourceManual indicates the change was triggered manually (e.g., API call).
	SourceManual ChangeSource = "Manual"
)

// ReconcileResult represents the outcome of a reconciliation attempt.
type ReconcileResult struct {
	// Requeue indicates whether the resource should be requeued for retry.
	Requeue bool

	// RequeueAfter specifies when to requeue (0 means use default backoff).
	RequeueAfter time.Duration

	// Error is any error that occurred during reconciliation.
	Error error
}

// ReconcileRequest represents a request to reconcile a specific resource.
type ReconcileRequest struct {
	// Type is the type of resource to reconcile.
	Type ResourceType

	// Name is the name of the resource.
	Name string

	// Namespace is the Kubernetes namespace (empty for filesystem mode).
	Namespace string

	// Attempt is the current retry attempt number (starts at 1).
	Attempt int

	// LastError is the error from the previous attempt, if any.
	LastError error
}

// Reconciler is the interface that resource-specific reconcilers must implement.
//
// Each resource type (MCPServer, ServiceClass, Workflow) has its own reconciler
// that knows how to reconcile that specific type of resource.
type Reconciler interface {
	// Reconcile processes a single reconciliation request.
	// It should be idempotent - calling it multiple times with the same
	// input should produce the same result.
	//
	// The reconciler should:
	//  1. Fetch the current desired state from the source (CRD/YAML)
	//  2. Compare with the actual running state
	//  3. Take actions to bring actual state in line with desired state
	//  4. Return a result indicating success or need for retry
	Reconcile(ctx context.Context, req ReconcileRequest) ReconcileResult

	// GetResourceType returns the type of resource this reconciler handles.
	GetResourceType() ResourceType
}

// ChangeDetector is the interface for components that detect changes in resources.
//
// Different implementations exist for filesystem watching and Kubernetes informers.
type ChangeDetector interface {
	// Start begins watching for changes.
	// The detector should send change events to the provided channel.
	Start(ctx context.Context, changes chan<- ChangeEvent) error

	// Stop gracefully stops the change detector.
	Stop() error

	// GetSource returns the source type this detector monitors.
	GetSource() ChangeSource

	// AddResourceType adds a resource type to watch.
	// This allows dynamic configuration of which resources to monitor.
	AddResourceType(resourceType ResourceType) error

	// RemoveResourceType removes a resource type from watching.
	RemoveResourceType(resourceType ResourceType) error
}

// ReconcileQueue represents a queue of resources awaiting reconciliation.
type ReconcileQueue interface {
	// Add adds a request to the queue.
	// If the same resource is already queued, the existing entry is updated.
	Add(req ReconcileRequest)

	// Get retrieves the next request from the queue.
	// Blocks until a request is available or the context is cancelled.
	Get(ctx context.Context) (ReconcileRequest, bool)

	// Done marks a request as processed.
	// This should be called after processing to enable rate limiting.
	Done(req ReconcileRequest)

	// Len returns the current queue length.
	Len() int

	// Shutdown signals the queue to stop accepting new items.
	Shutdown()
}

// ManagerConfig holds configuration for the ReconcileManager.
type ManagerConfig struct {
	// Mode specifies whether to use Kubernetes or filesystem watching.
	// If empty, the system will auto-detect based on available resources.
	Mode WatchMode

	// FilesystemPath is the base path for filesystem watching.
	// Only used when Mode is WatchModeFilesystem.
	FilesystemPath string

	// Namespace is the Kubernetes namespace to watch.
	// Only used when Mode is WatchModeKubernetes.
	Namespace string

	// WorkerCount is the number of concurrent reconciliation workers.
	// Defaults to 2 if not specified.
	WorkerCount int

	// MaxRetries is the maximum number of retry attempts for failed reconciliations.
	// Defaults to 5 if not specified.
	MaxRetries int

	// InitialBackoff is the initial backoff duration for retries.
	// Defaults to 1 second if not specified.
	InitialBackoff time.Duration

	// MaxBackoff is the maximum backoff duration for retries.
	// Defaults to 5 minutes if not specified.
	MaxBackoff time.Duration

	// DebounceInterval is how long to wait for additional changes before reconciling.
	// Defaults to 500ms if not specified.
	DebounceInterval time.Duration

	// Debug enables debug logging for reconciliation operations.
	Debug bool

	// DisabledResourceTypes is a set of resource types that should not be reconciled.
	// This allows selective disabling of reconciliation for specific resource types.
	// Empty or nil means all registered resource types are enabled.
	DisabledResourceTypes map[ResourceType]bool
}

// WatchMode specifies how to detect configuration changes.
type WatchMode string

const (
	// WatchModeFilesystem uses filesystem watching for YAML files.
	WatchModeFilesystem WatchMode = "filesystem"

	// WatchModeKubernetes uses Kubernetes informers for CRDs.
	WatchModeKubernetes WatchMode = "kubernetes"

	// WatchModeAuto automatically selects based on environment.
	WatchModeAuto WatchMode = "auto"
)

// ReconcileStatus represents the current status of reconciliation for a resource.
type ReconcileStatus struct {
	// ResourceType is the type of the resource.
	ResourceType ResourceType

	// Name is the name of the resource.
	Name string

	// Namespace is the Kubernetes namespace (empty for filesystem mode).
	Namespace string

	// LastReconcileTime is when the resource was last successfully reconciled.
	LastReconcileTime *time.Time

	// LastError is the most recent error, if any.
	LastError string

	// RetryCount is the number of retry attempts.
	RetryCount int

	// State describes the current reconciliation state.
	State ReconcileState
}

// ReconcileState represents the state of a resource's reconciliation.
type ReconcileState string

const (
	// StatePending means the resource is awaiting reconciliation.
	StatePending ReconcileState = "Pending"

	// StateReconciling means reconciliation is in progress.
	StateReconciling ReconcileState = "Reconciling"

	// StateSynced means the resource is successfully reconciled.
	StateSynced ReconcileState = "Synced"

	// StateError means reconciliation failed and may be retried.
	StateError ReconcileState = "Error"

	// StateFailed means reconciliation failed permanently (max retries exceeded).
	StateFailed ReconcileState = "Failed"
)

