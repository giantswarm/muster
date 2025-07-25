package app

import (
	"fmt"

	"muster/internal/aggregator"
	"muster/internal/api"
	"muster/internal/client"
	"muster/internal/config"
	"muster/internal/events"
	mcpserverPkg "muster/internal/mcpserver"
	"muster/internal/orchestrator"
	"muster/internal/serviceclass"
	"muster/internal/services"
	aggregatorService "muster/internal/services/aggregator"
	"muster/internal/workflow"
	"muster/pkg/logging"
)

// Services holds all initialized services and APIs used by the application.
// This struct serves as the central registry for all core application components,
// providing access to service management, API interfaces, and runtime configuration.
//
// The Services struct follows the dependency injection pattern, ensuring that
// all components are properly initialized and registered before being made
// available to the application runtime.
//
// Field descriptions:
//   - Orchestrator: Core service orchestrator responsible for service lifecycle management
//   - OrchestratorAPI: API interface for orchestrator operations (start, stop, status)
//   - AggregatorPort: Port number where the MCP aggregator service is listening
//
// Service Dependencies:
// The services are initialized in a specific order to handle dependencies:
//  1. Storage and tool interfaces (shared dependencies)
//  2. Service adapters and API registrations
//  3. Manager instances (ServiceClass, Workflow, MCPServer)
//  4. Concrete service instances
//  5. Aggregator service (when enabled)
type Services struct {
	// Orchestrator manages the lifecycle of all registered services.
	// It handles service startup, shutdown, dependency resolution, and health monitoring.
	Orchestrator *orchestrator.Orchestrator

	// OrchestratorAPI provides programmatic access to orchestrator operations.
	// This interface allows other components to interact with service management
	// functionality through the central API layer.
	OrchestratorAPI api.OrchestratorAPI

	// ConfigAPI provides programmatic access to configuration management.
	// This interface enables runtime configuration updates, persistence,
	// and retrieval through the central API layer.
	ConfigAPI api.ConfigAPI

	// AggregatorPort specifies the port number for the MCP aggregator service.
	// This port is used by external MCP clients to connect to the aggregator
	// for tool discovery and execution.
	AggregatorPort int
}

