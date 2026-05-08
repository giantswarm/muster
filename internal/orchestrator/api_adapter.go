package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/mcpserver"
	"github.com/giantswarm/muster/internal/services"
)

// formatOAuthAuthenticationError creates a standardized error result for OAuth authentication errors.
// This is used when a service requires OAuth authentication but the operation cannot proceed
// because authentication is session-scoped and must be done via the authenticate tool.
//
// Uses structured AuthRequiredError detection (ADR-008) instead of string matching.
func formatOAuthAuthenticationError(name string, err error) *api.CallToolResult {
	var authErr *mcpserver.AuthRequiredError
	if errors.As(err, &authErr) {
		return &api.CallToolResult{
			Content: []interface{}{fmt.Sprintf(
				"Service '%s' requires OAuth authentication.\n\n"+
					"To connect to this server, use the core_auth_login tool:\n"+
					"  core_auth_login(server=\"%s\")\n\n"+
					"The service start/restart command cannot be used for OAuth-protected servers "+
					"because authentication is session-scoped.",
				name, name,
			)},
			IsError: true,
		}
	}
	return nil
}

// Adapter adapts the orchestrator to implement api.ServiceManagerHandler
type Adapter struct {
	orchestrator *Orchestrator
}

// NewAPIAdapter creates a new orchestrator adapter
func NewAPIAdapter(orchestrator *Orchestrator) *Adapter {
	return &Adapter{
		orchestrator: orchestrator,
	}
}

// Register registers the adapter with the API
func (a *Adapter) Register() {
	api.RegisterServiceManager(a)
}

// Service lifecycle management
func (a *Adapter) StartService(name string) error {
	return a.orchestrator.StartService(name)
}

func (a *Adapter) StopService(name string) error {
	return a.orchestrator.StopService(name)
}

func (a *Adapter) RestartService(name string) error {
	return a.orchestrator.RestartService(name)
}

func (a *Adapter) SubscribeToStateChanges() <-chan api.ServiceStateChangedEvent {
	// Convert internal events to API events
	internalChan := a.orchestrator.SubscribeToStateChanges()
	apiChan := make(chan api.ServiceStateChangedEvent, 100)

	go func() {
		for event := range internalChan {
			apiChan <- api.ServiceStateChangedEvent{
				Name:        event.Name,
				ServiceType: event.ServiceType,
				OldState:    event.OldState,
				NewState:    event.NewState,
				Health:      event.Health,
				Error:       event.Error,
				Timestamp:   time.Now(),
			}
		}
		close(apiChan)
	}()

	return apiChan
}

// Service status
func (a *Adapter) GetServiceStatus(name string) (*api.ServiceStatus, error) {
	service, exists := a.orchestrator.registry.Get(name)
	if !exists {
		return nil, fmt.Errorf("service %s not found", name)
	}

	status := &api.ServiceStatus{
		Name:        service.GetName(),
		ServiceType: string(service.GetType()),
		State:       api.ServiceState(service.GetState()),
		Health:      api.HealthStatus(service.GetHealth()),
	}

	// Add error if present
	if err := service.GetLastError(); err != nil {
		status.Error = err.Error()
	}

	// Add metadata if available
	if provider, ok := service.(services.ServiceDataProvider); ok {
		if data := provider.GetServiceData(); data != nil {
			status.Metadata = data
		}
	}

	// Add outputs if this is a ServiceClass-based service instance
	if genericInstance, ok := service.(*services.GenericServiceInstance); ok {
		if outputs := genericInstance.GetOutputs(); len(outputs) > 0 {
			status.Outputs = outputs
		}
	}

	return status, nil
}

func (a *Adapter) GetAllServices() []api.ServiceStatus {
	allServices := a.orchestrator.registry.GetAll()
	statuses := make([]api.ServiceStatus, 0, len(allServices))

	for _, service := range allServices {
		status := api.ServiceStatus{
			Name:        service.GetName(),
			ServiceType: string(service.GetType()),
			State:       api.ServiceState(service.GetState()),
			Health:      api.HealthStatus(service.GetHealth()),
		}

		// Add error if present
		if err := service.GetLastError(); err != nil {
			status.Error = err.Error()
		}

		// Add metadata if available
		if provider, ok := service.(services.ServiceDataProvider); ok {
			if data := provider.GetServiceData(); data != nil {
				status.Metadata = data
			}
		}

		// Add outputs if this is a ServiceClass-based service instance
		if genericInstance, ok := service.(*services.GenericServiceInstance); ok {
			if outputs := genericInstance.GetOutputs(); len(outputs) > 0 {
				status.Outputs = outputs
			}
		}

		statuses = append(statuses, status)
	}

	return statuses
}

