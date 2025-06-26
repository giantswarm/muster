package app

import (
	"fmt"
	"muster/internal/aggregator"
	"muster/internal/api"
	"muster/internal/capability"
	"muster/internal/config"
	mcpserverPkg "muster/internal/mcpserver"
	"muster/internal/orchestrator"
	"muster/internal/serviceclass"
	"muster/internal/services"
	aggregatorService "muster/internal/services/aggregator"
	"muster/internal/services/mcpserver"
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
//   - ConfigAPI: API interface for configuration management and persistence
//   - AggregatorPort: Port number where the MCP aggregator service is listening
//
// Service Dependencies:
// The services are initialized in a specific order to handle dependencies:
//  1. Storage and tool interfaces (shared dependencies)
//  2. Service adapters and API registrations
//  3. Manager instances (ServiceClass, Capability, Workflow, MCPServer)
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
//  6. **Manager Initialization**: Creates managers for ServiceClass, Capability, Workflow, MCPServer
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
	// Create storage for shared use across services including orchestrator persistence
	var storage *config.Storage
	if cfg.ConfigPath != "" {
		storage = config.NewStorageWithPath(cfg.ConfigPath)
	} else {
		storage = config.NewStorage()
	}

	// Create API-based tool checker and caller
	toolChecker := api.NewToolChecker()
	toolCaller := api.NewToolCaller()

	// Create orchestrator without ToolCaller initially
	orchConfig := orchestrator.Config{
		Aggregator: cfg.MusterConfig.Aggregator,
		Yolo:       cfg.Yolo,
		ToolCaller: toolCaller,
		Storage:    storage,
	}

	orch := orchestrator.New(orchConfig)

	// Get the service registry
	registry := orch.GetServiceRegistry()

	// Step 1: Create and register adapters BEFORE creating APIs
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

	// Initialize and register ServiceClass manager
	// This needs to be done before orchestrator starts to handle ServiceClass-based services

	// Get the service registry handler from the API
	registryHandler := api.GetServiceRegistry()
	if registryHandler == nil {
		return nil, fmt.Errorf("service registry handler not available")
	}

	// Use the shared storage created earlier
	serviceClassManager, err := serviceclass.NewServiceClassManager(toolChecker, storage)
	if err != nil {
		return nil, fmt.Errorf("failed to create ServiceClass manager: %w", err)
	}

	// Create and register ServiceClass adapter
	serviceClassAdapter := serviceclass.NewAdapter(serviceClassManager)
	serviceClassAdapter.Register()

	// Load ServiceClass definitions
	if cfg.ConfigPath != "" {
		serviceClassManager.SetConfigPath(cfg.ConfigPath)
	}
	if err := serviceClassManager.LoadDefinitions(); err != nil {
		// Log warning but don't fail - ServiceClass is optional
		logging.Warn("Services", "Failed to load ServiceClass definitions: %v", err)
	}

	// Initialize and register Capability adapter
	capabilityAdapter, err := capability.NewAdapter(toolChecker, toolCaller, storage)
	if err != nil {
		return nil, fmt.Errorf("failed to create Capability adapter: %w", err)
	}
	capabilityAdapter.Register()

	// Load Capability definitions
	if cfg.ConfigPath != "" {
		capabilityAdapter.SetConfigPath(cfg.ConfigPath)
	}
	if err := capabilityAdapter.LoadDefinitions(); err != nil {
		// Log warning but don't fail - Capability is optional
		logging.Warn("Services", "Failed to load Capability definitions: %v", err)
	}

	// Create and register Workflow adapter
	workflowManager, err := workflow.NewWorkflowManager(storage, nil, toolChecker)
	if err != nil {
		return nil, fmt.Errorf("failed to create Workflow manager: %w", err)
	}

	workflowAdapter := workflow.NewAdapter(workflowManager, toolCaller)
	workflowAdapter.Register()

	// Load Workflow definitions
	if cfg.ConfigPath != "" {
		workflowManager.SetConfigPath(cfg.ConfigPath)
	}
	if err := workflowManager.LoadDefinitions(); err != nil {
		// Log warning but don't fail - Workflow is optional
		logging.Warn("Services", "Failed to load Workflow definitions: %v", err)
	}

	// Initialize and register MCPServer manager (new unified configuration approach)
	mcpServerManager, err := mcpserverPkg.NewMCPServerManager(storage)
	if err != nil {
		return nil, fmt.Errorf("failed to create MCP server manager: %w", err)
	}

	// Create and register MCPServer adapter
	mcpServerAdapter := mcpserverPkg.NewAdapter(mcpServerManager)
	mcpServerAdapter.Register()

	// Load MCP server definitions
	if cfg.ConfigPath != "" {
		mcpServerManager.SetConfigPath(cfg.ConfigPath)
	}
	if err := mcpServerManager.LoadDefinitions(); err != nil {
		// Log warning but don't fail - MCP servers are optional
		logging.Warn("Services", "Failed to load MCP server definitions: %v", err)
	}

	// Step 2: Create APIs that use the registered handlers
	orchestratorAPI := api.NewOrchestratorAPI()
	configAPI := api.NewConfigServiceAPI()

	// Step 3: Create and register actual services
	// Create MCP server services
	mcpServerDefinitions := mcpServerManager.ListDefinitions()
	for _, mcpDef := range mcpServerDefinitions {
		if mcpDef.AutoStart {
			mcpService, err := mcpserver.NewService(&mcpDef, mcpServerManager)
			if err != nil {
				logging.Warn("Services", "Failed to create MCP server service %s: %v", mcpDef.Name, err)
				continue
			}
			if mcpService != nil {
				registry.Register(mcpService)
			}
		}
	}

	// Create aggregator service - enable by default unless explicitly disabled
	// This ensures the aggregator starts even with no MCP servers configured
	aggregatorEnabled := true
	if cfg.MusterConfig.Aggregator.Port != 0 || cfg.MusterConfig.Aggregator.Host != "" {
		// If aggregator config exists, respect the enabled flag
		aggregatorEnabled = cfg.MusterConfig.Aggregator.Enabled
	}

	if aggregatorEnabled {
		// Need to get the service registry handler from the registry adapter
		registryHandler := api.GetServiceRegistry()
		if registryHandler != nil {
			// Auto-detect config directory or use custom path
			var configDir string
			if cfg.ConfigPath != "" {
				configDir = cfg.ConfigPath
			} else {
				userConfigDir, err := config.GetUserConfigDir()
				if err != nil {
					// Fallback to empty string if auto-detection fails
					configDir = ""
				} else {
					configDir = userConfigDir
				}
			}

			// Convert config types
			aggConfig := aggregator.AggregatorConfig{
				Port:         cfg.MusterConfig.Aggregator.Port,
				Host:         cfg.MusterConfig.Aggregator.Host,
				Transport:    cfg.MusterConfig.Aggregator.Transport,
				MusterPrefix: cfg.MusterConfig.Aggregator.MusterPrefix,
				Yolo:         cfg.Yolo,
				ConfigDir:    configDir,
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
	}

	return &Services{
		Orchestrator:    orch,
		OrchestratorAPI: orchestratorAPI,
		ConfigAPI:       configAPI,
		AggregatorPort:  cfg.MusterConfig.Aggregator.Port,
	}, nil
}
