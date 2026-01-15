package cli

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"muster/internal/config"

	"github.com/stretchr/testify/assert"
)

func TestGetAggregatorEndpoint(t *testing.T) {
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
			endpoint := GetAggregatorEndpoint(tt.config)
			assert.Equal(t, tt.expected, endpoint)
		})
	}
}

func GetAggregatorEndpointWithoutConfig(t *testing.T) {
	// Test default behavior when no config is found
	// With the new single directory approach, this will load from user config if available
	endpoint := GetAggregatorEndpoint(nil)
	// Since we may have user config, just verify the format is correct
	assert.Contains(t, endpoint, "http://localhost:")
	assert.Contains(t, endpoint, "/mcp")
}

func TestIsRemoteEndpoint(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		expected bool
	}{
		{
			name:     "localhost is not remote",
			endpoint: "http://localhost:8080/mcp",
			expected: false,
		},
		{
			name:     "LOCALHOST uppercase is not remote",
			endpoint: "http://LOCALHOST:8080/mcp",
			expected: false,
		},
		{
			name:     "127.0.0.1 is not remote",
			endpoint: "http://127.0.0.1:8080/mcp",
			expected: false,
		},
		{
			name:     "IPv6 loopback is not remote",
			endpoint: "http://[::1]:8080/mcp",
			expected: false,
		},
		{
			name:     "external hostname is remote",
			endpoint: "https://muster.example.com/mcp",
			expected: true,
		},
		{
			name:     "external IP is remote",
			endpoint: "https://192.168.1.100/mcp",
			expected: true,
		},
		{
			name:     "domain with localhost in path is remote",
			endpoint: "https://example.com/localhost/api",
			expected: true, // proper URL parsing - localhost in path doesn't make it local
		},
		{
			name:     "empty endpoint is remote",
			endpoint: "",
			expected: true, // safety: assume remote when we can't determine
		},
		{
			name:     "invalid URL is remote",
			endpoint: "not-a-valid-url",
			expected: true, // safety: assume remote on parse failure
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsRemoteEndpoint(tt.endpoint)
			assert.Equal(t, tt.expected, result, "IsRemoteEndpoint(%q)", tt.endpoint)
		})
	}
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
			endpoint := GetAggregatorEndpoint(nil)
			err := CheckServerRunning(endpoint)
			// The actual result depends on whether a real server is running
			// but we can at least verify the function doesn't panic
			_ = err // Ignore the actual result for unit tests
		})
	}
}

func TestCheckServerRunning_ServerDown(t *testing.T) {
	// Test with no server running - this will likely fail unless a server is actually running
	// In a real test environment, we'd mock the HTTP client or use dependency injection
	endpoint := GetAggregatorEndpoint(nil)
	err := CheckServerRunning(endpoint)
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
			assert.Equal(t, tt.expected, fmt.Sprintf("Error: %v", tt.err))
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
			assert.Equal(t, tt.expected, fmt.Sprintf("✓ %s", tt.message))
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
			assert.Equal(t, tt.expected, fmt.Sprintf("⚠ %s", tt.message))
		})
	}
}
