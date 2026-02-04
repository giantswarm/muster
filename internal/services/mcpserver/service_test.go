package mcpserver

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/services"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServiceInfo(t *testing.T) {
	def := &api.MCPServer{
		Name:    "test-server",
		Type:    api.MCPServerTypeStdio,
		Command: "echo",
		Args:    []string{"hello"},
	}

	svc, err := NewService(def)
	require.NoError(t, err)

	assert.Equal(t, "test-server", svc.GetName())
	assert.Equal(t, services.TypeMCPServer, svc.GetType())
	assert.Equal(t, services.StateUnknown, svc.GetState())
	assert.Equal(t, services.HealthUnknown, svc.GetHealth())
}

func TestStartStop(t *testing.T) {
	def := &api.MCPServer{
		Name:    "test-server",
		Type:    api.MCPServerTypeStdio,
		Command: "echo",
		Args:    []string{"hello"},
	}

	svc, err := NewService(def)
	require.NoError(t, err)

	// Test start with short timeout context (echo exits immediately and isn't an MCP server)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	err = svc.Start(ctx)
	// Start will fail since echo exits immediately, but we test the interface
	assert.Error(t, err) // Echo is not an MCP server, so it should fail

	assert.Equal(t, services.StateFailed, svc.GetState())
	assert.Equal(t, services.HealthUnhealthy, svc.GetHealth())
}

func TestRestart(t *testing.T) {
	def := &api.MCPServer{
		Name:    "test-server",
		Type:    api.MCPServerTypeStdio,
		Command: "echo",
		Args:    []string{"hello"},
	}

	svc, err := NewService(def)
	require.NoError(t, err)

	// Test restart with short timeout context
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	err = svc.Restart(ctx)
	// Restart will fail since echo exits immediately, but we test the interface
	if err != nil {
		assert.Contains(t, err.Error(), "failed to start")
	}
}

func TestDependencies(t *testing.T) {
	def := &api.MCPServer{
		Name:    "test-server",
		Type:    api.MCPServerTypeStdio,
		Command: "echo",
		Args:    []string{"hello"},
	}

	svc, err := NewService(def)
	require.NoError(t, err)

	deps := svc.GetDependencies()
	assert.Empty(t, deps)
}

