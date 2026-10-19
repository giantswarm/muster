package events

import (
	"context"
	"strings"
	"testing"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/client"
)

// eventsClient answers every query with a fixed list of events.
type eventsClient struct {
	client.MusterClient
	events []api.EventResult
}

func (c *eventsClient) QueryEvents(context.Context, api.EventQueryOptions) (*api.EventQueryResult, error) {
	return &api.EventQueryResult{Events: c.events, TotalCount: len(c.events)}, nil
}

type fixedVisibility struct{ view api.ServerVisibility }

func (f fixedVisibility) CallerVisibility(context.Context) api.ServerVisibility { return f.view }
func (f fixedVisibility) LoginAudience() string                                 { return "" }

// core_events drops the events about an MCP server the caller may not see
// (agent-permissions D9); events about anything else stay.
func TestEventsQuery_DropsEventsOfServersTheCallerMayNotSee(t *testing.T) {
	event := func(kind, namespace, name string) api.EventResult {
		return api.EventResult{Namespace: namespace, Reason: "Seen-" + namespace + "-" + name,
			InvolvedObject: api.ObjectReference{Kind: kind, Name: name, Namespace: namespace}}
	}
	adapter := NewAdapter(&eventsClient{events: []api.EventResult{
		event("MCPServer", "muster", "github"),
		event("MCPServer", "org-a", "github"),
		event("MCPServer", "org-b", "github"),
		event("Workflow", "muster", "deploy"),
	}}, "muster")
	query := func() string {
		result, err := adapter.ExecuteTool(context.Background(), "events", map[string]interface{}{})
		if err != nil || result.IsError {
			t.Fatalf("events: %v %v", err, result)
		}
		return result.Content[0].(string)
	}

	if got := query(); !strings.Contains(got, "Seen-org-b-github") {
		t.Errorf("setting off: every event, as before; got %s", got)
	}

	api.RegisterMCPServerVisibility(fixedVisibility{view: api.ServersOfNamespacesVisible("muster", []string{"muster", "org-a"})})
	t.Cleanup(func() { api.RegisterMCPServerVisibility(nil) })
	got := query()
	for _, want := range []string{"Seen-muster-github", "Seen-org-a-github", "Seen-muster-deploy"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in %s", want, got)
		}
	}
	if strings.Contains(got, "Seen-org-b-github") {
		t.Errorf("an event of org-b's server reached a caller who may not see it: %s", got)
	}
}
