package orchestrator

import (
	"context"
	"testing"

	"github.com/giantswarm/muster/internal/api"
)

// fakeLifecycleManager implements the optional mcpServerLifecycleAsCaller
// capability on top of a nil MCPServerManagerHandler: only the lifecycle
// methods may be called.
type fakeLifecycleManager struct {
	api.MCPServerManagerHandler
	calls []string
}

func (f *fakeLifecycleManager) StartMCPServerAsCaller(_ context.Context, name string) (*api.CallToolResult, bool, error) {
	f.calls = append(f.calls, "start:"+name)
	return &api.CallToolResult{Content: []interface{}{"routed start"}}, true, nil
}

func (f *fakeLifecycleManager) StopMCPServerAsCaller(_ context.Context, name string) (*api.CallToolResult, bool, error) {
	f.calls = append(f.calls, "stop:"+name)
	return &api.CallToolResult{Content: []interface{}{"routed stop"}}, true, nil
}

func (f *fakeLifecycleManager) RestartMCPServerAsCaller(_ context.Context, name string) (*api.CallToolResult, bool, error) {
	f.calls = append(f.calls, "restart:"+name)
	return &api.CallToolResult{Content: []interface{}{"routed restart"}}, true, nil
}

// TestServiceLifecycleRoutesToCallerWrites pins the issue #1057 routing: when
// the registered MCPServer manager offers caller-identity lifecycle writes and
// reports the action handled, the service tools return its result without
// touching the orchestrator.
func TestServiceLifecycleRoutesToCallerWrites(t *testing.T) {
	fake := &fakeLifecycleManager{}
	previous := api.GetMCPServerManager()
	api.RegisterMCPServerManager(fake)
	t.Cleanup(func() { api.RegisterMCPServerManager(previous) })

	// The nil orchestrator proves the routed path never reaches it.
	adapter := &Adapter{}

	for tool, want := range map[string]string{
		"service_start":   "start:probe",
		"service_stop":    "stop:probe",
		"service_restart": "restart:probe",
	} {
		fake.calls = nil
		result, err := adapter.ExecuteTool(context.Background(), tool, map[string]interface{}{"name": "probe"})
		if err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
		if result.IsError {
			t.Fatalf("%s: unexpected tool error: %v", tool, result.Content)
		}
		if len(fake.calls) != 1 || fake.calls[0] != want {
			t.Fatalf("%s: expected routed call %q, got %v", tool, want, fake.calls)
		}
	}
}
