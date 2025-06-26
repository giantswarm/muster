// Package aggregator provides MCP (Model Context Protocol) server aggregation functionality
// for the muster system.
//
// The aggregator package acts as a central hub that collects and exposes tools, resources,
// and prompts from multiple backend MCP servers through a single unified interface.
// It implements intelligent name collision resolution, security filtering, automatic
// server lifecycle management, and seamless integration with the muster service architecture.
//
// # Architecture Overview
//
// The aggregator follows a layered architecture with clear separation of concerns:
//
//	┌─────────────────┐    ┌─────────────────┐    ┌─────────────────┐
//	│   MCP Clients   │    │  Core Tools     │    │  Workflow       │
//	│  (External)     │    │  (Internal)     │    │  Adapter        │
//	└─────────────────┘    └─────────────────┘    └─────────────────┘
//	         │                       │                       │
//	         └───────────────────────┼───────────────────────┘
//	                                 │
//	                    ┌─────────────────┐
//	                    │ AggregatorServer│
//	                    │  (Core MCP)     │
//	                    └─────────────────┘
//	                                 │
//	         ┌───────────────────────┼───────────────────────┐
//	         │                       │                       │
//	┌─────────────────┐    ┌─────────────────┐    ┌─────────────────┐
//	│ SSE Transport   │    │ Stdio Transport │    │ HTTP Transport  │
//	│    (:8080)      │    │   (CLI Mode)    │    │  (Streaming)    │
//	└─────────────────┘    └─────────────────┘    └─────────────────┘
//
// # Core Components
//
// ## AggregatorManager
//
// The AggregatorManager is the main entry point and coordinates all aggregator functionality.
// It manages the complete lifecycle, from server startup to graceful shutdown, and provides
// automatic MCP server registration based on service health status.
//
// Key responsibilities:
//   - Server lifecycle management (start/stop coordination)
//   - Event-driven MCP server registration/deregistration
//   - Integration with the muster service architecture
//   - Periodic retry mechanism for failed registrations
//   - Service monitoring and health reporting
//
// Usage:
//
//	config := AggregatorConfig{
//		Port:         8080,
//		Host:         "localhost",
//		Transport:    "sse",
//		Yolo:         false,
//		MusterPrefix: "x",
//	}
//
//	manager := NewAggregatorManager(config, orchestratorAPI, serviceRegistry)
//	err := manager.Start(ctx)
//	defer manager.Stop(ctx)
//
// ## AggregatorServer
//
// The AggregatorServer implements the core MCP server functionality, aggregating
// capabilities from multiple backend servers and exposing them through various transports.
// It provides real-time capability updates and maintains synchronization with backend servers.
//
// Features:
//   - Multi-transport support (SSE, stdio, streamable-http)
//   - Dynamic capability discovery and updates
//   - Core tool integration (workflow, capability, config management)
//   - Thread-safe concurrent operations
//   - Intelligent request routing to backend servers
//
// ## ServerRegistry
//
// The ServerRegistry manages the collection of registered MCP servers and their cached
// capabilities. It provides thread-safe access to server information and maintains
// bidirectional name mappings for routing requests to the correct backend servers.
//
// Features:
//   - Thread-safe server registration/deregistration
//   - Capability caching for performance optimization
//   - Server health tracking and connection status
//   - Efficient bulk operations for capability retrieval
//   - Update notifications for dynamic capability changes
//
// ## EventHandler
//
// The EventHandler bridges the gap between the muster service orchestrator and the
// aggregator by automatically registering/deregistering MCP servers based on their
// health status. It ensures that only healthy, running services are exposed through
// the aggregator.
//
// Event-driven behavior:
//   - Running + Healthy → Automatic registration
//   - Stopped/Failed/Unhealthy → Automatic deregistration
//   - Resilient to temporary failures
//   - Asynchronous processing for responsiveness
//
// ## NameTracker
//
// The NameTracker implements intelligent name collision resolution through a consistent
// prefixing scheme. It maintains bidirectional mappings between exposed (prefixed) names
// and their original server/name combinations.
//
// Naming scheme:
//
//	{muster_prefix}_{server_prefix}_{original_name}
//
// Examples:
//   - Original: "list_files" from "github" server
//   - Exposed: "x_github_list_files" (with muster prefix "x")
//   - With custom prefix: "x_gh_list_files" (server prefix "gh")
//
// # Transport Protocols
//
// ## Server-Sent Events (SSE)
//
// SSE transport provides real-time communication over HTTP with automatic reconnection
// and keep-alive support. Ideal for web-based integrations and real-time applications.
//
//	Endpoints:
//	- http://localhost:8080/sse     (SSE stream)
//	- http://localhost:8080/message (message posting)
//
// ## Standard I/O (stdio)
//
// Stdio transport enables CLI integration by communicating over standard input/output.
// Perfect for command-line tools and shell integrations.
//
// ## Streamable HTTP
//
// Streamable HTTP provides HTTP-based streaming protocol support with full bidirectional
// communication. This is the default transport for maximum compatibility.
//
// # Security Model
//
// ## Denylist System
//
// The aggregator implements a "secure by default" approach with a comprehensive denylist
// of potentially destructive tools. This prevents accidental execution of dangerous
// operations in production environments.
//
// Categories of blocked operations:
//   - Kubernetes resource modification (kubectl apply, delete, patch)
//   - Cluster API lifecycle operations (create/delete clusters)
//   - Helm package management (install, uninstall, upgrade)
//   - Flux GitOps operations (reconcile, suspend, resume)
//   - System maintenance operations (cleanup, incident creation)
//
// ## Yolo Mode
//
// The --yolo flag disables the security denylist for development environments where
// destructive operations may be needed. This should never be used in production.
//
//	config.Yolo = true  // Disables all security restrictions
//
// # Integration with Muster Architecture
//
// ## Central API Pattern
//
// The aggregator follows the central API pattern for inter-package communication:
//   - Receives service state events through api.OrchestratorAPI
//   - Queries service information via api.ServiceRegistryHandler
//   - Publishes tool update events via api.PublishToolUpdateEvent
//   - Integrates with workflow manager through api interfaces
//
// ## Tool Update Events
//
// The aggregator publishes ToolUpdateEvent notifications when its capability set changes,
// ensuring system-wide consistency with dependent components like ServiceClass and
// Capability managers.
//
//	Event flow:
//	Backend server change → Registry update → Capability refresh → Event publication
//
// ## Workflow Integration
//
// When configured with a config directory, the aggregator automatically integrates
// with the workflow system to expose workflow definitions as executable tools.
//
// # Performance Considerations
//
// ## Capability Caching
//
// Backend server capabilities are cached in memory to avoid repeated network calls.
// Caches are updated automatically when servers are registered/deregistered or
// when explicit refresh operations are performed.
//
// ## Batch Operations
//
// The aggregator uses batch operations where possible to minimize the number of
// MCP protocol messages and improve performance with multiple backend servers.
//
// ## Background Processing
//
// All registry updates and event processing happen asynchronously in background
// goroutines to ensure that the main request handling remains responsive.
//
// # Error Handling and Resilience
//
// ## Graceful Degradation
//
// The aggregator continues operating even when individual backend servers become
// unavailable. Healthy servers remain accessible while unhealthy servers are
// automatically deregistered.
//
// ## Retry Mechanisms
//
// A periodic retry mechanism attempts to register servers that are healthy but
// not yet registered, providing resilience against temporary registration failures.
//
// ## Connection Management
//
// Backend server connections are managed automatically with proper cleanup during
// deregistration and graceful shutdown procedures.
//
// # Monitoring and Observability
//
// ## Service Data
//
// The aggregator provides comprehensive service monitoring data including:
//   - Tool/resource/prompt counts and status
//   - Server connectivity statistics
//   - Security filtering statistics (blocked tools)
//   - Transport endpoint information
//   - Event handler status
//
// ## Logging
//
// Structured logging provides visibility into:
//   - Server registration/deregistration events
//   - Capability update operations
//   - Security filtering actions
//   - Error conditions and recovery
//
// # Thread Safety
//
// All public APIs are thread-safe and can be called concurrently. Internal state
// is protected by appropriate synchronization mechanisms (mutexes, channels, etc.).
// The package is designed for high-concurrency environments.
//
// # Configuration
//
// The AggregatorConfig structure provides comprehensive configuration options:
//
//	type AggregatorConfig struct {
//		Port         int    // Server port (default: 8080)
//		Host         string // Bind address (default: localhost)
//		Transport    string // Protocol: "sse", "stdio", "streamable-http"
//		Yolo         bool   // Disable security denylist (development only)
//		ConfigDir    string // Directory for workflow definitions
//		MusterPrefix string // Global prefix for all tools (default: "x")
//	}
//
// # Example: Complete Setup
//
//	// Create configuration
//	config := AggregatorConfig{
//		Port:         8080,
//		Host:         "localhost",
//		Transport:    "sse",
//		Yolo:         false,
//		ConfigDir:    "/etc/muster/workflows",
//		MusterPrefix: "x",
//	}
//
//	// Initialize dependencies through central API
//	orchestratorAPI := api.GetOrchestrator()
//	serviceRegistry := api.GetServiceRegistry()
//
//	// Create and start aggregator
//	manager := NewAggregatorManager(config, orchestratorAPI, serviceRegistry)
//	if err := manager.Start(ctx); err != nil {
//		log.Fatal("Failed to start aggregator:", err)
//	}
//	defer manager.Stop(ctx)
//
//	// Access aggregator endpoint
//	endpoint := manager.GetEndpoint()
//	fmt.Printf("Aggregator running at: %s\n", endpoint)
//
// The aggregator package is the cornerstone of muster's MCP integration, providing
// a robust, secure, and scalable foundation for tool aggregation and distribution.
package aggregator