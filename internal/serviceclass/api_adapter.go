package serviceclass

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"muster/internal/api"
	"muster/internal/client"
	musterv1alpha1 "muster/pkg/apis/muster/v1alpha1"
	"muster/pkg/logging"
)

// Adapter provides ServiceClass management functionality using the unified client
type Adapter struct {
	client    client.MusterClient
	namespace string
}

// NewAdapter creates a new ServiceClass API adapter with unified client support
func NewAdapter() (*Adapter, error) {
	musterClient, err := client.NewMusterClient()
	if err != nil {
		return nil, fmt.Errorf("failed to create muster client: %w", err)
	}

	return &Adapter{
		client:    musterClient,
		namespace: "default", // TODO: Make configurable
	}, nil
}

// NewAdapterWithClient creates a new adapter with a specific client (for testing)
func NewAdapterWithClient(musterClient client.MusterClient, namespace string) *Adapter {
	if namespace == "" {
		namespace = "default"
	}
	return &Adapter{
		client:    musterClient,
		namespace: namespace,
	}
}

// Register registers this adapter with the central API layer
// This method follows the project's API Service Locator Pattern
func (a *Adapter) Register() {
	api.RegisterServiceClassManager(a)
	logging.Info("ServiceClassAdapter", "Registered ServiceClass manager with API layer")
}

// Close performs cleanup for the adapter
func (a *Adapter) Close() error {
	if a.client != nil {
		return a.client.Close()
	}
	return nil
}

// GetServiceClass retrieves a specific ServiceClass by name
func (a *Adapter) GetServiceClass(name string) (*api.ServiceClass, error) {
	ctx := context.Background()
	sc, err := a.client.GetServiceClass(ctx, name, a.namespace)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil, api.NewServiceClassNotFoundError(name)
		}
		return nil, fmt.Errorf("failed to get ServiceClass %s: %w", name, err)
	}

	// Convert CRD to API type with status populated
	serviceClass := a.convertCRDToServiceClassWithStatus(sc)

	return &serviceClass, nil
}

// populateServiceClassStatus calculates and populates the status fields for a ServiceClass
func (a *Adapter) populateServiceClassStatus(serviceClass *api.ServiceClass) error {
	// Calculate required tools from lifecycle configuration
	requiredTools := a.extractRequiredTools(serviceClass)
	serviceClass.RequiredTools = requiredTools

	// Check tool availability using the API service locator pattern
	aggregatorHandler := api.GetAggregator()
	if aggregatorHandler != nil {
		availableTools := aggregatorHandler.GetAvailableTools()

		// Check which tools are missing
		missingTools := []string{}
		for _, tool := range requiredTools {
			// Core tools (starting with "core_") are always available
			if strings.HasPrefix(tool, "core_") {
				continue // Skip core tools - they're always available
			}

			// Check external tools (typically starting with "x_")
			if !contains(availableTools, tool) {
				missingTools = append(missingTools, tool)
			}
		}

		serviceClass.MissingTools = missingTools
		serviceClass.Available = len(missingTools) == 0

		// Set individual tool availability flags
		serviceClass.CreateToolAvailable = a.isToolAvailable(serviceClass.ServiceConfig.LifecycleTools.Start.Tool, availableTools)
		serviceClass.DeleteToolAvailable = a.isToolAvailable(serviceClass.ServiceConfig.LifecycleTools.Stop.Tool, availableTools)

		if serviceClass.ServiceConfig.LifecycleTools.HealthCheck != nil {
			serviceClass.HealthCheckToolAvailable = a.isToolAvailable(serviceClass.ServiceConfig.LifecycleTools.HealthCheck.Tool, availableTools)
		}

		if serviceClass.ServiceConfig.LifecycleTools.Status != nil {
			serviceClass.StatusToolAvailable = a.isToolAvailable(serviceClass.ServiceConfig.LifecycleTools.Status.Tool, availableTools)
		}
	} else {
		// If no aggregator is available, we can't check tool availability
		// But we can still consider core tools as available
		externalTools := []string{}
		for _, tool := range requiredTools {
			if !strings.HasPrefix(tool, "core_") {
				externalTools = append(externalTools, tool)
			}
		}
		serviceClass.Available = len(externalTools) == 0
		serviceClass.MissingTools = externalTools
	}

	return nil
}

