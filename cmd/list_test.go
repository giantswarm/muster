package cmd

import (
	"testing"
)

func TestMatchesWildcard(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		pattern  string
		expected bool
	}{
		// Empty pattern matches everything
		{
			name:     "empty pattern matches any name",
			input:    "core_service_list",
			pattern:  "",
			expected: true,
		},
		// Exact match
		{
			name:     "exact match",
			input:    "core_service_list",
			pattern:  "core_service_list",
			expected: true,
		},
		{
			name:     "exact match fails on different name",
			input:    "core_service_list",
			pattern:  "core_workflow_list",
			expected: false,
		},
		// Prefix wildcard
		{
			name:     "prefix wildcard matches",
			input:    "core_service_list",
			pattern:  "core_*",
			expected: true,
		},
		{
			name:     "prefix wildcard fails",
			input:    "github_create_issue",
			pattern:  "core_*",
			expected: false,
		},
		// Suffix wildcard
		{
			name:     "suffix wildcard matches",
			input:    "core_service_list",
			pattern:  "*_list",
			expected: true,
		},
		{
			name:     "suffix wildcard fails",
			input:    "core_service_status",
			pattern:  "*_list",
			expected: false,
		},
		// Contains wildcard
		{
			name:     "contains wildcard matches",
			input:    "core_service_list",
			pattern:  "*service*",
			expected: true,
		},
		{
			name:     "contains wildcard fails",
			input:    "core_workflow_list",
			pattern:  "*service*",
			expected: false,
		},
		// Question mark single character
		{
			name:     "question mark matches single character",
			input:    "tool1",
			pattern:  "tool?",
			expected: true,
		},
		{
			name:     "question mark fails on multiple characters",
			input:    "tool12",
			pattern:  "tool?",
			expected: false,
		},
		// Complex patterns
		{
			name:     "complex pattern matches",
			input:    "core_service_status",
			pattern:  "core_*_status",
			expected: true,
		},
		{
			name:     "complex pattern with question mark",
			input:    "item_a1_value",
			pattern:  "item_?1_*",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := matchesWildcard(tt.input, tt.pattern)
			if result != tt.expected {
				t.Errorf("matchesWildcard(%q, %q) = %v, expected %v",
					tt.input, tt.pattern, result, tt.expected)
			}
		})
	}
}

func TestMatchesDescription(t *testing.T) {
	tests := []struct {
		name        string
		description string
		filter      string
		expected    bool
	}{
		// Empty filter matches everything
		{
			name:        "empty filter matches any description",
			description: "List all services with their status",
			filter:      "",
			expected:    true,
		},
		{
			name:        "empty filter matches empty description",
			description: "",
			filter:      "",
			expected:    true,
		},
		// Case-insensitive matching
		{
			name:        "case-insensitive match lowercase filter",
			description: "List all Services with their Status",
			filter:      "services",
			expected:    true,
		},
		{
			name:        "case-insensitive match uppercase filter",
			description: "List all services with their status",
			filter:      "SERVICES",
			expected:    true,
		},
		{
			name:        "case-insensitive match mixed case filter",
			description: "list all SERVICES with their STATUS",
			filter:      "SeRvIcEs",
			expected:    true,
		},
		// Substring matching
		{
			name:        "substring at beginning",
			description: "List all services",
			filter:      "List",
			expected:    true,
		},
		{
			name:        "substring in middle",
			description: "List all services with status",
			filter:      "services",
			expected:    true,
		},
		{
			name:        "substring at end",
			description: "List all services",
			filter:      "services",
			expected:    true,
		},
		// No match
		{
			name:        "no match",
			description: "List all workflows",
			filter:      "services",
			expected:    false,
		},
		{
			name:        "empty description with non-empty filter",
			description: "",
			filter:      "test",
			expected:    false,
		},
		// Partial word matching
		{
			name:        "partial word match",
			description: "List all workflow executions",
			filter:      "exec",
			expected:    true,
		},
		// Space and special characters
		{
			name:        "filter with spaces",
			description: "List all services with their status",
			filter:      "with their",
			expected:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := matchesDescription(tt.description, tt.filter)
			if result != tt.expected {
				t.Errorf("matchesDescription(%q, %q) = %v, expected %v",
					tt.description, tt.filter, result, tt.expected)
			}
		})
	}
}

