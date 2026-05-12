package instrument

import (
	"context"
	"errors"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func setupTracer(t *testing.T) *tracetest.InMemoryExporter {
	t.Helper()
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { otel.SetTracerProvider(prev) })
	return exp
}

func callRequest(name string) mcp.CallToolRequest {
	return mcp.CallToolRequest{Params: mcp.CallToolParams{Name: name}}
}

func TestTracing(t *testing.T) {
	cases := []struct {
		name        string
		handler     server.ToolHandlerFunc
		toolName    string
		wantCode    codes.Code
		wantErrAttr bool
	}{
		{
			name: "ok result has no error status",
			handler: func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return &mcp.CallToolResult{}, nil
			},
			toolName: "x_kubernetes_list_pods",
			wantCode: codes.Unset,
		},
		{
			name: "handler error sets error status and records error",
			handler: func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return nil, errors.New("boom")
			},
			toolName:    "workflow_run",
			wantCode:    codes.Error,
			wantErrAttr: true,
		},
		{
			name: "IsError result sets error status without recordError",
			handler: func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return &mcp.CallToolResult{IsError: true}, nil
			},
			toolName: "x_prom_query",
			wantCode: codes.Error,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exp := setupTracer(t)
			wrapped := Tracing()(tc.handler)
			_, _ = wrapped(context.Background(), callRequest(tc.toolName))

			spans := exp.GetSpans()
			require.Len(t, spans, 1)
			s := spans[0]
			require.Equal(t, "tool."+tc.toolName, s.Name)

			var gotToolAttr string
			for _, a := range s.Attributes {
				if string(a.Key) == AttrToolName {
					gotToolAttr = a.Value.AsString()
				}
			}
			require.Equal(t, tc.toolName, gotToolAttr)
			require.Equal(t, tc.wantCode, s.Status.Code)
			if tc.wantErrAttr {
				require.NotEmpty(t, s.Events, "expected exception event recorded")
			}
		})
	}
}

func TestStartToolSpan(t *testing.T) {
	exp := setupTracer(t)
	ctx, end := StartToolSpan(context.Background(), "x_kubernetes_get_pod")
	require.NotEqual(t, context.Background(), ctx)
	end(&mcp.CallToolResult{IsError: true}, nil)

	spans := exp.GetSpans()
	require.Len(t, spans, 1)
	require.Equal(t, "tool.x_kubernetes_get_pod", spans[0].Name)
	require.Equal(t, codes.Error, spans[0].Status.Code)
}