// extractRequiredTools extracts the list of required tools from a ServiceClass
func (a *Adapter) extractRequiredTools(serviceClass *api.ServiceClass) []string {
	var tools []string

	// Add lifecycle tools
	if serviceClass.ServiceConfig.LifecycleTools.Start.Tool != "" {
		tools = append(tools, serviceClass.ServiceConfig.LifecycleTools.Start.Tool)
	}
	if serviceClass.ServiceConfig.LifecycleTools.Stop.Tool != "" {
		tools = append(tools, serviceClass.ServiceConfig.LifecycleTools.Stop.Tool)
	}

	// Add optional tools
	if serviceClass.ServiceConfig.LifecycleTools.Restart != nil && serviceClass.ServiceConfig.LifecycleTools.Restart.Tool != "" {
		tools = append(tools, serviceClass.ServiceConfig.LifecycleTools.Restart.Tool)
	}
	if serviceClass.ServiceConfig.LifecycleTools.HealthCheck != nil && serviceClass.ServiceConfig.LifecycleTools.HealthCheck.Tool != "" {
		tools = append(tools, serviceClass.ServiceConfig.LifecycleTools.HealthCheck.Tool)
	}
	if serviceClass.ServiceConfig.LifecycleTools.Status != nil && serviceClass.ServiceConfig.LifecycleTools.Status.Tool != "" {
		tools = append(tools, serviceClass.ServiceConfig.LifecycleTools.Status.Tool)
	}

	return tools
}

// contains checks if a string slice contains a specific string
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// ListServiceClasses returns all ServiceClasses with their status populated
func (a *Adapter) ListServiceClasses() []api.ServiceClass {
	ctx := context.Background()
	crdList, err := a.client.ListServiceClasses(ctx, a.namespace)
	if err != nil {
		// Log error and return empty list - don't fail completely
		log.Printf("Warning: Failed to list ServiceClasses: %v", err)
		return []api.ServiceClass{}
	}

	var serviceClasses []api.ServiceClass
	for _, crd := range crdList {
		serviceClass := a.convertCRDToServiceClassWithStatus(&crd)
		serviceClasses = append(serviceClasses, serviceClass)
	}

	return serviceClasses
}

// convertCRDToServiceClassWithStatus converts a ServiceClass CRD to api.ServiceClass and populates status fields

// convertCRDToServiceClassWithStatus converts a ServiceClass CRD to api.ServiceClass and populates status fields
func (a *Adapter) convertCRDToServiceClassWithStatus(sc *musterv1alpha1.ServiceClass) api.ServiceClass {
	serviceClass := convertCRDToServiceClass(sc)

	// Always populate status fields dynamically to ensure they're accurate
	// This is necessary because status fields from CRDs may be stale or empty
	if err := a.populateServiceClassStatus(&serviceClass); err != nil {
		log.Printf("Warning: Failed to populate ServiceClass status for %s: %v", sc.Name, err)
	}

	return serviceClass
}

// IsServiceClassAvailable checks if a ServiceClass is available

// IsServiceClassAvailable checks if a ServiceClass is available
func (a *Adapter) IsServiceClassAvailable(name string) bool {
	serviceClass, err := a.GetServiceClass(name)
	if err != nil {
		return false
	}

	return serviceClass.Available
}

// Tool-specific getter methods for orchestrator compatibility
func (a *Adapter) GetStartTool(name string) (toolName string, args map[string]interface{}, outputs map[string]string, err error) {
	serviceClass, err := a.GetServiceClass(name)
	if err != nil {
		return "", nil, nil, err
	}

	startTool := serviceClass.ServiceConfig.LifecycleTools.Start
	return startTool.Tool, startTool.Args, startTool.Outputs, nil
}

func (a *Adapter) GetStopTool(name string) (toolName string, args map[string]interface{}, outputs map[string]string, err error) {
	serviceClass, err := a.GetServiceClass(name)
	if err != nil {
		return "", nil, nil, err
	}

	stopTool := serviceClass.ServiceConfig.LifecycleTools.Stop
	return stopTool.Tool, stopTool.Args, stopTool.Outputs, nil
}

func (a *Adapter) GetRestartTool(name string) (toolName string, args map[string]interface{}, outputs map[string]string, err error) {
	serviceClass, err := a.GetServiceClass(name)
	if err != nil {
		return "", nil, nil, err
	}

	if serviceClass.ServiceConfig.LifecycleTools.Restart == nil {
		return "", nil, nil, nil // Optional tool
	}

	restartTool := *serviceClass.ServiceConfig.LifecycleTools.Restart
	return restartTool.Tool, restartTool.Args, restartTool.Outputs, nil
}

