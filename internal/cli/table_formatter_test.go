package cli

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTableFormatter_isWideMode(t *testing.T) {
	tests := []struct {
		name     string
		format   OutputFormat
		expected bool
	}{
		{
			name:     "table format is not wide",
			format:   OutputFormatTable,
			expected: false,
		},
		{
			name:     "wide format is wide",
			format:   OutputFormatWide,
			expected: true,
		},
		{
			name:     "json format is not wide",
			format:   OutputFormatJSON,
			expected: false,
		},
		{
			name:     "yaml format is not wide",
			format:   OutputFormatYAML,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			formatter := NewTableFormatter(ExecutorOptions{Format: tt.format})
			assert.Equal(t, tt.expected, formatter.isWideMode())
		})
	}
}

func TestTableFormatter_optimizeColumns_Services(t *testing.T) {
	tests := []struct {
		name           string
		format         OutputFormat
		objects        []interface{}
		expectContains []string
		expectMissing  []string
	}{
		{
			name:   "service table format shows base columns",
			format: OutputFormatTable,
			objects: []interface{}{
				map[string]interface{}{
					"name":         "test-service",
					"health":       "healthy",
					"state":        "Running",
					"service_type": "MCPServer",
					"endpoint":     "http://localhost:8090/mcp",
					"tools":        10,
				},
			},
			expectContains: []string{"name", "health", "state", "service_type"},
			expectMissing:  []string{"endpoint", "tools"},
		},
		{
			name:   "service wide format shows extended columns",
			format: OutputFormatWide,
			objects: []interface{}{
				map[string]interface{}{
					"name":         "test-service",
					"health":       "healthy",
					"state":        "Running",
					"service_type": "MCPServer",
					"endpoint":     "http://localhost:8090/mcp",
					"tools":        10,
				},
			},
			expectContains: []string{"name", "health", "state", "service_type", "endpoint", "tools"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			formatter := NewTableFormatter(ExecutorOptions{Format: tt.format})
			columns := formatter.optimizeColumns(tt.objects)

			for _, col := range tt.expectContains {
				assert.Contains(t, columns, col, "expected column %s to be present", col)
			}
			for _, col := range tt.expectMissing {
				assert.NotContains(t, columns, col, "expected column %s to be missing", col)
			}
		})
	}
}

func TestTableFormatter_optimizeColumns_MCPServers(t *testing.T) {
	tests := []struct {
		name           string
		format         OutputFormat
		objects        []interface{}
		expectContains []string
		expectMissing  []string
	}{
		{
			name:   "mcpserver table format shows base columns",
			format: OutputFormatTable,
			objects: []interface{}{
				map[string]interface{}{
					"name":      "github",
					"type":      "streamable-http",
					"autoStart": true,
					"url":       "https://github.example.com/mcp",
					"timeout":   "30s",
				},
			},
			expectContains: []string{"name", "type", "autoStart"},
			expectMissing:  []string{"url", "timeout"},
		},
		{
			name:   "mcpserver wide format shows extended columns",
			format: OutputFormatWide,
			objects: []interface{}{
				map[string]interface{}{
					"name":      "github",
					"type":      "streamable-http",
					"autoStart": true,
					"url":       "https://github.example.com/mcp",
					"timeout":   "30s",
				},
			},
			expectContains: []string{"name", "type", "autoStart", "url", "timeout"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			formatter := NewTableFormatter(ExecutorOptions{Format: tt.format})
			columns := formatter.optimizeColumns(tt.objects)

			for _, col := range tt.expectContains {
				assert.Contains(t, columns, col, "expected column %s to be present", col)
			}
			for _, col := range tt.expectMissing {
				assert.NotContains(t, columns, col, "expected column %s to be missing", col)
			}
		})
	}
}

