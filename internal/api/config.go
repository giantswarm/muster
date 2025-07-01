package api

import (
	"context"
	"fmt"

	"muster/internal/config"
)

// ConfigServiceAPI defines the interface for managing configuration at runtime.
// This interface provides a higher-level abstraction over the ConfigHandler for
// configuration management operations, including retrieval, updates, and persistence.
//
// The ConfigServiceAPI is designed for direct use by components that need configuration
// management capabilities, while ConfigHandler is used in the Service Locator Pattern.
//
// Example usage:
//
//	configAPI := api.NewConfigServiceAPI()
//
//	// Get current configuration
//	cfg, err := configAPI.GetConfig(ctx)
//	if err != nil {
//	    return fmt.Errorf("failed to get config: %w", err)
//	}
//
//	// Update aggregator settings
//	cfg.Aggregator.Port = 8080
//	err = configAPI.UpdateAggregatorConfig(ctx, cfg.Aggregator)
//	if err != nil {
//	    return fmt.Errorf("failed to update config: %w", err)
//	}
//
//	// Persist changes to disk
//	err = configAPI.SaveConfig(ctx)
//	if err != nil {
//	    return fmt.Errorf("failed to save config: %w", err)
//	}
type ConfigAPI interface {
	// Configuration retrieval methods

	// GetConfig returns the entire muster configuration including all sections.
	// This provides access to the complete configuration state for comprehensive
	// configuration management scenarios.
	//
	// Args:
	//   - ctx: Context for the operation, including cancellation and timeout
	//
	// Returns:
	//   - *config.MusterConfig: The complete system configuration
	//   - error: Error if configuration cannot be retrieved or parsed
	//
	// Example:
	//
	//	cfg, err := configAPI.GetConfig(ctx)
	//	if err != nil {
	//	    return fmt.Errorf("configuration unavailable: %w", err)
	//	}
	//	fmt.Printf("System listening on port: %d\n", cfg.Aggregator.Port)
	GetConfig(ctx context.Context) (*config.MusterConfig, error)

	// Configuration section retrieval methods

	// GetAggregatorConfig returns only the aggregator configuration section.
	// This is useful when only aggregator-specific settings are needed,
	// avoiding the overhead of retrieving the entire configuration.
	//
	// Args:
	//   - ctx: Context for the operation
	//
	// Returns:
	//   - *config.AggregatorConfig: The aggregator configuration section
	//   - error: Error if configuration cannot be retrieved
	//
	// Example:
	//
	//	aggConfig, err := configAPI.GetAggregatorConfig(ctx)
	//	if err != nil {
	//	    return err
	//	}
	//	fmt.Printf("Aggregator endpoint: %s:%d\n", aggConfig.Host, aggConfig.Port)
	GetAggregatorConfig(ctx context.Context) (*config.AggregatorConfig, error)

	// GetGlobalSettings returns the global system settings section.
	// These settings affect the behavior of the entire muster system.
	//
	// Args:
	//   - ctx: Context for the operation
	//
	// Returns:
	//   - *config.GlobalSettings: The global settings configuration
	//   - error: Error if configuration cannot be retrieved
	//
	// Example:
	//
	//	globals, err := configAPI.GetGlobalSettings(ctx)
	//	if err != nil {
	//	    return err
	//	}
	//	fmt.Printf("Log level: %s\n", globals.LogLevel)
	GetGlobalSettings(ctx context.Context) (*config.GlobalSettings, error)

	// Configuration update methods

	// UpdateAggregatorConfig updates the aggregator configuration section.
	// Changes take effect immediately but are not persisted until SaveConfig is called.
	//
	// Args:
	//   - ctx: Context for the operation
	//   - aggregator: The new aggregator configuration to apply
	//
	// Returns:
	//   - error: Error if the update fails or configuration is invalid
	//
	// Note: Changes are not persisted to disk until SaveConfig is called.
	//
	// Example:
	//
	//	aggConfig.Port = 9090
	//	err := configAPI.UpdateAggregatorConfig(ctx, aggConfig)
	//	if err != nil {
	//	    return fmt.Errorf("failed to update aggregator config: %w", err)
	//	}
	//	// Don't forget to save!
	//	err = configAPI.SaveConfig(ctx)
	UpdateAggregatorConfig(ctx context.Context, aggregator config.AggregatorConfig) error

	// UpdateGlobalSettings updates the global system settings.
	// Changes take effect immediately but are not persisted until SaveConfig is called.
	//
	// Args:
	//   - ctx: Context for the operation
	//   - settings: The new global settings to apply
	//
	// Returns:
	//   - error: Error if the update fails or settings are invalid
	//
	// Note: Changes are not persisted to disk until SaveConfig is called.
	//
	// Example:
	//
	//	settings.LogLevel = "debug"
	//	err := configAPI.UpdateGlobalSettings(ctx, settings)
	//	if err != nil {
	//	    return fmt.Errorf("failed to update global settings: %w", err)
	//	}
	//	err = configAPI.SaveConfig(ctx) // Persist changes
	UpdateGlobalSettings(ctx context.Context, settings config.GlobalSettings) error

	// Configuration persistence methods

	// SaveConfig persists the current in-memory configuration to disk.
	// This makes all pending configuration changes permanent and updates
	// the configuration files on disk.
	//
	// Args:
	//   - ctx: Context for the operation
	//
	// Returns:
	//   - error: Error if the configuration cannot be saved to disk
	//
	// Note: This operation is atomic - either all changes are saved or none are.
	//
	// Example:
	//
	//	// After making configuration changes...
	//	err := configAPI.SaveConfig(ctx)
	//	if err != nil {
	//	    return fmt.Errorf("failed to persist configuration: %w", err)
	//	}
	//	fmt.Println("Configuration saved successfully")
	SaveConfig(ctx context.Context) error

	// ReloadConfig reloads the configuration from disk, discarding any unsaved changes.
	// This is useful for reverting to the last saved configuration state or
	// picking up external changes to configuration files.
	//
	// Args:
	//   - ctx: Context for the operation
	//
	// Returns:
	//   - error: Error if the configuration cannot be reloaded from disk
	//
	// Warning: This operation discards all unsaved in-memory changes.
	//
	// Example:
	//
	//	// Revert to last saved configuration
	//	err := configAPI.ReloadConfig(ctx)
	//	if err != nil {
	//	    return fmt.Errorf("failed to reload configuration: %w", err)
	//	}
	//	fmt.Println("Configuration reloaded from disk")
	ReloadConfig(ctx context.Context) error
}

