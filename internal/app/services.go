package app

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"

	mcpserverPkg "github.com/giantswarm/muster/internal/mcpserver"

	"github.com/giantswarm/muster/internal/agentgateway/binary"
	"github.com/giantswarm/muster/internal/agentgateway/subprocess"
	"github.com/giantswarm/muster/internal/aggregator"
	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/client"
	"github.com/giantswarm/muster/internal/config"
	"github.com/giantswarm/muster/internal/events"
	"github.com/giantswarm/muster/internal/metatools"
	"github.com/giantswarm/muster/internal/orchestrator"
	"github.com/giantswarm/muster/internal/reconciler"
	k8sapply "github.com/giantswarm/muster/internal/reconciler/agentgateway/k8s"
	yamlapply "github.com/giantswarm/muster/internal/reconciler/agentgateway/yaml"
	"github.com/giantswarm/muster/internal/services"
	"github.com/giantswarm/muster/internal/teleport"
	"github.com/giantswarm/muster/internal/workflow"
	"github.com/giantswarm/muster/pkg/logging"
)

// upstreamProxyEnvVar is the environment variable cluster-mode muster reads
// for the agentgateway base URL. The Helm chart wires it through
// templates/deployment.yaml.
const upstreamProxyEnvVar = "MUSTER_AGW_UPSTREAM_URL"

// agentgatewayReadyURL is the standard agentgateway readiness endpoint the
// subprocess manager polls before Start returns.
const agentgatewayReadyURL = "http://localhost:15021/healthz/ready"

// agentgatewayStartupTimeout caps how long Start waits for the readiness probe
// before failing initialization. 30s is comfortable margin over agw's typical
// sub-second startup on a warm binary, with headroom for cold downloads.
const agentgatewayStartupTimeout = 30 * time.Second

// agentgatewayDrainTimeout bounds graceful shutdown before SIGKILL. agw
// flushes its access log on SIGTERM; 10s is generous.
const agentgatewayDrainTimeout = 10 * time.Second

// defaultGatewayName is the metadata.name of the Gateway resource HTTPRoutes
// emitted by the K8s Applier attach to. agentgateway's reference deployments
// use this name; environments that diverge will need a flag.
const defaultGatewayName = "agentgateway"

