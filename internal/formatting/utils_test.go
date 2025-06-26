package formatting

import (
	"testing"
)

func TestPrettyJSON(t *testing.T) {
	tests := []struct {
		name     string
		input    interface{}
		expected string
	}{
		{
			name:     "simple object",
			input:    map[string]interface{}{"name": "test", "value": 42},
			expected: "{\n  \"name\": \"test\",\n  \"value\": 42\n}",
		},
		{
			name:     "array",
			input:    []string{"a", "b", "c"},
			expected: "[\n  \"a\",\n  \"b\",\n  \"c\"\n]",
		},
		{
			name:     "string",
			input:    "hello world",
			expected: "\"hello world\"",
		},
		{
			name:     "number",
			input:    123,
			expected: "123",
		},
		{
			name:     "boolean",
			input:    true,
			expected: "true",
		},
		{
			name:     "nil",
			input:    nil,
			expected: "null",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := PrettyJSON(tt.input)
			if result != tt.expected {
				t.Errorf("PrettyJSON() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestPrettyJSONWithInvalidData(t *testing.T) {
	// Test with data that can't be marshaled (like a channel)
	ch := make(chan int)
	result := PrettyJSON(ch)
	
	// Should fallback to fmt.Sprintf format
	if result == "" {
		t.Error("PrettyJSON() should not return empty string for invalid data")
	}
	
	// Should contain some representation of the channel
	if len(result) < 5 {
		t.Error("PrettyJSON() fallback should provide meaningful output")
	}
} 