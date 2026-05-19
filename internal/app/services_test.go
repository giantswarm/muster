package app

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/giantswarm/muster/internal/config"
)

func TestInitializeServices(t *testing.T) {
	tests := []struct {
		name          string
		config        *Config
		expectError   bool
		checkServices func(*testing.T, *Services)
	}{
		{
			name: "default port",
			config: &Config{
				Debug: true,
				MusterConfig: &config.MusterConfig{
					Aggregator: config.AggregatorConfig{Port: 0},
				},
				ConfigPath: config.GetDefaultConfigPathOrPanic(),
			},
			expectError: false,
			checkServices: func(t *testing.T, s *Services) {
				assert.NotNil(t, s.AggregatorManager)
				assert.NotNil(t, s.ReconcileManager)
			},
		},
		{
			name: "explicit port",
			config: &Config{
				Debug: false,
				MusterConfig: &config.MusterConfig{
					Aggregator: config.AggregatorConfig{Port: 8090, Host: "localhost"},
				},
				ConfigPath: config.GetDefaultConfigPathOrPanic(),
			},
			expectError: false,
			checkServices: func(t *testing.T, s *Services) {
				assert.Equal(t, 8090, s.AggregatorPort)
				assert.NotNil(t, s.AggregatorManager)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			services, err := InitializeServices(tt.config)
			if tt.expectError {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			if tt.checkServices != nil {
				tt.checkServices(t, services)
			}
		})
	}
}
