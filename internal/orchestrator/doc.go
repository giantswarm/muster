// Package orchestrator manages the lifecycle of services registered in the
// shared service registry. It starts and stops services, retries failed
// MCPServer connections with bounded concurrency, and publishes service
// state-change events to subscribers.
//
// Service implementations live in subpackages of internal/services. The
// orchestrator drives them through the services.Service interface and the
// services.ServiceRegistry; it has no service-specific logic of its own.
package orchestrator