func (a *Adapter) GetHealthCheckTool(name string) (toolName string, args map[string]interface{}, expectation *api.HealthCheckExpectation, err error) {
	serviceClass, err := a.GetServiceClass(name)
	if err != nil {
		return "", nil, nil, err
	}

	if serviceClass.ServiceConfig.LifecycleTools.HealthCheck == nil {
		return "", nil, nil, fmt.Errorf("no health check tool defined for service class %s", name)
	}

	healthTool := *serviceClass.ServiceConfig.LifecycleTools.HealthCheck
	return healthTool.Tool, healthTool.Args, healthTool.Expect, nil
}

func (a *Adapter) GetHealthCheckConfig(name string) (enabled bool, interval time.Duration, failureThreshold, successThreshold int, err error) {
	serviceClass, err := a.GetServiceClass(name)
	if err != nil {
		return false, 0, 0, 0, err
	}

	config := serviceClass.ServiceConfig.HealthCheck
	return config.Enabled, config.Interval, config.FailureThreshold, config.SuccessThreshold, nil
}

func (a *Adapter) GetServiceDependencies(name string) ([]string, error) {
	serviceClass, err := a.GetServiceClass(name)
	if err != nil {
		return nil, err
	}

	return serviceClass.ServiceConfig.Dependencies, nil
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
			Args: []api.ArgMetadata{
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
			Args: []api.ArgMetadata{
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
			Description: "Validate a ServiceClass definition",
			Args: []api.ArgMetadata{
				{Name: "name", Type: "string", Required: true, Description: "ServiceClass name"},
				{Name: "description", Type: "string", Required: false, Description: "ServiceClass description"},
				{
					Name:        "args",
					Type:        "object",
					Required:    false,
					Description: "ServiceClass arguments schema",
					Schema: map[string]interface{}{
						"type":        "object",
						"description": "Argument definitions for service creation validation",
						"additionalProperties": map[string]interface{}{
							"type":                 "object",
							"description":          "Argument definition with validation rules",
							"additionalProperties": false,
							"properties": map[string]interface{}{
								"type": map[string]interface{}{
									"type":        "string",
									"description": "Expected data type for validation",
									"enum":        []string{"string", "integer", "boolean", "number"},
								},
								"required": map[string]interface{}{
									"type":        "boolean",
									"description": "Whether this argument is required",
								},
								"default": map[string]interface{}{
									"description": "Default value if argument is not provided",
								},
								"description": map[string]interface{}{
									"type":        "string",
									"description": "Human-readable documentation for this argument",
								},
							},
							"required": []string{"type"},
						},
					},
				},
				{
					Name:        "serviceConfig",
					Type:        "object",
					Required:    true,
					Description: "ServiceClass service configuration",
					Schema: map[string]interface{}{
						"type":                 "object",
						"description":          "Service configuration with lifecycle tools",
						"additionalProperties": false,
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
								"required":    []string{"start", "stop"},
								"properties": map[string]interface{}{
									"start":       getLifecycleToolSchema(),
									"stop":        getLifecycleToolSchema(),
									"restart":     getLifecycleToolSchema(),
									"status":      getLifecycleToolSchema(),
									"healthCheck": getHealthCheckToolSchema(),
								},
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
										"description": "Health check interval as duration string (e.g., '30s', '1m')",
									},
									"successThreshold": map[string]interface{}{
										"type":        "integer",
										"description": "Number of consecutive successes to mark healthy",
									},
									"failureThreshold": map[string]interface{}{
										"type":        "integer",
										"description": "Number of consecutive failures before marking unhealthy",
									},
								},
							},
							"timeout": map[string]interface{}{
								"type":        "object",
								"description": "Timeout configuration for service operations",
								"properties": map[string]interface{}{
									"create": map[string]interface{}{
										"type":        "string",
										"description": "Resource creation timeout as duration string (e.g., '30s', '2m')",
									},
									"delete": map[string]interface{}{
										"type":        "string",
										"description": "Resource deletion timeout as duration string (e.g., '30s', '2m')",
									},
									"healthCheck": map[string]interface{}{
										"type":        "string",
										"description": "Health check timeout as duration string (e.g., '10s', '30s')",
									},
								},
							},
							"outputs": map[string]interface{}{
								"type":        "object",
								"description": "Template-based outputs that should be generated when service instances are created",
								"additionalProperties": map[string]interface{}{
									"description": "Output value template using Go text/template syntax",
								},
							},
						},
						"required": []string{"serviceType", "lifecycleTools"},
					},
				},
			},
		},
		{
			Name:        "serviceclass_create",
			Description: "Create a new ServiceClass definition",
			Args: []api.ArgMetadata{
				{Name: "name", Type: "string", Required: true, Description: "ServiceClass name"},
				{Name: "description", Type: "string", Required: false, Description: "ServiceClass description"},
				{
					Name:        "args",
					Type:        "object",
					Required:    false,
					Description: "ServiceClass arguments schema",
					Schema: map[string]interface{}{
						"type":        "object",
						"description": "Argument definitions for service creation validation",
						"additionalProperties": map[string]interface{}{
							"type":                 "object",
							"description":          "Argument definition with validation rules",
							"additionalProperties": false,
							"properties": map[string]interface{}{
								"type": map[string]interface{}{
									"type":        "string",
									"description": "Expected data type for validation",
									"enum":        []string{"string", "integer", "boolean", "number"},
								},
								"required": map[string]interface{}{
									"type":        "boolean",
									"description": "Whether this argument is required",
								},
								"default": map[string]interface{}{
									"description": "Default value if argument is not provided",
								},
								"description": map[string]interface{}{
									"type":        "string",
									"description": "Human-readable documentation for this argument",
								},
							},
							"required": []string{"type"},
						},
					},
				},
				{
					Name:        "serviceConfig",
					Type:        "object",
					Required:    true,
					Description: "ServiceClass service configuration",
					Schema: map[string]interface{}{
						"type":                 "object",
						"description":          "Service configuration with lifecycle tools",
						"additionalProperties": false,
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
								"required":    []string{"start", "stop"},
								"properties": map[string]interface{}{
									"start":       getLifecycleToolSchema(),
									"stop":        getLifecycleToolSchema(),
									"restart":     getLifecycleToolSchema(),
									"status":      getLifecycleToolSchema(),
									"healthCheck": getHealthCheckToolSchema(),
								},
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
										"description": "Health check interval as duration string (e.g., '30s', '1m')",
									},
									"successThreshold": map[string]interface{}{
										"type":        "integer",
										"description": "Number of consecutive successes to mark healthy",
									},
									"failureThreshold": map[string]interface{}{
										"type":        "integer",
										"description": "Number of consecutive failures before marking unhealthy",
									},
								},
							},
							"timeout": map[string]interface{}{
								"type":        "object",
								"description": "Timeout configuration for service operations",
								"properties": map[string]interface{}{
									"create": map[string]interface{}{
										"type":        "string",
										"description": "Resource creation timeout as duration string (e.g., '30s', '2m')",
									},
									"delete": map[string]interface{}{
										"type":        "string",
										"description": "Resource deletion timeout as duration string (e.g., '30s', '2m')",
									},
									"healthCheck": map[string]interface{}{
										"type":        "string",
										"description": "Health check timeout as duration string (e.g., '10s', '30s')",
									},
								},
							},
							"outputs": map[string]interface{}{
								"type":        "object",
								"description": "Template-based outputs that should be generated when service instances are created",
								"additionalProperties": map[string]interface{}{
									"description": "Output value template using Go text/template syntax",
								},
							},
						},
						"required": []string{"serviceType", "lifecycleTools"},
					},
				},
			},
		},
		{
			Name:        "serviceclass_update",
			Description: "Update an existing ServiceClass definition",
			Args: []api.ArgMetadata{
				{Name: "name", Type: "string", Required: true, Description: "ServiceClass name"},
				{Name: "description", Type: "string", Required: false, Description: "ServiceClass description"},
				{
					Name:        "args",
					Type:        "object",
					Required:    false,
					Description: "ServiceClass arguments schema",
					Schema: map[string]interface{}{
						"type":        "object",
						"description": "Argument definitions for service creation validation",
						"additionalProperties": map[string]interface{}{
							"type":                 "object",
							"description":          "Argument definition with validation rules",
							"additionalProperties": false,
							"properties": map[string]interface{}{
								"type": map[string]interface{}{
									"type":        "string",
									"description": "Expected data type for validation",
									"enum":        []string{"string", "integer", "boolean", "number"},
								},
								"required": map[string]interface{}{
									"type":        "boolean",
									"description": "Whether this argument is required",
								},
								"default": map[string]interface{}{
									"description": "Default value if argument is not provided",
								},
								"description": map[string]interface{}{
									"type":        "string",
									"description": "Human-readable documentation for this argument",
								},
							},
							"required": []string{"type"},
						},
					},
				},
				{
					Name:        "serviceConfig",
					Type:        "object",
					Required:    false,
					Description: "ServiceClass service configuration",
					Schema: map[string]interface{}{
						"type":                 "object",
						"description":          "Service configuration with lifecycle tools",
						"additionalProperties": false,
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
								"required":    []string{"start", "stop"},
								"properties": map[string]interface{}{
									"start":       getLifecycleToolSchema(),
									"stop":        getLifecycleToolSchema(),
									"restart":     getLifecycleToolSchema(),
									"status":      getLifecycleToolSchema(),
									"healthCheck": getHealthCheckToolSchema(),
								},
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
										"description": "Health check interval as duration string (e.g., '30s', '1m')",
									},
									"successThreshold": map[string]interface{}{
										"type":        "integer",
										"description": "Number of consecutive successes to mark healthy",
									},
									"failureThreshold": map[string]interface{}{
										"type":        "integer",
										"description": "Number of consecutive failures before marking unhealthy",
									},
								},
							},
							"timeout": map[string]interface{}{
								"type":        "object",
								"description": "Timeout configuration for service operations",
								"properties": map[string]interface{}{
									"create": map[string]interface{}{
										"type":        "string",
										"description": "Resource creation timeout as duration string (e.g., '30s', '2m')",
									},
									"delete": map[string]interface{}{
										"type":        "string",
										"description": "Resource deletion timeout as duration string (e.g., '30s', '2m')",
									},
									"healthCheck": map[string]interface{}{
										"type":        "string",
										"description": "Health check timeout as duration string (e.g., '10s', '30s')",
									},
								},
							},
							"outputs": map[string]interface{}{
								"type":        "object",
								"description": "Template-based outputs that should be generated when service instances are created",
								"additionalProperties": map[string]interface{}{
									"description": "Output value template using Go text/template syntax",
								},
							},
						},
						"required": []string{"serviceType", "lifecycleTools"},
					},
				},
			},
		},
		{
			Name:        "serviceclass_delete",
			Description: "Delete a ServiceClass definition",
			Args: []api.ArgMetadata{
				{
					Name:        "name",
					Type:        "string",
					Required:    true,
					Description: "Name of the ServiceClass to delete",
				},
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
		"mode":           getClientMode(a.client),
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
			Content: []interface{}{"name argument is required"},
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
			Content: []interface{}{"name argument is required"},
			IsError: true,
		}, nil
	}

	available := a.IsServiceClassAvailable(name)

	result := map[string]interface{}{
		"available": available,
		"name":      name,
	}

	return &api.CallToolResult{
		Content: []interface{}{result},
		IsError: false,
	}, nil
}

