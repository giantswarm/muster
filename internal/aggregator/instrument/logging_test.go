package instrument

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/giantswarm/muster/pkg/logging"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"
)

// captureLog initializes muster's logger to write JSON to a buffer for
// the duration of one test.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	logging.InitForCLI(logging.LevelInfo, &buf)
	return &buf
}

func TestLogging(t *testing.T) {
	cases := []struct {
		name        string
		handler     server.ToolHandlerFunc
		toolName    string
		wantOutcome string
		wantErrText string
	}{
		{
			name: "ok",
			handler: func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return &mcp.CallToolResult{}, nil
			},
			toolName:    "x_kubernetes_list_pods",
			wantOutcome: outcomeOK,
		},
		{
			name: "error",
			handler: func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return nil, errors.New("upstream down")
			},
			toolName:    "x_prom_query",
			wantOutcome: outcomeError,
			wantErrText: "upstream down",
		},
		{
			name: "error_result",
			handler: func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return &mcp.CallToolResult{IsError: true}, nil
			},
			toolName:    "workflow_run",
			wantOutcome: outcomeErrorResult,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buf := captureLog(t)
			wrapped := Logging()(tc.handler)
			_, _ = wrapped(context.Background(), callRequest(tc.toolName))

			line := lastJSONLine(t, buf)
			require.Equal(t, LogMessage, line["msg"])
			require.Equal(t, LogSubsystem, line["subsystem"])
			require.Equal(t, tc.toolName, line["tool"])
			require.Equal(t, tc.wantOutcome, line["outcome"])
			require.Contains(t, line, "duration_s")
			if tc.wantErrText != "" {
				require.Equal(t, tc.wantErrText, line["error"])
			} else {
				_, hasErr := line["error"]
				require.False(t, hasErr, "expected no error field on non-error outcome")
			}
		})
	}
}

// lastJSONLine returns the last newline-terminated JSON object written
// to buf. muster's TextHandler is the default, so we accept the line
// being either JSON or a "key=value" string; here we parse the slog
// text form by extracting key=value pairs into a map.
func lastJSONLine(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	require.NotEmpty(t, lines)
	raw := lines[len(lines)-1]

	// Try JSON first.
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err == nil {
		return m
	}
	// Fall back to slog text format: "k1=v1 k2=v2 k3=\"v with spaces\"".
	return parseSlogText(t, raw)
}

func parseSlogText(t *testing.T, s string) map[string]any {
	t.Helper()
	out := map[string]any{}
	i := 0
	for i < len(s) {
		for i < len(s) && s[i] == ' ' {
			i++
		}
		eq := strings.IndexByte(s[i:], '=')
		if eq < 0 {
			break
		}
		key := s[i : i+eq]
		i += eq + 1
		var val string
		if i < len(s) && s[i] == '"' {
			end := strings.IndexByte(s[i+1:], '"')
			require.GreaterOrEqual(t, end, 0)
			val = s[i+1 : i+1+end]
			i += end + 2
		} else {
			end := strings.IndexByte(s[i:], ' ')
			if end < 0 {
				val = s[i:]
				i = len(s)
			} else {
				val = s[i : i+end]
				i += end
			}
		}
		out[key] = val
	}
	return out
}
