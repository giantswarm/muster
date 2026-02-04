package client

import (
	"context"
	"fmt"

	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	musterv1alpha1 "github.com/giantswarm/muster/pkg/apis/muster/v1alpha1"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/pkg/logging"
)

// MusterClient is a unified interface that abstracts both Kubernetes and filesystem clients.
// It provides a single interface for interacting with muster resources regardless of the
// deployment mode (Kubernetes cluster vs filesystem configuration).
//
// The interface automatically adapts to the environment:
//   - If Kubernetes cluster access is available, it uses the Kubernetes API
//   - If Kubernetes is not available, it falls back to filesystem operations
//
// This abstraction allows the same code to work in both environments without modification.
type MusterClient interface {
	// Controller-runtime client interface for basic CRUD operations
	client.Client

	// MCPServer operations
	GetMCPServer(ctx context.Context, name, namespace string) (*musterv1alpha1.MCPServer, error)
	ListMCPServers(ctx context.Context, namespace string) ([]musterv1alpha1.MCPServer, error)
	CreateMCPServer(ctx context.Context, server *musterv1alpha1.MCPServer) error
	UpdateMCPServer(ctx context.Context, server *musterv1alpha1.MCPServer) error
	DeleteMCPServer(ctx context.Context, name, namespace string) error

	// ServiceClass operations
	GetServiceClass(ctx context.Context, name, namespace string) (*musterv1alpha1.ServiceClass, error)
	ListServiceClasses(ctx context.Context, namespace string) ([]musterv1alpha1.ServiceClass, error)
	CreateServiceClass(ctx context.Context, serviceClass *musterv1alpha1.ServiceClass) error
	UpdateServiceClass(ctx context.Context, serviceClass *musterv1alpha1.ServiceClass) error
	DeleteServiceClass(ctx context.Context, name, namespace string) error

	// Workflow operations
	GetWorkflow(ctx context.Context, name, namespace string) (*musterv1alpha1.Workflow, error)
	ListWorkflows(ctx context.Context, namespace string) ([]musterv1alpha1.Workflow, error)
	CreateWorkflow(ctx context.Context, workflow *musterv1alpha1.Workflow) error
	UpdateWorkflow(ctx context.Context, workflow *musterv1alpha1.Workflow) error
	DeleteWorkflow(ctx context.Context, name, namespace string) error

	// Status update operations (uses Status subresource in Kubernetes mode)
	// These methods update only the Status field of the resource.
	// See ADR 007 for details on what status fields are synced.
	UpdateMCPServerStatus(ctx context.Context, server *musterv1alpha1.MCPServer) error
	UpdateServiceClassStatus(ctx context.Context, serviceClass *musterv1alpha1.ServiceClass) error
	UpdateWorkflowStatus(ctx context.Context, workflow *musterv1alpha1.Workflow) error

	// Service operations (to be implemented in future)
	// WorkflowExecution operations (to be implemented in future)

	// Event operations
	CreateEvent(ctx context.Context, obj client.Object, reason, message, eventType string) error
	CreateEventForCRD(ctx context.Context, crdType, name, namespace, reason, message, eventType string) error
	QueryEvents(ctx context.Context, options api.EventQueryOptions) (*api.EventQueryResult, error)

	// Utility methods
	IsKubernetesMode() bool
	Close() error
}

// NewMusterClient creates a new unified muster client with automatic environment detection.
//
// The client will attempt to use Kubernetes configuration (from kubeconfig, in-cluster config,
// or other standard methods). If Kubernetes is not available, it will fall back to filesystem mode.
//
// Returns:
//   - MusterClient: The unified client interface
//   - error: Error if client creation fails
func NewMusterClient() (MusterClient, error) {
	return NewMusterClientWithConfig(nil)
}

// NewMusterClientWithConfig creates a new unified muster client with optional configuration.
//
// This function provides more control over client creation for advanced use cases.
//
// Args:
//   - cfg: Optional Kubernetes configuration. If nil, uses standard detection methods.
//
// Returns:
//   - MusterClient: The unified client interface
//   - error: Error if client creation fails
func NewMusterClientWithConfig(cfg *MusterClientConfig) (MusterClient, error) {
	if cfg == nil {
		cfg = &MusterClientConfig{}
	}

	// Try Kubernetes configuration first
	if restConfig, err := detectKubernetesConfig(cfg); err == nil && restConfig != nil {
		k8sClient, err := NewKubernetesClient(restConfig)
		if err == nil {
			return k8sClient, nil
		}
		// Log the error but continue to filesystem fallback
		// This is expected behavior when CRDs are not installed
		// Only show warning in debug mode since filesystem is the expected fallback
		if cfg.Debug {
			logging.Debug("client", "Failed to create Kubernetes client: %v, falling back to filesystem mode", err)
		}
	}

	// Fall back to filesystem mode
	return NewFilesystemClient(cfg)
}

// MusterClientConfig provides configuration options for client creation.
type MusterClientConfig struct {
	// Namespace is the default namespace for operations (defaults to "default")
	Namespace string

	// FilesystemPath is the base path for filesystem storage (defaults to current directory)
	FilesystemPath string

	// ForceFilesystemMode forces filesystem mode even if Kubernetes is available
	ForceFilesystemMode bool

	// Debug enables debug-level logging and warnings
	Debug bool
}

// detectKubernetesConfig attempts to detect and load Kubernetes configuration.
func detectKubernetesConfig(cfg *MusterClientConfig) (*rest.Config, error) {
	if cfg.ForceFilesystemMode {
		return nil, fmt.Errorf("filesystem mode forced")
	}

	// Use controller-runtime's standard config detection
	// This handles in-cluster config, kubeconfig, and other standard methods
	restConfig, err := ctrl.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get Kubernetes config: %w", err)
	}

	return restConfig, nil
}
