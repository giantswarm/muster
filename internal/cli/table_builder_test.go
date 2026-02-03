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

func TestTableBuilder_FormatStatePlain(t *testing.T) {
	builder := &TableBuilder{}

	// State values are stored directly in the CRD with context-appropriate
	// terminology. No translation is needed - values are returned as-is.
	tests := []struct {
		name       string
		state      string
		serverType string
		expected   string
	}{
		// Remote server state values (stored directly in CRD)
		{
			name:       "Connected state with remote server",
			state:      "Connected",
			serverType: "streamable-http",
			expected:   "Connected",
		},
		{
			name:       "Connecting state with remote server",
			state:      "Connecting",
			serverType: "sse",
			expected:   "Connecting",
		},
		{
			name:       "Disconnected state with remote server",
			state:      "Disconnected",
			serverType: "streamable-http",
			expected:   "Disconnected",
		},
		// Local server state values (stored directly in CRD)
		{
			name:       "Running state with stdio server",
			state:      "Running",
			serverType: "stdio",
			expected:   "Running",
		},
		{
			name:       "Starting state with stdio server",
			state:      "Starting",
			serverType: "stdio",
			expected:   "Starting",
		},
		{
			name:       "Stopped state with stdio server",
			state:      "Stopped",
			serverType: "stdio",
			expected:   "Stopped",
		},
		// Common state values
		{
			name:       "Failed state",
			state:      "Failed",
			serverType: "stdio",
			expected:   "Failed",
		},
		// Edge cases
		{
			name:       "empty state",
			state:      "",
			serverType: "stdio",
			expected:   "", // normalizeState returns empty string as-is
		},
		{
			name:       "unknown state",
			state:      "Unknown",
			serverType: "stdio",
			expected:   "Unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// formatStatePlain just normalizes the state, server type is used by formatStateForServerTypePlain
			result := builder.formatStatePlain(tt.state)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestTableBuilder_FormatCellValuePlain_State(t *testing.T) {
	builder := &TableBuilder{}

	// State values are stored directly in the CRD with context-appropriate
	// terminology. No translation is needed - values are returned as-is.

	// Test that state column returns values as-is
	rowContext := map[string]interface{}{
		"type": "streamable-http",
	}
	result := builder.FormatCellValuePlain("state", "Connected", rowContext)
	assert.Equal(t, "Connected", result)

	// Test with stdio server type - values are already correct in CRD
	rowContext = map[string]interface{}{
		"type": "stdio",
	}
	result = builder.FormatCellValuePlain("state", "Running", rowContext)
	assert.Equal(t, "Running", result)

	// Test without context (should still work)
	result = builder.FormatCellValuePlain("state", "Failed", nil)
	assert.Equal(t, "Failed", result)

	// Test case insensitivity of column name
	result = builder.FormatCellValuePlain("STATE", "Connecting", nil)
	assert.Equal(t, "Connecting", result)
}

func TestTableBuilder_FormatSessionAuthPlain(t *testing.T) {
	builder := &TableBuilder{}

	// Per issue #337, session auth status should display user-friendly values
	tests := []struct {
		name     string
		auth     string
		expected string
	}{
		{"empty", "", "-"},
		{"authenticated", "authenticated", "Authenticated"},
		{"auth_required", "auth_required", "Pending Auth"},
		{"token_expired", "token_expired", "Expired"},
		{"unknown", "unknown", "-"},
		{"case insensitive - AUTHENTICATED", "AUTHENTICATED", "Authenticated"},
		{"case insensitive - Auth_Required", "Auth_Required", "Pending Auth"},
		{"custom value", "custom_status", "custom_status"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := builder.formatSessionAuthPlain(tt.auth)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestTableBuilder_FormatSessionStatusPlain(t *testing.T) {
	builder := &TableBuilder{}

	tests := []struct {
		name     string
		status   string
		expected string
	}{
		{"empty", "", "-"},
		{"connected", "connected", "Connected"},
		{"disconnected", "disconnected", "Disconnected"},
		{"pending_auth", "pending_auth", "Pending Auth"},
		{"failed", "failed", "Failed"},
		{"case insensitive - CONNECTED", "CONNECTED", "Connected"},
		{"custom value", "custom_status", "custom_status"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := builder.formatSessionStatusPlain(tt.status)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestTableBuilder_FormatCellValuePlain_SessionAuth(t *testing.T) {
	builder := &TableBuilder{}

	// Test that sessionauth column triggers formatSessionAuthPlain
	// Per issue #337, values should be user-friendly
	result := builder.FormatCellValuePlain("sessionAuth", "authenticated", nil)
	assert.Equal(t, "Authenticated", result)

	result = builder.FormatCellValuePlain("sessionAuth", "auth_required", nil)
	assert.Equal(t, "Pending Auth", result)

	result = builder.FormatCellValuePlain("sessionAuth", "", nil)
	assert.Equal(t, "-", result)
}

func TestTableBuilder_FormatCellValuePlain_SessionStatus(t *testing.T) {
	builder := &TableBuilder{}

	// Test that sessionstatus column triggers formatSessionStatusPlain
	result := builder.FormatCellValuePlain("sessionStatus", "connected", nil)
	assert.Equal(t, "Connected", result)

	result = builder.FormatCellValuePlain("sessionStatus", "pending_auth", nil)
	assert.Equal(t, "Pending Auth", result)
}

func TestTableBuilder_FormatCellValuePlain_ToolsCount(t *testing.T) {
	builder := &TableBuilder{}

	// Test toolscount column
	result := builder.FormatCellValuePlain("toolsCount", "15", nil)
	assert.Equal(t, "15", result)

	result = builder.FormatCellValuePlain("toolsCount", "0", nil)
	assert.Equal(t, "-", result)

	result = builder.FormatCellValuePlain("toolsCount", "", nil)
	assert.Equal(t, "-", result)
}
