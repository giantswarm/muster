package serviceclass

import (
	"context"
	"encoding/json"
	"fmt"
	"muster/internal/api"
	"muster/pkg/logging"
	"time"
)

// Adapter implements the api.ServiceClassManagerHandler interface
// This adapter bridges the ServiceClassManager implementation with the central API layer
type Adapter struct {
	manager *ServiceClassManager
}

// NewAdapter creates a new API adapter for the ServiceClassManager
func NewAdapter(manager *ServiceClassManager) *Adapter {
	return &Adapter{
		manager: manager,
	}
}

// Register registers this adapter with the central API layer
// This method follows the project's API Service Locator Pattern
func (a *Adapter) Register() {
	api.RegisterServiceClassManager(a)
	logging.Info("ServiceClassAdapter", "Registered ServiceClass manager with API layer")
}

// ListServiceClasses returns information about all registered service classes
func (a *Adapter) ListServiceClasses() []api.ServiceClass {
	if a.manager == nil {
		return []api.ServiceClass{}
	}

	// Get service class info from the manager - no conversion needed!
	return a.manager.ListServiceClasses()
}

// GetServiceClass returns a specific service class definition by name
func (a *Adapter) GetServiceClass(name string) (*api.ServiceClass, error) {
	if a.manager == nil {
		return nil, fmt.Errorf("service class manager not available")
	}

	def, exists := a.manager.GetServiceClassDefinition(name)
	if !exists {
		return nil, api.NewServiceClassNotFoundError(name)
	}

	return def, nil
}

// GetStartTool returns start tool information for a service class
func (a *Adapter) GetStartTool(name string) (toolName string, arguments map[string]interface{}, responseMapping map[string]string, err error) {
	if a.manager == nil {
		return "", nil, nil, fmt.Errorf("service class manager not available")
	}

	def, exists := a.manager.GetServiceClassDefinition(name)
	if !exists {
		return "", nil, nil, api.NewServiceClassNotFoundError(name)
	}

	startTool := def.ServiceConfig.LifecycleTools.Start
	if startTool.Tool == "" {
		return "", nil, nil, fmt.Errorf("no start tool defined for service class %s", name)
	}

	// Convert response mapping to simple map
	respMapping := map[string]string{
		"name":   startTool.ResponseMapping.Name,
		"status": startTool.ResponseMapping.Status,
		"health": startTool.ResponseMapping.Health,
		"error":  startTool.ResponseMapping.Error,
	}

	return startTool.Tool, startTool.Arguments, respMapping, nil
}

// GetStopTool returns stop tool information for a service class
func (a *Adapter) GetStopTool(name string) (toolName string, arguments map[string]interface{}, responseMapping map[string]string, err error) {
	if a.manager == nil {
		return "", nil, nil, fmt.Errorf("service class manager not available")
	}

	def, exists := a.manager.GetServiceClassDefinition(name)
	if !exists {
		return "", nil, nil, api.NewServiceClassNotFoundError(name)
	}

	stopTool := def.ServiceConfig.LifecycleTools.Stop
	if stopTool.Tool == "" {
		return "", nil, nil, fmt.Errorf("no stop tool defined for service class %s", name)
	}

	// Convert response mapping to simple map
	respMapping := map[string]string{
		"name":   stopTool.ResponseMapping.Name,
		"status": stopTool.ResponseMapping.Status,
		"health": stopTool.ResponseMapping.Health,
		"error":  stopTool.ResponseMapping.Error,
	}

	return stopTool.Tool, stopTool.Arguments, respMapping, nil
}

// GetRestartTool returns restart tool information for a service class
func (a *Adapter) GetRestartTool(name string) (toolName string, arguments map[string]interface{}, responseMapping map[string]string, err error) {
	if a.manager == nil {
		return "", nil, nil, fmt.Errorf("service class manager not available")
	}

	def, exists := a.manager.GetServiceClassDefinition(name)
	if !exists {
		return "", nil, nil, api.NewServiceClassNotFoundError(name)
	}

	restartTool := def.ServiceConfig.LifecycleTools.Restart
	if restartTool == nil || restartTool.Tool == "" {
		// This is an optional tool, so we return no error, just empty info
		return "", nil, nil, nil
	}

	// Convert response mapping to simple map
	respMapping := map[string]string{
		"name":   restartTool.ResponseMapping.Name,
		"status": restartTool.ResponseMapping.Status,
		"health": restartTool.ResponseMapping.Health,
		"error":  restartTool.ResponseMapping.Error,
	}

	return restartTool.Tool, restartTool.Arguments, respMapping, nil
}

