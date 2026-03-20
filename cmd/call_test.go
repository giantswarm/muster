package cmd

import (
	"testing"
	"os"
)

func TestCoerceValue(t *testing.T) {
	tests := []struct {
		input    string
		expected interface{}
	}{
		{"true", true},
		{"false", false},
		{"null", nil},
		{"42", int64(42)},
		{"-7", int64(-7)},
		{"3.14", float64(3.14)},
		{"-0.5", float64(-0.5)},
		{"hello", "hello"},
		{"", ""},
		{"123abc", "123abc"},
		{"1e5", float64(1e5)},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := coerceValue(tt.input)
			if got != tt.expected {
				t.Errorf("coerceValue(%q) = %v (%T), want %v (%T)", tt.input, got, got, tt.expected, tt.expected)
)

func TestIsKnownFlag(t *testing.T) {
	tests := []struct {
		name     string
		flag     string
		expected bool
	}{
		{name: "output flag", flag: "output", expected: true},
		{name: "output= flag", flag: "output=json", expected: true},
		{name: "quiet flag", flag: "quiet", expected: true},
		{name: "debug flag", flag: "debug", expected: true},
		{name: "config-path flag", flag: "config-path", expected: true},
		{name: "endpoint flag", flag: "endpoint", expected: true},
		{name: "context flag", flag: "context", expected: true},
		{name: "auth flag", flag: "auth", expected: true},
		{name: "no-headers flag", flag: "no-headers", expected: true},
		{name: "json flag", flag: "json", expected: true},
		{name: "short output flag", flag: "o", expected: true},
		{name: "short quiet flag", flag: "q", expected: true},
		{name: "unknown flag", flag: "name", expected: false},
		{name: "tool arg", flag: "environment", expected: false},
		{name: "similar to known", flag: "outputs", expected: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isKnownFlag(tt.flag)
			if result != tt.expected {
				t.Errorf("isKnownFlag(%q) = %v, expected %v", tt.flag, result, tt.expected)
			}
		})
	}
}

func TestParseCallArguments(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		toolName string
		expected map[string]interface{}
	}{
		{
			name:     "no arguments",
			args:     []string{"muster", "call", "core_service_list"},
			toolName: "core_service_list",
			expected: map[string]interface{}{},
		},
		{
			name:     "single key=value argument",
			args:     []string{"muster", "call", "core_service_status", "--name=prometheus"},
			toolName: "core_service_status",
			expected: map[string]interface{}{
				"name": "prometheus",
			},
		},
		{
			name:     "single key value argument",
			args:     []string{"muster", "call", "core_service_status", "--name", "prometheus"},
			toolName: "core_service_status",
			expected: map[string]interface{}{
				"name": "prometheus",
			},
		},
		{
			name:     "multiple arguments",
			args:     []string{"muster", "call", "workflow_deploy", "--environment=production", "--replicas=3"},
			toolName: "workflow_deploy",
			expected: map[string]interface{}{
				"environment": "production",
				"replicas":    "3",
			},
		},
		{
			name:     "boolean flag",
			args:     []string{"muster", "call", "some_tool", "--verbose"},
			toolName: "some_tool",
			expected: map[string]interface{}{
				"verbose": "true",
			},
		},
		{
			name:     "skips known output flag with value",
			args:     []string{"muster", "call", "core_service_status", "--name=prometheus", "--output", "json"},
			toolName: "core_service_status",
			expected: map[string]interface{}{
				"name": "prometheus",
			},
		},
		{
			name:     "skips known output= flag",
			args:     []string{"muster", "call", "core_service_status", "--name=prometheus", "--output=json"},
			toolName: "core_service_status",
			expected: map[string]interface{}{
				"name": "prometheus",
			},
		},
		{
			name:     "skips known quiet flag",
			args:     []string{"muster", "call", "core_service_status", "--quiet", "--name=prometheus"},
			toolName: "core_service_status",
			expected: map[string]interface{}{
				"name": "prometheus",
			},
		},
		{
			name:     "skips json flag with value",
			args:     []string{"muster", "call", "core_service_create", "--json", `{"name":"test"}`},
			toolName: "core_service_create",
			expected: map[string]interface{}{},
		},
		{
			name:     "mixed known and unknown flags",
			args:     []string{"muster", "call", "some_tool", "--debug", "--name=test", "--endpoint", "http://localhost", "--count=5"},
			toolName: "some_tool",
			expected: map[string]interface{}{
				"name":  "test",
				"count": "5",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Save and restore os.Args
			originalArgs := os.Args
			defer func() { os.Args = originalArgs }()
			os.Args = tt.args

			result := parseCallArguments(tt.toolName)

			if len(result) != len(tt.expected) {
				t.Errorf("parseCallArguments(%q) returned %d args, expected %d: got %v, expected %v",
					tt.toolName, len(result), len(tt.expected), result, tt.expected)
				return
			}

			for key, expectedValue := range tt.expected {
				actualValue, exists := result[key]
				if !exists {
					t.Errorf("parseCallArguments(%q) missing key %q", tt.toolName, key)
					continue
				}
				if actualValue != expectedValue {
					t.Errorf("parseCallArguments(%q)[%q] = %v, expected %v",
						tt.toolName, key, actualValue, expectedValue)
				}
			}
		})
	}
}

func TestGetJSONFlag(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		expected string
	}{
		{
			name:     "no json flag",
			args:     []string{"muster", "call", "core_service_list"},
			expected: "",
		},
		{
			name:     "json flag with space",
			args:     []string{"muster", "call", "core_service_create", "--json", `{"name":"test"}`},
			expected: `{"name":"test"}`,
		},
		{
			name:     "json flag with equals",
			args:     []string{"muster", "call", "core_service_create", `--json={"name":"test"}`},
			expected: `{"name":"test"}`,
		},
		{
			name:     "json flag at end without value",
			args:     []string{"muster", "call", "core_service_create", "--json"},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			originalArgs := os.Args
			defer func() { os.Args = originalArgs }()
			os.Args = tt.args

			result := getJSONFlag()
			if result != tt.expected {
				t.Errorf("getJSONFlag() = %q, expected %q", result, tt.expected)
			}
		})
	}
}
