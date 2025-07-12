package serviceclass

import (
	"testing"

	"muster/internal/api"
)

// TestAdapterCreation tests basic adapter creation
func TestNewAdapterWithClient(t *testing.T) {
	// Test with empty namespace should default to "default"
	adapter := NewAdapterWithClient(nil, "")
	if adapter.namespace != "default" {
		t.Errorf("Expected default namespace 'default', got %q", adapter.namespace)
	}

	// Test with specific namespace
	adapter = NewAdapterWithClient(nil, "test-namespace")
	if adapter.namespace != "test-namespace" {
		t.Errorf("Expected namespace 'test-namespace', got %q", adapter.namespace)
	}
}

// TestExtractRequiredTools tests the tool extraction logic
func TestExtractRequiredTools(t *testing.T) {
	tests := []struct {
		name          string
		serviceClass  *api.ServiceClass
		expectedTools []string
	}{
		{
			name: "basic tools",
			serviceClass: &api.ServiceClass{
				ServiceConfig: api.ServiceConfig{
					LifecycleTools: api.LifecycleTools{
						Start: api.ToolCall{Tool: "docker_run"},
						Stop:  api.ToolCall{Tool: "docker_stop"},
					},
				},
			},
			expectedTools: []string{"docker_run", "docker_stop"},
		},
		{
			name: "with optional tools",
			serviceClass: &api.ServiceClass{
				ServiceConfig: api.ServiceConfig{
					LifecycleTools: api.LifecycleTools{
						Start:   api.ToolCall{Tool: "docker_run"},
						Stop:    api.ToolCall{Tool: "docker_stop"},
						Restart: &api.ToolCall{Tool: "docker_restart"},
						Status:  &api.ToolCall{Tool: "docker_status"},
						HealthCheck: &api.HealthCheckToolCall{
							Tool: "health_check",
						},
					},
				},
			},
			expectedTools: []string{"docker_run", "docker_stop", "docker_restart", "health_check", "docker_status"},
		},
		{
			name: "empty tools",
			serviceClass: &api.ServiceClass{
				ServiceConfig: api.ServiceConfig{
					LifecycleTools: api.LifecycleTools{
						Start: api.ToolCall{Tool: ""},
						Stop:  api.ToolCall{Tool: ""},
					},
				},
			},
			expectedTools: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adapter := &Adapter{}
			result := adapter.extractRequiredTools(tt.serviceClass)

			if len(result) != len(tt.expectedTools) {
				t.Errorf("Expected %d tools, got %d", len(tt.expectedTools), len(result))
			}

			// Check that all expected tools are present
			for _, expectedTool := range tt.expectedTools {
				found := false
				for _, actualTool := range result {
					if actualTool == expectedTool {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("Expected tool %q not found in result", expectedTool)
				}
			}
		})
	}
}

// TestContains tests the helper function
func TestContains(t *testing.T) {
	tests := []struct {
		name     string
		slice    []string
		item     string
		expected bool
	}{
		{
			name:     "item exists",
			slice:    []string{"a", "b", "c"},
			item:     "b",
			expected: true,
		},
		{
			name:     "item does not exist",
			slice:    []string{"a", "b", "c"},
			item:     "d",
			expected: false,
		},
		{
			name:     "empty slice",
			slice:    []string{},
			item:     "a",
			expected: false,
		},
		{
			name:     "nil slice",
			slice:    nil,
			item:     "a",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := contains(tt.slice, tt.item)
			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}

// TestIsServiceConfigEmpty tests the empty check function
func TestIsServiceConfigEmpty(t *testing.T) {
	tests := []struct {
		name     string
		config   api.ServiceConfig
		expected bool
	}{
		{
			name:     "empty config",
			config:   api.ServiceConfig{},
			expected: true,
		},
		{
			name: "config with default name",
			config: api.ServiceConfig{
				DefaultName: "test-{{.name}}",
			},
			expected: false,
		},
		{
			name: "config with dependencies",
			config: api.ServiceConfig{
				Dependencies: []string{"postgres"},
			},
			expected: false,
		},
		{
			name: "config with lifecycle tools",
			config: api.ServiceConfig{
				LifecycleTools: api.LifecycleTools{
					Start: api.ToolCall{Tool: "docker_run"},
				},
			},
			expected: false,
		},
		{
			name: "config with stop tool",
			config: api.ServiceConfig{
				LifecycleTools: api.LifecycleTools{
					Stop: api.ToolCall{Tool: "docker_stop"},
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isServiceConfigEmpty(tt.config)
			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}

// TestIsToolAvailable tests the tool availability check
func TestIsToolAvailable(t *testing.T) {
	tests := []struct {
		name           string
		tool           string
		availableTools []string
		expected       bool
	}{
		{
			name:           "core tool always available",
			tool:           "core_service_create",
			availableTools: []string{},
			expected:       true,
		},
		{
			name:           "external tool available",
			tool:           "x_docker_run",
			availableTools: []string{"x_docker_run", "x_docker_stop"},
			expected:       true,
		},
		{
			name:           "external tool not available",
			tool:           "x_missing_tool",
			availableTools: []string{"x_docker_run", "x_docker_stop"},
			expected:       false,
		},
		{
			name:           "empty available tools",
			tool:           "x_some_tool",
			availableTools: []string{},
			expected:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adapter := &Adapter{}
			result := adapter.isToolAvailable(tt.tool, tt.availableTools)
			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}
