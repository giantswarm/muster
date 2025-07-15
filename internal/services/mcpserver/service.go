package mcpserver

import (
	"context"
	"fmt"
	"sync"
	"time"

	"muster/internal/api"
	"muster/internal/mcpserver"
	"muster/internal/services"
	"muster/pkg/logging"
)

// Service implements the Service interface for MCP server management
// The MCP client now handles both process management AND MCP communication
type Service struct {
	*services.BaseService
	definition      *api.MCPServer
	client          interface{} // MCP client that manages the process AND handles MCP communication
	clientInitMutex sync.Mutex  // Protects client operations
}

// NewService creates a new MCP server service
func NewService(definition *api.MCPServer) (*Service, error) {
	baseService := services.NewBaseService(definition.Name, services.TypeMCPServer, []string{})

	service := &Service{
		BaseService: baseService,
		definition:  definition,
	}

	return service, nil
}

// Start starts the MCP server service by creating and initializing the MCP client
// The client handles both process startup and MCP protocol initialization
func (s *Service) Start(ctx context.Context) error {
	if s.IsRunning() {
		return fmt.Errorf("service %s is already running", s.GetName())
	}

	s.UpdateState(services.StateStarting, services.HealthUnknown, nil)
	s.LogInfo("Starting MCP server service")

	// Create and initialize the MCP client (this starts the process AND establishes MCP communication)
	if err := s.createAndInitializeClient(ctx); err != nil {
		s.UpdateState(services.StateFailed, services.HealthUnhealthy, err)
		return fmt.Errorf("failed to start MCP server: %w", err)
	}

	s.UpdateState(services.StateRunning, services.HealthHealthy, nil)
	s.LogInfo("MCP server started successfully")

	return nil
}

// Stop stops the MCP server service by closing the MCP client
// The client handles both MCP protocol cleanup and process termination
func (s *Service) Stop(ctx context.Context) error {
	currentState := s.GetState()

	// If already stopped, nothing to do
	if currentState == services.StateStopped {
		s.LogDebug("Service %s is already stopped", s.GetName())
		return nil
	}

	// If not running and not failed, nothing to stop
	if currentState != services.StateRunning && currentState != services.StateFailed {
		s.LogDebug("Service %s is not in a stoppable state (%s), transitioning to stopped", s.GetName(), currentState)
		s.UpdateState(services.StateStopped, services.HealthUnknown, nil)
		return nil
	}

	s.UpdateState(services.StateStopping, s.GetHealth(), nil)
	s.LogInfo("Stopping MCP server service")

	// Close the MCP client (this stops the process AND closes MCP communication)
	if err := s.closeClient(); err != nil {
		s.LogWarn("Error during client cleanup: %v", err)
		// Still transition to stopped state for graceful shutdown
	}

	s.UpdateState(services.StateStopped, services.HealthUnknown, nil)
	s.LogInfo("MCP server stopped successfully")

	return nil
}

// Restart restarts the MCP server service
func (s *Service) Restart(ctx context.Context) error {
	s.LogInfo("Restarting MCP server service")

	if s.IsRunning() {
		if err := s.Stop(ctx); err != nil {
			return fmt.Errorf("failed to stop service during restart: %w", err)
		}
	}

	// Wait a moment between stop and start
	time.Sleep(200 * time.Millisecond)

	return s.Start(ctx)
}

// IsRunning checks if the MCP server is running
func (s *Service) IsRunning() bool {
	return s.GetState() == services.StateRunning
}

// IsHealthy checks if the MCP server is healthy
func (s *Service) IsHealthy() bool {
	return s.GetHealth() == services.HealthHealthy && s.IsRunning()
}

// GetServiceType returns the service type
func (s *Service) GetServiceType() string {
	return "mcpserver"
}

// GetConfiguration returns the MCP server configuration
func (s *Service) GetConfiguration() interface{} {
	return s.definition
}

// ValidateConfiguration validates the MCP server configuration
func (s *Service) ValidateConfiguration() error {
	if s.definition == nil {
		return fmt.Errorf("MCP server definition is nil")
	}

	if s.definition.Name == "" {
		return fmt.Errorf("MCP server name is required")
	}

	// Type-specific validation
	if s.definition.Type == api.MCPServerTypeLocalCommand {
		if len(s.definition.Command) == 0 {
			return fmt.Errorf("command is required for localCommand type")
		}
	} else {
		return fmt.Errorf("unsupported MCP server type: %s", s.definition.Type)
	}

	return nil
}

// UpdateConfiguration updates the MCP server configuration
func (s *Service) UpdateConfiguration(newConfig interface{}) error {
	newDef, ok := newConfig.(*api.MCPServer)
	if !ok {
		return fmt.Errorf("invalid configuration type for MCP server")
	}

	s.definition = newDef
	return nil
}

