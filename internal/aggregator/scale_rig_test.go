package aggregator

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
	"github.com/valkey-io/valkey-go"
	"gopkg.in/yaml.v3"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/client"
	"github.com/giantswarm/muster/v5/internal/client/filesystem"
	"github.com/giantswarm/muster/v5/internal/metatools"
	oauthstore "github.com/giantswarm/muster/v5/internal/oauth/store"
	"github.com/giantswarm/muster/v5/internal/testing/fixtures/scale"
	"github.com/giantswarm/muster/v5/internal/workflow"
	musterv1alpha1 "github.com/giantswarm/muster/v5/pkg/apis/muster/v1alpha1"
)

// scaleRig is the aggregator assembled in-process over the scale fixture,
// the way muster serve assembles it: the registry with every server (the
// in-house ones connected, the session-authenticated ones pending), the
// session auth and capability stores on a Valkey stand-in, the workflow
// provider over the fixture's definitions behind a client that counts the
// reads an API server would answer, and the meta-tools over the aggregator.
// Every session of the fixture is authenticated to every session-
// authenticated server, with the server's capability document in the store.
type scaleRig struct {
	fixture   *scale.Fixture
	agg       *AggregatorServer
	valkey    *miniredis.Miniredis
	reads     *countingClient
	metaTools *metatools.Provider
}

// newScaleRig builds the rig. The workflow and meta-tools providers are
// process-global (the service locator), so the rig registers them for the
// test and restores what was there.
func newScaleRig(t *testing.T) *scaleRig {
	t.Helper()
	f := scale.Get()
	ctx := context.Background()

	srv := miniredis.RunT(t)
	vk, err := valkey.NewClient(valkey.ClientOption{InitAddress: []string{srv.Addr()}, DisableCache: true})
	require.NoError(t, err)
	t.Cleanup(vk.Close)

	pool := NewSessionConnectionPool(DefaultConnectionPoolMaxAge)
	t.Cleanup(pool.Stop)
	agg := &AggregatorServer{
		config:            AggregatorConfig{MusterPrefix: "x"},
		registry:          NewServerRegistry("x"),
		toolManager:       newActiveItemManager(),
		authStore:         oauthstore.NewValkeySessionAuthStore(vk, oauthstore.DefaultCapabilityStoreTTL, "muster:"),
		capabilityStore:   oauthstore.NewValkeyCapabilityStore(vk, oauthstore.DefaultCapabilityStoreTTL, "muster:"),
		connPool:          pool,
		ssoTracker:        newSSOTracker(),
		subjectSessions:   newSubjectSessionTracker(),
		authMetrics:       NewAuthMetrics(),
		downstreamMetrics: newDownstreamMetrics(),
		core:              newCoreCatalogue(),
	}

	for _, s := range f.Servers {
		doc, ok := f.DocumentByName(s.Document)
		require.True(t, ok, "server %s: document %s", s.Name, s.Document)
		if !s.SessionAuth {
			require.NoError(t, agg.registry.Register(ctx, ServerRegistration{Name: s.Name}, &mockMCPClient{tools: mcpToolsOf(doc)}))
			continue
		}
		require.NoError(t, agg.registry.RegisterPendingAuth(PendingAuthRegistration{
			ServerRegistration: ServerRegistration{Name: s.Name, Family: &api.MCPServerFamily{Name: s.Family, InstanceArg: s.InstanceArg}},
			URL:                s.URL(),
			AuthConfig:         &api.MCPServerAuth{ForwardToken: true},
		}))
	}

	dir := t.TempDir()
	writeFixtureWorkflows(t, dir, f)
	reads := &countingClient{MusterClient: filesystem.New(dir)}
	previousWorkflow := api.GetWorkflow()
	adapter := workflow.NewAdapterWithClient(reads, "default", api.NewToolCaller(), api.NewToolChecker(), dir)
	adapter.Register()
	t.Cleanup(func() {
		adapter.Stop()
		api.RegisterWorkflow(previousWorkflow)
	})

	previousMeta, previousData := api.GetMetaTools(), api.GetMetaToolsDataProvider()
	meta := metatools.NewAdapter()
	meta.Register()
	api.RegisterMetaToolsDataProvider(agg)
	t.Cleanup(func() {
		api.RegisterMetaTools(previousMeta)
		api.RegisterMetaToolsDataProvider(previousData)
	})

	rig := &scaleRig{fixture: f, agg: agg, valkey: srv, reads: reads, metaTools: meta.GetProvider()}
	rig.seedSessions(t)
	return rig
}