// ServiceClass-based dynamic service instance management

// CreateServiceClassInstance is an alias that delegates to CreateService for backward compatibility
func (a *Adapter) CreateServiceClassInstance(ctx context.Context, req api.CreateServiceInstanceRequest) (*api.ServiceInstance, error) {
	return a.CreateService(ctx, req)
}

// DeleteServiceClassInstance deletes a ServiceClass-based service instance
func (a *Adapter) DeleteServiceClassInstance(ctx context.Context, name string) error {
	return a.orchestrator.DeleteServiceClassInstance(ctx, name)
}

// GetServiceClassInstance returns service instance info by ID
func (a *Adapter) GetServiceClassInstance(name string) (*api.ServiceInstance, error) {
	internalInfo, err := a.orchestrator.GetServiceClassInstance(name)
	if err != nil {
		return nil, err
	}
	return a.convertToAPIServiceInstance(internalInfo), nil
}

// ListServiceClassInstances returns all service class instances
func (a *Adapter) ListServiceClassInstances() []api.ServiceInstance {
	internalInfos := a.orchestrator.ListServiceClassInstances()
	apiInfos := make([]api.ServiceInstance, len(internalInfos))

	for i, internalInfo := range internalInfos {
		apiInfos[i] = *a.convertToAPIServiceInstance(&internalInfo)
	}

	return apiInfos
}

// SubscribeToServiceInstanceEvents returns a channel for service instance events
func (a *Adapter) SubscribeToServiceInstanceEvents() <-chan api.ServiceInstanceEvent {
	internalChan := a.orchestrator.SubscribeToServiceInstanceEvents()
	apiChan := make(chan api.ServiceInstanceEvent, 100)

	go func() {
		for internalEvent := range internalChan {
			apiChan <- api.ServiceInstanceEvent{
				Name:        internalEvent.Name,
				ServiceType: internalEvent.ServiceType,
				OldState:    internalEvent.OldState,
				NewState:    internalEvent.NewState,
				OldHealth:   internalEvent.OldHealth,
				NewHealth:   internalEvent.NewHealth,
				Error:       internalEvent.Error,
				Timestamp:   internalEvent.Timestamp,
				Metadata:    internalEvent.Metadata,
			}
		}
		close(apiChan)
	}()

	return apiChan
}

// Conversion helpers

// convertToAPIServiceInstance converts internal ServiceInstanceInfo to API ServiceInstance
func (a *Adapter) convertToAPIServiceInstance(internalInfo *ServiceInstanceInfo) *api.ServiceInstance {
	return &api.ServiceInstance{
		Name:             internalInfo.Name,
		ServiceClassName: internalInfo.ServiceClassName,
		ServiceClassType: internalInfo.ServiceClassType,
		State:            api.ServiceState(internalInfo.State),
		Health:           api.HealthStatus(internalInfo.Health),
		LastError:        internalInfo.LastError,
		CreatedAt:        internalInfo.CreatedAt,
		UpdatedAt:        internalInfo.UpdatedAt,
		LastChecked:      internalInfo.LastChecked,
		ServiceData:      internalInfo.ServiceData,
		Args:             internalInfo.CreationArgs,
		Outputs:          internalInfo.Outputs,
	}
}

