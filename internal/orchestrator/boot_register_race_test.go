package orchestrator

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/config"
	"github.com/giantswarm/muster/internal/services"
	"github.com/giantswarm/muster/pkg/logging"
)

// lockedBuffer is a log sink the service start goroutines of the boot pass
// can write to while the test reads it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) Lines() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.Split(b.buf.String(), "\n")
}

// TestBootPassToleratesConcurrentRegistration provokes the boot-time race
// between the orchestrator and the MCPServer reconciler (issue #1222). The
// reconciler's first pass runs while processAutoStartMCPServers iterates the
// definitions; for a definition the loop has not reached yet it finds no
// service and StartService registers it lazily. When the loop then reaches
// that definition, registry.Register refuses the duplicate -- which the boot
// pass used to report as an error for a service that exists and is being
// started.
//
// The race is made deterministic by running StartService for one of the
// definitions from the manager's ListMCPServers -- the boot pass's first
// call -- so the registration lands between the listing and the loop
// reaching that name, the way the reconciler's does.
func TestBootPassToleratesConcurrentRegistration(t *testing.T) {
	// The sink stays installed after the test: the orchestrator's background
	// goroutines outlive Stop and read the package logger, so swapping it
	// back would race with them.
	sink := &lockedBuffer{}
	logging.InitForCLI(logging.LevelDebug, sink)

	manager := &mockMCPServerManager{servers: map[string]api.MCPServerInfo{
		"raced-mcp": remoteServerInfo("raced-mcp", false),
		"quiet-mcp": remoteServerInfo("quiet-mcp", false),
	}}
	api.RegisterMCPServerManager(manager)
	t.Cleanup(func() { api.RegisterMCPServerManager(nil) })

	o := New(Config{Aggregator: config.AggregatorConfig{}})
	t.Cleanup(func() { _ = o.Stop() })

	var racedService services.Service
	manager.onList = func() {
		manager.onList = nil
		// The start itself fails (nothing listens on the port); the
		// registration is what races with the boot pass.
		_ = o.StartService("raced-mcp")
		var ok bool
		racedService, ok = o.registry.Get("raced-mcp")
		require.True(t, ok, "precondition: StartService registered the definition before the boot loop reached it")
	}

	require.NoError(t, o.Start(context.Background()))

	got, ok := o.registry.Get("raced-mcp")
	require.True(t, ok, "the raced definition stays registered")
	assert.Same(t, racedService, got, "the boot pass must leave the service StartService registered in place")

	_, ok = o.registry.Get("quiet-mcp")
	assert.True(t, ok, "a definition nobody raced for is registered by the boot pass as before")

	for _, line := range sink.Lines() {
		if strings.Contains(line, "Failed to create MCPServer service") {
			t.Errorf("the boot pass reported the lost race as a failure: %s", line)
		}
		if strings.Contains(line, "level=ERROR") && strings.Contains(line, "already registered") {
			t.Errorf("the boot pass logged the lost race at error level: %s", line)
		}
	}
}
