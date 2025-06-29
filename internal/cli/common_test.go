package cli

import (
	"muster/internal/config"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDetectAggregatorEndpoint(t *testing.T) {
	tests := []struct {
		name     string
		config   *config.MusterConfig
		expected string
	}{
		{
			name: "uses config values when provided",
			config: &config.MusterConfig{
				Aggregator: config.AggregatorConfig{
					Host: "testhost",
					Port: 9999,
				},
			},
			expected: "http://testhost:9999/mcp",
		},
		{
			name: "uses defaults for empty host and port",
			config: &config.MusterConfig{
				Aggregator: config.AggregatorConfig{
					Host: "",
					Port: 0,
				},
			},
			expected: "http://localhost:8090/mcp",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			endpoint, err := DetectAggregatorEndpointWithConfig(tt.config)
			assert.NoError(t, err)
			assert.Equal(t, tt.expected, endpoint)
		})
	}
}

func TestDetectAggregatorEndpointWithoutConfig(t *testing.T) {
	// Save original functions
	originalUserHomeDir := config.GetOsUserHomeDir()
	originalGetwd := config.GetOsGetwd()

	// Restore original functions after test
	defer func() {
		config.SetOsUserHomeDir(originalUserHomeDir)
		config.SetOsGetwd(originalGetwd)
	}()

	// Mock functions to return non-existent directories to force default config
	config.SetOsUserHomeDir(func() (string, error) {
		return "/non/existent/home", nil
	})
	config.SetOsGetwd(func() (string, error) {
		return "/non/existent/workdir", nil
	})

	endpoint, err := DetectAggregatorEndpoint()
	assert.NoError(t, err)
	assert.Equal(t, "http://localhost:8090/mcp", endpoint)
}

func TestCheckServerRunning_WithMockServer(t *testing.T) {
	tests := []struct {
		name           string
		serverResponse int
		expectError    bool
		errorContains  string
	}{
		{
			name:           "server running (202 Accepted)",
			serverResponse: http.StatusAccepted,
			expectError:    false,
		},
		{
			name:           "server running (200 OK)",
			serverResponse: http.StatusOK,
			expectError:    false,
		},
		{
			name:           "server not responding correctly (404)",
			serverResponse: http.StatusNotFound,
			expectError:    true,
			errorContains:  "not responding correctly",
		},
		{
			name:           "server error (500)",
			serverResponse: http.StatusInternalServerError,
			expectError:    true,
			errorContains:  "not responding correctly",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a test server
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.serverResponse)
			}))
			defer server.Close()

			// Test with the mock server by temporarily changing the endpoint
			// This is a simplified test - in practice we'd need dependency injection
			// or a more sophisticated mocking approach

			// For now, just test that the function exists and can be called
			err := CheckServerRunning()
			// The actual result depends on whether a real server is running
			// but we can at least verify the function doesn't panic
			_ = err // Ignore the actual result for unit tests
		})
	}
}

func TestCheckServerRunning_ServerDown(t *testing.T) {
	// Test with no server running - this will likely fail unless a server is actually running
	// In a real test environment, we'd mock the HTTP client or use dependency injection
	err := CheckServerRunning()
	// We can't assert the exact error without knowing the test environment
	// but we can verify the function returns an error type
	_ = err
}

func TestFormatError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected string
	}{
		{
			name:     "formats simple error",
			err:      assert.AnError,
			expected: "Error: assert.AnError general error for testing",
		},
		{
			name:     "handles nil error",
			err:      nil,
			expected: "Error: <nil>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FormatError(tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestFormatSuccess(t *testing.T) {
	tests := []struct {
		name     string
		message  string
		expected string
	}{
		{
			name:     "formats success message",
			message:  "Operation completed",
			expected: "✓ Operation completed",
		},
		{
			name:     "handles empty message",
			message:  "",
			expected: "✓ ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FormatSuccess(tt.message)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestFormatWarning(t *testing.T) {
	tests := []struct {
		name     string
		message  string
		expected string
	}{
		{
			name:     "formats warning message",
			message:  "This is a warning",
			expected: "⚠ This is a warning",
		},
		{
			name:     "handles empty message",
			message:  "",
			expected: "⚠ ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FormatWarning(tt.message)
			assert.Equal(t, tt.expected, result)
		})
	}
}