// GetHealthCheckTool returns health check tool information for a service class
func (a *Adapter) GetHealthCheckTool(name string) (toolName string, arguments map[string]interface{}, responseMapping map[string]string, err error) {
	if a.manager == nil {
		return "", nil, nil, fmt.Errorf("service class manager not available")
	}

	def, exists := a.manager.GetServiceClassDefinition(name)
	if !exists {
		return "", nil, nil, api.NewServiceClassNotFoundError(name)
	}

	if def.ServiceConfig.LifecycleTools.HealthCheck == nil {
		return "", nil, nil, fmt.Errorf("no health check tool defined for service class %s", name)
	}

	healthTool := *def.ServiceConfig.LifecycleTools.HealthCheck
	if healthTool.Tool == "" {
		return "", nil, nil, fmt.Errorf("no health check tool defined for service class %s", name)
	}

	// Convert response mapping to simple map
	respMapping := map[string]string{
		"name":   healthTool.ResponseMapping.Name,
		"status": healthTool.ResponseMapping.Status,
		"health": healthTool.ResponseMapping.Health,
		"error":  healthTool.ResponseMapping.Error,
	}

	return healthTool.Tool, healthTool.Arguments, respMapping, nil
}

// GetHealthCheckConfig returns health check configuration for a service class
func (a *Adapter) GetHealthCheckConfig(name string) (enabled bool, interval time.Duration, failureThreshold, successThreshold int, err error) {
	if a.manager == nil {
		return false, 0, 0, 0, fmt.Errorf("service class manager not available")
	}

	def, exists := a.manager.GetServiceClassDefinition(name)
	if !exists {
		return false, 0, 0, 0, api.NewServiceClassNotFoundError(name)
	}

	config := def.ServiceConfig.HealthCheck
	return config.Enabled, config.Interval, config.FailureThreshold, config.SuccessThreshold, nil
}

// GetServiceDependencies returns dependencies for a service class
func (a *Adapter) GetServiceDependencies(name string) ([]string, error) {
	if a.manager == nil {
		return nil, fmt.Errorf("service class manager not available")
	}

	def, exists := a.manager.GetServiceClassDefinition(name)
	if !exists {
		return nil, api.NewServiceClassNotFoundError(name)
	}

	return def.ServiceConfig.Dependencies, nil
}

// IsServiceClassAvailable checks if a service class is available
func (a *Adapter) IsServiceClassAvailable(name string) bool {
	if a.manager == nil {
		return false
	}

	return a.manager.IsServiceClassAvailable(name)
}

// GetManager returns the underlying ServiceClassManager (for internal use)
// This should only be used by other internal packages that need direct access
func (a *Adapter) GetManager() *ServiceClassManager {
	return a.manager
}

// ToolProvider implementation

