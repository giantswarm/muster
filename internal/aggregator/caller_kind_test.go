package aggregator

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/credprovider"
)

// callerKindCounts sums a counter's data points by caller_kind, and fails on
// a point that carries any other identity attribute.
func callerKindCounts(t *testing.T, rm metricdata.ResourceMetrics, name string) map[string]int64 {
	t.Helper()
	counts := map[string]int64{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			require.True(t, ok, "%s is a Sum", name)
			for _, dp := range sum.DataPoints {
				kind, ok := dp.Attributes.Value(attrCallerKind)
				require.True(t, ok, "every %s point carries caller_kind", name)
				for _, kv := range dp.Attributes.ToSlice() {
					require.NotContains(t, []attribute.Key{"subject", "sub", "user", "person"}, kv.Key, "no person in the metric")
				}
				counts[kind.AsString()] += dp.Value
			}
		}
	}
	return counts
}

func TestToolCallMetricsCarryTheCallerKind(t *testing.T) {
	reader := setupMeter(t)
	ok := func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{}, nil
	}
	wrapped := Metrics()(ok)
	d := newDownstreamMetrics()
	require.NotNil(t, d)

	person := api.WithSubject(t.Context(), "alice")
	agent := api.WithAgentCaller(person)
	for _, ctx := range []context.Context{person, agent, agent} {
		_, _ = wrapped(ctx, callRequest("call_tool"))
		d.record(ctx, "kagent", "x_kagent_list_sessions", time.Now(), &mcp.CallToolResult{}, nil)
	}

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(t.Context(), &rm))
	want := map[string]int64{api.CallerKindPerson: 1, api.CallerKindAgent: 2}
	require.Equal(t, want, callerKindCounts(t, rm, "muster.tool_calls"))
	require.Equal(t, want, callerKindCounts(t, rm, "muster.downstream_tool_calls"))
}

func TestToolCallLinesCarryTheCallerKind(t *testing.T) {
	for _, tc := range []struct {
		name string
		ctx  func(context.Context) context.Context
		want string
	}{
		{"person", func(ctx context.Context) context.Context { return ctx }, api.CallerKindPerson},
		{"agent", api.WithAgentCaller, api.CallerKindAgent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			buf := captureLog(t)
			server, exposed := newDispatchTestServer(t, &sentinelClient{mockMCPClient: mockMCPClient{tools: []mcp.Tool{{Name: "list_pods"}}}})
			ctx := tc.ctx(api.WithSubject(t.Context(), dispatchTestSubject))

			_, err := server.CallToolInternal(ctx, exposed, nil)
			require.NoError(t, err)
			require.Equal(t, tc.want, theDispatchLine(t, buf.String(), outcomeOK)[attrCallerKind])

			buf.Reset()
			_, _ = Logging()(func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return &mcp.CallToolResult{}, nil
			})(ctx, callRequest("call_tool"))
			require.Equal(t, tc.want, parseLogLine(t, buf.String())[attrCallerKind])
		})
	}
}

// Only a request the actor bearer authenticated is an agent's; every other
// request, including one that reaches the OAuth chain, is a person's.
func TestActorBearerHandlerMarksTheAgentCaller(t *testing.T) {
	store := credprovider.NewMemoryStore()
	require.NoError(t, store.Put(t.Context(), "spiffe://substrate-actor.local/actor/kagent/session-a",
		credprovider.Binding{Subject: "alice", Session: "kagent/s1", Providers: []string{credprovider.MusterProvider}}, time.Hour))
	var kind string
	record := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { kind = api.CallerKindFromContext(r.Context()) })
	h := actorBearerHandler(testActorBearer(t, store), record, record, nil, nil)

	call := func(bearer string) string {
		kind = ""
		req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
		req.Header.Set("Authorization", "Bearer "+bearer)
		h.ServeHTTP(httptest.NewRecorder(), req)
		return kind
	}
	require.Equal(t, api.CallerKindAgent, call(fakeActorJWT(testActorIssuer, "actor/kagent/session-a")))
	require.Equal(t, api.CallerKindPerson, call(fakeActorJWT("https://dex.example.com", "alice")))
}
