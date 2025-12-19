package mcpserver

import (
	"context"
	"testing"
	"time"

	"muster/internal/api"
	"muster/internal/services"

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
