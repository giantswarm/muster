package aggregator

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func callRequest(name string) mcp.CallToolRequest {
	return mcp.CallToolRequest{Params: mcp.CallToolParams{Name: name}}
}

func setupTracer(t *testing.T) *tracetest.InMemoryExporter {
	t.Helper()
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { otel.SetTracerProvider(prev) })
	return exp
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
