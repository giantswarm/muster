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

func TestTableBuilder_FormatPhasePlain(t *testing.T) {
	builder := &TableBuilder{}

	tests := []struct {
		name       string
		phase      string
		serverType string
		expected   string
	}{
		// Remote server (http/sse) - uses Connected/Disconnected terminology
		{
			name:       "Ready phase with remote server",
			phase:      "Ready",
			serverType: "streamable-http",
			expected:   "Connected",
		},
		{
			name:       "Pending phase with remote server",
			phase:      "Pending",
			serverType: "sse",
			expected:   "Connecting",
		},
		{
			name:       "Failed phase with remote server",
			phase:      "Failed",
			serverType: "streamable-http",
			expected:   "Failed",
		},
		// Local server (stdio) - uses Running/Stopped terminology
		{
			name:       "Ready phase with stdio server",
			phase:      "Ready",
			serverType: "stdio",
			expected:   "Running",
		},
		{
			name:       "Pending phase with stdio server",
			phase:      "Pending",
			serverType: "stdio",
			expected:   "Starting",
		},
		{
			name:       "Failed phase with stdio server",
			phase:      "Failed",
			serverType: "stdio",
			expected:   "Failed",
		},
		// Edge cases
		{
			name:       "empty phase",
			phase:      "",
			serverType: "stdio",
			expected:   "-",
		},
		{
			name:       "unknown phase",
			phase:      "Unknown",
			serverType: "stdio",
			expected:   "Unknown",
		},
		{
			name:       "case insensitive - lowercase ready",
			phase:      "ready",
			serverType: "stdio",
			expected:   "Running",
		},
		{
			name:       "case insensitive - uppercase PENDING",
			phase:      "PENDING",
			serverType: "sse",
			expected:   "Connecting",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := builder.formatPhasePlain(tt.phase, tt.serverType)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestTableBuilder_NormalizePhase(t *testing.T) {
	builder := &TableBuilder{}

	tests := []struct {
		name     string
		phase    string
		isRemote bool
		expected string
	}{
		// Remote servers
		{"Ready remote", "Ready", true, "Connected"},
		{"Pending remote", "Pending", true, "Connecting"},
		{"Failed remote", "Failed", true, "Failed"},
		// Local servers
		{"Ready local", "Ready", false, "Running"},
		{"Pending local", "Pending", false, "Starting"},
		{"Failed local", "Failed", false, "Failed"},
		// Edge cases
		{"empty phase", "", false, "-"},
		{"unknown phase", "CustomPhase", false, "CustomPhase"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := builder.normalizePhase(tt.phase, tt.isRemote)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestTableBuilder_FormatCellValuePlain_Phase(t *testing.T) {
	builder := &TableBuilder{}

	// Test that phase column triggers formatPhasePlain with context
	rowContext := map[string]interface{}{
		"type": "streamable-http",
	}
	result := builder.FormatCellValuePlain("phase", "Ready", rowContext)
	assert.Equal(t, "Connected", result)

	// Test with stdio server type
	rowContext = map[string]interface{}{
		"type": "stdio",
	}
	result = builder.FormatCellValuePlain("phase", "Ready", rowContext)
	assert.Equal(t, "Running", result)

	// Test without context (should still work)
	result = builder.FormatCellValuePlain("phase", "Failed", nil)
	assert.Equal(t, "Failed", result)

	// Test case insensitivity of column name
	result = builder.FormatCellValuePlain("PHASE", "Pending", nil)
	assert.Equal(t, "Starting", result)
}

func TestTableBuilder_FormatSessionAuthPlain(t *testing.T) {
	builder := &TableBuilder{}

	tests := []struct {
		name     string
		auth     string
		expected string
	}{
		{"empty", "", "-"},
		{"authenticated", "authenticated", "OK"},
		{"auth_required", "auth_required", "Required"},
		{"token_expired", "token_expired", "Expired"},
		{"unknown", "unknown", "-"},
		{"case insensitive - AUTHENTICATED", "AUTHENTICATED", "OK"},
		{"case insensitive - Auth_Required", "Auth_Required", "Required"},
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
	result := builder.FormatCellValuePlain("sessionAuth", "authenticated", nil)
	assert.Equal(t, "OK", result)

	result = builder.FormatCellValuePlain("sessionAuth", "auth_required", nil)
	assert.Equal(t, "Required", result)

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
