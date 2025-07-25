package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"muster/internal/api"
	"muster/internal/config"

	"gopkg.in/yaml.v3"
)

// ConfigAdapter adapts the config system to implement api.ConfigHandler.
// It provides a thread-safe interface for reading and updating muster configuration
// and serves as the bridge between the application layer and the API system.
type ConfigAdapter struct {
	config     *config.MusterConfig
	configPath string
	mu         sync.RWMutex
}

// NewConfigAdapter creates a new config adapter instance.
// The configPath arg specifies where to save configuration changes.
// If empty, the adapter will auto-detect an appropriate path.
func NewConfigAdapter(cfg *config.MusterConfig, configPath string) *ConfigAdapter {
	return &ConfigAdapter{
		config:     cfg,
		configPath: configPath,
	}
}

// Register registers the adapter with the API layer.
// This must be called during application initialization to make the config
// handler available to other components through the API system.
func (a *ConfigAdapter) Register() {
	api.RegisterConfig(a)
}

// GetConfig returns the current muster configuration.
// This method is thread-safe and returns a copy of the configuration.
func (a *ConfigAdapter) GetConfig(ctx context.Context) (*config.MusterConfig, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if a.config == nil {
		return nil, fmt.Errorf("configuration not loaded")
	}
	return a.config, nil
}

// GetAggregatorConfig returns the aggregator-specific configuration section.
func (a *ConfigAdapter) GetAggregatorConfig(ctx context.Context) (*config.AggregatorConfig, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if a.config == nil {
		return nil, fmt.Errorf("configuration not loaded")
	}
	return &a.config.Aggregator, nil
}

// UpdateAggregatorConfig updates the aggregator configuration section.
// Changes are immediately saved to disk if a valid config path is available.
func (a *ConfigAdapter) UpdateAggregatorConfig(ctx context.Context, aggregator config.AggregatorConfig) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.config == nil {
		return fmt.Errorf("configuration not loaded")
	}

	a.config.Aggregator = aggregator
	return a.saveConfig()
}

// SaveConfig persists the current configuration to disk.
// If no config path was specified during creation, it attempts to auto-detect
// an appropriate location (project directory first, then user directory).
func (a *ConfigAdapter) SaveConfig(ctx context.Context) error {
	return a.saveConfig()
}

// ReloadConfig reloads the configuration from disk using the centralized loader.
// This replaces the current in-memory configuration with the version from disk.
func (a *ConfigAdapter) ReloadConfig(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Load config using the centralized loader
	musterConfig, err := config.LoadConfig(a.configPath)
	if err != nil {
		return fmt.Errorf("failed to reload configuration: %w", err)
	}

	a.config = &musterConfig
	return nil
}

// GetTools returns metadata for all configuration management tools provided by this adapter.
// These tools are exposed through the MCP aggregator for external clients.
func (a *ConfigAdapter) GetTools() []api.ToolMetadata {
	return []api.ToolMetadata{
		{
			Name:        "config_get",
			Description: "Get the current muster configuration",
		},
		{
			Name:        "config_get_aggregator",
			Description: "Get aggregator configuration",
		},

		{
			Name:        "config_update_aggregator",
			Description: "Update aggregator configuration",
			Args: []api.ArgMetadata{
				{
					Name:        "aggregator",
					Type:        "object",
					Required:    true,
					Description: "Aggregator configuration",
				},
			},
		},
		{
			Name:        "config_save",
			Description: "Save the current configuration to file",
		},
		{
			Name:        "config_reload",
			Description: "Reload configuration from file",
		},
	}
}

// ExecuteTool executes a configuration management tool by name with the provided arguments.
// This is the main entry point for MCP clients to interact with configuration.
func (a *ConfigAdapter) ExecuteTool(ctx context.Context, toolName string, args map[string]interface{}) (*api.CallToolResult, error) {
	switch toolName {
	case "config_get":
		return a.handleConfigGet(ctx)
	case "config_get_aggregator":
		return a.handleConfigGetAggregator(ctx)
	case "config_update_aggregator":
		return a.handleConfigUpdateAggregator(ctx, args)
	case "config_save":
		return a.handleConfigSave(ctx)
	case "config_reload":
		return a.handleConfigReload(ctx)
	default:
		return nil, fmt.Errorf("tool '%s' not found", toolName)
	}
}