func (a *Adapter) handleServiceClassValidate(args map[string]interface{}) (*api.CallToolResult, error) {
	var req api.ServiceClassValidateRequest
	if err := api.ParseRequest(args, &req); err != nil {
		return &api.CallToolResult{
			Content: []interface{}{err.Error()},
			IsError: true,
		}, nil
	}

	// Create ServiceClass CRD for validation
	serviceClass := a.convertRequestToCRD(req.Name, req.Description, req.Args, req.ServiceConfig)

	// Basic validation (more comprehensive validation would be done by the CRD schema)
	if err := a.validateServiceClass(serviceClass); err != nil {
		return &api.CallToolResult{
			Content: []interface{}{fmt.Sprintf("Validation failed: %v", err)},
			IsError: true,
		}, nil
	}

	return &api.CallToolResult{
		Content: []interface{}{fmt.Sprintf("Validation successful for ServiceClass %s", req.Name)},
		IsError: false,
	}, nil
}

func (a *Adapter) handleServiceClassCreate(args map[string]interface{}) (*api.CallToolResult, error) {
	var req api.ServiceClassCreateRequest
	if err := api.ParseRequest(args, &req); err != nil {
		return &api.CallToolResult{
			Content: []interface{}{err.Error()},
			IsError: true,
		}, nil
	}

	// Create ServiceClass CRD
	serviceClass := a.convertRequestToCRD(req.Name, req.Description, req.Args, req.ServiceConfig)

	// Validate the definition
	if err := a.validateServiceClass(serviceClass); err != nil {
		return simpleError(fmt.Sprintf("Invalid ServiceClass definition: %v", err))
	}

	// Create the new ServiceClass using the unified client
	ctx := context.Background()
	if err := a.client.CreateServiceClass(ctx, serviceClass); err != nil {
		if errors.IsAlreadyExists(err) {
			return simpleError(fmt.Sprintf("ServiceClass '%s' already exists", req.Name))
		}
		return simpleError(fmt.Sprintf("Failed to create ServiceClass: %v", err))
	}

	return simpleOK(fmt.Sprintf("ServiceClass '%s' created successfully", req.Name))
}

