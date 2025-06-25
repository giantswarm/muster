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

// ConfigAdapter adapts the config system to implement api.ConfigHandler
type ConfigAdapter struct {
	config     *config.MusterConfig
	configPath string
	mu         sync.RWMutex
}

// NewConfigAdapter creates a new config adapter
func NewConfigAdapter(cfg *config.MusterConfig, configPath string) *ConfigAdapter {
	return &ConfigAdapter{
		config:     cfg,
		configPath: configPath,
	}
}

// Register registers the adapter with the API
func (a *ConfigAdapter) Register() {
	api.RegisterConfig(a)
}

// Get configuration
func (a *ConfigAdapter) GetConfig(ctx context.Context) (*config.MusterConfig, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if a.config == nil {
		return nil, fmt.Errorf("configuration not loaded")
	}
	return a.config, nil
}

func (a *ConfigAdapter) GetAggregatorConfig(ctx context.Context) (*config.AggregatorConfig, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if a.config == nil {
		return nil, fmt.Errorf("configuration not loaded")
	}
	return &a.config.Aggregator, nil
}

func (a *ConfigAdapter) GetGlobalSettings(ctx context.Context) (*config.GlobalSettings, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if a.config == nil {
		return nil, fmt.Errorf("configuration not loaded")
	}
	return &a.config.GlobalSettings, nil
}

// Update configuration
func (a *ConfigAdapter) UpdateAggregatorConfig(ctx context.Context, aggregator config.AggregatorConfig) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.config == nil {
		return fmt.Errorf("configuration not loaded")
	}

	a.config.Aggregator = aggregator
	return a.saveConfig()
}

func (a *ConfigAdapter) UpdateGlobalSettings(ctx context.Context, settings config.GlobalSettings) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.config == nil {
		return fmt.Errorf("configuration not loaded")
	}

	a.config.GlobalSettings = settings
	return a.saveConfig()
}

// Save configuration
func (a *ConfigAdapter) SaveConfig(ctx context.Context) error {
	return a.saveConfig()
}

// ReloadConfig reloads the configuration from disk
func (a *ConfigAdapter) ReloadConfig(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Load config using the centralized loader
	musterConfig, err := config.LoadConfig()
	if err != nil {
		return fmt.Errorf("failed to reload configuration: %w", err)
	}

	a.config = &musterConfig
	return nil
}

// GetTools returns all tools this provider offers
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
			Name:        "config_get_global_settings",
			Description: "Get global settings",
		},
		{
			Name:        "config_update_aggregator",
			Description: "Update aggregator configuration",
			Parameters: []api.ParameterMetadata{
				{
					Name:        "aggregator",
					Type:        "object",
					Required:    true,
					Description: "Aggregator configuration",
				},
			},
		},
		{
			Name:        "config_update_global_settings",
			Description: "Update global settings",
			Parameters: []api.ParameterMetadata{
				{
					Name:        "settings",
					Type:        "object",
					Required:    true,
					Description: "Global settings",
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

// ExecuteTool executes a tool by name
func (a *ConfigAdapter) ExecuteTool(ctx context.Context, toolName string, args map[string]interface{}) (*api.CallToolResult, error) {
	switch toolName {
	case "config_get":
		return a.handleConfigGet(ctx)
	case "config_get_aggregator":
		return a.handleConfigGetAggregator(ctx)
	case "config_get_global_settings":
		return a.handleConfigGetGlobalSettings(ctx)
	case "config_update_aggregator":
		return a.handleConfigUpdateAggregator(ctx, args)
	case "config_update_global_settings":
		return a.handleConfigUpdateGlobalSettings(ctx, args)
	case "config_save":
		return a.handleConfigSave(ctx)
	case "config_reload":
		return a.handleConfigReload(ctx)
	default:
		return nil, fmt.Errorf("tool '%s' not found", toolName)
	}
}

// Helper to save configuration
func (a *ConfigAdapter) saveConfig() error {
	if a.configPath == "" {
		// Try to determine the config path - check project dir first, then user dir
		projectPath, err := getProjectConfigPath()
		if err == nil {
			// Create directory if it doesn't exist
			dir := filepath.Dir(projectPath)
			if err := os.MkdirAll(dir, 0755); err == nil {
				a.configPath = projectPath
			}
		}

		// If we still don't have a path, try user config
		if a.configPath == "" {
			userPath, err := getUserConfigPath()
			if err == nil {
				// Create directory if it doesn't exist
				dir := filepath.Dir(userPath)
				if err := os.MkdirAll(dir, 0755); err == nil {
					a.configPath = userPath
				}
			}
		}

		if a.configPath == "" {
			return fmt.Errorf("unable to determine config file path")
		}
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

// Helper functions to get config paths
func getUserConfigPath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(homeDir, ".config", "muster", "config.yaml"), nil
}

func getProjectConfigPath() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Join(wd, ".muster", "config.yaml"), nil
}

// Handler implementations
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

func (a *ConfigAdapter) handleConfigGetGlobalSettings(ctx context.Context) (*api.CallToolResult, error) {
	settings, err := a.GetGlobalSettings(ctx)
	if err != nil {
		return &api.CallToolResult{
			Content: []interface{}{fmt.Sprintf("Failed to get global settings: %v", err)},
			IsError: true,
		}, nil
	}

	return &api.CallToolResult{
		Content: []interface{}{settings},
		IsError: false,
	}, nil
}

// Update handlers
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

func (a *ConfigAdapter) handleConfigUpdateGlobalSettings(ctx context.Context, args map[string]interface{}) (*api.CallToolResult, error) {
	settingsData, ok := args["settings"]
	if !ok {
		return &api.CallToolResult{
			Content: []interface{}{"settings is required"},
			IsError: true,
		}, nil
	}

	// Convert to config.GlobalSettings
	var settings config.GlobalSettings
	if err := convertToStruct(settingsData, &settings); err != nil {
		return &api.CallToolResult{
			Content: []interface{}{fmt.Sprintf("Failed to parse global settings: %v", err)},
			IsError: true,
		}, nil
	}

	if err := a.UpdateGlobalSettings(ctx, settings); err != nil {
		return &api.CallToolResult{
			Content: []interface{}{fmt.Sprintf("Failed to update global settings: %v", err)},
			IsError: true,
		}, nil
	}

	return &api.CallToolResult{
		Content: []interface{}{"Successfully updated global settings"},
		IsError: false,
	}, nil
}

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

func (a *ConfigAdapter) handleConfigReload(ctx context.Context) (*api.CallToolResult, error) {
	// Reload main configuration
	if err := a.ReloadConfig(ctx); err != nil {
		return nil, err
	}

	// Trigger capability definitions reload if capability handler exists
	if capHandler := api.GetCapability(); capHandler != nil {
		if reloader, ok := capHandler.(interface{ ReloadDefinitions() error }); ok {
			if err := reloader.ReloadDefinitions(); err != nil {
				return nil, fmt.Errorf("failed to reload capability definitions: %w", err)
			}
		}
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

// Helper function to convert interface{} to struct
func convertToStruct(data interface{}, target interface{}) error {
	// For simplicity, we'll use JSON marshaling/unmarshaling
	// In production, you might want a more efficient approach
	jsonBytes, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return json.Unmarshal(jsonBytes, target)
}