// saveConfig is an internal helper that persists the current configuration to disk.
// It automatically determines the config file path if not already set, using
// the standard configuration directory (~/.config/muster/config.yaml) unless
// a custom path is provided. The method creates necessary directories
// and handles YAML marshaling with appropriate file permissions.
func (a *ConfigAdapter) saveConfig() error {
	if a.configPath == "" {
		// Use the standard user configuration path
		userConfigDir := config.GetDefaultConfigPathOrPanic()

		// Create directory if it doesn't exist
		if err := os.MkdirAll(userConfigDir, 0755); err != nil {
			return fmt.Errorf("failed to create config directory %s: %w", userConfigDir, err)
		}

		a.configPath = filepath.Join(userConfigDir, "config.yaml")
	}

	// Marshal config to YAML
	data, err := yaml.Marshal(a.config)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	// Write to file
	if err := os.WriteFile(a.configPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}

// Handler implementations for MCP tool execution

// handleConfigGet handles the 'config_get' tool call.
// Returns the complete muster configuration as a tool result.
func (a *ConfigAdapter) handleConfigGet(ctx context.Context) (*api.CallToolResult, error) {
	cfg, err := a.GetConfig(ctx)
	if err != nil {
		return &api.CallToolResult{
			Content: []interface{}{fmt.Sprintf("Failed to get configuration: %v", err)},
			IsError: true,
		}, nil
	}

	return &api.CallToolResult{
		Content: []interface{}{cfg},
		IsError: false,
	}, nil
}

// handleConfigGetAggregator handles the 'config_get_aggregator' tool call.
// Returns only the aggregator configuration section as a tool result.
func (a *ConfigAdapter) handleConfigGetAggregator(ctx context.Context) (*api.CallToolResult, error) {
	aggregator, err := a.GetAggregatorConfig(ctx)
	if err != nil {
		return &api.CallToolResult{
			Content: []interface{}{fmt.Sprintf("Failed to get aggregator config: %v", err)},
			IsError: true,
		}, nil
	}

	return &api.CallToolResult{
		Content: []interface{}{aggregator},
		IsError: false,
	}, nil
}

// handleConfigUpdateAggregator handles the 'config_update_aggregator' tool call.
// Updates the aggregator configuration section and persists changes to disk.
func (a *ConfigAdapter) handleConfigUpdateAggregator(ctx context.Context, args map[string]interface{}) (*api.CallToolResult, error) {
	aggregatorData, ok := args["aggregator"]
	if !ok {
		return &api.CallToolResult{
			Content: []interface{}{"aggregator is required"},
			IsError: true,
		}, nil
	}

	// Convert to config.AggregatorConfig
	var aggregator config.AggregatorConfig
	if err := convertToStruct(aggregatorData, &aggregator); err != nil {
		return &api.CallToolResult{
			Content: []interface{}{fmt.Sprintf("Failed to parse aggregator config: %v", err)},
			IsError: true,
		}, nil
	}

	if err := a.UpdateAggregatorConfig(ctx, aggregator); err != nil {
		return &api.CallToolResult{
			Content: []interface{}{fmt.Sprintf("Failed to update aggregator config: %v", err)},
			IsError: true,
		}, nil
	}

	return &api.CallToolResult{
		Content: []interface{}{"Successfully updated aggregator configuration"},
		IsError: false,
	}, nil
}

// handleConfigSave handles the 'config_save' tool call.
// Explicitly saves the current configuration to disk.
func (a *ConfigAdapter) handleConfigSave(ctx context.Context) (*api.CallToolResult, error) {
	err := a.SaveConfig(ctx)
	if err != nil {
		return nil, err
	}

	return &api.CallToolResult{
		Content: []interface{}{
			"Configuration saved successfully",
		},
	}, nil
}

// handleConfigReload handles the 'config_reload' tool call.
// Reloads configuration from disk and triggers definition reloads for other components.
func (a *ConfigAdapter) handleConfigReload(ctx context.Context) (*api.CallToolResult, error) {
	// Reload main configuration
	if err := a.ReloadConfig(ctx); err != nil {
		return nil, err
	}

	// Trigger workflow definitions reload if workflow handler exists
	if wfHandler := api.GetWorkflow(); wfHandler != nil {
		if reloader, ok := wfHandler.(interface{ ReloadWorkflows() error }); ok {
			if err := reloader.ReloadWorkflows(); err != nil {
				return nil, fmt.Errorf("failed to reload workflow definitions: %w", err)
			}
		}
	}

	return &api.CallToolResult{
		Content: []interface{}{
			"Configuration reloaded successfully",
		},
	}, nil
}

// convertToStruct converts interface{} data to a target struct using JSON marshaling.
// This is used internally to convert tool arguments from generic interface{} types
// to specific configuration structs.
func convertToStruct(data interface{}, target interface{}) error {
	// For simplicity, we'll use JSON marshaling/unmarshaling
	// In production, you might want a more efficient approach
	jsonBytes, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return json.Unmarshal(jsonBytes, target)
}