func TestMatchesMCPFilter(t *testing.T) {
	tests := []struct {
		name        string
		toolName    string
		description string
		opts        MCPFilterOptions
		expected    bool
	}{
		// No filters - matches everything
		{
			name:        "no filters matches everything",
			toolName:    "core_service_list",
			description: "List all services",
			opts:        MCPFilterOptions{},
			expected:    true,
		},
		// Pattern only
		{
			name:        "pattern only - matches",
			toolName:    "core_service_list",
			description: "List all services",
			opts:        MCPFilterOptions{Pattern: "core_*"},
			expected:    true,
		},
		{
			name:        "pattern only - no match",
			toolName:    "github_create_issue",
			description: "Create an issue",
			opts:        MCPFilterOptions{Pattern: "core_*"},
			expected:    false,
		},
		// Description only
		{
			name:        "description only - matches",
			toolName:    "core_service_list",
			description: "List all services",
			opts:        MCPFilterOptions{Description: "services"},
			expected:    true,
		},
		{
			name:        "description only - no match",
			toolName:    "core_service_list",
			description: "List all services",
			opts:        MCPFilterOptions{Description: "workflow"},
			expected:    false,
		},
		// Both filters - both must match
		{
			name:        "both filters - both match",
			toolName:    "core_service_list",
			description: "List all services with their status",
			opts:        MCPFilterOptions{Pattern: "core_*", Description: "status"},
			expected:    true,
		},
		{
			name:        "both filters - pattern matches but description doesn't",
			toolName:    "core_service_list",
			description: "List all services",
			opts:        MCPFilterOptions{Pattern: "core_*", Description: "workflow"},
			expected:    false,
		},
		{
			name:        "both filters - description matches but pattern doesn't",
			toolName:    "github_service_list",
			description: "List all services",
			opts:        MCPFilterOptions{Pattern: "core_*", Description: "services"},
			expected:    false,
		},
		{
			name:        "both filters - neither matches",
			toolName:    "github_create_issue",
			description: "Create an issue in GitHub",
			opts:        MCPFilterOptions{Pattern: "core_*", Description: "services"},
			expected:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := matchesMCPFilter(tt.toolName, tt.description, tt.opts)
			if result != tt.expected {
				t.Errorf("matchesMCPFilter(%q, %q, %+v) = %v, expected %v",
					tt.toolName, tt.description, tt.opts, result, tt.expected)
			}
		})
	}
}

func TestMCPResourceTypes(t *testing.T) {
	// Test that all expected resource types are mapped
	expectedMappings := map[string]string{
		"tool":      "tool",
		"tools":     "tool",
		"resource":  "resource",
		"resources": "resource",
		"prompt":    "prompt",
		"prompts":   "prompt",
	}

	for input, expectedType := range expectedMappings {
		t.Run(input, func(t *testing.T) {
			actualType, exists := mcpResourceTypes[input]
			if !exists {
				t.Errorf("Expected mcpResourceTypes to contain %q", input)
				return
			}
			if actualType != expectedType {
				t.Errorf("mcpResourceTypes[%q] = %q, expected %q", input, actualType, expectedType)
			}
		})
	}
}

func TestGetListResourceTypes(t *testing.T) {
	types := getListResourceTypes()

	if len(types) == 0 {
		t.Error("Expected getListResourceTypes() to return non-empty slice")
	}

	// Check that common types are present
	expectedTypes := []string{"service", "services", "workflow", "workflows"}
	typeSet := make(map[string]bool)
	for _, t := range types {
		typeSet[t] = true
	}

	for _, expected := range expectedTypes {
		if !typeSet[expected] {
			t.Errorf("Expected getListResourceTypes() to include %q", expected)
		}
	}
}

func TestGetListResourceMappings(t *testing.T) {
	mappings := getListResourceMappings()

	if len(mappings) == 0 {
		t.Error("Expected getListResourceMappings() to return non-empty map")
	}

	// Check specific mappings
	expectedMappings := map[string]string{
		"service":    "core_service_list",
		"services":   "core_service_list",
		"workflow":   "core_workflow_list",
		"workflows":  "core_workflow_list",
		"mcpserver":  "core_mcpserver_list",
		"mcpservers": "core_mcpserver_list",
	}

	for alias, expectedTool := range expectedMappings {
		t.Run(alias, func(t *testing.T) {
			actualTool, exists := mappings[alias]
			if !exists {
				t.Errorf("Expected mapping for %q", alias)
				return
			}
			if actualTool != expectedTool {
				t.Errorf("mappings[%q] = %q, expected %q", alias, actualTool, expectedTool)
			}
		})
	}
}
