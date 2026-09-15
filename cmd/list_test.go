package cmd

import (
	"slices"
	"strings"
	"testing"

	"github.com/giantswarm/muster/internal/cli"
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
			result := matchesMCPFilter(tt.toolName, tt.description, "", tt.opts)
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
			if matchesMCPFilter(tool.Name, tool.Description, "", opts) {
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
			if matchesMCPFilter(tool.Name, tool.Description, "", opts) {
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
		server   string // the server the aggregator reports for the tool
		filter   string
		expected bool
	}{
		{
			name:     "empty filter matches any tool",
			toolName: "x_files_read_file",
			server:   "files",
			filter:   "",
			expected: true,
		},
		// The aggregated server's name is what a user registered it as (#1248):
		// the exposed name carries an "x_" prefix the old prefix match never saw past.
		{
			name:     "aggregated server by its name",
			toolName: "x_files_read_file",
			server:   "files",
			filter:   "files",
			expected: true,
		},
		{
			name:     "aggregated server by the exposed prefix",
			toolName: "x_files_read_file",
			server:   "files",
			filter:   "x_files",
			expected: true,
		},
		{
			name:     "server name is matched case-insensitively",
			toolName: "x_files_read_file",
			server:   "files",
			filter:   "FILES",
			expected: true,
		},
		{
			name:     "exposed prefix is matched case-insensitively",
			toolName: "X_Files_read_file",
			server:   "files",
			filter:   "x_files",
			expected: true,
		},
		// A server whose toolPrefix differs from its name is found by its name only.
		{
			name:     "prefix-divergent server by its name",
			toolName: "x_pro_issues",
			server:   "gazelle-mcp-pro",
			filter:   "gazelle-mcp-pro",
			expected: true,
		},
		{
			name:     "prefix-divergent server by its exposed prefix",
			toolName: "x_pro_issues",
			server:   "gazelle-mcp-pro",
			filter:   "x_pro",
			expected: true,
		},
		{
			name:     "prefix-divergent server is not found by the bare prefix",
			toolName: "x_pro_issues",
			server:   "gazelle-mcp-pro",
			filter:   "pro",
			expected: false,
		},
		// muster's own tools are grouped under their kind.
		{
			name:     "core tool by kind",
			toolName: "core_service_list",
			server:   "core",
			filter:   "core",
			expected: true,
		},
		{
			name:     "workflow tool by kind",
			toolName: "workflow_deploy",
			server:   "workflow",
			filter:   "workflow",
			expected: true,
		},
		// No attribution (resources and prompts): only the exposed prefix can match.
		{
			name:     "no attribution matches the exposed prefix",
			toolName: "x_pp_triage",
			server:   "",
			filter:   "x_pp",
			expected: true,
		},
		{
			name:     "no attribution does not match the server name",
			toolName: "x_pp_triage",
			server:   "",
			filter:   "promptserver",
			expected: false,
		},
		// No partial matches: the filter names one server.
		{
			name:     "different server",
			toolName: "x_files_read_file",
			server:   "files",
			filter:   "core",
			expected: false,
		},
		{
			name:     "partial server name",
			toolName: "x_github_create_issue",
			server:   "github",
			filter:   "git",
			expected: false,
		},
		{
			name:     "filter longer than the prefix",
			toolName: "x_git_action",
			server:   "git",
			filter:   "github",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := matchesServer(tt.toolName, tt.server, tt.filter)
			if result != tt.expected {
				t.Errorf("matchesServer(%q, %q, %q) = %v, expected %v",
					tt.toolName, tt.server, tt.filter, result, tt.expected)
			}
		})
	}
}

