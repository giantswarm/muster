package workflow

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/giantswarm/muster/pkg/observability"
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

func TestStartStepSpan(t *testing.T) {
	const (
		workflowName = "deploy-app"
		stepID       = "apply-manifests"
		toolName     = "x_kubernetes_apply"
	)

	cases := []struct {
		name       string
		isError    bool
		err        error
		wantCode   codes.Code
		wantEvents bool
	}{
		{
			name:     "ok result has no error status",
			wantCode: codes.Unset,
		},
		{
			name:       "handler error sets error status and records error",
			err:        errors.New("boom"),
			wantCode:   codes.Error,
			wantEvents: true,
		},
		{
			name:     "IsError result sets error status without recordError",
			isError:  true,
			wantCode: codes.Error,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exp := setupTracer(t)

			ctx, end := startStepSpan(context.Background(), workflowName, stepID, toolName)
			require.NotEqual(t, context.Background(), ctx)
			end(tc.isError, tc.err)

			spans := exp.GetSpans()
			require.Len(t, spans, 1)
			s := spans[0]
			require.Equal(t, "workflow.step", s.Name)
			require.Equal(t, observability.TracerName, s.InstrumentationScope.Name)

			attrs := map[string]string{}
			for _, a := range s.Attributes {
				attrs[string(a.Key)] = a.Value.AsString()
			}
			require.Equal(t, workflowName, attrs["workflow.name"])
			require.Equal(t, stepID, attrs["workflow.step.id"])
			require.Equal(t, toolName, attrs[observability.AttrToolName])

			require.Equal(t, tc.wantCode, s.Status.Code)
			if tc.wantEvents {
				require.NotEmpty(t, s.Events, "expected exception event recorded")
			} else {
				require.Empty(t, s.Events)
			}
		})
	}
}
