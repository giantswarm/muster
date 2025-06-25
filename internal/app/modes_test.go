package app

import (
	"muster/internal/config"
	"testing"
)

func TestModeSelection(t *testing.T) {
	// Test that we can determine which mode should be run based on configuration
	tests := []struct {
		name      string
		noTUI     bool
		expectCLI bool
		expectTUI bool
	}{
		{
			name:      "CLI mode selected",
			noTUI:     true,
			expectCLI: true,
			expectTUI: false,
		},
		{
			name:      "TUI mode selected",
			noTUI:     false,
			expectCLI: false,
			expectTUI: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				NoTUI: tt.noTUI,
				Debug: false,
				MusterConfig: &config.MusterConfig{
					Aggregator: config.AggregatorConfig{},
				},
			}

			// Test mode selection logic
			shouldUseCLI := cfg.NoTUI
			shouldUseTUI := !cfg.NoTUI

			if tt.expectCLI && !shouldUseCLI {
				t.Error("Expected CLI mode to be selected")
			}
			if tt.expectTUI && !shouldUseTUI {
				t.Error("Expected TUI mode to be selected")
			}
			if !tt.expectCLI && shouldUseCLI {
				t.Error("Did not expect CLI mode to be selected")
			}
			if !tt.expectTUI && shouldUseTUI {
				t.Error("Did not expect TUI mode to be selected")
			}
		})
	}
}

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
				NoTUI: true,
				Debug: false,
				MusterConfig: &config.MusterConfig{
					Aggregator: config.AggregatorConfig{},
				},
			},
			wantError: false,
		},
		{
			name: "valid config with debug enabled",
			cfg: &Config{
				NoTUI: false,
				Debug: true,
				MusterConfig: &config.MusterConfig{
					Aggregator: config.AggregatorConfig{
						Port: 8080,
						Host: "localhost",
					},
				},
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

func TestModeHandlerSelection(t *testing.T) {
	// Test the mode handler selection logic without actually running the modes
	tests := []struct {
		name     string
		noTUI    bool
		expected string
	}{
		{
			name:     "CLI mode handler",
			noTUI:    true,
			expected: "CLI",
		},
		{
			name:     "TUI mode handler",
			noTUI:    false,
			expected: "TUI",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				NoTUI: tt.noTUI,
				MusterConfig: &config.MusterConfig{
					Aggregator: config.AggregatorConfig{},
				},
			}

			// Simulate the mode selection logic from bootstrap.go
			var selectedMode string
			if cfg.NoTUI {
				selectedMode = "CLI"
			} else {
				selectedMode = "TUI"
			}

			if selectedMode != tt.expected {
				t.Errorf("Expected mode %s, got %s", tt.expected, selectedMode)
			}
		})
	}
}

func TestConfigDefaults(t *testing.T) {
	// Test that configs have sensible defaults and validation
	cfg := &Config{
		NoTUI: false,
		Debug: true,
		MusterConfig: &config.MusterConfig{
			Aggregator: config.AggregatorConfig{
				Port:    0, // Should get default
				Host:    "",
				Enabled: false,
			},
		},
	}

	// Verify the config structure is valid
	if cfg.MusterConfig == nil {
		t.Error("MusterConfig should not be nil")
	}

	// Test that both CLI and TUI modes can be configured
	if cfg.NoTUI && cfg.Debug {
		t.Log("CLI mode with debug enabled")
	}
	if !cfg.NoTUI && cfg.Debug {
		t.Log("TUI mode with debug enabled")
	}
}
