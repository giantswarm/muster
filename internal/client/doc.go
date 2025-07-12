// Package client provides a unified client abstraction for accessing muster resources
// both locally (filesystem) and in-cluster (Kubernetes API).
//
// # Overview
//
// The client package implements the unified access pattern described in Issues #30 and #36,
// providing seamless operation across different deployment environments:
//
// - **Local Development**: Filesystem-based storage with YAML files
// - **In-Cluster**: Native Kubernetes API access using controller-runtime
// - **External Tools**: kubectl-compatible access patterns
//
// # Architecture
//
// The package implements a facade pattern with automatic environment detection:
//
//	┌─────────────────┐
//	│   MusterClient  │  ← Unified Interface
//	│   (Interface)   │
//	└─────────────────┘
//	         │
//	    ┌────┴────┐
//	    │ Factory │  ← Environment Detection
//	    └────┬────┘
//	         │
//	   ┌─────┴─────┐
//	   │           │
//	┌──▼──┐    ┌───▼──┐
//	│ K8s │    │ File │  ← Backend Implementations
//	│     │    │      │
//	└─────┘    └──────┘
//
// # Usage Examples
//
// ## Basic Usage (Automatic Detection)
//
//	client, err := client.NewMusterClient()
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer client.Close()
//
//	servers, err := client.ListMCPServers(ctx, "default")
//	if err != nil {
//	    log.Fatal(err)
//	}
//
// ## Explicit Configuration
//
//	config := &client.MusterClientConfig{
//	    Namespace: "muster-system",
//	    ForceFilesystemMode: true,
//	}
//	client, err := client.NewMusterClientWithConfig(config)
//
// # Environment Detection
//
// The client automatically detects the execution environment:
//
// 1. **Kubernetes Detection**: Uses controller-runtime's standard config detection
//   - In-cluster service account credentials
//   - kubeconfig file (~/.kube/config)
//   - Environment variables (KUBECONFIG)
//
// 2. **Filesystem Fallback**: Used when Kubernetes is not available
//   - Local development environments
//   - Standalone deployment scenarios
//   - Testing and debugging
//
// # Interface Compatibility
//
// MusterClient extends controller-runtime's client.Client interface, ensuring
// compatibility with existing Kubernetes tooling while adding muster-specific
// convenience methods.
//
// # Future Extensibility
//
// The interface design supports future CRD implementations:
//
//	// Current (v1alpha1)
//	client.GetMCPServer(ctx, name, namespace)
//
//	// Future (as CRDs are implemented)
//	client.GetServiceClass(ctx, name, namespace)
//	client.ListWorkflows(ctx, namespace)
//	client.CreateService(ctx, service)
//
// # Error Handling
//
// The client provides consistent error handling across both backends:
//
//	server, err := client.GetMCPServer(ctx, "server-name", "default")
//	if err != nil {
//	    if errors.IsNotFound(err) {
//	        // Handle not found consistently across backends
//	    }
//	    // Handle other errors
//	}
//
// # Thread Safety
//
// All client implementations are thread-safe and can be used concurrently
// from multiple goroutines.
package client
