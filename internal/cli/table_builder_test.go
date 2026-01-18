package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTableBuilder_FormatTimeoutPlain(t *testing.T) {
	builder := &TableBuilder{}

	tests := []struct {
		name     string
		value    interface{}
		expected string
	}{
		{
			name:     "nil value",
			value:    nil,
			expected: "-",
		},
		{
			name:     "zero int",
			value:    0,
			expected: "-",
		},
		{
			name:     "zero float",
			value:    0.0,
			expected: "-",
		},
		{
			name:     "positive int",
			value:    30,
			expected: "30s",
		},
		{
			name:     "positive int64",
			value:    int64(60),
			expected: "60s",
		},
		{
			name:     "positive float (whole number)",
			value:    30.0,
			expected: "30s",
		},
		{
			name:     "positive float (decimal)",
			value:    30.5,
			expected: "30.5s",
		},
		{
			name:     "string number",
			value:    "45",
			expected: "45s",
		},
		{
			name:     "string zero",
			value:    "0",
			expected: "-",
		},
		{
			name:     "string empty",
			value:    "",
			expected: "-",
		},
		{
			name:     "string with s suffix already",
			value:    "30s",
			expected: "30s",
		},
		{
			name:     "string with m suffix",
			value:    "5m",
			expected: "5m",
		},
		{
			name:     "string with h suffix",
			value:    "2h",
			expected: "2h",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := builder.formatTimeoutPlain(tt.value)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestTableBuilder_FormatCellValuePlain_Timeout(t *testing.T) {
	builder := &TableBuilder{}

	// Test that timeout column triggers formatTimeoutPlain
	result := builder.FormatCellValuePlain("timeout", 30, nil)
	assert.Equal(t, "30s", result)

	result = builder.FormatCellValuePlain("TIMEOUT", 60.0, nil)
	assert.Equal(t, "60s", result)
}