// GetServiceData implements ServiceDataProvider
func (s *Service) GetServiceData() map[string]interface{} {
	data := map[string]interface{}{
		"name":      s.definition.Name,
		"type":      s.definition.Type,
		"autoStart": s.definition.AutoStart,
		"state":     s.GetState(),
		"health":    s.GetHealth(),
	}

	if s.definition.Type == api.MCPServerTypeLocalCommand {
		data["command"] = s.definition.Command
	}

	if s.GetLastError() != nil {
		data["error"] = s.GetLastError().Error()
	}

	// Add client to service data if available and ready
	s.clientInitMutex.Lock()
	if s.client != nil {
		data["client"] = s.client
		data["clientReady"] = true
	} else {
		data["clientReady"] = false
	}
	s.clientInitMutex.Unlock()

	// Add tool prefix for aggregator registration
	data["toolPrefix"] = ""

	return data
}

// CheckHealth implements HealthChecker using MCP protocol
func (s *Service) CheckHealth(ctx context.Context) (services.HealthStatus, error) {
	s.clientInitMutex.Lock()
	client := s.client
	s.clientInitMutex.Unlock()

	if client == nil {
		s.UpdateHealth(services.HealthUnhealthy)
		return services.HealthUnhealthy, fmt.Errorf("MCP client not available")
	}

	// Use MCP ping to check health instead of process checking
	if pinger, ok := client.(interface{ Ping(context.Context) error }); ok {
		if err := pinger.Ping(ctx); err != nil {
			s.UpdateHealth(services.HealthUnhealthy)
			return services.HealthUnhealthy, fmt.Errorf("MCP ping failed: %w", err)
		}
	}

	s.UpdateHealth(services.HealthHealthy)
	return services.HealthHealthy, nil
}

// GetHealthCheckInterval implements HealthChecker
func (s *Service) GetHealthCheckInterval() time.Duration {
	// Fixed default health check interval since MCP servers no longer define their own health check interval
	// Health checking is managed by the service orchestrator
	return 30 * time.Second
}

// GetLogContext returns the logging context for this service
func (s *Service) GetLogContext() string {
	return fmt.Sprintf("MCPServerService-%s", s.GetName())
}

// LogInfo logs an info message with service context
func (s *Service) LogInfo(format string, args ...interface{}) {
	logging.Info(s.GetLogContext(), format, args...)
}

// LogDebug logs a debug message with service context
func (s *Service) LogDebug(format string, args ...interface{}) {
	logging.Debug(s.GetLogContext(), format, args...)
}

// LogError logs an error message with service context
func (s *Service) LogError(err error, format string, args ...interface{}) {
	logging.Error(s.GetLogContext(), err, format, args...)
}

// LogWarn logs a warning message with service context
func (s *Service) LogWarn(format string, args ...interface{}) {
	logging.Warn(s.GetLogContext(), format, args...)
}

// createAndInitializeClient creates and initializes the MCP client
// This single operation starts the process AND establishes MCP communication
func (s *Service) createAndInitializeClient(ctx context.Context) error {
	s.clientInitMutex.Lock()
	defer s.clientInitMutex.Unlock()

	switch s.definition.Type {
	case api.MCPServerTypeLocalCommand:
		if len(s.definition.Command) == 0 {
			return fmt.Errorf("no command specified for local command server")
		}

		command := s.definition.Command[0]
		args := s.definition.Command[1:]

		// Create the stdio client - this is our process manager AND MCP client
		client := mcpserver.NewStdioClientWithEnv(command, args, s.definition.Env)
		s.LogDebug("Created stdio MCP client for command: %s", command)

		// Initialize the client - this starts the process AND establishes MCP communication
		initCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()

		if err := client.Initialize(initCtx); err != nil {
			return fmt.Errorf("failed to initialize MCP client/process: %w", err)
		}

		s.client = client
		s.LogDebug("MCP client initialized successfully for %s", s.GetName())
		return nil

	default:
		return fmt.Errorf("unsupported MCP server type: %s", s.definition.Type)
	}
}

// closeClient closes the MCP client, which also terminates the process
func (s *Service) closeClient() error {
	s.clientInitMutex.Lock()
	defer s.clientInitMutex.Unlock()

	if s.client == nil {
		return nil
	}

	// Close the client - this handles both MCP protocol cleanup and process termination
	if closer, ok := s.client.(interface{ Close() error }); ok {
		if err := closer.Close(); err != nil {
			s.LogWarn("Error closing MCP client: %v", err)
			return err
		} else {
			s.LogDebug("MCP client closed successfully")
		}
	}

	s.client = nil
	return nil
}

// GetMCPClient returns the MCP client for this service (used by aggregator)
// This is now much simpler since the client IS the process manager
func (s *Service) GetMCPClient() interface{} {
	s.clientInitMutex.Lock()
	defer s.clientInitMutex.Unlock()
	return s.client
}

// IsClientReady returns whether the MCP client is initialized and ready
func (s *Service) IsClientReady() bool {
	s.clientInitMutex.Lock()
	defer s.clientInitMutex.Unlock()
	return s.client != nil
}
