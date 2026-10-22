package orchestrator

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/config"
	"github.com/giantswarm/muster/v5/internal/services"
)

// stopLog records the order in which services were stopped.
type stopLog struct {
	mu    sync.Mutex
	names []string
}

func (l *stopLog) add(name string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.names = append(l.names, name)
}

func (l *stopLog) get() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.names...)
}

// stopRecordingService records its Stop in log; block, when set, holds Stop
// until its context is done or block is closed.
type stopRecordingService struct {
	mockService
	typ          services.ServiceType
	dependencies []string
	log          *stopLog
	block        chan struct{}

	// servicesCtx is the context Start received; ctxLiveAtStop reports
	// whether it was still live when Stop ran.
	mu            sync.Mutex
	servicesCtx   context.Context
	ctxLiveAtStop bool
}

func (s *stopRecordingService) GetType() services.ServiceType { return s.typ }
func (s *stopRecordingService) GetDependencies() []string     { return s.dependencies }

func (s *stopRecordingService) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.servicesCtx = ctx
	return nil
}

func (s *stopRecordingService) started() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.servicesCtx != nil
}

func (s *stopRecordingService) liveAtStop() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ctxLiveAtStop
}

func (s *stopRecordingService) Stop(ctx context.Context) error {
	s.mu.Lock()
	s.ctxLiveAtStop = s.servicesCtx != nil && s.servicesCtx.Err() == nil
	s.mu.Unlock()
	if s.block != nil {
		select {
		case <-s.block:
		case <-ctx.Done():
		}
	}
	s.log.add(s.GetName())
	return nil
}

func newStopTestOrchestrator(t *testing.T, svcs ...*stopRecordingService) *Orchestrator {
	t.Helper()
	api.RegisterMCPServerManager(&mockMCPServerManager{servers: map[string]api.MCPServerInfo{}})
	t.Cleanup(func() { api.RegisterMCPServerManager(nil) })

	o := New(Config{Aggregator: config.AggregatorConfig{}})
	for _, svc := range svcs {
		require.NoError(t, o.registry.Register(svc))
	}
	return o
}

// TestStopStopsEveryServiceInReverseDependencyOrder covers issue #1665: a
// stopped orchestrator has run each registered service's Stop, the aggregator
// before the MCPServers it aggregates and a service before its declared
// dependencies, while the services' context was still live.
func TestStopStopsEveryServiceInReverseDependencyOrder(t *testing.T) {
	log := &stopLog{}
	aggregator := &stopRecordingService{mockService: mockService{name: "mcp-aggregator"}, typ: services.TypeAggregator, log: log}
	alpha := &stopRecordingService{mockService: mockService{name: "alpha"}, typ: services.TypeMCPServer, log: log}
	beta := &stopRecordingService{mockService: mockService{name: "beta"}, typ: services.TypeMCPServer, log: log}
	base := &stopRecordingService{mockService: mockService{name: "base"}, typ: services.ServiceType("Other"), log: log}
	alpha.dependencies = []string{"base"}

	o := newStopTestOrchestrator(t, aggregator, alpha, beta, base)
	require.NoError(t, o.Start(context.Background()))
	require.Eventually(t, func() bool {
		for _, svc := range []*stopRecordingService{aggregator, alpha, beta, base} {
			if !svc.started() {
				return false
			}
		}
		return true
	}, 2*time.Second, time.Millisecond, "Start must start every static service")

	require.NoError(t, o.Stop())

	stopped := log.get()
	require.ElementsMatch(t, []string{"mcp-aggregator", "alpha", "beta", "base"}, stopped, "every service must be stopped")
	assert.Equal(t, "mcp-aggregator", stopped[0], "the aggregator stops before the MCPServers")
	assert.Equal(t, "base", stopped[3], "a dependency stops after its dependents")
	for _, svc := range []*stopRecordingService{aggregator, alpha, beta, base} {
		assert.True(t, svc.liveAtStop(), "%s must be stopped before the services' context is cancelled", svc.GetName())
	}
	assert.Error(t, o.ctx.Err(), "Stop cancels the services' context once they are stopped")
}

// TestStopIsBoundedByShutdownTimeout: a service whose Stop does not return
// cannot hold Stop past shutdownTimeout.
func TestStopIsBoundedByShutdownTimeout(t *testing.T) {
	log := &stopLog{}
	stuck := &stopRecordingService{mockService: mockService{name: "stuck"}, typ: services.TypeMCPServer, log: log, block: make(chan struct{})}
	t.Cleanup(func() { close(stuck.block) })

	o := newStopTestOrchestrator(t, stuck)
	o.shutdownTimeout = 50 * time.Millisecond
	require.NoError(t, o.Start(context.Background()))

	done := make(chan struct{})
	go func() {
		_ = o.Stop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop must return once shutdownTimeout has passed")
	}
	assert.Error(t, o.ctx.Err(), "Stop cancels the services' context even after a timeout")
}

func TestShutdownWaves(t *testing.T) {
	svc := func(name string, typ services.ServiceType, deps ...string) services.Service {
		return &stopRecordingService{mockService: mockService{name: name}, typ: typ, dependencies: deps}
	}
	names := func(waves [][]services.Service) [][]string {
		var out [][]string
		for _, wave := range waves {
			var w []string
			for _, s := range wave {
				w = append(w, s.GetName())
			}
			out = append(out, w)
		}
		return out
	}

	t.Run("aggregator before MCPServers", func(t *testing.T) {
		waves := shutdownWaves([]services.Service{
			svc("a", services.TypeMCPServer),
			svc("mcp-aggregator", services.TypeAggregator),
			svc("b", services.TypeMCPServer),
		})
		assert.Equal(t, [][]string{{"mcp-aggregator"}, {"a", "b"}}, names(waves))
	})

	t.Run("declared dependencies", func(t *testing.T) {
		waves := shutdownWaves([]services.Service{
			svc("c", services.TypeMCPServer, "b"),
			svc("b", services.TypeMCPServer, "a"),
			svc("a", services.TypeMCPServer),
		})
		assert.Equal(t, [][]string{{"c"}, {"b"}, {"a"}}, names(waves))
	})

	t.Run("a cycle ends in one wave", func(t *testing.T) {
		waves := shutdownWaves([]services.Service{
			svc("x", services.TypeMCPServer, "y"),
			svc("y", services.TypeMCPServer, "x"),
		})
		assert.Equal(t, [][]string{{"x", "y"}}, names(waves))
	})

	t.Run("no services", func(t *testing.T) {
		assert.Empty(t, shutdownWaves(nil))
	})
}
