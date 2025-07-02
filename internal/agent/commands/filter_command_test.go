package commands

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMatchWildcard(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		pattern  string
		expected bool
	}{
		// Basic exact matches
		{"exact match", "hello", "hello", true},
		{"no match", "hello", "world", false},
		{"empty pattern", "hello", "", false},
		{"empty text", "", "hello", false},
		{"both empty", "", "", true},

		// Single wildcard patterns
		{"single wildcard", "hello", "*", true},
		{"prefix wildcard", "hello", "*llo", true},
		{"prefix wildcard no match", "hello", "*xyz", false},
		{"suffix wildcard", "hello", "hel*", true},
		{"suffix wildcard no match", "hello", "xyz*", false},
		{"middle wildcard", "hello", "he*lo", true},
		{"middle wildcard no match", "hello", "he*xyz", false},

		// Multiple wildcard patterns
		{"both ends wildcard", "hello", "*ell*", true},
		{"both ends wildcard no match", "hello", "*xyz*", false},
		{"multiple wildcards", "hello_world", "hel*o_wor*d", true},
		{"multiple wildcards no match", "hello_world", "hel*xyz*d", false},

		// Consecutive wildcards
		{"consecutive wildcards", "hello", "h**o", true},
		{"consecutive wildcards at start", "hello", "**ello", true},
		{"consecutive wildcards at end", "hello", "hell**", true},

		// Edge cases that were broken before
		{"complex pattern in order", "service_list_tools", "service*list", true},
		{"complex pattern wrong order", "list_service_tools", "service*list", false},
		{"pattern with multiple parts", "kubernetes_get_pods", "kube*get*pods", true},
		{"pattern with multiple parts wrong order", "get_kubernetes_pods", "kube*get*pods", false},

		// Substring matching (no wildcards)
		{"substring match", "hello_world", "ello", true},
		{"substring no match", "hello_world", "xyz", false},

		// Case sensitivity handled by caller
		{"case sensitive", "Hello", "hello", false},
		{"case insensitive would be handled by caller", "hello", "hello", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := matchWildcard(tt.text, tt.pattern)
			assert.Equal(t, tt.expected, result, "matchWildcard(%q, %q) = %v, want %v", tt.text, tt.pattern, result, tt.expected)
		})
	}
}

func TestMatchesPattern(t *testing.T) {
	cmd := &FilterCommand{}

	tests := []struct {
		name          string
		toolName      string
		pattern       string
		caseSensitive bool
		expected      bool
	}{
		// Case sensitivity tests
		{"case sensitive match", "Hello", "Hello", true, true},
		{"case sensitive no match", "Hello", "hello", true, false},
		{"case insensitive match", "Hello", "hello", false, true},
		{"case insensitive wildcard", "Hello_World", "hello*world", false, true},

		// Practical tool filtering examples
		{"kubernetes tools", "kubernetes_get_pods", "kube*", false, true},
		{"service tools", "core_service_list", "*service*", false, true},
		{"docker tools", "docker_container_list", "*container*", false, true},
		{"exact tool name", "git_status", "git_status", false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cmd.matchesPattern(tt.toolName, tt.pattern, tt.caseSensitive)
			assert.Equal(t, tt.expected, result, "matchesPattern(%q, %q, %v) = %v, want %v", tt.toolName, tt.pattern, tt.caseSensitive, result, tt.expected)
		})
	}
}
