package aggregator

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/pkg/observability"
)

// contextRecordingMCPClient keeps the context of the last CallTool.
type contextRecordingMCPClient struct {
	mockMCPClient
	lastCtx context.Context
}

func (c *contextRecordingMCPClient) CallTool(ctx context.Context, name string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	c.lastCtx = ctx
	return c.mockMCPClient.CallTool(ctx, name, args)
}

func recordSpans(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { otel.SetTracerProvider(prev) })
	return rec
}

func endedSpanAttrs(t *testing.T, rec *tracetest.SpanRecorder, name string, kind trace.SpanKind) map[string]string {
	t.Helper()
	for _, sp := range rec.Ended() {
		if sp.Name() == name && sp.SpanKind() == kind {
			attrs := map[string]string{}
			for _, kv := range sp.Attributes() {
				attrs[string(kv.Key)] = kv.Value.AsString()
			}
			return attrs
		}
	}
	require.Failf(t, "span not found", "no ended %s span of kind %s", name, kind)
	return nil
}

func TestDispatchResolvedTool_NamesDownstream(t *testing.T) {
	rec := recordSpans(t)
	server, err := NewAggregatorServer(t.Context(), AggregatorConfig{Host: "localhost", Port: 0}, nil)
	require.NoError(t, err)
	backend := &contextRecordingMCPClient{mockMCPClient: mockMCPClient{tools: []mcp.Tool{{Name: "list_pods"}}}}
	require.NoError(t, server.RegisterServer(t.Context(), ServerRegistration{Name: "kubernetes"}, backend))
	exposed := server.registry.ExposedToolName("kubernetes", "list_pods")

	ctx, span := otel.Tracer("test").Start(t.Context(), "tool.call_tool", trace.WithSpanKind(trace.SpanKindInternal))
	_, err = server.CallToolInternal(ctx, exposed, map[string]any{})
	span.End()
	require.NoError(t, err)

	call, ok := observability.DownstreamCallFromContext(backend.lastCtx)
	require.True(t, ok, "the backend client gets the DownstreamCall")
	require.Equal(t, observability.DownstreamCall{Server: "kubernetes", Tool: "list_pods"}, call)

	attrs := endedSpanAttrs(t, rec, "tool.call_tool", trace.SpanKindInternal)
	require.Equal(t, "kubernetes", attrs[observability.AttrMCPServerName])
	require.Equal(t, exposed, attrs[observability.AttrDownstreamToolName])
}

// The session capability-cache branch, end to end: the backend is reached
// over HTTP by a traced client, so the client span carries the backend's
// native tool name.
func TestCallToolInternal_SessionCacheBranchNamesDownstream(t *testing.T) {
	rec := recordSpans(t)
	backend, _ := newGrantBackend(t, subjectGrantToken)
	a := newTestAggregatorWithPool(t)
	registerSubjectGrantServer(t, a, backend.URL, api.GrantScopeSubject)

	handler := newSubjectGrantMockHandler()
	handler.grant("alice", subjectGrantIssuerURL, subjectGrantToken)
	api.RegisterOAuthHandler(handler)
	t.Cleanup(func() { api.RegisterOAuthHandler(nil) })

	const exposed = "x_github_get_me"
	_, _, err := a.registry.ResolveToolName(exposed)
	require.Error(t, err, "precondition: the call takes the session capability-cache branch")

	ctx, span := otel.Tracer("test").Start(sessionContext("sess-2", "alice"), "tool.call_tool", trace.WithSpanKind(trace.SpanKindInternal))
	_, err = a.CallToolInternal(ctx, exposed, map[string]any{})
	span.End()
	require.NoError(t, err)

	attrs := endedSpanAttrs(t, rec, "tool.call_tool", trace.SpanKindInternal)
	require.Equal(t, "github", attrs[observability.AttrMCPServerName])
	require.Equal(t, exposed, attrs[observability.AttrDownstreamToolName])

	client := endedSpanAttrs(t, rec, "mcp.tools/call", trace.SpanKindClient)
	require.Equal(t, "get_me", client[observability.AttrGenAIToolName])
	require.Equal(t, "github", client[observability.AttrMCPServerName])
}