// GetTools returns all tools this provider offers
func (a *Adapter) GetTools() []api.ToolMetadata {
	return []api.ToolMetadata{
		// Unified service management tools
		{
			Name:        "service_list",
			Description: "List all services (both static and ServiceClass-based) with their current status",
		},
		{
			Name:        "service_start",
			Description: "Start a specific service (works for both static and ServiceClass-based services)",
			Args: []api.ArgMetadata{
				{
					Name:        "name",
					Type:        "string",
					Required:    true,
					Description: "Service name to start",
				},
			},
		},
		{
			Name:        "service_stop",
			Description: "Stop a specific service (works for both static and ServiceClass-based services)",
			Args: []api.ArgMetadata{
				{
					Name:        "name",
					Type:        "string",
					Required:    true,
					Description: "Service name to stop",
				},
			},
		},
		{
			Name:        "service_restart",
			Description: "Restart a specific service (works for both static and ServiceClass-based services)",
			Args: []api.ArgMetadata{
				{
					Name:        "name",
					Type:        "string",
					Required:    true,
					Description: "Service name to restart",
				},
			},
		},
		{
			Name:        "service_status",
			Description: "Get status of a specific service (works for both static and ServiceClass-based services)",
			Args: []api.ArgMetadata{
				{
					Name:        "name",
					Type:        "string",
					Required:    true,
					Description: "Service name to get status for",
				},
			},
		},
	}
}

// ExecuteTool executes a tool by name
func (a *Adapter) ExecuteTool(ctx context.Context, toolName string, args map[string]interface{}) (*api.CallToolResult, error) {
	switch toolName {
	// Static service management
	case "service_list":
		return a.handleServiceList()
	case "service_start":
		return a.handleServiceStart(args)
	case "service_stop":
		return a.handleServiceStop(args)
	case "service_restart":
		return a.handleServiceRestart(args)
	case "service_status":
		return a.handleServiceStatus(args)
	default:
		return nil, fmt.Errorf("unknown tool: %s", toolName)
	}
}

// Static service management handlers
func (a *Adapter) handleServiceList() (*api.CallToolResult, error) {
	services := a.GetAllServices()

	result := map[string]interface{}{
		"services": services,
		"total":    len(services),
	}

	return &api.CallToolResult{
		Content: []interface{}{result},
		IsError: false,
	}, nil
}

func (a *Adapter) handleServiceStart(args map[string]interface{}) (*api.CallToolResult, error) {
	name, ok := args["name"].(string)
	if !ok {
		return &api.CallToolResult{
			Content: []interface{}{"name is required"},
			IsError: true,
		}, nil
	}

	// Check current state before starting
	status, err := a.GetServiceStatus(name)
	if err != nil {
		return &api.CallToolResult{
			Content: []interface{}{fmt.Sprintf("Failed to start service: %v", err)},
			IsError: true,
		}, nil
	}

	// If already running, return appropriate message
	if status.State == "running" {
		return &api.CallToolResult{
			Content: []interface{}{fmt.Sprintf("Service '%s' is already running", name)},
			IsError: false,
		}, nil
	}

	if err := a.StartService(name); err != nil {
		// Check if this is an authentication required error
		if authResult := formatOAuthAuthenticationError(name, err); authResult != nil {
			return authResult, nil
		}
		return &api.CallToolResult{
			Content: []interface{}{fmt.Sprintf("Failed to start service: %v", err)},
			IsError: true,
		}, nil
	}

	return &api.CallToolResult{
		Content: []interface{}{fmt.Sprintf("Successfully started service '%s'", name)},
		IsError: false,
	}, nil
}

func (a *Adapter) handleServiceStop(args map[string]interface{}) (*api.CallToolResult, error) {
	name, ok := args["name"].(string)
	if !ok {
		return &api.CallToolResult{
			Content: []interface{}{"name is required"},
			IsError: true,
		}, nil
	}

	// Check current state before stopping
	status, err := a.GetServiceStatus(name)
	if err != nil {
		return &api.CallToolResult{
			Content: []interface{}{fmt.Sprintf("Failed to stop service: %v", err)},
			IsError: true,
		}, nil
	}

	// If already stopped, return appropriate message
	if status.State == "stopped" {
		return &api.CallToolResult{
			Content: []interface{}{fmt.Sprintf("Service '%s' is already stopped", name)},
			IsError: false,
		}, nil
	}

	if err := a.StopService(name); err != nil {
		return &api.CallToolResult{
			Content: []interface{}{fmt.Sprintf("Failed to stop service: %v", err)},
			IsError: true,
		}, nil
	}

	return &api.CallToolResult{
		Content: []interface{}{fmt.Sprintf("Successfully stopped service '%s'", name)},
		IsError: false,
	}, nil
}