// agentgatewayConfigSubdir is the directory under configPath where the YAML
// applier writes one agw native config file per MCPServer.
const agentgatewayConfigSubdir = "agentgateway"

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
//  3. Manager instances (Workflow, MCPServer)
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
	// configuration changes for MCPServers and Workflows.
	// This enables automatic synchronization between desired state (YAML/CRDs)
	// and actual state (running services).
	ReconcileManager *reconciler.Manager

	// StateChangeBridge bridges service state changes from the orchestrator to
	// the reconciliation system. This enables status sync when services change
	// state at runtime (e.g., crash, health check failure, restart).
	StateChangeBridge *reconciler.StateChangeBridge

	// AgentgatewayConfigDir is the absolute path to the directory the yaml
	// Applier writes per-MCPServer agentgateway native configs into. Set in
	// filesystem mode, empty in cluster mode. runOrchestrator uses this to
	// decide whether to spawn the agentgateway subprocess.
	AgentgatewayConfigDir string

	// AgentgatewayManager owns the agentgateway subprocess once runOrchestrator
	// has spawned it. nil before Run, nil in cluster mode, and nil when the
	// subprocess fails to start. Shutdown calls Stop on it after the
	// reconciliation manager has drained.
	AgentgatewayManager *subprocess.Manager

	// Aggregator is the MCP aggregator service muster boots directly (rather
	// than via the orchestrator service registry) so its lifecycle is decoupled
	// from the orchestrator's. nil when registryHandler was unavailable at
	// initialization. runOrchestrator starts it after the StateChangeBridge and
	// stops it before the orchestrator during shutdown.
	AggregatorManager *aggregator.AggregatorManager
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
//  6. **Manager Initialization**: Creates managers for Workflow, MCPServer
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

	orchConfig := orchestrator.Config{
		Aggregator: cfg.MusterConfig.Aggregator,
		Yolo:       cfg.Yolo,
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

	// Register event manager adapter only when events are enabled (alpha feature).
	// Events can be enabled via --enable-events flag or config.yaml `events: true`.
	if cfg.EnableEvents || cfg.MusterConfig.Events {
		eventAdapter := events.NewAdapter(musterClient)
		eventAdapter.Register()
		logging.Info("Services", "Kubernetes event emission enabled (alpha)")
	} else {
		logging.Debug("Services", "Kubernetes event emission disabled (use --enable-events to enable)")
	}

	// Register Teleport client adapter for private installation access
	// The adapter uses the muster client for Kubernetes secret access when in K8s mode
	var teleportAdapter *teleport.Adapter
	if musterClient.IsKubernetesMode() {
		teleportAdapter = teleport.NewAdapterWithClient(musterClient)
	} else {
		teleportAdapter = teleport.NewAdapter()
	}
	teleportAdapter.Register()

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

	// aggManager is constructed inside the registryHandler block and bound to
	// the Services struct so runOrchestrator can start/stop it directly,
	// bypassing the orchestrator service registry.
	var aggManager *aggregator.AggregatorManager

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
			Admin: aggregator.AdminConfig{
				Enabled:     cfg.MusterConfig.Aggregator.Admin.Enabled,
				Port:        cfg.MusterConfig.Aggregator.Admin.Port,
				BindAddress: cfg.MusterConfig.Aggregator.Admin.BindAddress,
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
		if aggConfig.Admin.Enabled {
			if aggConfig.Admin.Port == 0 {
				aggConfig.Admin.Port = 9999
			}
			if aggConfig.Admin.BindAddress == "" {
				aggConfig.Admin.BindAddress = "127.0.0.1"
			}
		}

		if cfg.MusterConfig.Kubernetes {
			aggConfig.UpstreamProxy = os.Getenv(upstreamProxyEnvVar)
		} else {
			port, err := reserveFilesystemModeAgwPort()
			if err != nil {
				return nil, fmt.Errorf("reserve agentgateway port: %w", err)
			}
			aggConfig.AgentgatewayListenerPort = port
			aggConfig.UpstreamProxy = "http://localhost:" + strconv.Itoa(int(port))
		}
		if aggConfig.UpstreamProxy == "" {
			return nil, fmt.Errorf("aggregator: UpstreamProxy required; cluster mode reads %s, filesystem mode picks a free port at startup",
				upstreamProxyEnvVar)
		}

		aggManager = aggregator.NewAggregatorManager(
			aggConfig,
			orchestratorAPI,
			registryHandler,
			aggregatorErrorCallback,
		)

		aggAdapter := aggregator.NewAPIAdapter(aggManager)
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
	var agentgatewayConfigDir string
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
			listenerPort := uint16(0)
			if aggManager != nil {
				listenerPort = aggManager.AgentgatewayListenerPort()
			}
			mcpReconciler, configDir, err := buildMCPServerReconciler(
				cfg.MusterConfig.Kubernetes,
				mcpServerMgr,
				musterClient,
				namespace,
				cfg.ConfigPath,
				listenerPort,
			)
			if err != nil {
				return nil, fmt.Errorf("build MCPServer reconciler: %w", err)
			}
			agentgatewayConfigDir = configDir
			if err := reconcileManager.RegisterReconciler(mcpReconciler); err != nil {
				logging.Warn("Services", "Failed to register MCPServer reconciler: %v", err)
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
		Orchestrator:          orch,
		OrchestratorAPI:       orchestratorAPI,
		ConfigAPI:             configAPI,
		AggregatorPort:        cfg.MusterConfig.Aggregator.Port,
		AggregatorManager:     aggManager,
		ReconcileManager:      reconcileManager,
		StateChangeBridge:     stateChangeBridge,
		AgentgatewayConfigDir: agentgatewayConfigDir,
	}, nil
}

// aggregatorErrorCallback is the sink for fatal async errors surfaced by the
// aggregator server's background goroutines. With the BaseService wrapper
// gone, the operator's only signal is a log line; restart is the user's call.
func aggregatorErrorCallback(err error) {
	logging.Error("Aggregator", err, "Aggregator manager encountered a fatal error")
}

// reserveFilesystemModeAgwPort grabs an unused TCP port for the agentgateway
// subprocess to bind. Filesystem-mode muster spawns its own agentgateway,
// so a hardcoded port would prevent parallel muster instances (BDD test
// harness, multiple local dev sessions) from coexisting. There is a tiny
// race window between Close and agentgateway's bind; in practice it's
// dominated by the kernel's TIME_WAIT behavior, not contention.
func reserveFilesystemModeAgwPort() (uint16, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("listen: %w", err)
	}
	addr := listener.Addr().(*net.TCPAddr)
	port := uint16(addr.Port)
	if err := listener.Close(); err != nil {
		return 0, fmt.Errorf("close: %w", err)
	}
	return port, nil
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

// buildMCPServerReconciler wires the MCPServer reconciler for the active mode:
//
//   - Cluster mode: per-Reconcile k8s.NewApplier(client, ownerRef, cfg) with
//     ownerRef bound for cascade deletion. No Deleter — K8s owns cleanup via
//     OwnerReferences. agentgateway runs as a sibling pod; configDir is "".
//   - Filesystem mode: a single long-lived yaml.NewApplier(dir). The same
//     instance serves as the Deleter so reconcileDelete can remove the file.
//     The reconciler is constructed with WithDisableLocalSpawn(true) so the
//     orchestrator does not race agentgateway over stdio child processes.
//     configDir is the absolute path the subprocess manager must point agw
//     at via `-f <configDir>` once runOrchestrator spawns it.
func buildMCPServerReconciler(
	kubernetesMode bool,
	mcpServerMgr reconciler.MCPServerManager,
	musterClient client.MusterClient,
	namespace string,
	configPath string,
	listenerPort uint16,
) (*reconciler.MCPServerReconciler, string, error) {
	if kubernetesMode {
		r := reconciler.NewMCPServerReconcilerCluster(
			mcpServerMgr,
			musterClient,
			k8sapply.Config{
				GatewayName:      defaultGatewayName,
				GatewayNamespace: namespace,
			},
		).WithStatusUpdater(musterClient, namespace)
		return r, "", nil
	}
	if configPath == "" {
		return nil, "", fmt.Errorf("filesystem mode requires a non-empty configPath for the YAML Applier")
	}
	dir := filepath.Join(configPath, agentgatewayConfigSubdir)
	var applierOpts []yamlapply.Option
	if listenerPort != 0 {
		applierOpts = append(applierOpts, yamlapply.WithListenerPort(listenerPort))
	}
	applier, err := yamlapply.NewApplier(dir, applierOpts...)
	if err != nil {
		return nil, "", fmt.Errorf("construct YAML Applier at %s: %w", dir, err)
	}
	logging.Info("Services", "Wired YAML Applier writing to %s (listener port=%d)", dir, listenerPort)

	r := reconciler.NewMCPServerReconcilerFilesystem(
		mcpServerMgr,
		applier,
		applier,
	).WithStatusUpdater(musterClient, namespace)
	return r, dir, nil
}

// startAgentgatewaySubprocess resolves the pinned agentgateway binary, spawns
// it with -f <configDir>, and waits for its readiness endpoint to respond
// before returning. The returned Manager owns the process; the caller stops
// it during application shutdown.
func startAgentgatewaySubprocess(ctx context.Context, configDir string) (*subprocess.Manager, error) {
	startCtx, cancel := context.WithTimeout(ctx, agentgatewayStartupTimeout)
	defer cancel()

	binaryPath, err := binary.Resolve(startCtx)
	if err != nil {
		return nil, fmt.Errorf("resolve agentgateway binary: %w", err)
	}
	logging.Info("Services", "Resolved agentgateway binary at %s", binaryPath)

	manager, err := subprocess.New(slog.Default(),
		subprocess.WithStartupTimeout(agentgatewayStartupTimeout),
		subprocess.WithDrainTimeout(agentgatewayDrainTimeout),
	)
	if err != nil {
		return nil, fmt.Errorf("construct subprocess manager: %w", err)
	}

	configFile := filepath.Join(configDir, yamlapply.ConfigFilename)
	probe := subprocess.HTTPReadyProbe(agentgatewayReadyURL, 0)
	if err := manager.Start(startCtx, binaryPath, []string{"-f", configFile}, nil, probe); err != nil {
		return nil, fmt.Errorf("start agentgateway: %w", err)
	}
	logging.Info("Services", "agentgateway subprocess ready (pid=%d, config=%s)", manager.PID(), configFile)
	return manager, nil
}
