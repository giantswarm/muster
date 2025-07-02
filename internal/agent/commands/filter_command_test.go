package commands

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMatchesPattern(t *testing.T) {
	cmd := &FilterCommand{}

	tests := []struct {
		name          string
		toolName      string
		pattern       string
		caseSensitive bool
		expected      bool
	}{
		// Basic exact matches
		{"exact match", "hello", "hello", true, true},
		{"no match", "hello", "world", true, false},
		{"empty pattern", "hello", "", true, true}, // empty pattern should match everything
		{"empty text", "", "hello", true, false},
		{"both empty", "", "", true, true},

		// Case sensitivity tests
		{"case sensitive match", "Hello", "Hello", true, true},
		{"case sensitive no match", "Hello", "hello", true, false},
		{"case insensitive match", "Hello", "hello", false, true},
		{"case insensitive wildcard", "Hello_World", "hello*world", false, true},

		// Substring matching (no wildcards) - this is the new behavior
		{"substring match", "hello_world", "ello", false, true},
		{"substring no match", "hello_world", "xyz", false, false},
		{"substring case sensitive", "Hello_World", "hello", true, false},
		{"substring case insensitive", "Hello_World", "hello", false, true},

		// Single wildcard patterns
		{"single wildcard", "hello", "*", true, true},
		{"prefix wildcard", "hello", "*llo", true, true},
		{"prefix wildcard no match", "hello", "*xyz", true, false},
		{"suffix wildcard", "hello", "hel*", true, true},
		{"suffix wildcard no match", "hello", "xyz*", true, false},
		{"middle wildcard", "hello", "he*lo", true, true},
		{"middle wildcard no match", "hello", "he*xyz", true, false},

		// Multiple wildcard patterns
		{"both ends wildcard", "hello", "*ell*", true, true},
		{"both ends wildcard no match", "hello", "*xyz*", true, false},
		{"multiple wildcards", "hello_world", "hel*o_wor*d", true, true},
		{"multiple wildcards no match", "hello_world", "hel*xyz*d", true, false},

		// Edge cases that were important in the original tests
		{"complex pattern in order", "service_list_tools", "service*list", true, true},
		{"complex pattern wrong order", "list_service_tools", "service*list", true, false},
		{"pattern with multiple parts", "kubernetes_get_pods", "kube*get*pods", true, true},
		{"pattern with multiple parts wrong order", "get_kubernetes_pods", "kube*get*pods", true, false},

		// Practical tool filtering examples
		{"kubernetes tools", "kubernetes_get_pods", "kube*", false, true},
		{"service tools", "core_service_list", "*service*", false, true},
		{"docker tools", "docker_container_list", "*container*", false, true},
		{"exact tool name", "git_status", "git_status", false, true},

		// Case insensitive versions of complex patterns
		{"complex pattern case insensitive", "Service_List_Tools", "service*list", false, true},
		{"kubernetes case insensitive", "Kubernetes_Get_Pods", "kube*get*pods", false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cmd.matchesPattern(tt.toolName, tt.pattern, tt.caseSensitive)
			assert.Equal(t, tt.expected, result, "matchesPattern(%q, %q, %v) = %v, want %v", tt.toolName, tt.pattern, tt.caseSensitive, result, tt.expected)
		})
	}
}

// TestMatchesPatternEdgeCases tests edge cases that might break with filepath.Match
func TestMatchesPatternEdgeCases(t *testing.T) {
	cmd := &FilterCommand{}

	tests := []struct {
		name          string
		toolName      string
		pattern       string
		caseSensitive bool
		expected      bool
	}{
		// Consecutive wildcards - filepath.Match should handle these fine
		{"consecutive wildcards", "hello", "h**o", true, true},
		{"consecutive wildcards at start", "hello", "**ello", true, true},
		{"consecutive wildcards at end", "hello", "hell**", true, true},

		// Complex real-world patterns
		{"workflow pattern", "workflow_execute_deploy", "workflow*deploy", true, true},
		{"service pattern complex", "core_service_kubernetes_start", "*service*kubernetes*", true, true},
		{"tool name with underscores", "git_branch_list_remote", "git*list*", true, true},

		// Patterns that should not match
		{"partial match should fail", "hello_world", "hello_universe", true, false},
		{"wrong wildcard position", "prefix_suffix", "suffix*prefix", true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cmd.matchesPattern(tt.toolName, tt.pattern, tt.caseSensitive)
			assert.Equal(t, tt.expected, result, "matchesPattern(%q, %q, %v) = %v, want %v", tt.toolName, tt.pattern, tt.caseSensitive, result, tt.expected)
		})
	}
}
