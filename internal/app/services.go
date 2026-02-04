package app

import (
	"fmt"

	"muster/internal/aggregator"
	"muster/internal/api"
	"muster/internal/client"
	"muster/internal/config"
	"muster/internal/events"
	mcpserverPkg "muster/internal/mcpserver"
	"muster/internal/metatools"
	"muster/internal/orchestrator"
	"muster/internal/reconciler"
	"muster/internal/serviceclass"
	"muster/internal/services"
	aggregatorService "muster/internal/services/aggregator"
	"muster/internal/teleport"
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
//   - ReconcileManager: Reconciliation manager for automatic change detection
//
// Service Dependencies:
// The services are initialized in a specific order to handle dependencies:
//  1. Storage and tool interfaces (shared dependencies)
//  2. Service adapters and API registrations
//  3. Manager instances (ServiceClass, Workflow, MCPServer)
//  4. Concrete service instances
//  5. Aggregator service (when enabled)
//  6. Reconciliation manager (for automatic change detection)
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

	// ReconcileManager handles automatic detection and reconciliation of
	// configuration changes for MCPServers, ServiceClasses, and Workflows.
	// This enables automatic synchronization between desired state (YAML/CRDs)
	// and actual state (running services).
	ReconcileManager *reconciler.Manager

	// StateChangeBridge bridges service state changes from the orchestrator to
	// the reconciliation system. This enables status sync when services change
	// state at runtime (e.g., crash, health check failure, restart).
	StateChangeBridge *reconciler.StateChangeBridge
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
	// Validate required configuration
	if cfg.ConfigPath == "" {
		return nil, fmt.Errorf("ConfigPath is required for service initialization")
	}

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
	musterClient, err := createMusterClientWithConfig(cfg.ConfigPath, cfg.Debug, *cfg.MusterConfig)
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

	// Get namespace from config, defaulting to "default" if not specified
	namespace := cfg.MusterConfig.Namespace
	if namespace == "" {
		namespace = "default"
	}

	// Register event manager adapter using the unified client
	eventAdapter := events.NewAdapter(musterClient)
	eventAdapter.Register()

	// Register Teleport client adapter for private installation access
	// The adapter uses the muster client for Kubernetes secret access when in K8s mode
	var teleportAdapter *teleport.Adapter
	if musterClient.IsKubernetesMode() {
		teleportAdapter = teleport.NewAdapterWithClient(musterClient)
	} else {
		teleportAdapter = teleport.NewAdapter()
	}
	teleportAdapter.Register()

	// Initialize and register ServiceClass adapter using the muster client
	serviceClassAdapter := serviceclass.NewAdapterWithClient(musterClient, namespace)
	serviceClassAdapter.Register()

	// Trigger ServiceClass loading by calling ListServiceClasses
	// This ensures filesystem-based ServiceClasses are loaded into memory
	serviceClasses := serviceClassAdapter.ListServiceClasses()
	if len(serviceClasses) > 0 {
		logging.Info("Services", "Loaded %d ServiceClass definitions from filesystem", len(serviceClasses))
	}

	// Create and register Workflow adapter using the muster client
	workflowAdapter := workflow.NewAdapterWithClient(musterClient, namespace, toolCaller, toolChecker, cfg.ConfigPath)
	workflowAdapter.Register()

	// Initialize and register MCPServer adapter using the muster client
	mcpServerAdapter := mcpserverPkg.NewAdapterWithClient(musterClient, namespace)
	mcpServerAdapter.Register()

	// Initialize and register credentials adapter for loading OAuth client credentials from secrets
	credentialsAdapter := mcpserverPkg.NewCredentialsAdapter(musterClient)
	credentialsAdapter.Register()

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
		// Merge OAuth MCP client/proxy config: serve command flags override config file, but use config file as fallback
		oauthMCPClientEnabled := cfg.OAuthMCPClientEnabled || cfg.MusterConfig.Aggregator.OAuth.MCPClient.Enabled
		oauthPublicURL := cfg.OAuthMCPClientPublicURL
		if oauthPublicURL == "" {
			oauthPublicURL = cfg.MusterConfig.Aggregator.OAuth.MCPClient.PublicURL
		}

		// Build a merged OAuthMCPClientConfig for GetEffectiveClientID()
		mergedOAuthMCPClientConfig := config.OAuthMCPClientConfig{
			Enabled:      oauthMCPClientEnabled,
			PublicURL:    oauthPublicURL,
			ClientID:     cfg.OAuthMCPClientID, // serve command flag value (empty if not specified)
			CallbackPath: cfg.MusterConfig.Aggregator.OAuth.MCPClient.CallbackPath,
			CIMD:         cfg.MusterConfig.Aggregator.OAuth.MCPClient.CIMD,
			CAFile:       cfg.MusterConfig.Aggregator.OAuth.MCPClient.CAFile,
		}
		// If serve command flag didn't set ClientID, check config file
		if mergedOAuthMCPClientConfig.ClientID == "" {
			mergedOAuthMCPClientConfig.ClientID = cfg.MusterConfig.Aggregator.OAuth.MCPClient.ClientID
		}
		// Use GetEffectiveClientID() to auto-derive from PublicURL if still empty
		effectiveClientID := mergedOAuthMCPClientConfig.GetEffectiveClientID()

		// Convert config types
		aggConfig := aggregator.AggregatorConfig{
			Port:         cfg.MusterConfig.Aggregator.Port,
			Host:         cfg.MusterConfig.Aggregator.Host,
			Transport:    cfg.MusterConfig.Aggregator.Transport,
			MusterPrefix: cfg.MusterConfig.Aggregator.MusterPrefix,
			Version:      cfg.Version,
			Yolo:         cfg.Yolo,
			ConfigDir:    cfg.ConfigPath,
			Debug:        cfg.Debug,
			OAuth: aggregator.OAuthProxyConfig{
				Enabled:      oauthMCPClientEnabled,
				PublicURL:    oauthPublicURL,
				ClientID:     effectiveClientID,
				CallbackPath: mergedOAuthMCPClientConfig.CallbackPath,
				CAFile:       mergedOAuthMCPClientConfig.CAFile,
			},
			OAuthServer: aggregator.OAuthServerConfig{
				// serve command flag overrides config file if enabled
				Enabled: cfg.OAuthServerEnabled || cfg.MusterConfig.Aggregator.OAuth.Server.Enabled,
				Config:  mergeOAuthServerConfig(cfg),
			},
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

		// Step 4b: Initialize meta-tools for server-side tool management (Issue #343)
		// The metatools adapter provides the MetaToolsHandler interface that the
		// metatools package uses. The aggregator is registered as the data provider
		// which gives the adapter access to list/call tools, resources, and prompts.
		metaToolsAdapter := metatools.NewAdapter()
		metaToolsAdapter.Register()
		logging.Info("Services", "Registered meta-tools adapter")
	}

	// Step 5: Initialize reconciliation manager for automatic change detection
	var reconcileManager *reconciler.Manager
	if cfg.ConfigPath != "" {
		// Determine watch mode based on config - this must match the MusterClient mode
		// to ensure consistent behavior between the client and the reconciler.
		// Use the shared helper to ensure consistent mode selection across the codebase.
		watchMode := reconciler.WatchModeFromKubernetesFlag(cfg.MusterConfig.Kubernetes)

		reconcileConfig := reconciler.ManagerConfig{
			Mode:           watchMode,
			FilesystemPath: cfg.ConfigPath,
			Namespace:      namespace,
			WorkerCount:    2,
			MaxRetries:     5,
			Debug:          cfg.Debug,
		}

		reconcileManager = reconciler.NewManager(reconcileConfig)

		// Get handlers for reconciler dependencies
		mcpServerMgr := api.GetMCPServerManager()
		if mcpServerMgr != nil {
			// Create and register MCPServer reconciler with status updater for CRD status sync
			// See ADR 007 for details on what status fields are synced
			mcpReconciler := reconciler.NewMCPServerReconciler(
				orchestratorAPI,
				mcpServerMgr,
				registryHandler,
			).WithStatusUpdater(musterClient, namespace)
			if err := reconcileManager.RegisterReconciler(mcpReconciler); err != nil {
				logging.Warn("Services", "Failed to register MCPServer reconciler: %v", err)
			}
		}

		// Get ServiceClass manager and register ServiceClass reconciler with status updater
		serviceClassMgr := api.GetServiceClassManager()
		if serviceClassMgr != nil {
			serviceClassReconciler := reconciler.NewServiceClassReconciler(serviceClassMgr).
				WithStatusUpdater(musterClient, namespace)
			if err := reconcileManager.RegisterReconciler(serviceClassReconciler); err != nil {
				logging.Warn("Services", "Failed to register ServiceClass reconciler: %v", err)
			}
		}

		// Get Workflow manager and register Workflow reconciler with status updater
		workflowMgr := api.GetWorkflow()
		if workflowMgr != nil {
			workflowReconciler := reconciler.NewWorkflowReconciler(workflowMgr).
				WithStatusUpdater(musterClient, namespace)
			if err := reconcileManager.RegisterReconciler(workflowReconciler); err != nil {
				logging.Warn("Services", "Failed to register Workflow reconciler: %v", err)
			}
		}

		// Create and register reconciler API adapter
		reconcileAdapter := reconciler.NewAdapter(reconcileManager)
		reconcileAdapter.Register()

		logging.Info("Services", "Initialized reconciliation manager with filesystem watching for %s", cfg.ConfigPath)
	}

	// Step 6: Create StateChangeBridge to sync runtime state changes to CRD status
	// This bridges service state changes from the orchestrator to the reconciliation system.
	var stateChangeBridge *reconciler.StateChangeBridge
	if reconcileManager != nil {
		stateChangeBridge = reconciler.NewStateChangeBridge(
			orchestratorAPI,
			reconcileManager,
			namespace,
		)
		logging.Info("Services", "Initialized state change bridge for runtime status sync")
	}

	return &Services{
		Orchestrator:      orch,
		OrchestratorAPI:   orchestratorAPI,
		ConfigAPI:         configAPI,
		AggregatorPort:    cfg.MusterConfig.Aggregator.Port,
		ReconcileManager:  reconcileManager,
		StateChangeBridge: stateChangeBridge,
	}, nil
}

