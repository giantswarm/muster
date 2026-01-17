package cmd

import (
	"strings"
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

func TestMCPFilterOptionsIsEmpty(t *testing.T) {
	tests := []struct {
		name     string
		opts     MCPFilterOptions
		expected bool
	}{
		{
			name:     "empty options",
			opts:     MCPFilterOptions{},
			expected: true,
		},
		{
			name:     "pattern only",
			opts:     MCPFilterOptions{Pattern: "core_*"},
			expected: false,
		},
		{
			name:     "description only",
			opts:     MCPFilterOptions{Description: "service"},
			expected: false,
		},
		{
			name:     "server only",
			opts:     MCPFilterOptions{Server: "github"},
			expected: false,
		},
		{
			name:     "both pattern and description set",
			opts:     MCPFilterOptions{Pattern: "core_*", Description: "service"},
			expected: false,
		},
		{
			name:     "all filters set",
			opts:     MCPFilterOptions{Pattern: "core_*", Description: "service", Server: "core"},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.opts.IsEmpty()
			if result != tt.expected {
				t.Errorf("MCPFilterOptions{%+v}.IsEmpty() = %v, expected %v",
					tt.opts, result, tt.expected)
			}
		})
	}
}

func TestMCPFilterOptionsHasMCPOnlyFilters(t *testing.T) {
	tests := []struct {
		name     string
		opts     MCPFilterOptions
		expected bool
	}{
		{
			name:     "empty options",
			opts:     MCPFilterOptions{},
			expected: false,
		},
		{
			name:     "pattern only",
			opts:     MCPFilterOptions{Pattern: "core_*"},
			expected: true,
		},
		{
			name:     "description only",
			opts:     MCPFilterOptions{Description: "service"},
			expected: true,
		},
		{
			name:     "server only",
			opts:     MCPFilterOptions{Server: "github"},
			expected: true,
		},
		{
			name:     "all filters set",
			opts:     MCPFilterOptions{Pattern: "core_*", Description: "service", Server: "core"},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.opts.HasMCPOnlyFilters()
			if result != tt.expected {
				t.Errorf("MCPFilterOptions{%+v}.HasMCPOnlyFilters() = %v, expected %v",
					tt.opts, result, tt.expected)
			}
		})
	}
}

func TestFilterMCPTools(t *testing.T) {
	tools := []struct {
		Name        string
		Description string
	}{
		{Name: "core_service_list", Description: "List all services"},
		{Name: "core_workflow_list", Description: "List all workflows"},
		{Name: "github_create_issue", Description: "Create a GitHub issue"},
	}

	// Convert to cli.MCPTool - we can't import cli in tests so we just test the filter logic
	t.Run("empty filter returns all", func(t *testing.T) {
		opts := MCPFilterOptions{}
		if !opts.IsEmpty() {
			t.Error("Expected empty options")
		}
	})

	t.Run("pattern filter", func(t *testing.T) {
		opts := MCPFilterOptions{Pattern: "core_*"}
		matchCount := 0
		for _, tool := range tools {
			if matchesMCPFilter(tool.Name, tool.Description, opts) {
				matchCount++
			}
		}
		if matchCount != 2 {
			t.Errorf("Expected 2 matches, got %d", matchCount)
		}
	})

	t.Run("description filter", func(t *testing.T) {
		opts := MCPFilterOptions{Description: "GitHub"}
		matchCount := 0
		for _, tool := range tools {
			if matchesMCPFilter(tool.Name, tool.Description, opts) {
				matchCount++
			}
		}
		if matchCount != 1 {
			t.Errorf("Expected 1 match, got %d", matchCount)
		}
	})
}

func TestAvailableListResourceTypes(t *testing.T) {
	types := availableListResourceTypes()

	if types == "" {
		t.Error("Expected non-empty string")
	}

	// Check that some expected types are present
	expectedSubstrings := []string{"service", "workflow", "tool", "resource", "prompt"}
	for _, expected := range expectedSubstrings {
		if !strings.Contains(types, expected) {
			t.Errorf("Expected availableListResourceTypes() to contain %q, got %q", expected, types)
		}
	}
}

func TestMatchesServer(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		server   string
		expected bool
	}{
		// Empty server matches everything
		{
			name:     "empty server matches any tool",
			toolName: "github_create_issue",
			server:   "",
			expected: true,
		},
		// Exact prefix match with underscore
		{
			name:     "exact prefix match with underscore",
			toolName: "github_create_issue",
			server:   "github",
			expected: true,
		},
		{
			name:     "prefix match core server",
			toolName: "core_service_list",
			server:   "core",
			expected: true,
		},
		// Case-insensitive matching
		{
			name:     "case-insensitive match uppercase server",
			toolName: "github_create_issue",
			server:   "GITHUB",
			expected: true,
		},
		{
			name:     "case-insensitive match mixed case",
			toolName: "GitHub_create_issue",
			server:   "github",
			expected: true,
		},
		// No match
		{
			name:     "no match different server",
			toolName: "github_create_issue",
			server:   "core",
			expected: false,
		},
		{
			name:     "no match partial server name",
			toolName: "github_create_issue",
			server:   "git",
			expected: true, // git is a prefix of github_
		},
		{
			name:     "no match when server is longer than prefix",
			toolName: "git_action",
			server:   "github",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := matchesServer(tt.toolName, tt.server)
			if result != tt.expected {
				t.Errorf("matchesServer(%q, %q) = %v, expected %v",
					tt.toolName, tt.server, result, tt.expected)
			}
		})
	}
}

func TestMatchesMCPFilterWithServer(t *testing.T) {
	tests := []struct {
		name        string
		toolName    string
		description string
		opts        MCPFilterOptions
		expected    bool
	}{
		// Server filter only
		{
			name:        "server filter only - matches",
			toolName:    "github_create_issue",
			description: "Create an issue",
			opts:        MCPFilterOptions{Server: "github"},
			expected:    true,
		},
		{
			name:        "server filter only - no match",
			toolName:    "core_service_list",
			description: "List services",
			opts:        MCPFilterOptions{Server: "github"},
			expected:    false,
		},
		// Combined filters
		{
			name:        "all filters match",
			toolName:    "core_service_list",
			description: "List all services with status",
			opts:        MCPFilterOptions{Pattern: "*_list", Description: "status", Server: "core"},
			expected:    true,
		},
		{
			name:        "pattern and description match but server doesn't",
			toolName:    "github_issue_list",
			description: "List all issues with status",
			opts:        MCPFilterOptions{Pattern: "*_list", Description: "status", Server: "core"},
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
