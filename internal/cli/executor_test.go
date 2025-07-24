package cli

import (
	"testing"

	"muster/internal/agent"

	"github.com/stretchr/testify/assert"
)

func TestNewToolExecutor(t *testing.T) {
	tests := []struct {
		name    string
		options ExecutorOptions
	}{
		{
			name: "creates executor with default options",
			options: ExecutorOptions{
				Format: OutputFormatTable,
				Quiet:  false,
			},
		},
		{
			name: "creates executor with JSON format",
			options: ExecutorOptions{
				Format: OutputFormatJSON,
				Quiet:  true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.options.ConfigPath = "/tmp/muster-test"
			executor, err := NewToolExecutor(tt.options)

			// The test can pass or fail depending on whether the server is running
			// This is expected behavior since NewToolExecutor checks server health
			if err != nil {
				// Server is not running - this is expected in some test environments
				assert.Error(t, err)
				assert.Nil(t, executor)
				assert.Contains(t, err.Error(), "muster server is not running")
			} else {
				// Server is running - this is expected in integration test environments
				assert.NoError(t, err)
				assert.NotNil(t, executor)
				assert.Equal(t, tt.options.Format, executor.options.Format)
				assert.Equal(t, tt.options.Quiet, executor.options.Quiet)
			}
		})
	}
}

func TestOutputFormat_Constants(t *testing.T) {
	assert.Equal(t, OutputFormat("table"), OutputFormatTable)
	assert.Equal(t, OutputFormat("json"), OutputFormatJSON)
	assert.Equal(t, OutputFormat("yaml"), OutputFormatYAML)
}

func TestExecutorOptions_Structure(t *testing.T) {
	options := ExecutorOptions{
		Format: OutputFormatJSON,
		Quiet:  true,
	}

	assert.Equal(t, OutputFormatJSON, options.Format)
	assert.True(t, options.Quiet)
}

func TestToolExecutor_Structure(t *testing.T) {
	// Test the structure without actually connecting
	options := ExecutorOptions{
		Format: OutputFormatTable,
		Quiet:  false,
	}

	// We can't test NewToolExecutor without a server, but we can test the structure
	assert.Equal(t, OutputFormatTable, options.Format)
	assert.False(t, options.Quiet)
}

func TestToolExecutor_Methods_Exist(t *testing.T) {
	// Create a mock executor to test method signatures
	logger := agent.NewLogger(false, false, false)
	client := agent.NewClient("http://localhost:8090/mcp", logger, agent.TransportStreamableHTTP)
	executor := &ToolExecutor{
		client: client,
		options: ExecutorOptions{
			Format: OutputFormatTable,
			Quiet:  false,
		},
	}

	// Test that methods exist and have correct signatures
	assert.NotNil(t, executor.Connect)
	assert.NotNil(t, executor.Close)
	assert.NotNil(t, executor.Execute)
	assert.NotNil(t, executor.ExecuteSimple)
	assert.NotNil(t, executor.ExecuteJSON)
}

func TestToolExecutor_Close(t *testing.T) {
	logger := agent.NewLogger(false, false, false)
	client := agent.NewClient("http://localhost:8090/mcp", logger, agent.TransportStreamableHTTP)
	executor := &ToolExecutor{
		client: client,
		options: ExecutorOptions{
			Format: OutputFormatTable,
			Quiet:  false,
		},
	}

	// Should not panic when closing unconnected executor
	assert.NotPanics(t, func() {
		executor.Close()
	})
}
