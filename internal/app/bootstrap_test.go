package app

import (
	"muster/internal/config"
	"testing"
)

// Note: Testing NewApplication fully requires mocking global dependencies
// which is not easily done in Go. These tests focus on the testable parts
// of the application structure and configuration validation.

func TestNewApplication_ConfigValidation(t *testing.T) {
	// Test that config structure is properly validated
	tests := []struct {
		name        string
		cfg         *Config
		expectError bool
		errorReason string
	}{
		{
			name: "valid config structure",
			cfg: &Config{
				NoTUI: false,
				Debug: true,
				// Pre-populate MusterConfig to avoid LoadConfig call
				MusterConfig: &config.MusterConfig{
					Aggregator: config.AggregatorConfig{
						Port:    8090,
						Host:    "localhost",
						Enabled: false,
					},
				},
			},
			expectError: false,
			errorReason: "valid config should succeed",
		},
		{
			name: "no-tui config",
			cfg: &Config{
				NoTUI: true,
				Debug: false,
				// Pre-populate MusterConfig to avoid LoadConfig call
				MusterConfig: &config.MusterConfig{
					Aggregator: config.AggregatorConfig{
						Port:    8090,
						Host:    "localhost",
						Enabled: false,
					},
				},
			},
			expectError: false,
			errorReason: "no-tui config should work",
		},
		{
			name: "minimal config",
			cfg: &Config{
				NoTUI: true,
				Debug: false,
				// Pre-populate MusterConfig to avoid LoadConfig call
				MusterConfig: &config.MusterConfig{
					Aggregator: config.AggregatorConfig{
						Port:    8090,
						Host:    "localhost",
						Enabled: false,
					},
				},
			},
			expectError: false,
			errorReason: "minimal config should work",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app, err := NewApplication(tt.cfg)

			if tt.expectError && err == nil {
				t.Errorf("Expected error (%s) but got none", tt.errorReason)
			}
			if !tt.expectError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			// App should not be nil when there's no error
			if !tt.expectError && app == nil {
				t.Error("App should not be nil when NewApplication succeeds")
			}
			if tt.expectError && app != nil {
				t.Error("App should be nil when NewApplication fails")
			}

			// Verify the config is properly set if app was created
			if app != nil {
				if app.config.NoTUI != tt.cfg.NoTUI {
					t.Errorf("NoTUI = %v, want %v", app.config.NoTUI, tt.cfg.NoTUI)
				}
				if app.config.Debug != tt.cfg.Debug {
					t.Errorf("Debug = %v, want %v", app.config.Debug, tt.cfg.Debug)
				}
			}
		})
	}
}

func TestApplication_Structure(t *testing.T) {
	// Test that the application structure is properly set up
	cfg := &Config{
		NoTUI: false,
		Debug: true,
		MusterConfig: &config.MusterConfig{
			Aggregator: config.AggregatorConfig{
				Port:    8090,
				Host:    "localhost",
				Enabled: false,
			},
		},
	}

	services := &Services{
		AggregatorPort: 8090,
	}

	app := &Application{
		config:   cfg,
		services: services,
	}

	// Verify application fields
	if app.config != cfg {
		t.Error("Application config not set correctly")
	}
	if app.services != services {
		t.Error("Application services not set correctly")
	}
}

func TestApplication_ModeSelection(t *testing.T) {
	tests := []struct {
		name      string
		noTUI     bool
		expectTUI bool
	}{
		{
			name:      "run CLI mode",
			noTUI:     true,
			expectTUI: false,
		},
		{
			name:      "run TUI mode",
			noTUI:     false,
			expectTUI: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				NoTUI: tt.noTUI,
				MusterConfig: &config.MusterConfig{
					Aggregator: config.AggregatorConfig{
						Port:    8090,
						Host:    "localhost",
						Enabled: false,
					},
				},
			}

			app := &Application{
				config: cfg,
				services: &Services{
					AggregatorPort: 8090,
				},
			}

			// Verify that the correct mode would be selected
			if app.config.NoTUI != tt.noTUI {
				t.Errorf("config.NoTUI = %v, want %v", app.config.NoTUI, tt.noTUI)
			}

			// Test mode selection logic
			shouldUseTUI := !app.config.NoTUI

			if tt.expectTUI && !shouldUseTUI {
				t.Error("Expected TUI mode to be selected")
			}
			if !tt.expectTUI && shouldUseTUI {
				t.Error("Did not expect TUI mode to be selected")
			}
		})
	}
}

func TestConfig_WithMusterConfig(t *testing.T) {
	// Test configuration with muster config
	cfg := &Config{
		NoTUI: true,
		Debug: false,
		MusterConfig: &config.MusterConfig{
			Aggregator: config.AggregatorConfig{
				Port:    9090,
				Host:    "0.0.0.0",
				Enabled: true,
			},
		},
	}

	// Verify configuration is accessible
	if cfg.MusterConfig.Aggregator.Port != 9090 {
		t.Errorf("Expected aggregator port 9090, got %d", cfg.MusterConfig.Aggregator.Port)
	}
}

func TestConfigureLogging(t *testing.T) {
	// Test logging configuration based on debug flag
	tests := []struct {
		name  string
		debug bool
	}{
		{
			name:  "debug logging enabled",
			debug: true,
		},
		{
			name:  "info logging enabled",
			debug: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				Debug: tt.debug,
				// Pre-populate MusterConfig to avoid LoadConfig call
				MusterConfig: &config.MusterConfig{
					Aggregator: config.AggregatorConfig{
						Port:    8090,
						Host:    "localhost",
						Enabled: false,
					},
				},
			}

			// Verify debug flag is set correctly
			if cfg.Debug != tt.debug {
				t.Errorf("Debug flag = %v, want %v", cfg.Debug, tt.debug)
			}

			// Test that NewApplication can be called with this config
			app, err := NewApplication(cfg)

			// The application should be created successfully
			if err != nil {
				t.Errorf("Unexpected error creating application: %v", err)
			}

			if app == nil {
				t.Error("Application should not be nil")
			}

			// Verify the debug setting is preserved
			if app != nil && app.config.Debug != tt.debug {
				t.Errorf("Application debug setting = %v, want %v", app.config.Debug, tt.debug)
			}
		})
	}
}
