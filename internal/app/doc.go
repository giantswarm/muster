// Package app provides application bootstrap, lifecycle management, and configuration management for muster.
//
// This package implements the central application lifecycle control following the API Service Locator Pattern
// and clean separation of concerns. It handles initialization, configuration loading and management,
// service setup, API adapter registration, and execution mode determination.
//
// # Architecture Overview
//
// The app package serves as the application's bootstrap layer and configuration bridge, with five core components:
//
// 1. **Bootstrap (`bootstrap.go`)**: Application initialization and lifecycle management
// 2. **Configuration (`config.go`)**: Application runtime configuration structure
// 3. **Configuration Adapter (`config_adapter.go`)**: API integration and MCP configuration tools
// 4. **Services (`services.go`)**: Service initialization, registration, and dependency management
// 5. **Modes (`modes.go`)**: Execution mode handlers (CLI and TUI)
//
// # Core Components
//
// ## Bootstrap (bootstrap.go)
//
// The bootstrap component handles the complete application initialization sequence:
//
//   - **Logging Configuration**: Sets up logging based on debug flags and execution mode
//   - **Configuration Loading**: Supports both layered and single-path configuration strategies
//   - **Service Initialization**: Creates and initializes all required services and API adapters
//   - **Mode Selection**: Determines and executes appropriate mode (CLI/TUI) based on configuration
//
// ### Configuration Loading Strategies
//
// **Layered Configuration (Default)**:
//  1. Default configuration (embedded in binary)
//  2. User configuration (~/.config/muster/config.yaml)
//  3. Project configuration (./.muster/config.yaml)
//
// **Single Path Configuration**:
//   - Loads from specified directory only
//   - Useful for testing and deployment scenarios
//
// ## Configuration Management (config.go & config_adapter.go)
//
// The configuration system has two layers:
//
// **Application Configuration (`config.go`)**:
//   - Runtime settings: UI mode, debug flags, safety settings
//   - Configuration loading behavior control
//   - Bootstrap args and preferences
//
// **Configuration Adapter (`config_adapter.go`)**:
//   - Implements `api.ConfigHandler` interface
//   - Provides thread-safe configuration access and updates
//   - Exposes MCP tools for external configuration management
//   - Handles configuration persistence and reloading
//   - Supports both project-local and user-global configuration files
//
// ### MCP Configuration Tools
//
// The ConfigAdapter exposes the following MCP tools for external clients:
//   - `config_get`: Retrieve complete muster configuration
//   - `config_get_aggregator`: Get aggregator-specific settings
//   - `config_get_global_settings`: Get global configuration settings
//   - `config_update_aggregator`: Update aggregator configuration
//   - `config_update_global_settings`: Update global settings
//   - `config_save`: Persist current configuration to disk
//   - `config_reload`: Reload configuration from disk and trigger component reloads
//
// ## Service Management (services.go)
//
// The Services component implements comprehensive service initialization following the API Service Locator Pattern:
//
// ### Initialization Sequence
//
//  1. **Storage Creation**: Shared configuration storage for persistence
//  2. **API Interfaces**: Tool checker and caller for service integration
//  3. **Orchestrator Setup**: Core service lifecycle manager initialization
//  4. **Service Registry**: Dependency injection and service discovery setup
//  5. **API Adapter Registration**: Register all service adapters with API layer (CRITICAL)
//  6. **Manager Creation**: ServiceClass, Capability, Workflow, MCPServer managers
//  7. **Definition Loading**: Load component definitions from configuration directories
//  8. **Service Instantiation**: Create concrete service instances from definitions
//  9. **Aggregator Service**: MCP aggregator for external tool access (when enabled)
//
// ### Service Dependencies and Registration
//
// **Critical Ordering**: API adapters MUST be registered before API interfaces are created.
// This ensures handlers are available when APIs attempt to locate them.
//
// **Auto-Start Services**: Services with `AutoStart=true` are automatically registered
// with the orchestrator and started during application launch.
//
// **Service Types Managed**:
//   - MCP Server services (external MCP protocol servers)
//   - Aggregator service (tool aggregation and MCP endpoint)
//   - Workflow execution services
//   - Capability management services
//   - ServiceClass-based services
//
// ## Execution Modes (modes.go)
//
// ### CLI Mode (Non-Interactive)
// Activated with `NoTUI=true`:
//   - **Purpose**: Automation, scripting, headless environments, containers
//   - **Logging**: Text-based output to stdout/stderr
//   - **Lifecycle**: Start services → Wait for signals → Graceful shutdown
//   - **Signals**: SIGINT (Ctrl+C), SIGTERM handled for graceful shutdown
//   - **Use Cases**: systemd services, Docker containers, CI/CD pipelines
//
// ### TUI Mode (Interactive) - Currently Disabled
// Activated with `NoTUI=false`:
//   - **Status**: Implementation temporarily removed from codebase
//   - **Behavior**: Currently logs notification and returns immediately
//   - **Planned Features**: Real-time monitoring, interactive controls, live logs
//
// # Configuration Persistence Strategy
//
// The ConfigAdapter implements intelligent configuration file management:
//
// **Path Resolution Priority**:
//  1. Custom path (if specified via `ConfigPath`)
//  2. Project configuration (`./.muster/config.yaml`)
//  3. User configuration (`~/.config/muster/config.yaml`)
//
// **Automatic Directory Creation**: Creates necessary directories when saving configuration
//
// **Thread Safety**: All configuration access is protected with read-write mutex
//
// # Usage Patterns
//
// ## Standard Application Startup
//
//	cfg := app.NewConfig(false, true, false, "")  // TUI mode, debug enabled
//	application, err := app.NewApplication(cfg)
//	if err != nil {
//	    return fmt.Errorf("bootstrap failed: %w", err)
//	}
//	return application.Run(ctx)
//
// ## CLI Mode with Custom Configuration
//
//	cfg := app.NewConfig(true, false, false, "/opt/muster/config")
//	application, err := app.NewApplication(cfg)
//	if err != nil {
//	    return fmt.Errorf("bootstrap failed: %w", err)
//	}
//	return application.Run(ctx)
//
// ## Debug Mode for Development
//
//	cfg := app.NewConfig(false, true, true, "")  // TUI, debug, yolo mode
//	application, err := app.NewApplication(cfg)
//	if err != nil {
//	    return fmt.Errorf("bootstrap failed: %w", err)
//	}
//	return application.Run(ctx)
//
// # API Service Locator Pattern Compliance
//
// The app package strictly follows the architectural principle that all inter-package
// communication goes through the central API layer:
//
// **Adapter Registration**: Each service creates an API adapter that implements the
// corresponding handler interface and registers itself with the API layer.
//
// **Service Discovery**: Components retrieve other services through `api.GetXXX()` methods
// rather than direct package imports.
//
// **Dependency Flow**: app → api ← other packages (one-way dependency)
//
// # Error Handling and Resilience
//
// **Fail-Fast Approach**: Critical components (storage, orchestrator, API registration)
// cause immediate initialization failure if they cannot be set up properly.
//
// **Graceful Degradation**: Optional components (ServiceClass definitions, Capability
// definitions, Workflow definitions) log warnings but don't halt initialization.
//
// **Configuration Recovery**: Config reload functionality allows recovery from
// configuration errors without application restart.
//
// **Signal Handling**: Proper SIGINT/SIGTERM handling ensures graceful shutdown
// with service cleanup.
//
// # Testing Strategy
//
// The package includes comprehensive test coverage with patterns for:
//
// **Unit Testing**: Configuration validation, mode selection, service structure validation
//
// **Integration Testing**: Service initialization sequences, API adapter registration
//
// **Mock-Friendly Design**: Pre-populated configurations in tests to avoid global dependencies
//
// **Test Coverage Focus**: Configuration handling, service creation, mode selection logic
//
// # Dependencies and Integration
//
// **Core Dependencies**:
//   - `internal/config`: Environment configuration and entity persistence
//   - `internal/orchestrator`: Service lifecycle management
//   - `internal/api`: Central API layer and service locator
//   - `internal/aggregator`: MCP tool aggregation service
//   - `internal/services`: Service abstractions and registry
//   - `pkg/logging`: Logging system initialization
//
// **Service Components**:
//   - `internal/serviceclass`: ServiceClass definition management
//   - `internal/capability`: Capability definition management
//   - `internal/workflow`: Workflow definition and execution
//   - `internal/mcpserver`: MCP server process management
//
// **API Integration**: All service interactions use the API layer interfaces,
// maintaining clean architectural boundaries and enabling independent testing.
//
// # Benefits of This Architecture
//
// **Separation of Concerns**: Clear boundaries between bootstrap, configuration, services, and execution
//
// **Testability**: Each component can be tested independently with mocked dependencies
//
// **Flexibility**: Easy to add new services, modify behavior, or change execution modes
//
// **Configuration Management**: Comprehensive configuration handling with persistence and MCP integration
//
// **Service Isolation**: Services are loosely coupled through the API layer
//
// **Operational Readiness**: Proper signal handling, logging, and error management for production use
package app