// GetTools returns all tools this provider offers
func (a *Adapter) GetTools() []api.ToolMetadata {
	return []api.ToolMetadata{
		{
			Name:        "serviceclass_list",
			Description: "List all ServiceClass definitions with their availability status",
		},
		{
			Name:        "serviceclass_get",
			Description: "Get detailed information about a specific ServiceClass definition",
			Parameters: []api.ParameterMetadata{
				{
					Name:        "name",
					Type:        "string",
					Required:    true,
					Description: "Name of the ServiceClass to retrieve",
				},
			},
		},
		{
			Name:        "serviceclass_available",
			Description: "Check if a ServiceClass is available (all required tools present)",
			Parameters: []api.ParameterMetadata{
				{
					Name:        "name",
					Type:        "string",
					Required:    true,
					Description: "Name of the ServiceClass to check",
				},
			},
		},
		{
			Name:        "serviceclass_validate",
			Description: "Validate a serviceclass definition",
			Parameters: []api.ParameterMetadata{
				{Name: "name", Type: "string", Required: true, Description: "ServiceClass name"},
				{
					Name:        "serviceConfig",
					Type:        "object",
					Required:    true,
					Description: "Service configuration with lifecycle tools",
					Schema: map[string]interface{}{
						"type":        "object",
						"description": "Service configuration with lifecycle tools and management settings",
						"properties": map[string]interface{}{
							"serviceType": map[string]interface{}{
								"type":        "string",
								"description": "Type of service this configuration manages",
							},
							"defaultName": map[string]interface{}{
								"type":        "string",
								"description": "Default name pattern for service instances",
							},
							"dependencies": map[string]interface{}{
								"type":        "array",
								"description": "List of ServiceClass names that must be available",
								"items": map[string]interface{}{
									"type": "string",
								},
							},
							"lifecycleTools": map[string]interface{}{
								"type":        "object",
								"description": "Tools for managing service lifecycle",
								"properties": map[string]interface{}{
									"start": map[string]interface{}{
										"type":        "object",
										"description": "Tool to start the service",
										"properties": map[string]interface{}{
											"tool": map[string]interface{}{
												"type":        "string",
												"description": "Name of the tool to call",
											},
											"arguments": map[string]interface{}{
												"type":        "object",
												"description": "Arguments to pass to the tool",
											},
											"responseMapping": map[string]interface{}{
												"type":        "object",
												"description": "Mapping of tool response fields to service fields",
												"properties": map[string]interface{}{
													"name":   map[string]interface{}{"type": "string"},
													"status": map[string]interface{}{"type": "string"},
													"health": map[string]interface{}{"type": "string"},
													"error":  map[string]interface{}{"type": "string"},
												},
											},
										},
										"required": []string{"tool"},
									},
									"stop": map[string]interface{}{
										"type":        "object",
										"description": "Tool to stop the service",
										"properties": map[string]interface{}{
											"tool": map[string]interface{}{
												"type":        "string",
												"description": "Name of the tool to call",
											},
											"arguments": map[string]interface{}{
												"type":        "object",
												"description": "Arguments to pass to the tool",
											},
											"responseMapping": map[string]interface{}{
												"type":        "object",
												"description": "Mapping of tool response fields to service fields",
												"properties": map[string]interface{}{
													"name":   map[string]interface{}{"type": "string"},
													"status": map[string]interface{}{"type": "string"},
													"health": map[string]interface{}{"type": "string"},
													"error":  map[string]interface{}{"type": "string"},
												},
											},
										},
										"required": []string{"tool"},
									},
									"restart": map[string]interface{}{
										"type":        "object",
										"description": "Tool to restart the service (optional)",
										"properties": map[string]interface{}{
											"tool": map[string]interface{}{
												"type":        "string",
												"description": "Name of the tool to call",
											},
											"arguments": map[string]interface{}{
												"type":        "object",
												"description": "Arguments to pass to the tool",
											},
											"responseMapping": map[string]interface{}{
												"type":        "object",
												"description": "Mapping of tool response fields to service fields",
												"properties": map[string]interface{}{
													"name":   map[string]interface{}{"type": "string"},
													"status": map[string]interface{}{"type": "string"},
													"health": map[string]interface{}{"type": "string"},
													"error":  map[string]interface{}{"type": "string"},
												},
											},
										},
									},
									"healthCheck": map[string]interface{}{
										"type":        "object",
										"description": "Tool to check service health (optional)",
										"properties": map[string]interface{}{
											"tool": map[string]interface{}{
												"type":        "string",
												"description": "Name of the tool to call",
											},
											"arguments": map[string]interface{}{
												"type":        "object",
												"description": "Arguments to pass to the tool",
											},
											"responseMapping": map[string]interface{}{
												"type":        "object",
												"description": "Mapping of tool response fields to service fields",
												"properties": map[string]interface{}{
													"name":   map[string]interface{}{"type": "string"},
													"status": map[string]interface{}{"type": "string"},
													"health": map[string]interface{}{"type": "string"},
													"error":  map[string]interface{}{"type": "string"},
												},
											},
										},
									},
								},
								"required": []string{"start", "stop"},
							},
							"healthCheck": map[string]interface{}{
								"type":        "object",
								"description": "Health check configuration",
								"properties": map[string]interface{}{
									"enabled": map[string]interface{}{
										"type":        "boolean",
										"description": "Whether health checks are enabled",
									},
									"interval": map[string]interface{}{
										"type":        "string",
										"description": "Health check interval (e.g., '30s', '1m')",
									},
									"failureThreshold": map[string]interface{}{
										"type":        "integer",
										"description": "Number of consecutive failures before marking unhealthy",
									},
									"successThreshold": map[string]interface{}{
										"type":        "integer",
										"description": "Number of consecutive successes to mark healthy",
									},
								},
							},
							"timeout": map[string]interface{}{
								"type":        "object",
								"description": "Timeout configuration for service operations",
								"properties": map[string]interface{}{
									"start": map[string]interface{}{
										"type":        "string",
										"description": "Start operation timeout (e.g., '30s', '2m')",
									},
									"stop": map[string]interface{}{
										"type":        "string",
										"description": "Stop operation timeout (e.g., '30s', '2m')",
									},
									"healthCheck": map[string]interface{}{
										"type":        "string",
										"description": "Health check timeout (e.g., '10s', '30s')",
									},
								},
							},
						},
						"required":             []string{"serviceType", "lifecycleTools"},
						"additionalProperties": false,
					},
				},
				{Name: "description", Type: "string", Required: false, Description: "ServiceClass description"},
				{Name: "version", Type: "string", Required: false, Description: "ServiceClass version"},
			},
		},
		{
			Name:        "serviceclass_create",
			Description: "Create a new service class",
			Parameters: []api.ParameterMetadata{
				{Name: "name", Type: "string", Required: true, Description: "ServiceClass name"},
				{
					Name:        "serviceConfig",
					Type:        "object",
					Required:    true,
					Description: "Service configuration with lifecycle tools",
					Schema: map[string]interface{}{
						"type":        "object",
						"description": "Service configuration with lifecycle tools and management settings",
						"properties": map[string]interface{}{
							"serviceType": map[string]interface{}{
								"type":        "string",
								"description": "Type of service this configuration manages",
							},
							"defaultName": map[string]interface{}{
								"type":        "string",
								"description": "Default name pattern for service instances",
							},
							"dependencies": map[string]interface{}{
								"type":        "array",
								"description": "List of ServiceClass names that must be available",
								"items": map[string]interface{}{
									"type": "string",
								},
							},
							"lifecycleTools": map[string]interface{}{
								"type":        "object",
								"description": "Tools for managing service lifecycle",
								"properties": map[string]interface{}{
									"start": map[string]interface{}{
										"type":        "object",
										"description": "Tool to start the service",
										"properties": map[string]interface{}{
											"tool": map[string]interface{}{
												"type":        "string",
												"description": "Name of the tool to call",
											},
											"arguments": map[string]interface{}{
												"type":        "object",
												"description": "Arguments to pass to the tool",
											},
											"responseMapping": map[string]interface{}{
												"type":        "object",
												"description": "Mapping of tool response fields to service fields",
												"properties": map[string]interface{}{
													"name":   map[string]interface{}{"type": "string"},
													"status": map[string]interface{}{"type": "string"},
													"health": map[string]interface{}{"type": "string"},
													"error":  map[string]interface{}{"type": "string"},
												},
											},
										},
										"required": []string{"tool"},
									},
									"stop": map[string]interface{}{
										"type":        "object",
										"description": "Tool to stop the service",
										"properties": map[string]interface{}{
											"tool": map[string]interface{}{
												"type":        "string",
												"description": "Name of the tool to call",
											},
											"arguments": map[string]interface{}{
												"type":        "object",
												"description": "Arguments to pass to the tool",
											},
											"responseMapping": map[string]interface{}{
												"type":        "object",
												"description": "Mapping of tool response fields to service fields",
												"properties": map[string]interface{}{
													"name":   map[string]interface{}{"type": "string"},
													"status": map[string]interface{}{"type": "string"},
													"health": map[string]interface{}{"type": "string"},
													"error":  map[string]interface{}{"type": "string"},
												},
											},
										},
										"required": []string{"tool"},
									},
									"restart": map[string]interface{}{
										"type":        "object",
										"description": "Tool to restart the service (optional)",
										"properties": map[string]interface{}{
											"tool": map[string]interface{}{
												"type":        "string",
												"description": "Name of the tool to call",
											},
											"arguments": map[string]interface{}{
												"type":        "object",
												"description": "Arguments to pass to the tool",
											},
											"responseMapping": map[string]interface{}{
												"type":        "object",
												"description": "Mapping of tool response fields to service fields",
												"properties": map[string]interface{}{
													"name":   map[string]interface{}{"type": "string"},
													"status": map[string]interface{}{"type": "string"},
													"health": map[string]interface{}{"type": "string"},
													"error":  map[string]interface{}{"type": "string"},
												},
											},
										},
									},
									"healthCheck": map[string]interface{}{
										"type":        "object",
										"description": "Tool to check service health (optional)",
										"properties": map[string]interface{}{
											"tool": map[string]interface{}{
												"type":        "string",
												"description": "Name of the tool to call",
											},
											"arguments": map[string]interface{}{
												"type":        "object",
												"description": "Arguments to pass to the tool",
											},
											"responseMapping": map[string]interface{}{
												"type":        "object",
												"description": "Mapping of tool response fields to service fields",
												"properties": map[string]interface{}{
													"name":   map[string]interface{}{"type": "string"},
													"status": map[string]interface{}{"type": "string"},
													"health": map[string]interface{}{"type": "string"},
													"error":  map[string]interface{}{"type": "string"},
												},
											},
										},
									},
								},
								"required": []string{"start", "stop"},
							},
							"healthCheck": map[string]interface{}{
								"type":        "object",
								"description": "Health check configuration",
								"properties": map[string]interface{}{
									"enabled": map[string]interface{}{
										"type":        "boolean",
										"description": "Whether health checks are enabled",
									},
									"interval": map[string]interface{}{
										"type":        "string",
										"description": "Health check interval (e.g., '30s', '1m')",
									},
									"failureThreshold": map[string]interface{}{
										"type":        "integer",
										"description": "Number of consecutive failures before marking unhealthy",
									},
									"successThreshold": map[string]interface{}{
										"type":        "integer",
										"description": "Number of consecutive successes to mark healthy",
									},
								},
							},
							"timeout": map[string]interface{}{
								"type":        "object",
								"description": "Timeout configuration for service operations",
								"properties": map[string]interface{}{
									"start": map[string]interface{}{
										"type":        "string",
										"description": "Start operation timeout (e.g., '30s', '2m')",
									},
									"stop": map[string]interface{}{
										"type":        "string",
										"description": "Stop operation timeout (e.g., '30s', '2m')",
									},
									"healthCheck": map[string]interface{}{
										"type":        "string",
										"description": "Health check timeout (e.g., '10s', '30s')",
									},
								},
							},
						},
						"required":             []string{"serviceType", "lifecycleTools"},
						"additionalProperties": false,
					},
				},
				{Name: "description", Type: "string", Required: false, Description: "ServiceClass description"},
				{Name: "version", Type: "string", Required: false, Description: "ServiceClass version"},
			},
		},
		{
			Name:        "serviceclass_update",
			Description: "Update an existing service class",
			Parameters: []api.ParameterMetadata{
				{Name: "name", Type: "string", Required: true, Description: "Name of the ServiceClass to update"},
				{
					Name:        "serviceConfig",
					Type:        "object",
					Required:    false,
					Description: "Service configuration with lifecycle tools",
					Schema: map[string]interface{}{
						"type":        "object",
						"description": "Service configuration with lifecycle tools and management settings",
						"properties": map[string]interface{}{
							"serviceType": map[string]interface{}{
								"type":        "string",
								"description": "Type of service this configuration manages",
							},
							"defaultName": map[string]interface{}{
								"type":        "string",
								"description": "Default name pattern for service instances",
							},
							"dependencies": map[string]interface{}{
								"type":        "array",
								"description": "List of ServiceClass names that must be available",
								"items": map[string]interface{}{
									"type": "string",
								},
							},
							"lifecycleTools": map[string]interface{}{
								"type":        "object",
								"description": "Tools for managing service lifecycle",
								"properties": map[string]interface{}{
									"start": map[string]interface{}{
										"type":        "object",
										"description": "Tool to start the service",
										"properties": map[string]interface{}{
											"tool": map[string]interface{}{
												"type":        "string",
												"description": "Name of the tool to call",
											},
											"arguments": map[string]interface{}{
												"type":        "object",
												"description": "Arguments to pass to the tool",
											},
											"responseMapping": map[string]interface{}{
												"type":        "object",
												"description": "Mapping of tool response fields to service fields",
												"properties": map[string]interface{}{
													"name":   map[string]interface{}{"type": "string"},
													"status": map[string]interface{}{"type": "string"},
													"health": map[string]interface{}{"type": "string"},
													"error":  map[string]interface{}{"type": "string"},
												},
											},
										},
										"required": []string{"tool"},
									},
									"stop": map[string]interface{}{
										"type":        "object",
										"description": "Tool to stop the service",
										"properties": map[string]interface{}{
											"tool": map[string]interface{}{
												"type":        "string",
												"description": "Name of the tool to call",
											},
											"arguments": map[string]interface{}{
												"type":        "object",
												"description": "Arguments to pass to the tool",
											},
											"responseMapping": map[string]interface{}{
												"type":        "object",
												"description": "Mapping of tool response fields to service fields",
												"properties": map[string]interface{}{
													"name":   map[string]interface{}{"type": "string"},
													"status": map[string]interface{}{"type": "string"},
													"health": map[string]interface{}{"type": "string"},
													"error":  map[string]interface{}{"type": "string"},
												},
											},
										},
										"required": []string{"tool"},
									},
									"restart": map[string]interface{}{
										"type":        "object",
										"description": "Tool to restart the service (optional)",
										"properties": map[string]interface{}{
											"tool": map[string]interface{}{
												"type":        "string",
												"description": "Name of the tool to call",
											},
											"arguments": map[string]interface{}{
												"type":        "object",
												"description": "Arguments to pass to the tool",
											},
											"responseMapping": map[string]interface{}{
												"type":        "object",
												"description": "Mapping of tool response fields to service fields",
												"properties": map[string]interface{}{
													"name":   map[string]interface{}{"type": "string"},
													"status": map[string]interface{}{"type": "string"},
													"health": map[string]interface{}{"type": "string"},
													"error":  map[string]interface{}{"type": "string"},
												},
											},
										},
									},
									"healthCheck": map[string]interface{}{
										"type":        "object",
										"description": "Tool to check service health (optional)",
										"properties": map[string]interface{}{
											"tool": map[string]interface{}{
												"type":        "string",
												"description": "Name of the tool to call",
											},
											"arguments": map[string]interface{}{
												"type":        "object",
												"description": "Arguments to pass to the tool",
											},
											"responseMapping": map[string]interface{}{
												"type":        "object",
												"description": "Mapping of tool response fields to service fields",
												"properties": map[string]interface{}{
													"name":   map[string]interface{}{"type": "string"},
													"status": map[string]interface{}{"type": "string"},
													"health": map[string]interface{}{"type": "string"},
													"error":  map[string]interface{}{"type": "string"},
												},
											},
										},
									},
								},
								"required": []string{"start", "stop"},
							},
							"healthCheck": map[string]interface{}{
								"type":        "object",
								"description": "Health check configuration",
								"properties": map[string]interface{}{
									"enabled": map[string]interface{}{
										"type":        "boolean",
										"description": "Whether health checks are enabled",
									},
									"interval": map[string]interface{}{
										"type":        "string",
										"description": "Health check interval (e.g., '30s', '1m')",
									},
									"failureThreshold": map[string]interface{}{
										"type":        "integer",
										"description": "Number of consecutive failures before marking unhealthy",
									},
									"successThreshold": map[string]interface{}{
										"type":        "integer",
										"description": "Number of consecutive successes to mark healthy",
									},
								},
							},
							"timeout": map[string]interface{}{
								"type":        "object",
								"description": "Timeout configuration for service operations",
								"properties": map[string]interface{}{
									"start": map[string]interface{}{
										"type":        "string",
										"description": "Start operation timeout (e.g., '30s', '2m')",
									},
									"stop": map[string]interface{}{
										"type":        "string",
										"description": "Stop operation timeout (e.g., '30s', '2m')",
									},
									"healthCheck": map[string]interface{}{
										"type":        "string",
										"description": "Health check timeout (e.g., '10s', '30s')",
									},
								},
							},
						},
						"required":             []string{"serviceType", "lifecycleTools"},
						"additionalProperties": false,
					},
				},
				{Name: "description", Type: "string", Required: false, Description: "ServiceClass description"},
				{Name: "version", Type: "string", Required: false, Description: "ServiceClass version"},
			},
		},
		{
			Name:        "serviceclass_delete",
			Description: "Delete a service class",
			Parameters: []api.ParameterMetadata{
				{Name: "name", Type: "string", Required: true, Description: "Name of the ServiceClass to delete"},
			},
		},
	}
}

