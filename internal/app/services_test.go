package app

import (
	"muster/internal/config"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestInitializeServices(t *testing.T) {
	tests := []struct {
		name          string
		config        *Config
		expectError   bool
		checkServices func(*testing.T, *Services)
	}{
		{
			name: "basic initialization without aggregator",
			config: &Config{
				Debug: true,
				MusterConfig: &config.MusterConfig{
					Aggregator: config.AggregatorConfig{
						Enabled: false,
						Port:    0,
					},
				},
			},
			expectError: false,
			checkServices: func(t *testing.T, s *Services) {
				if s.Orchestrator == nil {
					t.Error("Orchestrator should not be nil")
				}
				if s.OrchestratorAPI == nil {
					t.Error("OrchestratorAPI should not be nil")
				}
				if s.ConfigAPI == nil {
					t.Error("ConfigAPI should not be nil")
				}
			},
		},
		{
			name: "initialization with aggregator",
			config: &Config{
				Debug: false,
				MusterConfig: &config.MusterConfig{
					Aggregator: config.AggregatorConfig{
						Enabled: true,
						Port:    8090,
						Host:    "localhost",
					},
				},
			},
			expectError: false,
			checkServices: func(t *testing.T, s *Services) {
				if s.AggregatorPort != 8090 {
					t.Errorf("AggregatorPort = %d, want 8090", s.AggregatorPort)
				}
			},
		},
		{
			name: "initialization with default aggregator port",
			config: &Config{
				Debug: false,
				MusterConfig: &config.MusterConfig{
					Aggregator: config.AggregatorConfig{
						Enabled: true,
						Port:    0, // Should default to 8080
						Host:    "",
					},
				},
			},
			expectError: false,
			checkServices: func(t *testing.T, s *Services) {
				// The aggregator port in Services will still be 0,
				// but the actual aggregator service will use 8080
				if s.AggregatorPort != 0 {
					t.Errorf("AggregatorPort in Services = %d, want 0", s.AggregatorPort)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			services, err := InitializeServices(tt.config)

			if tt.expectError && err == nil {
				t.Error("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			if !tt.expectError && tt.checkServices != nil {
				tt.checkServices(t, services)
			}
		})
	}
}

func TestInitializeServices_OrchestratorConfig(t *testing.T) {
	cfg := &Config{
		Debug: false,
		MusterConfig: &config.MusterConfig{
			Aggregator: config.AggregatorConfig{
				Port: 9090,
			},
		},
	}

	// We can't easily test the full initialization without mocking orchestrator.New
	// The orchestrator now gets MCPServers from MCPServerManager, not from config
	// This test verifies that the config structure is valid for services initialization
	services, err := InitializeServices(cfg)
	if err != nil {
		t.Fatalf("Failed to initialize services: %v", err)
	}

	// Verify that services were created successfully
	if services.Orchestrator == nil {
		t.Error("Orchestrator should not be nil")
	}
}

// Test that services are created
func TestServices_Creation(t *testing.T) {
	cfg := &Config{
		Debug: false,
		MusterConfig: &config.MusterConfig{
			Aggregator: config.AggregatorConfig{Enabled: false},
		},
	}

	services, err := InitializeServices(cfg)
	assert.NoError(t, err)

	// Test that services are created
	assert.NotNil(t, services.Orchestrator)
	assert.NotNil(t, services.OrchestratorAPI)
	assert.NotNil(t, services.ConfigAPI)
}
