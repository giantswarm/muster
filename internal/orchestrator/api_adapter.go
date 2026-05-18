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

// Adapter adapts the orchestrator to implement api.ServiceManagerHandler.
type Adapter struct {
	orchestrator *Orchestrator
}

// NewAPIAdapter creates a new orchestrator adapter.
func NewAPIAdapter(orchestrator *Orchestrator) *Adapter {
	return &Adapter{
		orchestrator: orchestrator,
	}
}

// Register registers the adapter with the API.
func (a *Adapter) Register() {
	api.RegisterServiceManager(a)
}

// Service lifecycle management.
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

// GetServiceStatus returns the current status of a service.
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

	if err := service.GetLastError(); err != nil {
		status.Error = err.Error()
	}

	if provider, ok := service.(services.ServiceDataProvider); ok {
		if data := provider.GetServiceData(); data != nil {
			status.Metadata = data
		}
	}

	return status, nil
}

// GetAllServices returns the status of all services.
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

		if err := service.GetLastError(); err != nil {
			status.Error = err.Error()
		}

		if provider, ok := service.(services.ServiceDataProvider); ok {
			if data := provider.GetServiceData(); data != nil {
				status.Metadata = data
			}
		}

		statuses = append(statuses, status)
	}

	return statuses
}

// GetTools returns all tools this provider offers.
func (a *Adapter) GetTools() []api.ToolMetadata {
	return []api.ToolMetadata{
		{
			Name:        "service_list",
			Description: "List all services with their current status",
		},
		{
			Name:        "service_start",
			Description: "Start a specific service",
			Args: []api.ArgMetadata{
				{Name: "name", Type: "string", Required: true, Description: "Service name to start"},
			},
		},
		{
			Name:        "service_stop",
			Description: "Stop a specific service",
			Args: []api.ArgMetadata{
				{Name: "name", Type: "string", Required: true, Description: "Service name to stop"},
			},
		},
		{
			Name:        "service_restart",
			Description: "Restart a specific service",
			Args: []api.ArgMetadata{
				{Name: "name", Type: "string", Required: true, Description: "Service name to restart"},
			},
		},
		{
			Name:        "service_status",
			Description: "Get status of a specific service",
			Args: []api.ArgMetadata{
				{Name: "name", Type: "string", Required: true, Description: "Service name to get status for"},
			},
		},
	}
}

// ExecuteTool executes a tool by name.
func (a *Adapter) ExecuteTool(ctx context.Context, toolName string, args map[string]interface{}) (*api.CallToolResult, error) {
	switch toolName {
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

func (a *Adapter) handleServiceList() (*api.CallToolResult, error) {
	svcs := a.GetAllServices()

	result := map[string]interface{}{
		"services": svcs,
		"total":    len(svcs),
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

	status, err := a.GetServiceStatus(name)
	if err != nil {
		return &api.CallToolResult{
			Content: []interface{}{fmt.Sprintf("Failed to start service: %v", err)},
			IsError: true,
		}, nil
	}

	if status.State == "running" {
		return &api.CallToolResult{
			Content: []interface{}{fmt.Sprintf("Service '%s' is already running", name)},
			IsError: false,
		}, nil
	}

	if err := a.StartService(name); err != nil {
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

	status, err := a.GetServiceStatus(name)
	if err != nil {
		return &api.CallToolResult{
			Content: []interface{}{fmt.Sprintf("Failed to stop service: %v", err)},
			IsError: true,
		}, nil
	}

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