// configAPI implements the ConfigServiceAPI interface.
// This is a thin wrapper around the ConfigHandler that provides
// a direct interface for configuration management operations.
//
// The implementation delegates all operations to the registered
// ConfigHandler through the Service Locator Pattern.
type configAPI struct {
	// No fields - uses handlers from registry through Service Locator Pattern
}

// NewConfigServiceAPI creates a new ConfigServiceAPI instance.
// This function returns a ready-to-use configuration API that delegates
// operations to the registered ConfigHandler.
//
// Returns:
//   - ConfigAPI: A new configuration API instance
//
// Note: The returned API will only work if a ConfigHandler has been
// registered through the Service Locator Pattern. Operations will fail
// with appropriate error messages if no handler is available.
//
// Example:
//
//	configAPI := api.NewConfigServiceAPI()
//	// Use the API for configuration operations...
func NewConfigServiceAPI() ConfigAPI {
	return &configAPI{}
}

// ConfigHandler provides configuration management functionality within the Service Locator Pattern.
// This handler is the primary interface for runtime configuration management, including
// configuration retrieval, updates, and persistence operations.
//
// The ConfigHandler abstracts the underlying configuration storage and management,
// allowing components to manage configuration without knowing the specific
// implementation details of how configuration is stored or processed.
//
// Key features:
// - Runtime configuration updates without system restart
// - Atomic configuration persistence to prevent partial updates
// - Configuration validation to ensure system stability
// - Section-specific access for performance optimization
//
// Thread-safety: All methods are safe for concurrent use.
type ConfigHandler interface {
	// Configuration retrieval methods

	// GetConfig returns the entire system configuration.
	// This provides access to all configuration sections including
	// aggregator settings, global settings, and any other configured components.
	//
	// Args:
	//   - ctx: Context for the operation, including cancellation and timeout
	//
	// Returns:
	//   - *config.MusterConfig: The complete system configuration
	//   - error: Error if configuration cannot be retrieved or is corrupted
	//
	// Example:
	//
	//	cfg, err := handler.GetConfig(ctx)
	//	if err != nil {
	//	    return fmt.Errorf("configuration error: %w", err)
	//	}
	//	// Access any configuration section
	//	fmt.Printf("System has %d MCP servers configured\n", len(cfg.MCPServers))
	GetConfig(ctx context.Context) (*config.MusterConfig, error)

	// GetAggregatorConfig returns the aggregator-specific configuration section.
	// This method is optimized for cases where only aggregator settings are needed.
	//
	// Args:
	//   - ctx: Context for the operation
	//
	// Returns:
	//   - *config.AggregatorConfig: The aggregator configuration
	//   - error: Error if configuration cannot be retrieved
	GetAggregatorConfig(ctx context.Context) (*config.AggregatorConfig, error)

	// GetGlobalSettings returns the global system settings.
	// These settings control system-wide behavior like logging, timeouts, and defaults.
	//
	// Args:
	//   - ctx: Context for the operation
	//
	// Returns:
	//   - *config.GlobalSettings: The global settings
	//   - error: Error if configuration cannot be retrieved
	GetGlobalSettings(ctx context.Context) (*config.GlobalSettings, error)

	// Configuration update methods

	// UpdateAggregatorConfig updates the aggregator configuration section.
	// The new configuration is validated before being applied to ensure system stability.
	//
	// Args:
	//   - ctx: Context for the operation
	//   - aggregator: The new aggregator configuration to apply
	//
	// Returns:
	//   - error: Error if the update fails, validation fails, or configuration is invalid
	//
	// Note: Changes are applied immediately but not persisted until SaveConfig is called.
	UpdateAggregatorConfig(ctx context.Context, aggregator config.AggregatorConfig) error

	// UpdateGlobalSettings updates the global system settings.
	// Settings are validated for consistency and correctness before being applied.
	//
	// Args:
	//   - ctx: Context for the operation
	//   - settings: The new global settings to apply
	//
	// Returns:
	//   - error: Error if the update fails, validation fails, or settings are invalid
	//
	// Note: Changes are applied immediately but not persisted until SaveConfig is called.
	UpdateGlobalSettings(ctx context.Context, settings config.GlobalSettings) error

	// Configuration persistence methods

	// SaveConfig persists the current configuration to disk.
	// This operation is atomic - either all changes are saved successfully
	// or no changes are made to the persistent storage.
	//
	// Args:
	//   - ctx: Context for the operation
	//
	// Returns:
	//   - error: Error if the configuration cannot be saved, disk is full, or permissions are insufficient
	//
	// Example:
	//
	//	err := handler.SaveConfig(ctx)
	//	if err != nil {
	//	    return fmt.Errorf("configuration persistence failed: %w", err)
	//	}
	SaveConfig(ctx context.Context) error

	// ReloadConfig reloads the configuration from disk, discarding any unsaved changes.
	// This is useful for reverting to the last known good configuration or
	// picking up external configuration changes.
	//
	// Args:
	//   - ctx: Context for the operation
	//
	// Returns:
	//   - error: Error if the configuration cannot be reloaded, files are corrupted, or parsing fails
	//
	// Warning: This operation discards all unsaved in-memory configuration changes.
	ReloadConfig(ctx context.Context) error

	// ToolProvider integration for exposing configuration management as discoverable MCP tools.
	// This allows configuration operations to be invoked through the standard
	// tool discovery and execution mechanisms, enabling external configuration management.
	ToolProvider
}

