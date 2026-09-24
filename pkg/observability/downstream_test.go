package observability_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/giantswarm/muster/v5/pkg/observability"
)

func spanAttrs(t *testing.T, rec *tracetest.SpanRecorder, name string) map[string]string {
	t.Helper()
	for _, sp := range rec.Ended() {
		if sp.Name() == name {
			attrs := make(map[string]string)
			for _, kv := range sp.Attributes() {
				attrs[string(kv.Key)] = kv.Value.AsString()
			}
			return attrs
		}
	}
	require.Failf(t, "span not found", "no ended span %q", name)
	return nil
}

func TestAnnotateDownstreamCall(t *testing.T) {
	tests := []struct {
		name            string
		dropRequestSpan bool
		wantOnRequest   bool
	}{
		{name: "labels the current and the request span", wantOnRequest: true},
		{name: "leaves a dropped request span alone", dropRequestSpan: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := tracetest.NewSpanRecorder()
			tracer := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)).Tracer("test")

			ctx, request := tracer.Start(t.Context(), "request")
			ctx = observability.ContextWithRequestSpan(ctx, request)
			if tt.dropRequestSpan {
				ctx = observability.WithoutRequestSpan(ctx)
			}
			ctx, current := tracer.Start(ctx, "current")

			ctx = observability.AnnotateDownstreamCall(ctx, "kubernetes", "x_kubernetes_list_pods", "list_pods")
			current.End()
			request.End()

			call, ok := observability.DownstreamCallFromContext(ctx)
			require.True(t, ok)
			require.Equal(t, observability.DownstreamCall{Server: "kubernetes", Tool: "list_pods"}, call)

			want := map[string]string{
				observability.AttrMCPServerName:      "kubernetes",
				observability.AttrDownstreamToolName: "x_kubernetes_list_pods",
			}
			require.Equal(t, want, spanAttrs(t, rec, "current"))
			if tt.wantOnRequest {
				require.Equal(t, want, spanAttrs(t, rec, "request"))
			} else {
				require.Empty(t, spanAttrs(t, rec, "request"))
			}
		})
	}
}

func TestAnnotateDownstreamCall_RequestSpanIsCurrent(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	tracer := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)).Tracer("test")

	ctx, span := tracer.Start(t.Context(), "request")
	ctx = observability.ContextWithRequestSpan(ctx, span)
	observability.AnnotateDownstreamCall(ctx, "kubernetes", "x_kubernetes_list_pods", "list_pods")
	span.End()

	require.Len(t, rec.Ended()[0].Attributes(), 2)
}

func TestDownstreamCallFromContext_Absent(t *testing.T) {
	_, ok := observability.DownstreamCallFromContext(t.Context())
	require.False(t, ok)
	require.NotPanics(t, func() {
		observability.AnnotateDownstreamCall(observability.WithoutRequestSpan(t.Context()), "s", "t", "t")
	})
}
