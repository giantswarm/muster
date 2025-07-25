package api

import (
	"context"
	"time"
)

// EventQueryOptions represents filtering options for event queries.
type EventQueryOptions struct {
	// ResourceType filters events by object kind (MCPServer, ServiceClass, Workflow, ServiceInstance)
	ResourceType string `json:"resourceType,omitempty"`

	// ResourceName filters events by object name
	ResourceName string `json:"resourceName,omitempty"`

	// Namespace filters events by namespace
	Namespace string `json:"namespace,omitempty"`

	// EventType filters by event type (Normal, Warning)
	EventType string `json:"eventType,omitempty"`

	// Since filters events that occurred after this time
	Since *time.Time `json:"since,omitempty"`

	// Until filters events that occurred before this time
	Until *time.Time `json:"until,omitempty"`

	// Limit restricts the number of events returned
	Limit int `json:"limit,omitempty"`
}

// EventResult represents a single event result.
type EventResult struct {
	// Timestamp when the event occurred
	Timestamp time.Time `json:"timestamp"`

	// Namespace of the involved object
	Namespace string `json:"namespace"`

	// InvolvedObject information
	InvolvedObject ObjectReference `json:"involvedObject"`

	// Reason for the event
	Reason string `json:"reason"`

	// Message describing the event
	Message string `json:"message"`

	// Type of event (Normal, Warning)
	Type string `json:"type"`

	// Source component that generated the event
	Source string `json:"source"`

	// Count for how many times this event occurred (Kubernetes mode)
	Count int32 `json:"count,omitempty"`
}

// EventQueryResult represents the result of an event query.
type EventQueryResult struct {
	// Events is the list of events matching the query
	Events []EventResult `json:"events"`

	// TotalCount is the total number of events (before limit is applied)
	TotalCount int `json:"totalCount"`
}

// EventManagerHandler provides Kubernetes Event generation functionality for muster
// CRD lifecycle operations and service management.
//
// This interface abstracts the event generation system, allowing components to
// create events without knowing the specific implementation details of how events
// are stored or delivered (Kubernetes Events API vs filesystem logging).
//
// The handler automatically adapts to the current client mode:
//   - Kubernetes mode: Creates actual Kubernetes Events API objects
//   - Filesystem mode: Logs events to console and events.log file
//
// Key features:
// - Unified event generation across both Kubernetes and filesystem modes
// - Dynamic message templating with contextual data
// - Automatic event type classification (Normal vs Warning)
// - Support for both CRD objects and synthetic references
//
// Thread-safety: All methods are safe for concurrent use.
type EventManagerHandler interface {
	// Event creation methods

	// CreateEvent creates an event for a specific object reference.
	// This method is used when you have the complete object reference information
	// but not necessarily the actual Kubernetes object.
	//
	// Args:
	//   - ctx: Context for the operation, including cancellation and timeout
	//   - objectRef: Reference to the object this event relates to
	//   - reason: Short, machine-readable reason for the event (e.g., "Created", "Failed")
	//   - message: Human-readable description of the event
	//   - eventType: Type of event ("Normal" or "Warning")
	//
	// Returns:
	//   - error: Error if event creation fails
	//
	// Example:
	//
	//	objectRef := ObjectReference{
	//		Kind:      "MCPServer",
	//		Name:      "github-server",
	//		Namespace: "default",
	//	}
	//	err := handler.CreateEvent(ctx, objectRef, "Created", "MCPServer successfully created", "Normal")
	CreateEvent(ctx context.Context, objectRef ObjectReference, reason, message, eventType string) error

	// CreateEventForCRD creates an event for a CRD by type, name, and namespace.
	// This method is used when you know the CRD details but don't have the full object reference.
	//
	// Args:
	//   - ctx: Context for the operation
	//   - crdType: Type of CRD ("MCPServer", "ServiceClass", "Workflow")
	//   - name: Name of the CRD instance
	//   - namespace: Namespace of the CRD instance
	//   - reason: Short, machine-readable reason for the event
	//   - message: Human-readable description of the event
	//   - eventType: Type of event ("Normal" or "Warning")
	//
	// Returns:
	//   - error: Error if event creation fails
	//
	// Example:
	//
	//	err := handler.CreateEventForCRD(ctx, "MCPServer", "github-server", "default",
	//		"Started", "MCPServer service started successfully", "Normal")
	CreateEventForCRD(ctx context.Context, crdType, name, namespace, reason, message, eventType string) error

	// Event querying methods

	// QueryEvents retrieves events based on the provided filtering options.
	// This method works with both Kubernetes and filesystem modes:
	//   - Kubernetes mode: Queries the native Kubernetes Events API
	//   - Filesystem mode: Parses stored event files
	//
	// Args:
	//   - ctx: Context for the operation
	//   - options: Filtering options for the event query
	//
	// Returns:
	//   - *EventQueryResult: Query result containing matching events
	//   - error: Error if query fails
	//
	// Example:
	//
	//	options := EventQueryOptions{
	//		ResourceType: "MCPServer",
	//		Namespace:    "default",
	//		Limit:        50,
	//	}
	//	result, err := handler.QueryEvents(ctx, options)
	QueryEvents(ctx context.Context, options EventQueryOptions) (*EventQueryResult, error)

	// Utility methods

	// IsKubernetesMode returns true if the event manager is using Kubernetes mode.
	// This can be useful for components that need to adapt their behavior
	// based on the deployment environment.
	//
	// Returns:
	//   - bool: true if using Kubernetes mode, false if using filesystem mode
	//
	// Example:
	//
	//	if handler.IsKubernetesMode() {
	//		// Can access events via kubectl get events
	//	} else {
	//		// Events are logged to console and events.log
	//	}
	IsKubernetesMode() bool
}

// ObjectReference represents a reference to a Kubernetes object for event creation.
// This structure is used to identify the object that an event relates to.
type ObjectReference struct {
	// APIVersion is the API version of the object (e.g., "muster.giantswarm.io/v1alpha1")
	APIVersion string `json:"apiVersion,omitempty"`

	// Kind is the kind of the object (e.g., "MCPServer", "ServiceClass", "Workflow")
	Kind string `json:"kind"`

	// Name is the name of the object
	Name string `json:"name"`

	// Namespace is the namespace of the object (required for namespaced objects)
	Namespace string `json:"namespace"`

	// UID is the unique identifier of the object (optional, helps with precision)
	UID string `json:"uid,omitempty"`
}