func (a *Adapter) handleServiceClassUpdate(args map[string]interface{}) (*api.CallToolResult, error) {
	var req api.ServiceClassUpdateRequest
	if err := api.ParseRequest(args, &req); err != nil {
		return &api.CallToolResult{
			Content: []interface{}{err.Error()},
			IsError: true,
		}, nil
	}

	// Get existing ServiceClass first
	ctx := context.Background()
	existing, err := a.client.GetServiceClass(ctx, req.Name, a.namespace)
	if err != nil {
		if errors.IsNotFound(err) {
			return api.HandleErrorWithPrefix(api.NewServiceClassNotFoundError(req.Name), "Failed to update ServiceClass"), nil
		}
		return simpleError(fmt.Sprintf("Failed to get existing ServiceClass: %v", err))
	}

	// Update fields from request
	if req.Description != "" {
		existing.Spec.Description = req.Description
	}
	if req.Args != nil {
		existing.Spec.Args = convertArgsRequestToCRD(req.Args)
	}
	if !isServiceConfigEmpty(req.ServiceConfig) {
		existing.Spec.ServiceConfig = convertServiceConfigRequestToCRD(req.ServiceConfig)
	}

	// Validate the definition
	if err := a.validateServiceClass(existing); err != nil {
		return simpleError(fmt.Sprintf("Invalid ServiceClass definition: %v", err))
	}

	// Update the ServiceClass using the unified client
	if err := a.client.UpdateServiceClass(ctx, existing); err != nil {
		return api.HandleErrorWithPrefix(err, "Failed to update ServiceClass"), nil
	}

	return simpleOK(fmt.Sprintf("ServiceClass '%s' updated successfully", req.Name))
}

