package mcpserver

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/pkg/logging"
)

func TestOperationTimeoutIsClampedToTheCRDMaximum(t *testing.T) {
	var logs bytes.Buffer
	logging.InitForCLI(logging.LevelInfo, &logs)
	t.Cleanup(func() { logging.InitForCLI(logging.LevelInfo, os.Stderr) })

	svc, err := NewService(&api.MCPServer{Name: "slow", Type: api.MCPServerTypeStreamableHTTP, URL: "http://example.com/mcp", Timeout: 900})
	require.NoError(t, err)
	require.Equal(t, api.MaxTimeoutSeconds*time.Second, svc.operationTimeout())
	require.Equal(t, 1, strings.Count(logs.String(), "spec.timeout 900s is above the maximum of 300s"))

	ctx, cancel := svc.getRemoteInitContext(t.Context())
	t.Cleanup(cancel)
	deadline, ok := ctx.Deadline()
	require.True(t, ok)
	require.LessOrEqual(t, time.Until(deadline), api.MaxTimeoutSeconds*time.Second)

	require.NoError(t, svc.UpdateConfiguration(&api.MCPServer{Name: "slow", Type: api.MCPServerTypeStreamableHTTP, URL: "http://example.com/mcp", Timeout: 600}))
	require.Equal(t, api.MaxTimeoutSeconds*time.Second, svc.operationTimeout())
	require.Equal(t, 1, strings.Count(logs.String(), "spec.timeout 600s is above the maximum of 300s"))
}

func TestOperationTimeoutWithinTheMaximumIsNotLogged(t *testing.T) {
	var logs bytes.Buffer
	logging.InitForCLI(logging.LevelInfo, &logs)
	t.Cleanup(func() { logging.InitForCLI(logging.LevelInfo, os.Stderr) })

	svc, err := NewService(&api.MCPServer{Name: "bounded", Type: api.MCPServerTypeStreamableHTTP, URL: "http://example.com/mcp", Timeout: api.MaxTimeoutSeconds})
	require.NoError(t, err)
	require.Equal(t, api.MaxTimeoutSeconds*time.Second, svc.operationTimeout())
	require.NotContains(t, logs.String(), "above the maximum")
}
