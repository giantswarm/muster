package orchestrator

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/internal/api"
)

// stubAggregator records pending-auth registrations. Every other
// AggregatorHandler method is an unused no-op.
type stubAggregator struct {
	mu            sync.Mutex
	registrations []api.PendingAuthRegistration
}

func (s *stubAggregator) registered() []api.PendingAuthRegistration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]api.PendingAuthRegistration(nil), s.registrations...)
}

func (s *stubAggregator) RegisterServerPendingAuth(registration api.PendingAuthRegistration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.registrations = append(s.registrations, registration)
	return nil
}

func (s *stubAggregator) GetServiceData() map[string]interface{} { return nil }
func (s *stubAggregator) GetEndpoint() string                    { return "" }
func (s *stubAggregator) GetPort() int                           { return 0 }
func (s *stubAggregator) CallTool(context.Context, string, map[string]interface{}) (*api.CallToolResult, error) {
	return nil, nil
}
func (s *stubAggregator) CallToolInternal(context.Context, string, map[string]interface{}) (*mcp.CallToolResult, error) {
	return nil, nil
}
func (s *stubAggregator) IsToolAvailable(string) bool { return false }
func (s *stubAggregator) MissingToolsForSession(context.Context, []string) []string {
	return nil
}
func (s *stubAggregator) GetAvailableTools() []string { return nil }
func (s *stubAggregator) UpdateCapabilities()         {}
func (s *stubAggregator) DeregisterServer(string) error {
	return nil
}

// swapAggregatorHandler installs h as the global aggregator handler and
// restores the previous one when the test ends.
func swapAggregatorHandler(t *testing.T, h api.AggregatorHandler) {
	t.Helper()
	previous := api.GetAggregator()
	api.RegisterAggregator(h)
	t.Cleanup(func() { api.RegisterAggregator(previous) })
}

// TestRegisterPendingAuthWithRetry_WaitsForAggregator is the regression test
// for issue #1110: Orchestrator.Start launches the aggregator service and the
// MCPServer services as concurrent goroutines, so a fast 401 probe can reach
// the auth-required hook before the aggregator exists. The registration used
// to be dropped with only an error log, leaving the service in Auth Required
// state (instance readiness passed) while core_auth_login answered
// "Server not found" for the life of the process. The hook must instead wait
// for the aggregator and register once it is up.
func TestRegisterPendingAuthWithRetry_WaitsForAggregator(t *testing.T) {
	t.Run("registers immediately when the aggregator is up", func(t *testing.T) {
		stub := &stubAggregator{}
		swapAggregatorHandler(t, stub)

		o := &Orchestrator{ctx: context.Background()}
		err := o.registerPendingAuthWithRetry(api.PendingAuthRegistration{Name: "server-alpha"})

		require.NoError(t, err)
		registrations := stub.registered()
		require.Len(t, registrations, 1)
		assert.Equal(t, "server-alpha", registrations[0].Name)
	})

	t.Run("waits for an aggregator that comes up late", func(t *testing.T) {
		swapAggregatorHandler(t, nil)

		stub := &stubAggregator{}
		go func() {
			time.Sleep(3 * pendingAuthRegistrationInterval)
			api.RegisterAggregator(stub)
		}()

		o := &Orchestrator{ctx: context.Background()}
		err := o.registerPendingAuthWithRetry(api.PendingAuthRegistration{Name: "server-beta"})

		require.NoError(t, err)
		registrations := stub.registered()
		require.Len(t, registrations, 1)
		assert.Equal(t, "server-beta", registrations[0].Name)
	})

	t.Run("gives up when the orchestrator shuts down", func(t *testing.T) {
		swapAggregatorHandler(t, nil)

		ctx, cancel := context.WithCancel(context.Background())
		o := &Orchestrator{ctx: ctx}
		go func() {
			time.Sleep(pendingAuthRegistrationInterval / 2)
			cancel()
		}()

		err := o.registerPendingAuthWithRetry(api.PendingAuthRegistration{Name: "server-gamma"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "shutting down")
	})
}
