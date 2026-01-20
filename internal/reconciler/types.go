package reconciler

import (
	"context"
	"regexp"
	"strings"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/util/retry"

	musterv1alpha1 "muster/pkg/apis/muster/v1alpha1"
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

// ValidResourceTypes is the set of all valid resource types.
// Used for input validation when accepting resource types from external sources.
var ValidResourceTypes = map[ResourceType]bool{
	ResourceTypeMCPServer:    true,
	ResourceTypeServiceClass: true,
	ResourceTypeWorkflow:     true,
}

// IsValidResourceType checks if a resource type string is valid.
// This should be used when accepting resource type input from APIs or external sources.
func IsValidResourceType(resourceType string) bool {
	return ValidResourceTypes[ResourceType(resourceType)]
}

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

	// SourceServiceState indicates the change came from a service state change.
	// This is used when runtime state changes (e.g., service crashes, health check fails)
	// trigger reconciliation to sync status back to the CRD.
	SourceServiceState ChangeSource = "ServiceState"
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

	// ReconcileTimeout is the maximum duration for a single reconciliation operation.
	// If a reconciler takes longer than this, the context will be cancelled.
	// Defaults to 30 seconds if not specified.
	ReconcileTimeout time.Duration

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

// WatchModeFromKubernetesFlag returns the appropriate WatchMode based on whether
// Kubernetes mode is enabled. This helper ensures consistent mode selection
// across the codebase.
//
// When kubernetesEnabled is true, returns WatchModeKubernetes (CRD-based).
// When kubernetesEnabled is false, returns WatchModeFilesystem (YAML file-based).
func WatchModeFromKubernetesFlag(kubernetesEnabled bool) WatchMode {
	if kubernetesEnabled {
		return WatchModeKubernetes
	}
	return WatchModeFilesystem
}

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

// Service state constants for status syncing.
// These are used when a service doesn't exist or has no state.
const (
	// ServiceStateStopped indicates the service is not running.
	ServiceStateStopped = "stopped"

	// ServiceHealthUnknown indicates the health status is unknown.
	ServiceHealthUnknown = "unknown"
)

// DefaultNamespace is the default namespace for Kubernetes resources.
const DefaultNamespace = "default"

// DefaultStatusSyncInterval is how often to requeue for periodic status sync.
// This ensures status is eventually consistent even if state change events are missed.
//
// ## Purpose
//
// Reconcilers use this interval to schedule periodic re-reconciliation of resources.
// This implements the "level-triggered" reconciliation pattern from Kubernetes,
// where we periodically check that desired state matches actual state, rather than
// relying solely on "edge-triggered" events.
//
// ## Tuning Considerations
//
//   - **Shorter intervals** (e.g., 10s): More responsive status updates, but higher
//     API server load and more reconciliation overhead.
//   - **Longer intervals** (e.g., 60s): Lower load, but status may be stale longer
//     if state change events are missed.
//
// ## Default Value
//
// The default of 30 seconds provides a good balance between:
//   - Responsiveness: Status is refreshed at least every 30 seconds
//   - Efficiency: Low enough frequency to avoid overwhelming the API server
//   - Eventual consistency: Missed events are recovered within 30 seconds
//
// ## Performance Impact
//
// For a deployment with N resources, this generates approximately:
//   - N / 30 = reconciliations per second (e.g., 100 resources = ~3.3/s)
//   - Each reconciliation involves: 1 Get + 1 Status Update to the API server
//
// ## Customization
//
// To customize this interval, you can:
//  1. Define a custom reconciler with a different interval
//  2. Set RequeueAfter explicitly in your Reconcile() method
//
// Note: This constant is used by MCPServerReconciler for periodic status sync.
// ServiceClass and Workflow reconcilers don't currently use periodic requeue
// as they primarily manage static definitions.
const DefaultStatusSyncInterval = 30 * time.Second

// StatusUpdater is an interface for updating CRD status.
// This is implemented by the MusterClient.
type StatusUpdater interface {
	GetMCPServer(ctx context.Context, name, namespace string) (*musterv1alpha1.MCPServer, error)
	UpdateMCPServerStatus(ctx context.Context, server *musterv1alpha1.MCPServer) error
	GetServiceClass(ctx context.Context, name, namespace string) (*musterv1alpha1.ServiceClass, error)
	UpdateServiceClassStatus(ctx context.Context, serviceClass *musterv1alpha1.ServiceClass) error
	GetWorkflow(ctx context.Context, name, namespace string) (*musterv1alpha1.Workflow, error)
	UpdateWorkflowStatus(ctx context.Context, workflow *musterv1alpha1.Workflow) error
	IsKubernetesMode() bool
}

// BaseStatusConfig holds common configuration for status updates.
// This is used by reconcilers that sync status back to CRDs.
type BaseStatusConfig struct {
	// StatusUpdater provides access to update CRD status (optional)
	StatusUpdater StatusUpdater

	// Namespace is the namespace to use for status updates
	Namespace string
}

// SetStatusUpdater sets the status updater and namespace.
func (c *BaseStatusConfig) SetStatusUpdater(updater StatusUpdater, namespace string) {
	c.StatusUpdater = updater
	if namespace != "" {
		c.Namespace = namespace
	}
}

// GetNamespace returns the namespace to use, falling back to the default.
func (c *BaseStatusConfig) GetNamespace(reqNamespace string) string {
	if reqNamespace != "" {
		return reqNamespace
	}
	if c.Namespace != "" {
		return c.Namespace
	}
	return DefaultNamespace
}

// IsNotFoundError checks if an error indicates a resource was not found.
// It checks for Kubernetes NotFound errors first, then falls back to
// case-insensitive string matching for common "not found" patterns.
func IsNotFoundError(err error) bool {
	if err == nil {
		return false
	}

	// Check for Kubernetes NotFound errors
	if apierrors.IsNotFound(err) {
		return true
	}

	// Fall back to string matching for non-K8s errors (case-insensitive)
	errMsg := strings.ToLower(err.Error())
	return strings.Contains(errMsg, "not found") ||
		strings.Contains(errMsg, "does not exist")
}

// SanitizeErrorMessage sanitizes an error message for external exposure.
// It removes potentially sensitive information like absolute file paths,
// credentials, and internal implementation details.
//
// This should be used when error messages are exposed via APIs or stored
// in CRD status fields that may be visible to users.
func SanitizeErrorMessage(errMsg string) string {
	if errMsg == "" {
		return ""
	}

	// Replace absolute file paths with just the filename
	// Match patterns like /home/user/path/to/file.yaml or /var/lib/something
	pathPattern := regexp.MustCompile(`(?:/[\w.-]+)+/`)
	errMsg = pathPattern.ReplaceAllStringFunc(errMsg, func(path string) string {
		// Keep just "[path]/" to indicate there was a path
		return "[path]/"
	})

	// Redact potential secrets/tokens (anything that looks like a bearer token or API key)
	tokenPattern := regexp.MustCompile(`(?i)(bearer\s+|token[=:]\s*|apikey[=:]\s*|password[=:]\s*|secret[=:]\s*)[\w\-._~+/]+=*`)
	errMsg = tokenPattern.ReplaceAllString(errMsg, "$1[REDACTED]")

	// Redact base64-encoded looking strings (potential secrets) longer than 20 chars
	base64Pattern := regexp.MustCompile(`[A-Za-z0-9+/]{20,}={0,2}`)
	errMsg = base64Pattern.ReplaceAllStringFunc(errMsg, func(match string) string {
		// Only redact if it looks like a secret (not a filename or normal text)
		if len(match) > 40 {
			return "[REDACTED]"
		}
		return match
	})

	return errMsg
}

// StatusSyncRetryBackoff is the retry backoff configuration for status updates.
// It uses an aggressive retry strategy since status updates are idempotent and
// conflicts are expected during high-frequency reconciliation.
var StatusSyncRetryBackoff = retry.DefaultRetry

// IsConflictError returns true if the error is a Kubernetes conflict error.
// Conflict errors occur when the resource was modified since it was read,
// indicating the resource version is stale (optimistic locking failure).
func IsConflictError(err error) bool {
	return apierrors.IsConflict(err)
}

// StatusSyncFailureTracker tracks per-resource status sync failures to implement
// backoff-based logging. This reduces log spam when status syncs fail repeatedly
// for the same resource.
type StatusSyncFailureTracker struct {
	mu       sync.RWMutex
	failures map[string]*resourceFailureInfo
}

// resourceFailureInfo tracks failure information for a single resource.
type resourceFailureInfo struct {
	consecutiveFailures int
	lastFailure         time.Time
	lastError           string
	lastLoggedAt        time.Time
}

// NewStatusSyncFailureTracker creates a new failure tracker.
func NewStatusSyncFailureTracker() *StatusSyncFailureTracker {
	return &StatusSyncFailureTracker{
		failures: make(map[string]*resourceFailureInfo),
	}
}

// resourceKey generates a unique key for a resource type and name.
func resourceKey(resourceType ResourceType, name string) string {
	return string(resourceType) + "/" + name
}

// RecordFailure records a status sync failure for a resource.
// Returns true if this failure should be logged (based on backoff).
func (t *StatusSyncFailureTracker) RecordFailure(resourceType ResourceType, name string, err error) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	key := resourceKey(resourceType, name)
	info, exists := t.failures[key]
	if !exists {
		info = &resourceFailureInfo{}
		t.failures[key] = info
	}

	info.consecutiveFailures++
	info.lastFailure = time.Now()
	info.lastError = err.Error()

	// Use exponential backoff for logging:
	// - First 3 failures: log every time
	// - Then: log every 10, 100, 1000 failures (powers of 10)
	// - Also log if it's been more than 5 minutes since last log
	shouldLog := info.consecutiveFailures <= 3 ||
		info.consecutiveFailures%10 == 0 && info.consecutiveFailures <= 100 ||
		info.consecutiveFailures%100 == 0 && info.consecutiveFailures <= 1000 ||
		info.consecutiveFailures%1000 == 0 ||
		time.Since(info.lastLoggedAt) > 5*time.Minute

	if shouldLog {
		info.lastLoggedAt = time.Now()
	}

	return shouldLog
}

