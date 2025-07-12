package serviceclass

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"muster/internal/api"
	musterv1alpha1 "muster/pkg/apis/muster/v1alpha1"
)

func TestConvertCRDToServiceClass(t *testing.T) {
	tests := []struct {
		name     string
		input    *musterv1alpha1.ServiceClass
		expected api.ServiceClass
	}{
		{
			name: "basic conversion",
			input: &musterv1alpha1.ServiceClass{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-service",
				},
				Spec: musterv1alpha1.ServiceClassSpec{
					Description: "Test service",
					ServiceConfig: musterv1alpha1.ServiceConfig{
						DefaultName:  "test-{{.name}}",
						Dependencies: []string{"dep1", "dep2"},
						LifecycleTools: musterv1alpha1.LifecycleTools{
							Start: musterv1alpha1.ToolCall{
								Tool: "docker_run",
								Args: map[string]*runtime.RawExtension{
									"image": {Raw: []byte(`"nginx:latest"`)},
								},
								Outputs: map[string]string{
									"container_id": "result.id",
								},
							},
							Stop: musterv1alpha1.ToolCall{
								Tool: "docker_stop",
								Args: map[string]*runtime.RawExtension{
									"container_id": {Raw: []byte(`"{{.container_id}}"`)},
								},
							},
						},
					},
				},
				Status: musterv1alpha1.ServiceClassStatus{
					Available:     true,
					RequiredTools: []string{"docker_run", "docker_stop"},
					MissingTools:  []string{},
				},
			},
			expected: api.ServiceClass{
				Name:        "test-service",
				Description: "Test service",
				ServiceConfig: api.ServiceConfig{
					DefaultName:  "test-{{.name}}",
					Dependencies: []string{"dep1", "dep2"},
					LifecycleTools: api.LifecycleTools{
						Start: api.ToolCall{
							Tool: "docker_run",
							Args: map[string]interface{}{
								"image": "nginx:latest",
							},
							Outputs: map[string]string{
								"container_id": "result.id",
							},
						},
						Stop: api.ToolCall{
							Tool: "docker_stop",
							Args: map[string]interface{}{
								"container_id": "{{.container_id}}",
							},
						},
					},
				},
				Available:     true,
				RequiredTools: []string{"docker_run", "docker_stop"},
				MissingTools:  []string{},
			},
		},
		{
			name: "with health check and timeout",
			input: &musterv1alpha1.ServiceClass{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-service-complex",
				},
				Spec: musterv1alpha1.ServiceClassSpec{
					Description: "Complex test service",
					ServiceConfig: musterv1alpha1.ServiceConfig{
						LifecycleTools: musterv1alpha1.LifecycleTools{
							Start: musterv1alpha1.ToolCall{Tool: "test_start"},
							Stop:  musterv1alpha1.ToolCall{Tool: "test_stop"},
							HealthCheck: &musterv1alpha1.HealthCheckToolCall{
								Tool: "test_health",
								Expect: &musterv1alpha1.HealthCheckExpectation{
									Success: boolPtr(true),
									JSONPath: map[string]*runtime.RawExtension{
										"status": {Raw: []byte(`"healthy"`)},
									},
								},
							},
						},
						HealthCheck: &musterv1alpha1.HealthCheckConfig{
							Enabled:          true,
							Interval:         "30s",
							FailureThreshold: 3,
							SuccessThreshold: 1,
						},
						Timeout: &musterv1alpha1.TimeoutConfig{
							Create:      "5m",
							Delete:      "2m",
							HealthCheck: "10s",
						},
					},
				},
			},
			expected: api.ServiceClass{
				Name:        "test-service-complex",
				Description: "Complex test service",
				ServiceConfig: api.ServiceConfig{
					LifecycleTools: api.LifecycleTools{
						Start: api.ToolCall{Tool: "test_start"},
						Stop:  api.ToolCall{Tool: "test_stop"},
						HealthCheck: &api.HealthCheckToolCall{
							Tool: "test_health",
							Expect: &api.HealthCheckExpectation{
								Success: boolPtr(true),
								JsonPath: map[string]interface{}{
									"status": "healthy",
								},
							},
						},
					},
					HealthCheck: api.HealthCheckConfig{
						Enabled:          true,
						Interval:         30 * time.Second,
						FailureThreshold: 3,
						SuccessThreshold: 1,
					},
					Timeout: api.TimeoutConfig{
						Create:      5 * time.Minute,
						Delete:      2 * time.Minute,
						HealthCheck: 10 * time.Second,
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertCRDToServiceClass(tt.input)

			// Compare core fields
			if result.Name != tt.expected.Name {
				t.Errorf("Name mismatch: got %q, want %q", result.Name, tt.expected.Name)
			}
			if result.Description != tt.expected.Description {
				t.Errorf("Description mismatch: got %q, want %q", result.Description, tt.expected.Description)
			}

			// Compare service config
			if result.ServiceConfig.DefaultName != tt.expected.ServiceConfig.DefaultName {
				t.Errorf("DefaultName mismatch: got %q, want %q", result.ServiceConfig.DefaultName, tt.expected.ServiceConfig.DefaultName)
			}

			// Compare lifecycle tools
			if result.ServiceConfig.LifecycleTools.Start.Tool != tt.expected.ServiceConfig.LifecycleTools.Start.Tool {
				t.Errorf("Start tool mismatch: got %q, want %q", result.ServiceConfig.LifecycleTools.Start.Tool, tt.expected.ServiceConfig.LifecycleTools.Start.Tool)
			}
			if result.ServiceConfig.LifecycleTools.Stop.Tool != tt.expected.ServiceConfig.LifecycleTools.Stop.Tool {
				t.Errorf("Stop tool mismatch: got %q, want %q", result.ServiceConfig.LifecycleTools.Stop.Tool, tt.expected.ServiceConfig.LifecycleTools.Stop.Tool)
			}

			// Compare health check config if present
			if tt.expected.ServiceConfig.HealthCheck.Enabled {
				if result.ServiceConfig.HealthCheck.Enabled != tt.expected.ServiceConfig.HealthCheck.Enabled {
					t.Errorf("HealthCheck.Enabled mismatch: got %v, want %v", result.ServiceConfig.HealthCheck.Enabled, tt.expected.ServiceConfig.HealthCheck.Enabled)
				}
				if result.ServiceConfig.HealthCheck.Interval != tt.expected.ServiceConfig.HealthCheck.Interval {
					t.Errorf("HealthCheck.Interval mismatch: got %v, want %v", result.ServiceConfig.HealthCheck.Interval, tt.expected.ServiceConfig.HealthCheck.Interval)
				}
			}
		})
	}
}