func (a *Adapter) handleServiceClassDelete(args map[string]interface{}) (*api.CallToolResult, error) {
	name, ok := args["name"].(string)
	if !ok || name == "" {
		return simpleError("name argument is required")
	}

	// Delete the ServiceClass using the unified client
	ctx := context.Background()
	if err := a.client.DeleteServiceClass(ctx, name, a.namespace); err != nil {
		if errors.IsNotFound(err) {
			return api.HandleErrorWithPrefix(api.NewServiceClassNotFoundError(name), "Failed to delete ServiceClass"), nil
		}
		return api.HandleErrorWithPrefix(err, "Failed to delete ServiceClass"), nil
	}

	return simpleOK(fmt.Sprintf("ServiceClass '%s' deleted successfully", name))
}

// convertRequestToCRD converts a request to a ServiceClass CRD
func (a *Adapter) convertRequestToCRD(name, description string, args map[string]api.ArgDefinition, serviceConfig api.ServiceConfig) *musterv1alpha1.ServiceClass {
	serviceClass := &musterv1alpha1.ServiceClass{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "muster.giantswarm.io/v1alpha1",
			Kind:       "ServiceClass",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: a.namespace,
		},
		Spec: musterv1alpha1.ServiceClassSpec{
			Description: description,
		},
	}

	// Convert args if provided
	if args != nil {
		serviceClass.Spec.Args = convertArgsRequestToCRD(args)
	}

	// Convert serviceConfig
	serviceClass.Spec.ServiceConfig = convertServiceConfigRequestToCRD(serviceConfig)

	return serviceClass
}

// validateServiceClass performs basic validation on a ServiceClass

