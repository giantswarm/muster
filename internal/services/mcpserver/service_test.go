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
		Type:    api.MCPServerTypeLocalCommand,
		Command: []string{"echo", "hello"},
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
		Type:    api.MCPServerTypeLocalCommand,
		Command: []string{"echo", "hello"},
	}

	svc, err := NewService(def)
	require.NoError(t, err)

	// Test start with short timeout context (echo exits immediately and isn't an MCP server)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	err = svc.Start(ctx)
	// Start will fail since echo exits immediately, but we test the interface
	if err != nil {
		assert.Contains(t, err.Error(), "failed to start")
	}

	// Test stop - should work regardless of start failure
	err = svc.Stop(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, services.StateStopped, svc.GetState())
}

func TestRestart(t *testing.T) {
	def := &api.MCPServer{
		Name:    "test-server",
		Type:    api.MCPServerTypeLocalCommand,
		Command: []string{"echo", "hello"},
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
		Type:    api.MCPServerTypeLocalCommand,
		Command: []string{"echo", "hello"},
	}

	svc, err := NewService(def)
	require.NoError(t, err)

	deps := svc.GetDependencies()
	assert.Empty(t, deps)
}

func TestServiceData(t *testing.T) {
	def := &api.MCPServer{
		Name:    "test-server",
		Type:    api.MCPServerTypeLocalCommand,
		Command: []string{"echo", "hello"},
	}

	svc, err := NewService(def)
	require.NoError(t, err)

	data := svc.GetServiceData()
	assert.NotNil(t, data)
	assert.Contains(t, data, "name")
	assert.Equal(t, "test-server", data["name"])
	assert.Contains(t, data, "type")
	assert.Equal(t, api.MCPServerTypeLocalCommand, data["type"])
}