// seedSessions authenticates every session to every session-authenticated
// server through the stores' own writes, as the fan-out does on a connect,
// so the store holds exactly what muster would write.
func (r *scaleRig) seedSessions(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	servers := r.fixture.SessionAuthServers()
	docs := make(map[string]*oauthstore.Capabilities, len(r.fixture.Documents))
	for _, d := range r.fixture.Documents {
		docs[d.Name] = &oauthstore.Capabilities{Tools: mcpToolsOf(d)}
	}
	type work struct {
		session scale.Session
		server  scale.Server
	}
	queue := make(chan work, 256)
	var wg sync.WaitGroup
	var firstErr atomic.Pointer[error]
	for w := 0; w < 16; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range queue {
				if err := r.agg.authStore.MarkAuthenticated(ctx, item.session.ID, item.server.Name); err != nil {
					firstErr.CompareAndSwap(nil, &err)
					continue
				}
				if err := r.agg.capabilityStore.Set(ctx, item.session.ID, item.server.Name, docs[item.server.Document]); err != nil {
					firstErr.CompareAndSwap(nil, &err)
				}
			}
		}()
	}
	for _, s := range r.fixture.Sessions {
		for _, srv := range servers {
			queue <- work{session: s, server: srv}
		}
	}
	close(queue)
	wg.Wait()
	if errp := firstErr.Load(); errp != nil {
		t.Fatalf("seeding sessions: %v", *errp)
	}
}

// sessionContext is a request context of the given session of the fixture.
func (r *scaleRig) sessionContext(i int) context.Context {
	s := r.fixture.Sessions[i%len(r.fixture.Sessions)]
	ctx := api.WithSubject(context.Background(), s.Subject)
	return api.WithSessionID(ctx, s.ID)
}

// sessionID is the id of the given session of the fixture.
func (r *scaleRig) sessionID(i int) string {
	return r.fixture.Sessions[i%len(r.fixture.Sessions)].ID
}

// callMetaTool runs one meta-tool call and returns its result and the bytes
// of its text content -- what goes over the wire to the caller.
func (r *scaleRig) callMetaTool(t *testing.T, ctx context.Context, name string, args map[string]any) (*api.CallToolResult, int) {
	t.Helper()
	result, err := r.metaTools.ExecuteTool(ctx, name, args)
	require.NoError(t, err, "%s", name)
	text := resultText(result)
	require.False(t, result.IsError, "%s answered an error: %s", name, text)
	return result, len(text)
}

// valkeyCommands is the number of commands the stand-in has processed.
func (r *scaleRig) valkeyCommands() int {
	return r.valkey.Server().TotalCommands()
}

// capabilityFootprint measures the capability store on the stand-in: the
// sessions with entries, the entries, the distinct documents and the bytes
// of the cap:<session> hashes (fields and values) and the capblob documents.
func (r *scaleRig) capabilityFootprint() (sessions, entries, documents, bytes int) {
	for _, key := range r.valkey.Keys() {
		switch {
		case strings.HasPrefix(key, "muster:cap:"):
			sessions++
			bytes += len(key)
			fields, _ := r.valkey.HKeys(key)
			for _, field := range fields {
				entries++
				bytes += len(field) + len(r.valkey.HGet(key, field))
			}
		case strings.HasPrefix(key, "muster:capblob:"):
			documents++
			value, _ := r.valkey.Get(key)
			bytes += len(key) + len(value)
		}
	}
	return sessions, entries, documents, bytes
}

