package cli

import (
	"testing"
	"time"

	"github.com/giantswarm/muster/v5/internal/agent"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewToolExecutorCallTimeout(t *testing.T) {
	for _, tc := range []struct {
		name    string
		timeout time.Duration
		want    time.Duration
	}{
		{name: "unset keeps the default", want: agent.DefaultCallTimeout},
		{name: "set bounds every call", timeout: 45 * time.Minute, want: 45 * time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			executor, err := NewToolExecutor(ExecutorOptions{Endpoint: "https://muster.example.invalid/mcp", CallTimeout: tc.timeout})
			require.NoError(t, err)
			assert.Equal(t, tc.want, executor.client.CallTimeout())
		})
	}
}
