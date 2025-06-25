// Package dependency provides a directed acyclic graph (DAG) implementation
// for managing service dependencies in muster.
//
// This package is crucial for ensuring services start and stop in the correct
// order, and for implementing cascade operations when dependencies fail or
// recover. It provides the foundation for muster's intelligent service
// management.
//
// # Core Concepts
//
// Graph: A directed acyclic graph that represents service dependencies.
// Each node in the graph is a service, and edges represent dependencies.
//
// Node: Represents a service in the dependency graph with:
//   - ID: Unique identifier for the service
//   - FriendlyName: Human-readable name
//   - Kind: Type of service (K8s, PortForward, MCP)
//   - DependsOn: List of services this node depends on
//
// # Dependency Rules
//
// The dependency system enforces these rules:
//
//  1. No circular dependencies allowed (enforced by DAG)
//  2. A service cannot start unless all its dependencies are running
//  3. When a service stops, all services depending on it must stop
//  4. When a service recovers, dependent services can be restarted
//
// # Service Hierarchy
//
// The typical dependency hierarchy in muster:
//
//	K8s Connections (Foundation - no dependencies)
//	    ↓
//	Port Forwards (Depend on K8s connections)
//	    ↓
//	MCP Servers (May depend on port forwards)
//
// # Operations
//
// The graph supports several key operations:
//
// AddNode: Add a service to the graph
//   - Validates that dependencies exist
//   - Prevents circular dependencies
//
// Dependents: Find all services that depend on a given service
//   - Used for cascade stop operations
//   - Returns services in dependency order
//
// TopologicalSort: Order services for startup
//   - Ensures dependencies start before dependents
//   - Handles parallel startup where possible
//
// # Usage Example
//
//	// Create a new dependency graph
//	graph := dependency.New()
//
//	// Add K8s connection (no dependencies)
//	graph.AddNode(dependency.Node{
//	    ID:           "k8s-mc-prod",
//	    FriendlyName: "Production MC",
//	    Kind:         dependency.KindK8sConnection,
//	    DependsOn:    []dependency.NodeID{},
//	})
//
//	// Add port forward (depends on K8s)
//	graph.AddNode(dependency.Node{
//	    ID:           "pf:prometheus",
//	    FriendlyName: "Prometheus",
//	    Kind:         dependency.KindPortForward,
//	    DependsOn:    []dependency.NodeID{"k8s-mc-prod"},
//	})
//
//	// Add MCP server (depends on port forward)
//	graph.AddNode(dependency.Node{
//	    ID:           "mcp:prometheus",
//	    FriendlyName: "Prometheus MCP",
//	    Kind:         dependency.KindMCP,
//	    DependsOn:    []dependency.NodeID{"pf:prometheus"},
//	})
//
//	// Find all services that depend on the K8s connection
//	dependents := graph.Dependents("k8s-mc-prod")
//	// Returns: ["pf:prometheus", "mcp:prometheus"]
//
//	// Get startup order
//	order := graph.TopologicalSort()
//	// Returns: ["k8s-mc-prod", "pf:prometheus", "mcp:prometheus"]
//
// # Thread Safety
//
// The Graph type is thread-safe and can be used concurrently. All operations
// use internal locking to ensure consistency when accessed from multiple
// goroutines.
//
// # Error Handling
//
// The package provides clear error messages for:
//   - Circular dependency detection
//   - Missing dependency references
//   - Invalid node configurations
//   - Duplicate node IDs
package dependency