func TestMatchesMCPFilterWithServer(t *testing.T) {
	tests := []struct {
		name        string
		toolName    string
		description string
		server      string
		opts        MCPFilterOptions
		expected    bool
	}{
		// Server filter only
		{
			name:        "server filter only - matches",
			toolName:    "x_github_create_issue",
			description: "Create an issue",
			server:      "github",
			opts:        MCPFilterOptions{Server: "github"},
			expected:    true,
		},
		{
			name:        "server filter only - no match",
			toolName:    "core_service_list",
			description: "List services",
			server:      "core",
			opts:        MCPFilterOptions{Server: "github"},
			expected:    false,
		},
		// Combined filters
		{
			name:        "all filters match",
			toolName:    "core_service_list",
			description: "List all services with status",
			server:      "core",
			opts:        MCPFilterOptions{Pattern: "*_list", Description: "status", Server: "core"},
			expected:    true,
		},
		{
			name:        "pattern and description match but server doesn't",
			toolName:    "x_github_issue_list",
			description: "List all issues with status",
			server:      "github",
			opts:        MCPFilterOptions{Pattern: "*_list", Description: "status", Server: "core"},
			expected:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := matchesMCPFilter(tt.toolName, tt.description, tt.server, tt.opts)
			if result != tt.expected {
				t.Errorf("matchesMCPFilter(%q, %q, %q, %+v) = %v, expected %v",
					tt.toolName, tt.description, tt.server, tt.opts, result, tt.expected)
			}
		})
	}
}

// TestFilterMCPToolsByServer covers `muster list tool --server <name>` on the
// catalogue the aggregator reports: a server registered as "files" exposes
// x_files_* and is selected by its name (#1248), a server whose toolPrefix
// differs from its name by its name alone, and muster's own tools by kind.
func TestFilterMCPToolsByServer(t *testing.T) {
	catalogue := []cli.MCPToolInfo{
		{MCPTool: cli.MCPTool{Name: "core_service_list", Description: "List all services"}, Server: "core"},
		{MCPTool: cli.MCPTool{Name: "core_workflow_list", Description: "List all workflows"}, Server: "core"},
		{MCPTool: cli.MCPTool{Name: "workflow_deploy", Description: "Deploy"}, Server: "workflow"},
		{MCPTool: cli.MCPTool{Name: "x_files_list_directory", Description: "List a directory"}, Server: "files"},
		{MCPTool: cli.MCPTool{Name: "x_files_read_file", Description: "Read a file"}, Server: "files"},
		{MCPTool: cli.MCPTool{Name: "x_pro_issues", Description: "List issues"}, Server: "gazelle-mcp-pro"},
	}

	names := func(tools []cli.MCPToolInfo) []string {
		out := make([]string, 0, len(tools))
		for _, tool := range tools {
			out = append(out, tool.Name)
		}
		return out
	}

	tests := []struct {
		name     string
		opts     MCPFilterOptions
		expected []string
	}{
		{
			name:     "--server files lists the tools of the server registered as files",
			opts:     MCPFilterOptions{Server: "files"},
			expected: []string{"x_files_list_directory", "x_files_read_file"},
		},
		{
			name:     "--filter x_files_* lists the same tools",
			opts:     MCPFilterOptions{Pattern: "x_files_*"},
			expected: []string{"x_files_list_directory", "x_files_read_file"},
		},
		{
			name:     "--server x_files accepts the exposed prefix",
			opts:     MCPFilterOptions{Server: "x_files"},
			expected: []string{"x_files_list_directory", "x_files_read_file"},
		},
		{
			name:     "--server core keeps listing the core tools",
			opts:     MCPFilterOptions{Server: "core"},
			expected: []string{"core_service_list", "core_workflow_list"},
		},
		{
			name:     "--server workflow lists the workflow tools",
			opts:     MCPFilterOptions{Server: "workflow"},
			expected: []string{"workflow_deploy"},
		},
		{
			name:     "--server names a server whose toolPrefix differs from its name",
			opts:     MCPFilterOptions{Server: "gazelle-mcp-pro"},
			expected: []string{"x_pro_issues"},
		},
		{
			name:     "--server combines with the other filters",
			opts:     MCPFilterOptions{Server: "files", Description: "file"},
			expected: []string{"x_files_read_file"},
		},
		{
			name:     "--server for an unknown server selects nothing",
			opts:     MCPFilterOptions{Server: "nothing"},
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := names(filterMCPTools(catalogue, tt.opts))
			if !slices.Equal(got, tt.expected) {
				t.Errorf("filterMCPTools(%+v) = %v, expected %v", tt.opts, got, tt.expected)
			}
		})
	}
}