// ExecuteTool executes a tool by name
func (a *Adapter) ExecuteTool(ctx context.Context, toolName string, args map[string]interface{}) (*api.CallToolResult, error) {
	switch toolName {
	case "serviceclass_list":
		return a.handleServiceClassList()
	case "serviceclass_get":
		return a.handleServiceClassGet(args)
	case "serviceclass_available":
		return a.handleServiceClassAvailable(args)
	case "serviceclass_validate":
		return a.handleServiceClassValidate(args)
	case "serviceclass_create":
		return a.handleServiceClassCreate(args)
	case "serviceclass_update":
		return a.handleServiceClassUpdate(args)
	case "serviceclass_delete":
		return a.handleServiceClassDelete(args)
	default:
		return nil, fmt.Errorf("unknown tool: %s", toolName)
	}
}

// Tool handlers

func (a *Adapter) handleServiceClassList() (*api.CallToolResult, error) {
	serviceClasses := a.ListServiceClasses()

	result := map[string]interface{}{
		"serviceClasses": serviceClasses,
		"total":          len(serviceClasses),
	}

	return &api.CallToolResult{
		Content: []interface{}{result},
		IsError: false,
	}, nil
}

func (a *Adapter) handleServiceClassGet(args map[string]interface{}) (*api.CallToolResult, error) {
	name, ok := args["name"].(string)
	if !ok {
		return &api.CallToolResult{
			Content: []interface{}{"name parameter is required"},
			IsError: true,
		}, nil
	}

	serviceClass, err := a.GetServiceClass(name)
	if err != nil {
		return api.HandleErrorWithPrefix(err, "Failed to get ServiceClass"), nil
	}

	return &api.CallToolResult{
		Content: []interface{}{serviceClass},
		IsError: false,
	}, nil
}

