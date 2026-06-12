package orchestrator

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/internal/services"
)

func TestNewServiceMetrics_NoOp(t *testing.T) {
	// No MeterProvider configured — instruments should fall back to no-ops
	// and not panic.
	registry := services.NewRegistry()
	m := newServiceMetrics(registry)
	require.NotNil(t, m)

	// recordTransition must not panic.
	m.recordTransition(context.Background(), "svc", "mcpserver", "starting", "running")
	m.recordTransition(context.Background(), "svc", "mcpserver", "running", "failed")
}
