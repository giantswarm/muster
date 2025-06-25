# Changelog

All notable changes to this project will be documented in this file.

## [Unreleased]

### Added
- **Service Health Monitoring**
  - Added health checks for MCP servers using the `tools/list` JSON-RPC method
  - Added health checks for port forwards by testing TCP connectivity
  - Health checks run every 30 seconds for all running services
  - Health status is reported through the StateStore and displayed in the TUI
  - Created `ServiceHealthChecker` interface for extensible health checking
- **Improved State Reconciliation**
  - Implemented proper `ReconcileState()` method that syncs TUI state with StateStore
  - Updates service statuses, ports, PIDs, and error states from centralized store
  - Synchronizes cluster health information from K8sStateManager
  - Ensures UI consistency after startup and state changes
- **K8s Connections as Services**
  - Kubernetes connections are now modeled as services in the dependency graph
  - K8s connection health monitoring is now handled by dedicated K8s connection services
  - Unified service management architecture - all services (K8s, port forwards, MCPs) follow the same lifecycle
  - K8s connections can be stopped/restarted like other services with proper cascade handling
- Cascading stop functionality: stopping a service automatically stops all dependent services
- K8s connection health monitoring with automatic service lifecycle management
- Port forwards now depend on their kubernetes context being authenticated and healthy
- The kubernetes MCP server depends on the management cluster connection
- When k8s connections become unhealthy, dependent services are automatically stopped
- Manual stop (x key) now uses cascading stop to cleanly shut down dependent services
- New `StartServicesDependingOn` method in ServiceManager to restart services when dependencies recover
- New `orchestrator` package that manages application state and service lifecycle for both TUI and non-TUI modes
- New `HealthStatusUpdate` and `ReportHealth` for proper health status reporting
- Health-aware startup: Services now wait for their K8s dependencies to be healthy before starting
- Add comprehensive dependency management system for services
  - Services now track why they were stopped (manual vs dependency cascade)
  - Automatically restart services when their dependencies recover
  - Ensure correct startup order based on dependency graph
  - Prevent manually stopped services from auto-restarting
- **Phase 1 of Issue #45: Message Handling Architecture Improvements**
  - Added correlation ID support to `ManagedServiceUpdate` for tracing related messages and cascading effects
  - Implemented configurable buffer strategies for TUI message channels:
    - `BufferActionDrop`: Drop messages when buffer is full
    - `BufferActionBlock`: Block until space is available
    - `BufferActionEvictOldest`: Remove oldest message to make room for new ones
  - Added priority-based buffer strategies to handle different message types differently
  - Introduced `BufferedChannel` with metrics tracking (messages sent, dropped, blocked, evicted)
  - Enhanced orchestrator with correlation tracking for health checks and cascading operations
  - Updated service manager to use new correlation ID system for better debugging
  - Added comprehensive test coverage for buffer strategies and correlation tracking
- **Phase 2 of Issue #45: State Consolidation**
  - Implemented centralized `StateStore` as single source of truth for all service states
  - Added `ServiceStateSnapshot` for complete state information with correlation tracking
  - Introduced state change subscriptions with `StateSubscription` for reactive updates
  - Enhanced `ServiceReporter` interface with `GetStateStore()` method for direct state access
  - Updated `TUIReporter` and `ConsoleReporter` to use centralized state management
  - Migrated `ServiceManager` from local state tracking to centralized `StateStore`
  - Added comprehensive metrics tracking for state changes and subscription performance
  - Implemented state change event system with old/new state tracking
  - Added support for filtering services by type and state
  - Maintained full backwards compatibility while eliminating state duplication
- **Phase 3 of Issue #45: Structured Event System**
  - Implemented comprehensive event hierarchy with semantic event types:
    - `ServiceStateEvent` for service lifecycle changes with old/new state tracking
    - `HealthEvent` for cluster health status updates
    - `DependencyEvent` for cascade start/stop operations
    - `UserActionEvent` for user-initiated actions
    - `SystemEvent` for system-level operations
  - Added `EventBus` interface with publish/subscribe functionality
  - Implemented flexible event filtering system with composable filters:
    - Filter by event type, source, severity, correlation ID
    - Combine filters with AND/OR logic for complex subscriptions
  - Created `EventBusAdapter` for backwards compatibility with existing `ServiceReporter` interface
  - Added comprehensive event metrics tracking (published, delivered, dropped events)
  - Implemented both handler-based and channel-based event subscriptions
  - Added event severity levels (trace, debug, info, warn, error, fatal) for better categorization
  - Enhanced correlation tracking with event metadata support
  - Provided thread-safe concurrent event publishing and subscription management
  - Added extensive test coverage for all event types and bus functionality