// validateServiceClass performs basic validation on a ServiceClass
func (a *Adapter) validateServiceClass(serviceClass *musterv1alpha1.ServiceClass) error {
	if serviceClass.ObjectMeta.Name == "" {
		return fmt.Errorf("name is required")
	}

	if serviceClass.Spec.ServiceConfig.LifecycleTools.Start.Tool == "" {
		return fmt.Errorf("serviceConfig.lifecycleTools.start.tool is required")
	}

	if serviceClass.Spec.ServiceConfig.LifecycleTools.Stop.Tool == "" {
		return fmt.Errorf("serviceConfig.lifecycleTools.stop.tool is required")
	}

	return nil
}

// helper to create simple error CallToolResult
func simpleError(msg string) (*api.CallToolResult, error) {
	return &api.CallToolResult{Content: []interface{}{msg}, IsError: true}, nil
}

func simpleOK(msg string) (*api.CallToolResult, error) {
	return &api.CallToolResult{Content: []interface{}{msg}, IsError: false}, nil
}

// getClientMode returns a string indicating whether we're in Kubernetes or filesystem mode
func getClientMode(client client.MusterClient) string {
	if client.IsKubernetesMode() {
		return "kubernetes"
	}
	return "filesystem"
}

// isServiceConfigEmpty checks if a ServiceConfig is empty (zero value)
func isServiceConfigEmpty(config api.ServiceConfig) bool {
	// A ServiceConfig is considered empty if all main fields are zero values
	return config.DefaultName == "" &&
		len(config.Dependencies) == 0 &&
		config.LifecycleTools.Start.Tool == "" &&
		config.LifecycleTools.Stop.Tool == ""
}

// isToolAvailable checks if a specific tool is available, considering core tools as always available
func (a *Adapter) isToolAvailable(tool string, availableTools []string) bool {
	// Core tools are always available
	if strings.HasPrefix(tool, "core_") {
		return true
	}

	// Check external tools
	return contains(availableTools, tool)
}

// getLifecycleToolSchema returns the schema definition for lifecycle tools
func getLifecycleToolSchema() map[string]interface{} {
	return map[string]interface{}{
		"type":        "object",
		"description": "Tool configuration for lifecycle operations",
		"required":    []string{"tool"},
		"properties": map[string]interface{}{
			"tool": map[string]interface{}{
				"type":        "string",
				"description": "Name of the tool to call",
			},
			"args": map[string]interface{}{
				"type":        "object",
				"description": "Arguments to pass to the tool",
			},
			"outputs": map[string]interface{}{
				"type":        "object",
				"description": "JSON path mappings to extract values from tool response",
				"additionalProperties": map[string]interface{}{
					"type":        "string",
					"description": "JSON path expression (e.g., 'result.sessionID', 'status')",
				},
			},
		},
	}
}

// getHealthCheckToolSchema returns the schema definition for health check tools
func getHealthCheckToolSchema() map[string]interface{} {
	return map[string]interface{}{
		"type":        "object",
		"description": "Tool configuration for health check operations with expectation evaluation",
		"required":    []string{"tool"},
		"properties": map[string]interface{}{
			"tool": map[string]interface{}{
				"type":        "string",
				"description": "Name of the tool to call",
			},
			"args": map[string]interface{}{
				"type":        "object",
				"description": "Arguments to pass to the tool",
			},
			"expect": map[string]interface{}{
				"type":        "object",
				"description": "Conditions for determining service health status",
				"properties": map[string]interface{}{
					"success": map[string]interface{}{
						"type":        "boolean",
						"description": "Whether the tool call should succeed (default: true)",
					},
					"json_path": map[string]interface{}{
						"type":        "object",
						"description": "Field values that must match in the tool response",
						"additionalProperties": map[string]interface{}{
							"description": "Expected value for the field",
						},
					},
				},
			},
			"expect_not": map[string]interface{}{
				"type":        "object",
				"description": "Conditions for determining service health status",
				"properties": map[string]interface{}{
					"success": map[string]interface{}{
						"type":        "boolean",
						"description": "Whether the tool call should succeed (default: true)",
					},
					"json_path": map[string]interface{}{
						"type":        "object",
						"description": "Field values that must match in the tool response",
						"additionalProperties": map[string]interface{}{
							"description": "Expected value for the field",
						},
					},
				},
			},
		},
	}
}
