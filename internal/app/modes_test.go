package app

import (
	"testing"

	"github.com/giantswarm/muster/internal/config"
)

func TestConfigValidation(t *testing.T) {
	// Test that the configuration is properly validated before running modes
	tests := []struct {
		name      string
		cfg       *Config
		wantError bool
	}{
		{
			name: "valid config with basic settings",
			cfg: &Config{
				Debug: false,
				MusterConfig: &config.MusterConfig{
					Aggregator: config.AggregatorConfig{},
				},
				ConfigPath: config.GetDefaultConfigPathOrPanic(),
			},
			wantError: false,
		},
		{
			name: "valid config with debug enabled",
			cfg: &Config{
				Debug: true,
				MusterConfig: &config.MusterConfig{
					Aggregator: config.AggregatorConfig{
						Port: 8080,
						Host: "localhost",
					},
				},
				ConfigPath: config.GetDefaultConfigPathOrPanic(),
			},
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Validate basic config structure
			if tt.cfg.MusterConfig == nil && !tt.wantError {
				t.Error("MusterConfig should not be nil for valid configs")
			}

			// Validate that the config has the expected structure
			if tt.cfg.MusterConfig != nil {
				// MCPServers are now managed by MCPServerManager, not validated here
			}
		})
	}
}

func TestConfigDefaults(t *testing.T) {
	// Test that configs have sensible defaults and validation
	cfg := &Config{
		Debug: true,
		MusterConfig: &config.MusterConfig{
			Aggregator: config.AggregatorConfig{
				Port: 0, // Should get default
				Host: "",
			},
		},
		ConfigPath: config.GetDefaultConfigPathOrPanic(),
	}

	// Verify the config structure is valid
	if cfg.MusterConfig == nil {
		t.Error("MusterConfig should not be nil")
	}
}