func TestValidateConfiguration(t *testing.T) {
	tests := []struct {
		name    string
		def     *api.MCPServer
		wantErr bool
	}{
		{
			name: "valid stdio server",
			def: &api.MCPServer{
				Name:    "test-server",
				Type:    api.MCPServerTypeStdio,
				Command: "echo",
				Args:    []string{"hello"},
			},
			wantErr: false,
		},
		{
			name: "stdio server missing command",
			def: &api.MCPServer{
				Name: "test-server",
				Type: api.MCPServerTypeStdio,
			},
			wantErr: true,
		},
		{
			name: "valid streamable-http server",
			def: &api.MCPServer{
				Name:    "test-server",
				Type:    api.MCPServerTypeStreamableHTTP,
				URL:     "http://example.com/mcp",
				Timeout: 30,
			},
			wantErr: false,
		},
		{
			name: "streamable-http server missing URL",
			def: &api.MCPServer{
				Name:    "test-server",
				Type:    api.MCPServerTypeStreamableHTTP,
				Timeout: 30,
			},
			wantErr: true,
		},
		{
			name: "streamable-http server with default timeout",
			def: &api.MCPServer{
				Name: "test-server",
				Type: api.MCPServerTypeStreamableHTTP,
				URL:  "http://example.com/mcp",
				// Note: timeout defaults to DefaultRemoteTimeout when not specified
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, err := NewService(tt.def)
			require.NoError(t, err)

			err = svc.ValidateConfiguration()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestGetServiceData(t *testing.T) {
	def := &api.MCPServer{
		Name:      "test-server",
		Type:      api.MCPServerTypeStdio,
		Command:   "echo",
		Args:      []string{"hello"},
		AutoStart: true,
		Env:       map[string]string{"TEST": "value"},
	}

	svc, err := NewService(def)
	require.NoError(t, err)

	data := svc.GetServiceData()
	assert.Equal(t, "test-server", data["name"])
	assert.Equal(t, api.MCPServerTypeStdio, data["type"])
	assert.Equal(t, true, data["autoStart"])
	assert.Equal(t, "echo", data["command"])
	assert.Equal(t, []string{"hello"}, data["args"])
	assert.Equal(t, map[string]string{"TEST": "value"}, data["env"])
}

// TestDefaultRemoteTimeoutMatchesCRD verifies that DefaultRemoteTimeout matches the expected
// kubebuilder:default value defined in MCPServerSpec.Timeout.
// The CRD defines: +kubebuilder:default=30
// This test ensures the constant stays in sync with the CRD annotation.
func TestDefaultRemoteTimeoutMatchesCRD(t *testing.T) {
	// The kubebuilder:default annotation in pkg/apis/muster/v1alpha1/mcpserver_types.go
	// specifies +kubebuilder:default=30 for the Timeout field.
	// If this test fails, update DefaultRemoteTimeout to match the CRD annotation.
	const expectedCRDDefault = 30

	assert.Equal(t, expectedCRDDefault, DefaultRemoteTimeout,
		"DefaultRemoteTimeout (%d) should match the kubebuilder:default annotation (%d) in MCPServerSpec.Timeout. "+
			"Update the constant in service.go if the CRD default changes.",
		DefaultRemoteTimeout, expectedCRDDefault)
}

// TestGetRemoteInitContext verifies that the remote initialization context uses the correct timeout
func TestGetRemoteInitContext(t *testing.T) {
	tests := []struct {
		name            string
		timeout         int
		expectedTimeout time.Duration
	}{
		{
			name:            "uses configured timeout",
			timeout:         60,
			expectedTimeout: 60 * time.Second,
		},
		{
			name:            "uses default when zero",
			timeout:         0,
			expectedTimeout: time.Duration(DefaultRemoteTimeout) * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			def := &api.MCPServer{
				Name:    "test-server",
				Type:    api.MCPServerTypeStreamableHTTP,
				URL:     "http://example.com/mcp",
				Timeout: tt.timeout,
			}

			svc, err := NewService(def)
			require.NoError(t, err)

			ctx := context.Background()
			initCtx, cancel := svc.getRemoteInitContext(ctx)
			defer cancel()

			deadline, ok := initCtx.Deadline()
			assert.True(t, ok, "context should have a deadline")

			// The deadline should be approximately expectedTimeout from now
			// Allow 1 second tolerance for test execution time
			expectedDeadline := time.Now().Add(tt.expectedTimeout)
			assert.WithinDuration(t, expectedDeadline, deadline, 1*time.Second)
		})
	}
}

// TestIsTransientConnectivityError verifies detection of transient network errors
func TestIsTransientConnectivityError(t *testing.T) {
	svc, err := NewService(&api.MCPServer{
		Name:    "test-server",
		Type:    api.MCPServerTypeStreamableHTTP,
		URL:     "http://example.com/mcp",
		Timeout: 30,
	})
	require.NoError(t, err)

	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		// Transient errors - should return true
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "connection refused",
			err:      assert.AnError, // will be replaced
			expected: true,
		},
		{
			name:     "connection reset",
			err:      assert.AnError,
			expected: true,
		},
		{
			name:     "connection timed out",
			err:      assert.AnError,
			expected: true,
		},
		{
			name:     "no such host",
			err:      assert.AnError,
			expected: true,
		},
		{
			name:     "network is unreachable",
			err:      assert.AnError,
			expected: true,
		},
		{
			name:     "host is unreachable",
			err:      assert.AnError,
			expected: true,
		},
		{
			name:     "no route to host",
			err:      assert.AnError,
			expected: true,
		},
		{
			name:     "dial tcp error",
			err:      assert.AnError,
			expected: true,
		},
		{
			name:     "i/o timeout",
			err:      assert.AnError,
			expected: true,
		},
		{
			name:     "eof",
			err:      assert.AnError,
			expected: true,
		},
		{
			name:     "connection closed",
			err:      assert.AnError,
			expected: true,
		},
		{
			name:     "context deadline exceeded",
			err:      assert.AnError,
			expected: true,
		},
		{
			name:     "context canceled",
			err:      assert.AnError,
			expected: true,
		},
		// Configuration errors - should return false (not transient)
		{
			name:     "certificate error",
			err:      assert.AnError,
			expected: false,
		},
		{
			name:     "x509 error",
			err:      assert.AnError,
			expected: false,
		},
		{
			name:     "tls handshake failure",
			err:      assert.AnError,
			expected: false,
		},
		{
			name:     "certificate has expired",
			err:      assert.AnError,
			expected: false,
		},
		// Other errors - should return false
		{
			name:     "random application error",
			err:      assert.AnError,
			expected: false,
		},
	}

	// Build actual error messages for testing
	errorMessages := map[string]string{
		"connection refused":        "dial tcp 127.0.0.1:8080: connection refused",
		"connection reset":          "read tcp: connection reset by peer",
		"connection timed out":      "dial tcp: connection timed out",
		"no such host":              "lookup invalid.host: no such host",
		"network is unreachable":    "dial tcp: network is unreachable",
		"host is unreachable":       "dial tcp: host is unreachable",
		"no route to host":          "dial tcp: no route to host",
		"dial tcp error":            "dial tcp 192.168.1.1:443: connect: connection refused",
		"i/o timeout":               "read tcp: i/o timeout",
		"eof":                       "read: eof",
		"connection closed":         "connection closed by remote host",
		"context deadline exceeded": "context deadline exceeded",
		"context canceled":          "context canceled",
		"certificate error":         "x509: certificate signed by unknown authority",
		"x509 error":                "x509: certificate has expired",
		"tls handshake failure":     "tls handshake failure: certificate required",
		"certificate has expired":   "certificate has expired or is not yet valid",
		"random application error":  "some random error that is not connectivity related",
	}

	// Add HTTP 5xx error tests - comprehensive coverage of server errors
	http5xxTests := []struct {
		name     string
		errMsg   string
		expected bool
	}{
		// Core 5xx status codes
		{"status 500", "request failed with status 500: Internal Server Error", true},
		{"status 501", "request failed with status 501: Not Implemented", true},
		{"status 502", "request failed with status 502: Bad Gateway", true},
		{"status 503", "request failed with status 503: Service Temporarily Unavailable", true},
		{"status 504", "request failed with status 504: Gateway Timeout", true},
		{"status 505", "request failed with status 505: HTTP Version Not Supported", true},
		{"status 506", "request failed with status 506: Variant Also Negotiates", true},
		{"status 507", "request failed with status 507: Insufficient Storage", true},
		{"status 508", "request failed with status 508: Loop Detected", true},
		{"status 509", "request failed with status 509: Bandwidth Limit Exceeded", true},
		// Descriptive patterns for 505/506
		{"http version not supported", "HTTP Version Not Supported by server", true},
		{"variant also negotiates", "Variant Also Negotiates error from proxy", true},
		// Descriptive error messages
		{"internal server error", "HTTP 500 Internal Server Error from upstream", true},
		{"bad gateway", "502 Bad Gateway - upstream server unavailable", true},
		{"service unavailable", "Service Unavailable: try again later", true},
		{"gateway timeout", "Gateway Timeout waiting for response", true},
		// Mixed case (lowercased during check)
		{"mixed case service unavailable", "Service UNAVAILABLE", true},
		{"mixed case bad gateway", "BAD GATEWAY error", true},
		// Wrapped errors
		{"wrapped 503", "connection error: status 503 from server", true},
		// 4xx errors should NOT be transient
		{"status 400", "request failed with status 400: Bad Request", false},
		{"status 401", "request failed with status 401: Unauthorized", false},
		{"status 403", "request failed with status 403: Forbidden", false},
		{"status 404", "request failed with status 404: Not Found", false},
		{"status 429", "request failed with status 429: Too Many Requests", false},
	}

	for _, tt := range http5xxTests {
		t.Run("http5xx/"+tt.name, func(t *testing.T) {
			testErr := errors.New(tt.errMsg)
			result := svc.isTransientConnectivityError(testErr)
			assert.Equal(t, tt.expected, result, "isTransientConnectivityError(%q) = %v, want %v", tt.errMsg, result, tt.expected)
		})
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var testErr error
			if tt.err != nil {
				if msg, ok := errorMessages[tt.name]; ok {
					testErr = errors.New(msg)
				} else {
					testErr = tt.err
				}
			}

			result := svc.isTransientConnectivityError(testErr)
			assert.Equal(t, tt.expected, result, "isTransientConnectivityError(%v) = %v, want %v", testErr, result, tt.expected)
		})
	}
}