- **Phase 4 of Issue #45: Testing and Polish**
  - Added comprehensive integration tests covering end-to-end event flows
  - Implemented performance monitoring utilities with `PerformanceMonitor` and metrics tracking
  - Created event batching system with `EventBatchProcessor` for high-volume scenarios
  - Built `OptimizedEventBus` with configurable performance optimizations
  - Added object pooling system with `EventPoolManager` to reduce GC pressure
  - Implemented extensive error recovery testing including panic handling
  - Added memory usage monitoring and subscription cleanup verification
  - Created comprehensive documentation covering architecture, usage, and best practices
  - Fixed race conditions in event bus concurrent access patterns
  - Enhanced thread safety across all components with proper synchronization
  - Provided migration guides and troubleshooting documentation
  - Achieved high test coverage with robust integration and unit tests
- **Improved Dependency Management for Service Restarts**
  - When restarting a service, its dependencies are now automatically restarted if they're not active
  - This ensures services always have their requirements satisfied (e.g., restarting Grafana MCP will also restart its port forward if needed)
  - Dependencies are restarted regardless of their stop reason to guarantee service requirements
  - Clear manual stop reason when restarting a service to allow proper dependency management
- **Implemented Issue #46: Improved State Management Between TUI and Orchestrator**
  - **Phase 1: Unified State Management**
    - Added helper methods to TUI Model to use StateStore as single source of truth
    - Implemented state reconciliation on TUI startup to ensure consistency
    - Updated TUI controller to use StateStore instead of directly updating model maps
    - Eliminated state duplication between TUI Model and StateStore
  - **Phase 2: Message Sequencing**
    - Added sequence numbers to `ManagedServiceUpdate` for proper message ordering
    - Implemented `MessageBuffer` for handling out-of-order messages
    - Added global sequence counter with atomic operations for thread safety
  - **Phase 3: Enhanced Correlation Tracking**
    - Added `CascadeInfo` type for tracking cascade relationships between services
    - Added `StateTransition` type for tracking state changes with full context
    - Enhanced StateStore to record state transitions and cascade operations automatically
    - Updated orchestrator to record cascade operations for better observability
  - **Phase 4: Improved Error Handling**
    - Added retry logic for critical updates that are dropped due to buffer overflow
    - Implemented `BackpressureNotificationMsg` for user notifications about dropped messages
    - Added configurable retry attempts with exponential backoff
    - Enhanced TUIReporter with retry queue processing and user feedback
- **Comprehensive Documentation Suite**
  - Added [Architecture Overview](docs/architecture.md) documenting system design, components, and principles
  - Created [Quick Start Guide](docs/quickstart.md) for new users to get up and running quickly
  - Added [Troubleshooting Guide](docs/troubleshooting.md) with common issues and solutions
  - Enhanced development documentation with recent architectural improvements
  - Documented dependency management, state management, and message flow in detail

### Changed
- **Service Manager Refactoring**
  - ServiceManager now accepts an optional KubeManager parameter for K8s connection services
  - Added support for K8s connection services in the service lifecycle management
  - Improved service stop handling to report "Stopping" state before closing channels
- **Orchestrator Improvements**
  - Removed old health monitoring methods in favor of K8s connection services
  - Updated dependency graph to use service labels for K8s connections (e.g., "k8s-mc-mymc" instead of "k8s:context-name")
  - Improved service restart logic to properly handle dependencies
- Dependency graph now includes K8sConnection nodes as fundamental dependencies
- Service manager's StopServiceWithDependents method handles cascading stops
- Health check failures trigger automatic cleanup of dependent services
- Non-TUI mode now uses the orchestrator for health monitoring and dependency management
- TUI mode no longer performs its own health checks - the orchestrator handles all health monitoring and the TUI only displays results
- Proper separation of concerns: orchestrator manages health checks and service lifecycle, TUI only displays status
- Orchestrator now performs initial health check before starting services
- Refactored TUI message handling system
  - Introduced specialized controller/dispatcher for better separation of concerns
  - Controllers now focus on single responsibilities
  - Better error handling and logging throughout the message flow
