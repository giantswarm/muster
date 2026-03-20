package cmd

import (
	"testing"
)

// TestIsKnownFlag checks that known muster CLI flags are correctly identified.
func TestIsKnownFlag(t *testing.T) {
	tests := []struct {
		name     string
		flag     string
		expected bool
	}{
		{name: "output flag", flag: "output", expected: true},
		{name: "output with value inline", flag: "output=json", expected: true},
		{name: "quiet flag", flag: "quiet", expected: true},
		{name: "debug flag", flag: "debug", expected: true},
		{name: "config-path flag", flag: "config-path", expected: true},
		{name: "endpoint flag", flag: "endpoint", expected: true},
		{name: "context flag", flag: "context", expected: true},
		{name: "auth flag", flag: "auth", expected: true},
		{name: "no-headers flag", flag: "no-headers", expected: true},
		{name: "json flag", flag: "json", expected: true},
		{name: "short -o flag", flag: "o", expected: true},
		{name: "short -q flag", flag: "q", expected: true},
		{name: "unknown tool param", flag: "name", expected: false},
		{name: "unknown tool param with value", flag: "replicas=3", expected: false},
		{name: "empty flag", flag: "", expected: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isKnownFlag(tt.flag)
			if got != tt.expected {
				t.Errorf("isKnownFlag(%q) = %v, want %v", tt.flag, got, tt.expected)
			}
		})
	}
}

// TestCoerceValue verifies type coercion from strings to appropriate Go types.
func TestCoerceValue(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected interface{}
	}{
		{name: "boolean true", input: "true", expected: true},
		{name: "boolean false", input: "false", expected: false},
		{name: "null", input: "null", expected: nil},
		{name: "integer", input: "42", expected: int64(42)},
		{name: "negative integer", input: "-7", expected: int64(-7)},
		{name: "zero", input: "0", expected: int64(0)},
		{name: "float", input: "3.14", expected: float64(3.14)},
		{name: "negative float", input: "-1.5", expected: float64(-1.5)},
		{name: "plain string", input: "prometheus", expected: "prometheus"},
		{name: "string with digits", input: "v1.0.0", expected: "v1.0.0"},
		{name: "empty string", input: "", expected: ""},
		{name: "uppercase TRUE stays string", input: "TRUE", expected: "TRUE"},
		{name: "uppercase FALSE stays string", input: "FALSE", expected: "FALSE"},
		{name: "integer-looking float 42.0 is float64", input: "42.0", expected: float64(42)},
		{name: "scientific notation", input: "1e5", expected: float64(1e5)},
		{name: "string 123abc stays string", input: "123abc", expected: "123abc"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := coerceValue(tt.input)
			if got != tt.expected {
				t.Errorf("coerceValue(%q) = %v (%T), want %v (%T)",
					tt.input, got, got, tt.expected, tt.expected)
			}
		})
	}
}