// TestIsConfigurationError verifies detection of configuration errors
func TestIsConfigurationError(t *testing.T) {
	svc, err := NewService(&api.MCPServer{
		Name:    "test-server",
		Type:    api.MCPServerTypeStreamableHTTP,
		URL:     "http://example.com/mcp",
		Timeout: 30,
	})
	require.NoError(t, err)

	tests := []struct {
		name     string
		errMsg   string
		expected bool
	}{
		{
			name:     "certificate signed by unknown authority",
			errMsg:   "x509: certificate signed by unknown authority",
			expected: true,
		},
		{
			name:     "certificate has expired",
			errMsg:   "x509: certificate has expired or is not yet valid",
			expected: true,
		},
		{
			name:     "tls handshake failure",
			errMsg:   "tls handshake timeout",
			expected: true,
		},
		{
			name:     "x509 generic",
			errMsg:   "x509: something went wrong",
			expected: true,
		},
		{
			name:     "connection refused - not config error",
			errMsg:   "dial tcp: connection refused",
			expected: false,
		},
		{
			name:     "timeout - not config error",
			errMsg:   "context deadline exceeded",
			expected: false,
		},
		{
			name:     "random error - not config error",
			errMsg:   "something went wrong",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testErr := errors.New(tt.errMsg)
			result := svc.isConfigurationError(testErr)
			assert.Equal(t, tt.expected, result, "isConfigurationError(%q) = %v, want %v", tt.errMsg, result, tt.expected)
		})
	}
}

