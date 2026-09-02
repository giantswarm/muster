package metatools

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/pkg/logging"
)

// TestProvider_ExecuteToolLogsArgNamesOnly pins that the meta-tool dispatch
// log names the arguments and not their values. call_tool's arguments are the
// caller's tool arguments -- a workflow input, a header for an MCPServer spec
// -- and muster 5.7.11 printed the whole map with %v on every call.
func TestProvider_ExecuteToolLogsArgNamesOnly(t *testing.T) {
	var logBuf bytes.Buffer
	logging.InitForCLI(logging.LevelDebug, &logBuf)
	t.Cleanup(func() { logging.InitForCLI(logging.LevelInfo, io.Discard) })

	_, err := (&Provider{}).ExecuteTool(context.Background(), "no_such_meta_tool", map[string]any{
		"name":      "workflow_secret-passthrough",
		"arguments": map[string]any{"token": "eyJsecret-marker"},
	})
	require.EqualError(t, err, "unknown meta-tool: no_such_meta_tool")

	logged := logBuf.String()
	require.Contains(t, logged, "Executing tool no_such_meta_tool with args: arguments, name")
	require.NotContains(t, logged, "eyJ", "argument values must not be logged:\n%s", logged)
	require.NotContains(t, logged, "secret-passthrough", "nested argument values must not be logged:\n%s", logged)
}