func (a *Adapter) handleServiceRestart(args map[string]interface{}) (*api.CallToolResult, error) {
	name, ok := args["name"].(string)
	if !ok {
		return &api.CallToolResult{
			Content: []interface{}{"name is required"},
			IsError: true,
		}, nil
	}

	if err := a.RestartService(name); err != nil {
		// Check if this is an authentication required error
		if authResult := formatOAuthAuthenticationError(name, err); authResult != nil {
			return authResult, nil
		}
		return &api.CallToolResult{
			Content: []interface{}{fmt.Sprintf("Failed to restart service: %v", err)},
			IsError: true,
		}, nil
	}

	return &api.CallToolResult{
		Content: []interface{}{fmt.Sprintf("Successfully restarted service '%s'", name)},
		IsError: false,
	}, nil
}

func (a *Adapter) handleServiceStatus(args map[string]interface{}) (*api.CallToolResult, error) {
	name, ok := args["name"].(string)
	if !ok {
		return &api.CallToolResult{
			Content: []interface{}{"name is required"},
			IsError: true,
		}, nil
	}

	status, err := a.GetServiceStatus(name)
	if err != nil {
		return &api.CallToolResult{
			Content: []interface{}{fmt.Sprintf("Failed to get service status: %v", err)},
			IsError: true,
		}, nil
	}

	return &api.CallToolResult{
		Content: []interface{}{status},
		IsError: false,
	}, nil
}

// ServiceClass instance creation and deletion (unified methods)

// CreateService creates a new ServiceClass-based service instance (unified method)
func (a *Adapter) CreateService(ctx context.Context, req api.CreateServiceInstanceRequest) (*api.ServiceInstance, error) {
	// Convert API request to internal request
	internalReq := CreateServiceRequest{
		ServiceClassName: req.ServiceClassName,
		Name:             req.Name,
		Args:             req.Args,
		CreateTimeout:    req.CreateTimeout,
		DeleteTimeout:    req.DeleteTimeout,
	}

	// Create the instance using the orchestrator
	internalInfo, err := a.orchestrator.CreateServiceClassInstance(ctx, internalReq)
	if err != nil {
		return nil, err
	}

	// Convert internal response to API response
	return a.convertToAPIServiceInstance(internalInfo), nil
}

// DeleteService deletes a service (works for ServiceClass instances by name)
func (a *Adapter) DeleteService(ctx context.Context, name string) error {
	if instance, err := a.orchestrator.GetServiceClassInstance(name); err == nil {
		return a.orchestrator.DeleteServiceClassInstance(ctx, instance.Name)
	}

	// Not found as ServiceClass instance, cannot delete static services
	return fmt.Errorf("service '%s' not found or cannot be deleted (static services cannot be deleted)", name)
}

// GetService returns detailed service information by name
func (a *Adapter) GetService(name string) (*api.ServiceInstance, error) {
	// First check if the service exists at all by getting its status
	status, err := a.GetServiceStatus(name)
	if err != nil {
		return nil, fmt.Errorf("service '%s' not found", name)
	}

	// For ServiceClass instances, return detailed ServiceInstance information
	if internalInfo, err := a.orchestrator.GetServiceClassInstance(name); err == nil {
		return a.convertToAPIServiceInstance(internalInfo), nil
	}

	// For static services (MCP servers, aggregator), return service information
	// in ServiceInstance format with additional service_type field in ServiceData
	now := time.Time{}
	serviceData := status.Metadata
	if serviceData == nil {
		serviceData = make(map[string]interface{})
	}
	// Add service_type to serviceData for API compatibility
	serviceData["service_type"] = status.ServiceType

	return &api.ServiceInstance{
		Name:             status.Name,
		ServiceClassName: "", // Not applicable for static services
		ServiceClassType: "", // Not applicable for static services
		State:            api.ServiceState(status.State),
		Health:           api.HealthStatus(status.Health),
		LastError:        status.Error,
		CreatedAt:        now,         // Default for static services
		LastChecked:      nil,         // Default for static services
		ServiceData:      serviceData, // Include service_type in metadata
		Args:             nil,         // Not applicable for static services
	}, nil
}