// TestCalculateNextRetryTime verifies exponential backoff calculation
func TestCalculateNextRetryTime(t *testing.T) {
	tests := []struct {
		name             string
		failures         int
		expectedBackoff  time.Duration
		tolerancePercent float64
	}{
		{
			name:             "first failure - initial backoff",
			failures:         1,
			expectedBackoff:  InitialBackoff,
			tolerancePercent: 0.1, // 10% tolerance
		},
		{
			name:             "second failure - 2x backoff",
			failures:         2,
			expectedBackoff:  InitialBackoff * 2,
			tolerancePercent: 0.1,
		},
		{
			name:             "third failure - 4x backoff",
			failures:         3,
			expectedBackoff:  InitialBackoff * 4,
			tolerancePercent: 0.1,
		},
		{
			name:             "fourth failure - 8x backoff",
			failures:         4,
			expectedBackoff:  InitialBackoff * 8,
			tolerancePercent: 0.1,
		},
		{
			name:             "many failures - capped at max",
			failures:         100,
			expectedBackoff:  MaxBackoff,
			tolerancePercent: 0.1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, err := NewService(&api.MCPServer{
				Name:    "test-server",
				Type:    api.MCPServerTypeStreamableHTTP,
				URL:     "http://example.com/mcp",
				Timeout: 30,
			})
			require.NoError(t, err)

			// Set consecutive failures
			svc.failureMutex.Lock()
			svc.consecutiveFailures = tt.failures
			beforeCalc := time.Now()
			svc.calculateNextRetryTimeLocked()
			nextRetry := svc.nextRetryAfter
			svc.failureMutex.Unlock()

			require.NotNil(t, nextRetry, "nextRetryAfter should be set")

			// Verify the backoff duration is approximately correct
			actualBackoff := nextRetry.Sub(beforeCalc)
			tolerance := time.Duration(float64(tt.expectedBackoff) * tt.tolerancePercent)

			assert.InDelta(t, tt.expectedBackoff.Seconds(), actualBackoff.Seconds(), tolerance.Seconds(),
				"backoff duration should be approximately %v, got %v", tt.expectedBackoff, actualBackoff)
		})
	}
}