// TestGetJSONFlag verifies extraction of the --json flag value from an args slice.
func TestGetJSONFlag(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		expected string
	}{
		{
			name:     "no --json flag",
			args:     []string{"muster", "call", "my-tool", "--name=foo"},
			expected: "",
		},
		{
			name:     "--json with space-separated value",
			args:     []string{"muster", "call", "my-tool", "--json", `{"key":"val"}`},
			expected: `{"key":"val"}`,
		},
		{
			name:     "--json=value inline format",
			args:     []string{"muster", "call", "my-tool", `--json={"key":"val"}`},
			expected: `{"key":"val"}`,
		},
		{
			name:     "--json with no following value",
			args:     []string{"muster", "call", "my-tool", "--json"},
			expected: "",
		},
		{
			name:     "--json followed by another flag is ignored",
			args:     []string{"muster", "call", "my-tool", "--json", "--other"},
			expected: "",
		},
		{
			name:     "--json before the tool name",
			args:     []string{"muster", "call", "--json", `{"k":"v"}`, "my-tool"},
			expected: `{"k":"v"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getJSONFlag(tt.args)
			if got != tt.expected {
				t.Errorf("getJSONFlag(%v) = %q, want %q", tt.args, got, tt.expected)
			}
		})
	}
}

// TestParseCallArguments exercises the argv parser used to extract tool parameters.
func TestParseCallArguments(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		args     []string
		expected map[string]interface{}
	}{
		// --- Basic formats ---
		{
			name:     "no arguments",
			toolName: "my-tool",
			args:     []string{"muster", "call", "my-tool"},
			expected: map[string]interface{}{},
		},
		{
			name:     "--param=value format",
			toolName: "my-tool",
			args:     []string{"muster", "call", "my-tool", "--name=prometheus"},
			expected: map[string]interface{}{"name": "prometheus"},
		},
		{
			name:     "--param value format",
			toolName: "my-tool",
			args:     []string{"muster", "call", "my-tool", "--name", "prometheus"},
			expected: map[string]interface{}{"name": "prometheus"},
		},
		{
			name:     "boolean flag without value",
			toolName: "my-tool",
			args:     []string{"muster", "call", "my-tool", "--verbose"},
			expected: map[string]interface{}{"verbose": true},
		},
		{
			name:     "multiple params",
			toolName: "my-tool",
			args:     []string{"muster", "call", "my-tool", "--env=production", "--replicas=3"},
			expected: map[string]interface{}{"env": "production", "replicas": int64(3)},
		},

		// --- Type coercion ---
		{
			name:     "boolean true coercion",
			toolName: "my-tool",
			args:     []string{"muster", "call", "my-tool", "--enabled=true"},
			expected: map[string]interface{}{"enabled": true},
		},
		{
			name:     "boolean false coercion",
			toolName: "my-tool",
			args:     []string{"muster", "call", "my-tool", "--enabled=false"},
			expected: map[string]interface{}{"enabled": false},
		},
		{
			name:     "integer coercion",
			toolName: "my-tool",
			args:     []string{"muster", "call", "my-tool", "--replicas=5"},
			expected: map[string]interface{}{"replicas": int64(5)},
		},
		{
			name:     "float coercion",
			toolName: "my-tool",
			args:     []string{"muster", "call", "my-tool", "--rate=1.5"},
			expected: map[string]interface{}{"rate": float64(1.5)},
		},
		{
			name:     "string with digits stays string",
			toolName: "my-tool",
			args:     []string{"muster", "call", "my-tool", "--version=v1.2.3"},
			expected: map[string]interface{}{"version": "v1.2.3"},
		},

		// --- Known flags are skipped (not forwarded to the tool) ---
		{
			name:     "known --output flag is skipped",
			toolName: "my-tool",
			args:     []string{"muster", "call", "my-tool", "--output", "json", "--name=foo"},
			expected: map[string]interface{}{"name": "foo"},
		},
		{
			name:     "known --json flag is skipped",
			toolName: "my-tool",
			args:     []string{"muster", "call", "my-tool", "--json", `{"x":1}`, "--name=bar"},
			expected: map[string]interface{}{"name": "bar"},
		},
		{
			name:     "known inline --output=json is skipped",
			toolName: "my-tool",
			args:     []string{"muster", "call", "my-tool", "--output=json", "--name=baz"},
			expected: map[string]interface{}{"name": "baz"},
		},

		// --- Flags before the tool name are NOT forwarded to the tool ---
		{
			name:     "known flags before tool name are ignored",
			toolName: "my-tool",
			args:     []string{"muster", "call", "--output", "json", "my-tool", "--name=foo"},
			expected: map[string]interface{}{"name": "foo"},
		},
		{
			name:     "known inline flag before tool name is ignored",
			toolName: "my-tool",
			args:     []string{"muster", "call", "--output=json", "my-tool", "--env=prod"},
			expected: map[string]interface{}{"env": "prod"},
		},
		{
			name:     "no args when call subcommand is absent",
			toolName: "my-tool",
			args:     []string{"muster", "my-tool", "--name=foo"},
			expected: map[string]interface{}{},
		},

		// --- "--" separator stops flag parsing ---
		{
			name:     "-- stops parsing tool args",
			toolName: "my-tool",
			args:     []string{"muster", "call", "my-tool", "--name=foo", "--", "--ignored=bar"},
			expected: map[string]interface{}{"name": "foo"},
		},
		{
			name:     "-- immediately after tool name yields no params",
			toolName: "my-tool",
			args:     []string{"muster", "call", "my-tool", "--", "--name=foo"},
			expected: map[string]interface{}{},
		},
		{
			name:     "-- before tool name stops tool-name search",
			toolName: "my-tool",
			args:     []string{"muster", "call", "--", "my-tool", "--name=foo"},
			expected: map[string]interface{}{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseCallArguments(tt.toolName, tt.args)

			if len(got) != len(tt.expected) {
				t.Errorf("parseCallArguments() returned %d params, want %d\n  got:  %v\n  want: %v",
					len(got), len(tt.expected), got, tt.expected)
				return
			}

			for k, wantVal := range tt.expected {
				gotVal, ok := got[k]
				if !ok {
					t.Errorf("parseCallArguments(): missing key %q\n  got:  %v\n  want: %v",
						k, got, tt.expected)
					continue
				}
				if gotVal != wantVal {
					t.Errorf("parseCallArguments()[%q] = %v (%T), want %v (%T)",
						k, gotVal, gotVal, wantVal, wantVal)
				}
			}
		})
	}
}
