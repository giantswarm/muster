package cmd

import (
	"testing"
)

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