// TestFailureTrackingThreadSafety verifies thread-safe access to failure tracking fields
func TestFailureTrackingThreadSafety(t *testing.T) {
	svc, err := NewService(&api.MCPServer{
		Name:    "test-server",
		Type:    api.MCPServerTypeStreamableHTTP,
		URL:     "http://example.com/mcp",
		Timeout: 30,
	})
	require.NoError(t, err)

	// Simulate concurrent access to failure tracking
	done := make(chan bool)

	// Writer goroutine
	go func() {
		for i := 0; i < 100; i++ {
			svc.failureMutex.Lock()
			svc.consecutiveFailures = i
			now := time.Now()
			svc.lastAttempt = &now
			svc.calculateNextRetryTimeLocked()
			svc.failureMutex.Unlock()
		}
		done <- true
	}()

	// Reader goroutine
	go func() {
		for i := 0; i < 100; i++ {
			_ = svc.GetConsecutiveFailures()
			_ = svc.GetLastAttempt()
			_ = svc.GetNextRetryAfter()
		}
		done <- true
	}()

	// Wait for both goroutines
	<-done
	<-done

	// If we get here without a race condition, the test passes
	assert.True(t, true, "concurrent access completed without race conditions")
}

// TestIsRemoteServer verifies remote server detection
func TestIsRemoteServer(t *testing.T) {
	tests := []struct {
		name       string
		serverType api.MCPServerType
		expected   bool
	}{
		{
			name:       "stdio is not remote",
			serverType: api.MCPServerTypeStdio,
			expected:   false,
		},
		{
			name:       "streamable-http is remote",
			serverType: api.MCPServerTypeStreamableHTTP,
			expected:   true,
		},
		{
			name:       "sse is remote",
			serverType: api.MCPServerTypeSSE,
			expected:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			def := &api.MCPServer{
				Name: "test-server",
				Type: tt.serverType,
			}
			if tt.serverType == api.MCPServerTypeStdio {
				def.Command = "echo"
			} else {
				def.URL = "http://example.com/mcp"
			}

			svc, err := NewService(def)
			require.NoError(t, err)

			result := svc.isRemoteServer()
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestIsUnreachable verifies unreachable state detection
func TestIsUnreachable(t *testing.T) {
	svc, err := NewService(&api.MCPServer{
		Name:    "test-server",
		Type:    api.MCPServerTypeStreamableHTTP,
		URL:     "http://example.com/mcp",
		Timeout: 30,
	})
	require.NoError(t, err)

	// Initially not unreachable
	assert.False(t, svc.IsUnreachable())

	// Set to unreachable state
	svc.UpdateState(services.StateUnreachable, services.HealthUnknown, nil)
	assert.True(t, svc.IsUnreachable())

	// Change to running state
	svc.UpdateState(services.StateRunning, services.HealthHealthy, nil)
	assert.False(t, svc.IsUnreachable())
}
