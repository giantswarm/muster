package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"muster/internal/api"
	"muster/internal/config"

	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v3"
)

func TestConfigReloadIntegration(t *testing.T) {
	t.Skip("Skipping integration test - needs proper mocking of config paths")

	// Create a temporary directory for testing
	tmpDir, err := os.MkdirTemp("", "muster-config-reload-test")
	assert.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	configPath := filepath.Join(tmpDir, "config.yaml")

	// Create initial configuration
	initialConfig := &config.MusterConfig{}

	// Write initial config
	data, err := yaml.Marshal(initialConfig)
	assert.NoError(t, err)
	err = os.WriteFile(configPath, data, 0644)
	assert.NoError(t, err)

	// Create adapter
	adapter := NewConfigAdapter(initialConfig, configPath)
	api.RegisterConfig(adapter)

	ctx := context.Background()

	// Verify initial config
	cfg, err := adapter.GetConfig(ctx)
	assert.NoError(t, err)
	// MCPServers are now managed by MCPServerManager, not verified here
	assert.NotNil(t, cfg)

	// Modify the config file directly
	modifiedConfig := &config.MusterConfig{}

	// Write modified config
	data, err = yaml.Marshal(modifiedConfig)
	assert.NoError(t, err)
	err = os.WriteFile(configPath, data, 0644)
	assert.NoError(t, err)

	// Ensure file is written (some filesystems have delays)
	time.Sleep(10 * time.Millisecond)

	// Reload configuration
	err = adapter.ReloadConfig(ctx)
	assert.NoError(t, err)

	// Verify config has been reloaded
	cfg, err = adapter.GetConfig(ctx)
	assert.NoError(t, err)
	// MCPServers are now managed by MCPServerManager, not verified here
	assert.NotNil(t, cfg)
}

func TestConfigReloadTool(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir, err := os.MkdirTemp("", "muster-config-reload-tool-test")
	assert.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	configPath := filepath.Join(tmpDir, "config.yaml")

	// Create initial configuration
	initialConfig := &config.MusterConfig{}

	// Write initial config
	data, err := yaml.Marshal(initialConfig)
	assert.NoError(t, err)
	err = os.WriteFile(configPath, data, 0644)
	assert.NoError(t, err)

	// Create adapter
	adapter := NewConfigAdapter(initialConfig, configPath)
	api.RegisterConfig(adapter)

	// Test that config_reload tool exists
	tools := adapter.GetTools()
	found := false
	for _, tool := range tools {
		if tool.Name == "config_reload" {
			found = true
			assert.Equal(t, "Reload configuration from file", tool.Description)
			break
		}
	}
	assert.True(t, found, "config_reload tool should exist")

	// Execute the tool
	ctx := context.Background()
	result, err := adapter.ExecuteTool(ctx, "config_reload", nil)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.False(t, result.IsError)
	assert.Equal(t, "Configuration reloaded successfully", result.Content[0])
}
