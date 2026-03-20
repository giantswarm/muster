package cmd

import (
	"testing"
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
			}
		})
	}
}
