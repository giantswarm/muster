package cmd

import (
	"testing"
)

func TestParseToolArgs(t *testing.T) {
	tests := []struct {
		name     string
		toolArgs []string
		expected map[string]interface{}
	}{
		{
			name:     "no args",
			toolArgs: []string{},
			expected: map[string]interface{}{},
		},
		{
			name:     "single --key=value",
			toolArgs: []string{"--foo=bar"},
			expected: map[string]interface{}{"foo": "bar"},
		},
		{
			name:     "single --key value",
			toolArgs: []string{"--foo", "bar"},
			expected: map[string]interface{}{"foo": "bar"},
		},
		{
			name:     "boolean flag",
			toolArgs: []string{"--verbose"},
			expected: map[string]interface{}{"verbose": "true"},
		},
		{
			name:     "double dash sentinel stops parsing",
			toolArgs: []string{"--foo", "bar", "--", "--not-a-param"},
			expected: map[string]interface{}{"foo": "bar"},
		},
		{
			name:     "double dash sentinel at start",
			toolArgs: []string{"--", "--not-a-param"},
			expected: map[string]interface{}{},
		},
		{
			name:     "double dash alone does not add empty key",
			toolArgs: []string{"--"},
			expected: map[string]interface{}{},
		},
		{
			name:     "known flags are skipped",
			toolArgs: []string{"--output", "json", "--foo=bar"},
			expected: map[string]interface{}{"foo": "bar"},
		},
		{
			name:     "multiple params",
			toolArgs: []string{"--foo=bar", "--baz=qux"},
			expected: map[string]interface{}{"foo": "bar", "baz": "qux"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseToolArgs(tt.toolArgs)
			if len(result) != len(tt.expected) {
				t.Errorf("expected %d params, got %d: %v", len(tt.expected), len(result), result)
				return
			}
			for k, v := range tt.expected {
				got, ok := result[k]
				if !ok {
					t.Errorf("expected key %q not found in result %v", k, result)
				} else if got != v {
					t.Errorf("key %q: expected %q, got %q", k, v, got)
				}
			}
		})
	}
}
