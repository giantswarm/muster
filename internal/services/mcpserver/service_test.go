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
			name: "streamable-http server missing timeout",
			def: &api.MCPServer{
				Name: "test-server",
				Type: api.MCPServerTypeStreamableHTTP,
				URL:  "http://example.com/mcp",
			},
			wantErr: true,
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
