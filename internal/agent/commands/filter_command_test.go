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