// InitializeServices creates and registers all required services for the application.
// This function implements the complete service initialization sequence, following
// the API Service Locator Pattern for clean separation of concerns.
//
// Initialization Sequence:
//  1. **Storage Setup**: Creates shared configuration storage for persistence
//  2. **API Interfaces**: Creates tool checker and caller for service integration
//  3. **Orchestrator**: Initializes the core service orchestrator
//  4. **Service Registry**: Sets up the service registry for dependency injection
//  5. **API Adapters**: Creates and registers all service adapters with the API layer
//  6. **Manager Initialization**: Creates managers for ServiceClass, Workflow, MCPServer
//  7. **Definition Loading**: Loads component definitions from configuration directories
//  8. **Service Creation**: Creates concrete service instances based on definitions
//  9. **Aggregator Setup**: Initializes MCP aggregator service (when enabled)
//
// Configuration Handling:
//   - If cfg.ConfigPath is set: uses single-directory configuration loading
//   - If cfg.ConfigPath is empty: uses layered configuration approach
//
// Service Auto-registration:
// Services marked with AutoStart=true in their definitions are automatically
// registered with the orchestrator and will be started when the orchestrator starts.
//
// Error Handling:
// The function uses a fail-fast approach for critical components but continues
// initialization for optional components (logging warnings for failures).
// Critical failures include storage creation, orchestrator setup, and API registration.
//
// Aggregator Service:
// The MCP aggregator is enabled by default unless explicitly disabled in configuration.
// It provides MCP protocol support for external tool integration and discovery.
//
// Returns a fully initialized Services struct or an error if critical initialization fails.
func InitializeServices(cfg *Config) (*Services, error) {
	// Create API-based tool checker and caller
	toolChecker := api.NewToolChecker()
	toolCaller := api.NewToolCaller()

	// Create orchestrator without ToolCaller initially
	orchConfig := orchestrator.Config{
		Aggregator: cfg.MusterConfig.Aggregator,
		Yolo:       cfg.Yolo,
		ToolCaller: toolCaller,
	}

	orch := orchestrator.New(orchConfig)

	// Get the service registry
	registry := orch.GetServiceRegistry()

	// Step 1: Create unified muster client once
	// This avoids redundant Kubernetes connection attempts and CRD validation
	musterClient, err := createMusterClient(cfg.ConfigPath, cfg.Debug)
	if err != nil {
		return nil, fmt.Errorf("failed to create muster client: %w", err)
	}

	// Step 2: Create and register adapters using the muster client
	// This is critical - APIs need handlers to be registered first

	// Register service registry adapter
	registryAdapter := services.NewRegistryAdapter(registry)
	registryAdapter.Register()

	// Register orchestrator adapter
	orchAdapter := orchestrator.NewAPIAdapter(orch)
	orchAdapter.Register()

	// Register configuration adapter
	configAdapter := NewConfigAdapter(cfg.MusterConfig, "") // Empty path means auto-detect
	configAdapter.Register()

	// Register event manager adapter using the unified client
	eventAdapter := events.NewAdapter(musterClient)
	eventAdapter.Register()

	// Initialize and register ServiceClass adapter using the muster client
	serviceClassAdapter := serviceclass.NewAdapterWithClient(musterClient, "default")
	serviceClassAdapter.Register()

	// Load ServiceClass definitions to ensure they're available
	if cfg.ConfigPath == "" {
		panic("Logic error: empty ConfigPath")
	}
	// Trigger ServiceClass loading by calling ListServiceClasses
	// This ensures filesystem-based ServiceClasses are loaded into memory
	serviceClasses := serviceClassAdapter.ListServiceClasses()
	if len(serviceClasses) > 0 {
		logging.Info("Services", "Loaded %d ServiceClass definitions from filesystem", len(serviceClasses))
	}

	// Create and register Workflow adapter using the muster client
	workflowAdapter := workflow.NewAdapterWithClient(musterClient, "default", toolCaller, toolChecker, cfg.ConfigPath)
	workflowAdapter.Register()

	// Initialize and register MCPServer adapter using the muster client
	mcpServerAdapter := mcpserverPkg.NewAdapterWithClient(musterClient, "default")
	mcpServerAdapter.Register()

	// The new adapter uses the unified client instead of the manager
	// MCPServer operations now work through CRDs (Kubernetes) or filesystem fallback
	// Note: Definition loading is now handled by the unified client automatically

	// Step 3: Create APIs that use the registered handlers
	orchestratorAPI := api.NewOrchestratorAPI()
	configAPI := api.NewConfigServiceAPI()

	// Step 4: Create and register actual services
	// Note: Service creation (including MCPServer services) is handled by the orchestrator
	// during its Start() method. The orchestrator manages dependencies and lifecycle.

	// Need to get the service registry handler from the registry adapter
	registryHandler := api.GetServiceRegistry()
	if registryHandler != nil {
		if cfg.ConfigPath == "" {
			panic("Logic error: empty ConfigPath")
		}

		// Convert config types
		aggConfig := aggregator.AggregatorConfig{
			Port:         cfg.MusterConfig.Aggregator.Port,
			Host:         cfg.MusterConfig.Aggregator.Host,
			Transport:    cfg.MusterConfig.Aggregator.Transport,
			MusterPrefix: cfg.MusterConfig.Aggregator.MusterPrefix,
			Yolo:         cfg.Yolo,
			ConfigDir:    cfg.ConfigPath,
		}

		// Set defaults if not specified
		if aggConfig.Port == 0 {
			aggConfig.Port = 8090
		}
		if aggConfig.Host == "" {
			aggConfig.Host = "localhost"
		}
		if aggConfig.Transport == "" {
			aggConfig.Transport = config.MCPTransportStreamableHTTP
		}

		aggService := aggregatorService.NewAggregatorService(
			aggConfig,
			orchestratorAPI,
			registryHandler,
		)
		registry.Register(aggService)

		// Create aggregator API adapter
		aggAdapter := aggregatorService.NewAPIAdapter(aggService)
		aggAdapter.Register()
	}

	return &Services{
		Orchestrator:    orch,
		OrchestratorAPI: orchestratorAPI,
		ConfigAPI:       configAPI,
		AggregatorPort:  cfg.MusterConfig.Aggregator.Port,
	}, nil
}

// createMusterClient creates a single unified client that all adapters can use
// This avoids redundant Kubernetes connection attempts and CRD validation
func createMusterClient(configPath string, debug bool) (client.MusterClient, error) {
	if configPath == "" {
		// No config path specified, use default client creation
		return client.NewMusterClient()
	}

	// Create client confiForceFilesystemModeg with the filesystem path
	clientConfig := &client.MusterClientConfig{
		FilesystemPath:      configPath,
		Namespace:           "default",
		ForceFilesystemMode: false, // Let the client choose the best mode
		Debug:               debug,
	}

	// Create client with config
	musterClient, err := client.NewMusterClientWithConfig(clientConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create muster client with config path %s: %w", configPath, err)
	}

	return musterClient, nil
}

// Note: Removed the individual adapter creation functions as they're now replaced by the unified muster client approach

// Note: MCPServer service creation moved to orchestrator for proper dependency management