func (a *Adapter) handleServiceClassAvailable(args map[string]interface{}) (*api.CallToolResult, error) {
	name, ok := args["name"].(string)
	if !ok {
		return &api.CallToolResult{
			Content: []interface{}{"name parameter is required"},
			IsError: true,
		}, nil
	}

	available := a.IsServiceClassAvailable(name)

	result := map[string]interface{}{
		"name":      name,
		"available": available,
	}

	return &api.CallToolResult{
		Content: []interface{}{result},
		IsError: false,
	}, nil
}

// handleServiceClassValidate validates a serviceclass definition
func (a *Adapter) handleServiceClassValidate(args map[string]interface{}) (*api.CallToolResult, error) {
	var req api.ServiceClassValidateRequest
	if err := api.ParseRequest(args, &req); err != nil {
		return &api.CallToolResult{
			Content: []interface{}{err.Error()},
			IsError: true,
		}, nil
	}

	// Build ServiceClass definition
	def := api.ServiceClass{
		Name:          req.Name,
		Version:       req.Version,
		Description:   req.Description,
		ServiceConfig: req.ServiceConfig,                        // Already properly typed
		Operations:    make(map[string]api.OperationDefinition), // Not provided in validation requests
	}

	if err := a.manager.ValidateDefinition(&def); err != nil {
		return &api.CallToolResult{
			Content: []interface{}{fmt.Sprintf("Validation failed: %v", err)},
			IsError: true,
		}, nil
	}

	return &api.CallToolResult{
		Content: []interface{}{fmt.Sprintf("Validation successful for serviceclass %s", req.Name)},
		IsError: false,
	}, nil
}

