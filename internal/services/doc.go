// Package services provides the service abstraction layer for muster.
//
// This package defines the core interfaces and types that all services in muster
// must implement. It provides a unified way to manage different types of services
// through a common interface.
//
// # Core Concepts
//
// Service: The fundamental unit of work in muster. Each service represents a
// manageable component that can be started, stopped, and monitored.
//
// ServiceRegistry: A thread-safe registry that holds all active services and
// provides methods to query and manage them.
//
// ServiceState: Represents the current state of a service (unknown, waiting,
// starting, running, stopping, stopped, failed, retrying).
//
// # Service Architecture
//
// The services package supports static service types:
//
//   - TypeMCPServer: Runs Model Context Protocol servers for AI integration
//   - TypeAggregator: Coordinates multiple MCP servers
//
// # Service Interface
//
// All services must implement the core Service interface:
//
//	type Service interface {
//	    GetLabel() string                    // Unique identifier
//	    GetType() ServiceType                // Service type
//	    GetState() ServiceState              // Current state
//	    GetHealth() HealthStatus             // Health status
//	    GetLastError() error                 // Last error encountered
//	    GetDependencies() []string           // Service dependencies
//	    Start(ctx context.Context) error     // Start the service
//	    Stop(ctx context.Context) error      // Stop the service
//	    Restart(ctx context.Context) error   // Restart the service
//	    SetStateChangeCallback(StateChangeCallback) // State change notifications
//	}
//
// # Extended Interfaces
//
// Services can implement additional interfaces for extended functionality:
//
// HealthChecker: For services that support periodic health checks
//
//	type HealthChecker interface {
//	    CheckHealth(ctx context.Context) (HealthStatus, error)
//	    GetHealthCheckInterval() time.Duration
//	}
//
// ServiceDataProvider: For services that provide runtime data
//
//	type ServiceDataProvider interface {
//	    GetServiceData() map[string]interface{}
//	}
//
// StateUpdater: For external state management
//
//	type StateUpdater interface {
//	    UpdateState(state ServiceState, health HealthStatus, err error)
//	}
//
// # Service Lifecycle
//
//  1. Creation: Service created with static configuration
//  2. Registration: Service registered with ServiceRegistry
//  3. Starting: Service transitions through Stopped → Starting → Running
//  4. Health Monitoring: Optional periodic health checks
//  5. Stopping: Service transitions through Running → Stopping → Stopped
//  6. Failure: Service can transition to Failed state
//
// # State Change Events
//
// Services support state change notifications through callbacks:
//
//	type StateChangeCallback func(
//	    label string,
//	    oldState, newState ServiceState,
//	    health HealthStatus,
//	    err error,
//	)
//
// # Thread Safety
//
// All components in this package are designed for concurrent access:
//
//   - ServiceRegistry is fully thread-safe
//   - Service implementations maintain thread safety
//   - State updates are atomic and properly synchronized
//
// # API Integration
//
// Following the project's Service Locator Pattern, this package provides
// API adapters for integration with the central API layer:
//
//   - RegistryAdapter: Exposes service registry through API
//   - Proper interface segregation for different access patterns
//   - No direct inter-package dependencies
package services
