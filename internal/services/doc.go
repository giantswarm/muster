// Package services provides the service abstraction layer for muster.
//
// It defines the Service interface and the registry that holds all running
// services. The orchestrator drives service lifecycle through this package.
//
// Concrete service implementations live in subpackages:
//   - services/mcpserver — wraps an MCP server process as a managed service
//   - services/aggregator — wraps the aggregator as a managed service
//
// # Core Interface
//
//	type Service interface {
//	    GetLabel() string
//	    GetType() ServiceType
//	    GetState() ServiceState
//	    GetHealth() HealthStatus
//	    GetLastError() error
//	    GetDependencies() []string
//	    Start(ctx context.Context) error
//	    Stop(ctx context.Context) error
//	    Restart(ctx context.Context) error
//	    SetStateChangeCallback(StateChangeCallback)
//	}
//
// Optional capabilities are exposed via additional interfaces (HealthChecker,
// ServiceDataProvider, StateUpdater) that implementations may satisfy.
//
// # Registry
//
// ServiceRegistry is a thread-safe registry of running services. It is
// exposed to the rest of the codebase through the api package via
// RegistryAdapter, following the service-locator pattern.
package services