// helper to create simple error CallToolResult
func simpleError(msg string) (*api.CallToolResult, error) {
	return &api.CallToolResult{Content: []interface{}{msg}, IsError: true}, nil
}

func simpleOK(msg string) (*api.CallToolResult, error) {
	return &api.CallToolResult{Content: []interface{}{msg}, IsError: false}, nil
}

func (a *Adapter) handleServiceClassCreate(args map[string]interface{}) (*api.CallToolResult, error) {
	var req api.ServiceClassCreateRequest
	if err := api.ParseRequest(args, &req); err != nil {
		return &api.CallToolResult{
			Content: []interface{}{err.Error()},
			IsError: true,
		}, nil
	}

	// Build ServiceClass definition
	def := api.ServiceClass{
		Name:          req.Name,
		Version:       req.Version,
		Description:   req.Description,
		ServiceConfig: req.ServiceConfig,                        // Already properly typed
		Operations:    make(map[string]api.OperationDefinition), // Not provided in create requests
	}

	// Validate the definition
	if err := a.manager.ValidateDefinition(&def); err != nil {
		return simpleError(fmt.Sprintf("Invalid ServiceClass definition: %v", err))
	}

	// Check if it already exists
	if _, exists := a.manager.GetServiceClassDefinition(def.Name); exists {
		return simpleError(fmt.Sprintf("ServiceClass '%s' already exists", def.Name))
	}

	// Create the new ServiceClass
	if err := a.manager.CreateServiceClass(def); err != nil {
		return simpleError(fmt.Sprintf("Failed to create ServiceClass: %v", err))
	}

	return simpleOK(fmt.Sprintf("ServiceClass '%s' created successfully", def.Name))
}