func TestConvertRequestToCRD(t *testing.T) {
	tests := []struct {
		name         string
		inputName    string
		inputDesc    string
		inputArgs    map[string]api.ArgDefinition
		inputConfig  api.ServiceConfig
		expectedName string
		expectedDesc string
	}{
		{
			name:      "basic conversion",
			inputName: "test-service",
			inputDesc: "Test service description",
			inputArgs: map[string]api.ArgDefinition{
				"database_name": {
					Type:        "string",
					Required:    true,
					Description: "Database name",
				},
				"port": {
					Type:        "integer",
					Required:    false,
					Default:     5432,
					Description: "Database port",
				},
			},
			inputConfig: api.ServiceConfig{
				DefaultName:  "test-{{.database_name}}",
				Dependencies: []string{"postgres"},
				LifecycleTools: api.LifecycleTools{
					Start: api.ToolCall{
						Tool: "docker_run",
						Args: map[string]interface{}{
							"image": "postgres:13",
							"env": map[string]interface{}{
								"POSTGRES_DB": "{{.database_name}}",
							},
						},
						Outputs: map[string]string{
							"container_id": "result.id",
						},
					},
					Stop: api.ToolCall{
						Tool: "docker_stop",
						Args: map[string]interface{}{
							"container_id": "{{.container_id}}",
						},
					},
				},
			},
			expectedName: "test-service",
			expectedDesc: "Test service description",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertRequestToCRD(tt.inputName, tt.inputDesc, tt.inputArgs, tt.inputConfig)

			if result.ObjectMeta.Name != tt.expectedName {
				t.Errorf("Name mismatch: got %q, want %q", result.ObjectMeta.Name, tt.expectedName)
			}
			if result.Spec.Description != tt.expectedDesc {
				t.Errorf("Description mismatch: got %q, want %q", result.Spec.Description, tt.expectedDesc)
			}

			// Verify args conversion
			if len(result.Spec.Args) != len(tt.inputArgs) {
				t.Errorf("Args count mismatch: got %d, want %d", len(result.Spec.Args), len(tt.inputArgs))
			}

			// Verify lifecycle tools
			if result.Spec.ServiceConfig.LifecycleTools.Start.Tool != tt.inputConfig.LifecycleTools.Start.Tool {
				t.Errorf("Start tool mismatch: got %q, want %q", result.Spec.ServiceConfig.LifecycleTools.Start.Tool, tt.inputConfig.LifecycleTools.Start.Tool)
			}
			if result.Spec.ServiceConfig.LifecycleTools.Stop.Tool != tt.inputConfig.LifecycleTools.Stop.Tool {
				t.Errorf("Stop tool mismatch: got %q, want %q", result.Spec.ServiceConfig.LifecycleTools.Stop.Tool, tt.inputConfig.LifecycleTools.Stop.Tool)
			}
		})
	}
}