// Configuration API implementation methods

// GetConfig returns the entire configuration through the registered ConfigHandler.
// This method delegates to the handler retrieved from the Service Locator registry.
func (c *configAPI) GetConfig(ctx context.Context) (*config.MusterConfig, error) {
	handler := GetConfigHandler()
	if handler == nil {
		return nil, fmt.Errorf("config handler not registered")
	}
	return handler.GetConfig(ctx)
}

// GetAggregatorConfig returns the aggregator configuration through the registered ConfigHandler.
func (c *configAPI) GetAggregatorConfig(ctx context.Context) (*config.AggregatorConfig, error) {
	handler := GetConfigHandler()
	if handler == nil {
		return nil, fmt.Errorf("config handler not registered")
	}
	return handler.GetAggregatorConfig(ctx)
}

// GetGlobalSettings returns the global settings through the registered ConfigHandler.
func (c *configAPI) GetGlobalSettings(ctx context.Context) (*config.GlobalSettings, error) {
	handler := GetConfigHandler()
	if handler == nil {
		return nil, fmt.Errorf("config handler not registered")
	}
	return handler.GetGlobalSettings(ctx)
}

// UpdateAggregatorConfig updates the aggregator configuration through the registered ConfigHandler.
func (c *configAPI) UpdateAggregatorConfig(ctx context.Context, aggregator config.AggregatorConfig) error {
	handler := GetConfigHandler()
	if handler == nil {
		return fmt.Errorf("config handler not registered")
	}
	return handler.UpdateAggregatorConfig(ctx, aggregator)
}

// UpdateGlobalSettings updates the global settings through the registered ConfigHandler.
func (c *configAPI) UpdateGlobalSettings(ctx context.Context, settings config.GlobalSettings) error {
	handler := GetConfigHandler()
	if handler == nil {
		return fmt.Errorf("config handler not registered")
	}
	return handler.UpdateGlobalSettings(ctx, settings)
}

// SaveConfig persists the configuration through the registered ConfigHandler.
func (c *configAPI) SaveConfig(ctx context.Context) error {
	handler := GetConfigHandler()
	if handler == nil {
		return fmt.Errorf("config handler not registered")
	}
	return handler.SaveConfig(ctx)
}

// ReloadConfig reloads the configuration through the registered ConfigHandler.
func (c *configAPI) ReloadConfig(ctx context.Context) error {
	handler := GetConfigHandler()
	if handler == nil {
		return fmt.Errorf("config handler not registered")
	}
	return handler.ReloadConfig(ctx)
}