// countingClient counts the definition reads that are API-server requests
// in Kubernetes mode: one LIST per ListWorkflows/ListMCPServers, one GET per
// GetWorkflow/GetMCPServer (internal/client/kubernetes issues exactly these).
// The definitions themselves come from the filesystem client underneath.
type countingClient struct {
	client.MusterClient
	lists, gets atomic.Int64
}

func (c *countingClient) ListWorkflows(ctx context.Context, namespace string) ([]musterv1alpha1.Workflow, error) {
	c.lists.Add(1)
	return c.MusterClient.ListWorkflows(ctx, namespace)
}

func (c *countingClient) GetWorkflow(ctx context.Context, name, namespace string) (*musterv1alpha1.Workflow, error) {
	c.gets.Add(1)
	return c.MusterClient.GetWorkflow(ctx, name, namespace)
}

func (c *countingClient) ListMCPServers(ctx context.Context, namespace string) ([]musterv1alpha1.MCPServer, error) {
	c.lists.Add(1)
	return c.MusterClient.ListMCPServers(ctx, namespace)
}

func (c *countingClient) GetMCPServer(ctx context.Context, name, namespace string) (*musterv1alpha1.MCPServer, error) {
	c.gets.Add(1)
	return c.MusterClient.GetMCPServer(ctx, name, namespace)
}

// requests is the API-server requests issued so far.
func (c *countingClient) requests() int64 { return c.lists.Load() + c.gets.Load() }

// writeFixtureWorkflows renders the fixture's workflows as the definition
// files the filesystem client reads, the way the harness renders a
// scenario's.
func writeFixtureWorkflows(t *testing.T, dir string, f *scale.Fixture) {
	t.Helper()
	wfDir := filepath.Join(dir, "workflows")
	require.NoError(t, os.MkdirAll(wfDir, 0o755))
	for _, w := range f.Workflows {
		args := map[string]any{}
		for _, a := range w.Args {
			args[a.Name] = map[string]any{"type": a.Type, "required": a.Required, "description": a.Description}
		}
		steps := make([]any, 0, len(w.Steps))
		for _, st := range w.Steps {
			stepArgs := map[string]any{}
			for _, a := range st.Args {
				stepArgs[a.Name] = a.Value
			}
			steps = append(steps, map[string]any{"id": st.ID, "tool": st.Tool, "args": stepArgs, "store": true})
		}
		definition := map[string]any{
			"apiVersion": "muster.giantswarm.io/v1alpha1",
			"kind":       "Workflow",
			"metadata":   map[string]any{"name": w.Name, "namespace": "default", "labels": w.Labels},
			"spec":       map[string]any{"description": w.Description, "args": args, "steps": steps},
		}
		data, err := yaml.Marshal(definition)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(wfDir, w.Name+".yaml"), data, 0o600))
	}
}

// mcpToolsOf renders a fixture document as the tools a server lists.
func mcpToolsOf(d scale.Document) []mcp.Tool {
	tools := make([]mcp.Tool, 0, len(d.Tools))
	for _, t := range d.Tools {
		props := make(map[string]any, len(t.Properties))
		for _, p := range t.Properties {
			props[p.Name] = map[string]any{"type": "string", "description": p.Description}
		}
		readOnly := t.ReadOnly
		tools = append(tools, mcp.Tool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: mcp.ToolInputSchema{Type: "object", Properties: props, Required: append([]string(nil), t.Required...)},
			Annotations: mcp.ToolAnnotation{ReadOnlyHint: &readOnly},
		})
	}
	return tools
}

// median of a sample.
func median(samples []time.Duration) time.Duration {
	sorted := slices.Clone(samples)
	slices.Sort(sorted)
	return sorted[len(sorted)/2]
}