func TestConvertRawExtensionToInterface(t *testing.T) {
	tests := []struct {
		name     string
		input    *runtime.RawExtension
		expected interface{}
	}{
		{
			name:     "nil input",
			input:    nil,
			expected: nil,
		},
		{
			name:     "empty raw data",
			input:    &runtime.RawExtension{Raw: []byte{}},
			expected: nil,
		},
		{
			name:     "string value",
			input:    &runtime.RawExtension{Raw: []byte(`"hello world"`)},
			expected: "hello world",
		},
		{
			name:     "integer value",
			input:    &runtime.RawExtension{Raw: []byte(`42`)},
			expected: float64(42), // JSON unmarshaling returns float64 for numbers
		},
		{
			name:     "boolean value",
			input:    &runtime.RawExtension{Raw: []byte(`true`)},
			expected: true,
		},
		{
			name:     "object value",
			input:    &runtime.RawExtension{Raw: []byte(`{"key": "value"}`)},
			expected: map[string]interface{}{"key": "value"},
		},
		{
			name:     "template string",
			input:    &runtime.RawExtension{Raw: []byte(`"{{.database_name}}"`)},
			expected: "{{.database_name}}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertRawExtensionToInterface(tt.input)

			// Special handling for map comparison
			if resultMap, isMap := result.(map[string]interface{}); isMap {
				expectedMap, expectedIsMap := tt.expected.(map[string]interface{})
				if !expectedIsMap {
					t.Errorf("Result type mismatch: got %T, want %T", result, tt.expected)
					return
				}
				if len(resultMap) != len(expectedMap) {
					t.Errorf("Map length mismatch: got %d, want %d", len(resultMap), len(expectedMap))
					return
				}
				for k, v := range expectedMap {
					if resultMap[k] != v {
						t.Errorf("Map value mismatch for key %q: got %v, want %v", k, resultMap[k], v)
					}
				}
			} else {
				if result != tt.expected {
					t.Errorf("Result mismatch: got %v (%T), want %v (%T)", result, result, tt.expected, tt.expected)
				}
			}
		})
	}
}