func TestTableFormatter_optimizeColumns_Workflows(t *testing.T) {
	tests := []struct {
		name           string
		format         OutputFormat
		objects        []interface{}
		expectContains []string
	}{
		{
			name:   "workflow table format shows base columns",
			format: OutputFormatTable,
			objects: []interface{}{
				map[string]interface{}{
					"name":        "deploy-app",
					"status":      "available",
					"description": "Deploy an application",
					"steps":       []interface{}{"step1", "step2"},
					"args":        map[string]interface{}{"env": "production"},
				},
			},
			expectContains: []string{"name", "status", "description", "steps"},
		},
		{
			name:   "workflow wide format shows extended columns",
			format: OutputFormatWide,
			objects: []interface{}{
				map[string]interface{}{
					"name":        "deploy-app",
					"status":      "available",
					"description": "Deploy an application",
					"steps":       []interface{}{"step1", "step2"},
					"args":        map[string]interface{}{"env": "production"},
				},
			},
			expectContains: []string{"name", "status", "description", "steps", "args"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			formatter := NewTableFormatter(ExecutorOptions{Format: tt.format})
			columns := formatter.optimizeColumns(tt.objects)

			for _, col := range tt.expectContains {
				assert.Contains(t, columns, col, "expected column %s to be present", col)
			}
		})
	}
}

func TestTableFormatter_detectResourceType(t *testing.T) {
	tests := []struct {
		name     string
		sample   map[string]interface{}
		expected string
	}{
		{
			name: "detects service type",
			sample: map[string]interface{}{
				"name":   "test-service",
				"health": "healthy",
				"state":  "Running",
			},
			expected: "service",
		},
		{
			name: "detects event type",
			sample: map[string]interface{}{
				"timestamp":     "2024-01-01T00:00:00Z",
				"reason":        "Started",
				"resource_type": "MCPServer",
			},
			expected: "event",
		},
		{
			name: "detects mcpServer type by type field value",
			sample: map[string]interface{}{
				"name": "github",
				"type": "streamable-http",
			},
			expected: "mcpServers",
		},
		{
			name: "detects workflow type",
			sample: map[string]interface{}{
				"name":        "deploy",
				"steps":       []interface{}{},
				"description": "Deploy workflow",
			},
			expected: "workflows",
		},
		{
			name: "detects execution type",
			sample: map[string]interface{}{
				"workflow_name": "deploy",
				"started_at":    "2024-01-01T00:00:00Z",
			},
			expected: "execution",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			formatter := NewTableFormatter(ExecutorOptions{Format: OutputFormatTable})
			result := formatter.detectResourceType(tt.sample)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestTableFormatter_keyExists(t *testing.T) {
	formatter := NewTableFormatter(ExecutorOptions{})

	data := map[string]interface{}{
		"name":   "test",
		"value":  nil,
		"number": 42,
	}

	assert.True(t, formatter.keyExists(data, "name"))
	assert.True(t, formatter.keyExists(data, "value"))
	assert.True(t, formatter.keyExists(data, "number"))
	assert.False(t, formatter.keyExists(data, "missing"))
}

func TestSlicesContains(t *testing.T) {
	// Verify slices.Contains works as expected (standard library)
	slice := []string{"a", "b", "c"}

	assert.True(t, slices.Contains(slice, "a"))
	assert.True(t, slices.Contains(slice, "b"))
	assert.True(t, slices.Contains(slice, "c"))
	assert.False(t, slices.Contains(slice, "d"))
	assert.False(t, slices.Contains(slice, ""))
}

func TestTableFormatter_getRemainingKeys(t *testing.T) {
	formatter := NewTableFormatter(ExecutorOptions{})

	allKeys := []string{"a", "b", "c", "d", "e"}
	usedKeys := []string{"a", "c"}

	remaining := formatter.getRemainingKeys(allKeys, usedKeys)

	assert.Equal(t, []string{"b", "d", "e"}, remaining)
}

func TestTableFormatter_min(t *testing.T) {
	formatter := NewTableFormatter(ExecutorOptions{})

	assert.Equal(t, 3, formatter.min(3, 5))
	assert.Equal(t, 3, formatter.min(5, 3))
	assert.Equal(t, 0, formatter.min(0, 5))
	assert.Equal(t, -1, formatter.min(-1, 5))
}
