package client

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	musterv1alpha1 "github.com/giantswarm/muster/pkg/apis/muster/v1alpha1"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/client/filesystem"
	"github.com/giantswarm/muster/internal/client/kubernetes"
	"github.com/giantswarm/muster/pkg/logging"
)

// MusterClient is a unified interface that abstracts both Kubernetes and filesystem clients.
// It provides a single interface for interacting with muster resources regardless of the
// deployment mode (Kubernetes cluster vs filesystem configuration).
//
// The interface adapts to the environment:
//   - If Kubernetes cluster access is available, it uses the Kubernetes API
//   - If Kubernetes is not available and no mode was required, it falls back
//     to filesystem operations
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
	UpdateWorkflowStatus(ctx context.Context, workflow *musterv1alpha1.Workflow) error

	// Service operations (to be implemented in future)
	// WorkflowExecution operations (to be implemented in future)

	// Event operations
	CreateEvent(ctx context.Context, obj client.Object, reason, message, eventType string) error
	CreateEventForCRD(ctx context.Context, crdType, name, namespace, reason, message, eventType string) error
	QueryEvents(ctx context.Context, options api.EventQueryOptions) (*api.EventQueryResult, error)
	WatchEvents(ctx context.Context, options api.EventQueryOptions) (<-chan api.EventResult, error)

	// Utility methods
	IsKubernetesMode() bool
	Close() error
}

var (
	_ MusterClient = (*kubernetes.Client)(nil)
	_ MusterClient = (*filesystem.Client)(nil)
)

// KubernetesClientBackoff bounds how long a required Kubernetes client is
// retried before its creation fails: five attempts with about half a minute
// of sleeps between them (2, 4, 8 and 16 s). That covers the window in which
// an apiserver, or the kube-proxy rules in front of it, comes back after a
// node restart — the situation in which muster used to come up in the wrong
// mode (issue #1143). Anything longer is left to the kubelet's restart
// backoff, which is visible, rather than waited out inside a process that
// reports nothing.
//
// No Cap on purpose: wait.Backoff.Step zeroes the remaining steps once the
// sleep reaches the cap, which would silently cut the attempts short.
//
// It is a variable so tests can shorten it.
var KubernetesClientBackoff = wait.Backoff{
	Duration: 2 * time.Second,
	Factor:   2,
	Steps:    5,
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
// Mode selection:
//   - ForceFilesystemMode: the filesystem client, no apiserver is contacted.
//   - RequireKubernetesMode: the Kubernetes client, or an error. A muster
//     that was configured to run against an apiserver must never come up
//     against an empty directory instead — that is not a degraded mode but a
//     different program, which deletes the services of every CR it can no
//     longer see (issue #1143). Creation is retried per
//     KubernetesClientBackoff so a brief apiserver outage at startup is
//     waited out; a persistent one fails startup so the kubelet restarts the
//     container visibly.
//   - neither: Kubernetes when reachable, otherwise the filesystem, with a
//     warning naming the fallback.
//
// Args:
//   - cfg: Optional configuration. If nil, uses standard detection methods.
//
// Returns:
//   - MusterClient: The unified client interface
//   - error: Error if client creation fails
func NewMusterClientWithConfig(cfg *MusterClientConfig) (MusterClient, error) {
	if cfg == nil {
		cfg = &MusterClientConfig{}
	}

	if cfg.ForceFilesystemMode && cfg.RequireKubernetesMode {
		return nil, fmt.Errorf("muster client: ForceFilesystemMode and RequireKubernetesMode are mutually exclusive")
	}

	if cfg.ForceFilesystemMode {
		return filesystem.New(cfg.FilesystemPath), nil
	}

	if cfg.RequireKubernetesMode {
		return newRequiredKubernetesClient()
	}

	k8sClient, err := newKubernetesClient()
	if err == nil {
		return k8sClient, nil
	}

	// Automatic detection only: a change of storage backend is never silent.
	logging.Warn("client", "Kubernetes is not available, falling back to filesystem mode at %q: %v", cfg.FilesystemPath, err)
	return filesystem.New(cfg.FilesystemPath), nil
}

// newKubernetesClient makes one attempt at a Kubernetes-backed client: config
// detection, then construction including the CRD discovery check.
func newKubernetesClient() (MusterClient, error) {
	restConfig, err := detectKubernetesConfig()
	if err != nil {
		return nil, err
	}
	return kubernetes.New(restConfig)
}

// newRequiredKubernetesClient retries newKubernetesClient per
// KubernetesClientBackoff and never substitutes another backend. Each failed
// attempt is logged at warning level so an operator watching the pod sees
// what muster is waiting for; the final error names the cause and the
// refusal to fall back.
func newRequiredKubernetesClient() (MusterClient, error) {
	var k8sClient MusterClient
	attempt := 0
	err := retry.OnError(KubernetesClientBackoff, func(error) bool { return true }, func() error {
		attempt++
		c, err := newKubernetesClient()
		if err != nil {
			logging.Warn("client", "Kubernetes mode is configured but the Kubernetes client could not be created (attempt %d/%d): %v",
				attempt, KubernetesClientBackoff.Steps, err)
			return err
		}
		k8sClient = c
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("kubernetes mode is configured but no Kubernetes client could be created after %d attempts, refusing to fall back to filesystem mode: %w", attempt, err)
	}
	return k8sClient, nil
}

// MusterClientConfig provides configuration options for client creation.
type MusterClientConfig struct {
	// Namespace is the default namespace for operations (defaults to "default")
	Namespace string

	// FilesystemPath is the base path for filesystem storage (defaults to current directory)
	FilesystemPath string

	// ForceFilesystemMode forces filesystem mode even if Kubernetes is available
	ForceFilesystemMode bool

	// RequireKubernetesMode makes a failure to create the Kubernetes client an
	// error instead of a fallback to the filesystem. Set it whenever
	// Kubernetes mode is configured explicitly (`kubernetes: true`).
	// Mutually exclusive with ForceFilesystemMode.
	RequireKubernetesMode bool

	// Debug enables debug-level logging and warnings
	Debug bool
}

// detectKubernetesConfig attempts to detect and load Kubernetes configuration.
func detectKubernetesConfig() (*rest.Config, error) {
	// Use controller-runtime's standard config detection
	// This handles in-cluster config, kubeconfig, and other standard methods
	restConfig, err := ctrl.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get Kubernetes config: %w", err)
	}

	return restConfig, nil
}