func TestConvertInterfaceToRawExtension(t *testing.T) {
	tests := []struct {
		name     string
		input    interface{}
		expected string // Expected JSON string representation
	}{
		{
			name:     "nil input",
			input:    nil,
			expected: "",
		},
		{
			name:     "string value",
			input:    "hello world",
			expected: `"hello world"`,
		},
		{
			name:     "integer value",
			input:    42,
			expected: "42",
		},
		{
			name:     "boolean value",
			input:    true,
			expected: "true",
		},
		{
			name:     "template string",
			input:    "{{.database_name}}",
			expected: `"{{.database_name}}"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertInterfaceToRawExtension(tt.input)

			if tt.expected == "" {
				if result != nil {
					t.Errorf("Expected nil result, got %v", result)
				}
				return
			}

			if result == nil {
				t.Errorf("Expected non-nil result, got nil")
				return
			}

			if string(result.Raw) != tt.expected {
				t.Errorf("Raw data mismatch: got %q, want %q", string(result.Raw), tt.expected)
			}
		})
	}
}

func TestGetToolAvailability(t *testing.T) {
	tests := []struct {
		name     string
		status   *musterv1alpha1.ToolAvailabilityStatus
		toolType string
		expected bool
	}{
		{
			name:     "nil status",
			status:   nil,
			toolType: "start",
			expected: false,
		},
		{
			name: "start tool available",
			status: &musterv1alpha1.ToolAvailabilityStatus{
				StartToolAvailable: true,
			},
			toolType: "start",
			expected: true,
		},
		{
			name: "stop tool available",
			status: &musterv1alpha1.ToolAvailabilityStatus{
				StopToolAvailable: true,
			},
			toolType: "stop",
			expected: true,
		},
		{
			name: "health check tool available",
			status: &musterv1alpha1.ToolAvailabilityStatus{
				HealthCheckToolAvailable: true,
			},
			toolType: "healthCheck",
			expected: true,
		},
		{
			name: "unknown tool type",
			status: &musterv1alpha1.ToolAvailabilityStatus{
				StartToolAvailable: true,
			},
			toolType: "unknown",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getToolAvailability(tt.status, tt.toolType)

			if result != tt.expected {
				t.Errorf("Tool availability mismatch: got %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestIsHealthCheckConfigEmpty(t *testing.T) {
	tests := []struct {
		name     string
		config   api.HealthCheckConfig
		expected bool
	}{
		{
			name:     "empty config",
			config:   api.HealthCheckConfig{},
			expected: true,
		},
		{
			name: "enabled config",
			config: api.HealthCheckConfig{
				Enabled: true,
			},
			expected: false,
		},
		{
			name: "config with interval",
			config: api.HealthCheckConfig{
				Interval: 30 * time.Second,
			},
			expected: false,
		},
		{
			name: "config with thresholds",
			config: api.HealthCheckConfig{
				FailureThreshold: 3,
				SuccessThreshold: 1,
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isHealthCheckConfigEmpty(tt.config)

			if result != tt.expected {
				t.Errorf("Empty check mismatch: got %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestIsTimeoutConfigEmpty(t *testing.T) {
	tests := []struct {
		name     string
		config   api.TimeoutConfig
		expected bool
	}{
		{
			name:     "empty config",
			config:   api.TimeoutConfig{},
			expected: true,
		},
		{
			name: "config with create timeout",
			config: api.TimeoutConfig{
				Create: 5 * time.Minute,
			},
			expected: false,
		},
		{
			name: "config with delete timeout",
			config: api.TimeoutConfig{
				Delete: 2 * time.Minute,
			},
			expected: false,
		},
		{
			name: "config with health check timeout",
			config: api.TimeoutConfig{
				HealthCheck: 10 * time.Second,
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isTimeoutConfigEmpty(tt.config)

			if result != tt.expected {
				t.Errorf("Empty check mismatch: got %v, want %v", result, tt.expected)
			}
		})
	}
}

// Helper function to create a bool pointer
func boolPtr(b bool) *bool {
	return &b
}