// createMusterClientWithConfig creates a muster client with full configuration context.
// This avoids redundant Kubernetes connection attempts and CRD validation.
func createMusterClientWithConfig(configPath string, debug bool, musterConfig config.MusterConfig) (client.MusterClient, error) {
	if configPath == "" {
		// No config path specified, use default client creation
		return client.NewMusterClient()
	}

	// Get namespace from config, defaulting to "default" if not specified
	namespace := musterConfig.Namespace
	if namespace == "" {
		namespace = "default"
	}

	// Create client config with the filesystem path as fallback.
	// When kubernetes=true in config, use Kubernetes CRD mode.
	// Otherwise, force filesystem mode for local development and tests.
	clientConfig := &client.MusterClientConfig{
		FilesystemPath:      configPath,
		Namespace:           namespace,
		ForceFilesystemMode: !musterConfig.Kubernetes,
		Debug:               debug,
	}

	musterClient, err := client.NewMusterClientWithConfig(clientConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create muster client with config path %s: %w", configPath, err)
	}

	return musterClient, nil
}

// mergeOAuthServerConfig merges OAuth server configuration from CLI flags and config file.
// CLI flags override config file settings where specified.
func mergeOAuthServerConfig(cfg *Config) config.OAuthServerConfig {
	serverCfg := cfg.MusterConfig.Aggregator.OAuth.Server

	// Override base URL from CLI if provided
	if cfg.OAuthServerBaseURL != "" {
		serverCfg.BaseURL = cfg.OAuthServerBaseURL
	}

	// Enable from CLI flag if specified
	if cfg.OAuthServerEnabled {
		serverCfg.Enabled = true
	}

	return serverCfg
}

// Note: Removed the individual adapter creation functions as they're now replaced by the unified muster client approach

// Note: MCPServer service creation moved to orchestrator for proper dependency management