func (a *Adapter) handleServiceClassUpdate(args map[string]interface{}) (*api.CallToolResult, error) {
	var req api.ServiceClassUpdateRequest
	if err := api.ParseRequest(args, &req); err != nil {
		return &api.CallToolResult{
			Content: []interface{}{err.Error()},
			IsError: true,
		}, nil
	}

	// Build ServiceClass definition
	def := api.ServiceClass{
		Name:          req.Name,
		Version:       req.Version,
		Description:   req.Description,
		ServiceConfig: req.ServiceConfig,                        // Already properly typed
		Operations:    make(map[string]api.OperationDefinition), // Not provided in update requests
	}

	// Validate the definition
	if err := a.manager.ValidateDefinition(&def); err != nil {
		return simpleError(fmt.Sprintf("Invalid ServiceClass definition: %v", err))
	}

	// Check if it exists
	if _, exists := a.manager.GetServiceClassDefinition(req.Name); !exists {
		return api.HandleErrorWithPrefix(api.NewServiceClassNotFoundError(req.Name), "Failed to update ServiceClass"), nil
	}

	// Update the ServiceClass
	if err := a.manager.UpdateServiceClass(req.Name, def); err != nil {
		return api.HandleErrorWithPrefix(err, "Failed to update ServiceClass"), nil
	}

	return simpleOK(fmt.Sprintf("ServiceClass '%s' updated successfully", req.Name))
}

func (a *Adapter) handleServiceClassDelete(args map[string]interface{}) (*api.CallToolResult, error) {
	name, ok := args["name"].(string)
	if !ok || name == "" {
		return simpleError("name parameter is required")
	}

	if err := a.manager.DeleteServiceClass(name); err != nil {
		return api.HandleErrorWithPrefix(err, "Delete failed"), nil
	}

	return simpleOK(fmt.Sprintf("deleted service class %s", name))
}

// convertServiceConfigViJSON converts a map[string]interface{} to ServiceConfig using JSON marshaling
func convertServiceConfigViJSON(configMap map[string]interface{}) (api.ServiceConfig, error) {
	// Convert to JSON then unmarshal to struct for automatic type conversion
	jsonData, err := json.Marshal(configMap)
	if err != nil {
		return api.ServiceConfig{}, fmt.Errorf("failed to marshal config map: %w", err)
	}

	var config api.ServiceConfig
	err = json.Unmarshal(jsonData, &config)
	if err != nil {
		return api.ServiceConfig{}, fmt.Errorf("failed to unmarshal to ServiceConfig: %w", err)
	}

	return config, nil
}