// RecordSuccess records a successful status sync, resetting the failure counter.
func (t *StatusSyncFailureTracker) RecordSuccess(resourceType ResourceType, name string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	key := resourceKey(resourceType, name)
	delete(t.failures, key)
}

// GetFailureCount returns the current consecutive failure count for a resource.
func (t *StatusSyncFailureTracker) GetFailureCount(resourceType ResourceType, name string) int {
	t.mu.RLock()
	defer t.mu.RUnlock()

	key := resourceKey(resourceType, name)
	if info, exists := t.failures[key]; exists {
		return info.consecutiveFailures
	}
	return 0
}

// GetLastError returns the last error message for a resource.
func (t *StatusSyncFailureTracker) GetLastError(resourceType ResourceType, name string) string {
	t.mu.RLock()
	defer t.mu.RUnlock()

	key := resourceKey(resourceType, name)
	if info, exists := t.failures[key]; exists {
		return info.lastError
	}
	return ""
}

// Global failure tracker for status sync operations.
var (
	globalFailureTracker     *StatusSyncFailureTracker
	globalFailureTrackerOnce sync.Once
)

// GetStatusSyncFailureTracker returns the global failure tracker instance.
func GetStatusSyncFailureTracker() *StatusSyncFailureTracker {
	globalFailureTrackerOnce.Do(func() {
		globalFailureTracker = NewStatusSyncFailureTracker()
	})
	return globalFailureTracker
}

// ResetStatusSyncFailureTracker resets the global failure tracker.
// This is primarily for testing.
func ResetStatusSyncFailureTracker() {
	globalFailureTrackerOnce = sync.Once{}
	globalFailureTracker = nil
}
