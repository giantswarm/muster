package api

import (
	"context"
)

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