- Improved startup behavior - the UI now shows loading state until all clusters are fully loaded
- Port forwards no longer start before K8s health checks pass - orchestrator now checks K8s health before starting dependent services
- `ManagedServiceUpdate` now includes `CorrelationID`, `CausedBy`, and `ParentID` fields for tracing
- `TUIReporter` now uses configurable buffered channels instead of simple channels
- Service state updates now include correlation information in logs
- Orchestrator operations (stop/restart) now generate and track correlation IDs
- Removed unused `DependsOnServices` field from `MCPServerDefinition` - MCP servers never depend on other MCP servers
- Enhanced `RestartService` to use the new `startServiceWithDependencies` method for dependency-aware restarts
- Updated `handleServiceStateUpdate` to properly restart services with their dependencies
- **Improved Service Monitoring**
  - Fixed `monitorAndStartServices` to respect `StopReasonDependency` - services stopped due to dependency failure won't be restarted until their dependencies are restored
  - Added automatic restart of dependent services when a dependency becomes healthy again
  - Added 1-second delay before restarting services to ensure ports are properly released

### Fixed
- **Port Forwarding State Issue**
  - Fixed issue where port forwarding services would get stuck in "Stopping" state
  - ServiceManager now properly reports the "Stopping" state before closing the stop channel
  - Port forwarding processes correctly transition to "Stopped" state
- **Code Cleanup**
  - Removed commented-out `mcpServerProcess` struct that was marked for deletion
  - Removed duplicate `updatePortForwardFromSnapshot` and `updateMcpServerFromSnapshot` methods
  - Cleaned up unused code and improved code organization
- **Dependency-Related Fixes**
  - Fixed issue where MCP servers would restart even when their port forward dependencies were stopped
  - Services with `StopReasonDependency` now properly wait for their dependencies to be restored
  - When a service becomes healthy, its dependent services that were stopped due to dependency failure are automatically restarted
  - Fixed "address already in use" errors by adding proper restart delay
- **Fixed spurious error logs when stopping MCP servers**
  - Suppressed expected "file already closed" errors that occurred when stopping MCP server processes
  - Added proper error handling for both stdout and stderr pipe closures during shutdown
  - These were harmless errors but created unnecessary noise in the logs
- **Fixed cascade stops not triggering when K8s connections fail**
  - When a K8s connection transitions to Failed state (e.g., due to network issues), all dependent services (port forwards and MCP servers) are now properly stopped
  - This prevents orphaned services from continuing to run when their underlying K8s connection is no longer healthy
  - Services will automatically restart when the K8s connection recovers

### Documentation
- Added comprehensive documentation about dependency graph implementation
- Enhanced dependency management documentation with detailed examples
- Added explanation of dependency rules and startup/restart behavior
- Documented the relationship between stop reasons and automatic recovery
- Created comprehensive architecture documentation covering all major components
- Added troubleshooting guide with detailed debugging techniques
- Created quick start guide for new users
- Updated development guide with recent architectural improvements
- Documented the entire dependency management system with visual diagrams
- **Updated outdated documentation sections**
  - Removed obsolete "Package Design for Shared Core Logic" section from development.md
  - Updated development.md to reference the unified service architecture
  - Fixed test examples in development.md to match current implementation
  - Updated README.md prerequisites to remove mcp-proxy requirement
  - Clarified non-TUI mode behavior in README.md
  - Rewritten MCP Integration Notes in README.md to reflect YAML configuration system

### Technical Details
- New helper functions: `NewManagedServiceUpdate()`, `WithCause()`, `WithError()`, `WithServiceData()`
- New types: `BufferStrategy`, `BufferedChannel`, `ChannelMetrics`, `ChannelStats`
- Backwards compatibility maintained for existing interfaces
- All existing tests updated and new comprehensive test suite added

## [Previous]

### Added
- Support for containerized MCP servers (#41)
  - New `container` type for MCP server configuration
  - Docker-based execution with automatic container lifecycle management
  - Container-specific configuration fields: image, ports, volumes, environment
  - Automatic port detection from container logs
  - Health check support for containers
  - Example Dockerfiles for kubernetes, prometheus, and grafana MCP servers
  - GitHub Actions workflow for building and publishing container images

### Changed
- MCP server configuration now supports both `localCommand` and `container` types
- Updated documentation with containerized MCP server guide

### Technical Details
- Added `containerizer` package for container runtime abstraction
- Implemented Docker runtime with support for pull, start, stop, logs operations
- Extended MCP server startup logic to handle containerized servers
- Added container ID tracking in managed server info 

## [0.6.0] - 2025-01-15 