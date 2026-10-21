package aggregator_test

import (
	"testing"

	"github.com/giantswarm/mcp-toolkit/tooldesc"
	"github.com/giantswarm/mcp-toolkit/tooldesc/tooldesctest"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/aggregator"
	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/app"
	"github.com/giantswarm/muster/v5/internal/client/filesystem"
	"github.com/giantswarm/muster/v5/internal/config"
	"github.com/giantswarm/muster/v5/internal/events"
	"github.com/giantswarm/muster/v5/internal/mcpserver"
	"github.com/giantswarm/muster/v5/internal/metatools"
	"github.com/giantswarm/muster/v5/internal/orchestrator"
	"github.com/giantswarm/muster/v5/internal/workflow"
)

// TestToolDescriptionBudgets holds every tool muster serves itself, the
// meta-tools a client lists on connect and the core tools it reaches through
// call_tool, to the budget of giantswarm/giantswarm#38592.
func TestToolDescriptionBudgets(t *testing.T) {
	registerCoreProviders(t)
	meta, core := aggregator.RegisteredToolsForTest()
	require.Len(t, meta, len(metatools.MetaToolNames))
	require.NotEmpty(t, core)
	require.NoError(t, tooldesc.Check(meta, tooldesc.DefaultBudget))
	require.NoError(t, tooldesc.Check(core, tooldesc.DefaultBudget))
}

// registerCoreProviders registers every core tool provider over an empty
// filesystem client, as muster serve does, and restores the previous ones.
func registerCoreProviders(t *testing.T) {
	t.Helper()
	prevMeta, prevWorkflow, prevService := api.GetMetaTools(), api.GetWorkflow(), api.GetServiceManager()
	prevConfig, prevMCPServer, prevEvents := api.GetConfigHandler(), api.GetMCPServerManager(), api.GetEventManager()
	t.Cleanup(func() {
		api.RegisterMetaTools(prevMeta)
		api.RegisterWorkflow(prevWorkflow)
		api.RegisterServiceManager(prevService)
		api.RegisterConfigHandler(prevConfig)
		api.RegisterMCPServerManager(prevMCPServer)
		api.RegisterEventManager(prevEvents)
	})

	dir := t.TempDir()
	client := filesystem.New(dir)
	metatools.NewAdapter().Register()
	wf := workflow.NewAdapterWithClient(client, "default", api.NewToolCaller(), api.NewToolChecker(), dir)
	wf.Register()
	t.Cleanup(wf.Stop)
	orchestrator.NewAPIAdapter(nil).Register()
	app.NewConfigAdapter(&config.MusterConfig{}, dir).Register()
	mcpserver.NewAdapterWithClient(client, "default").Register()
	events.NewAdapter(client, "default").Register()
}

// TestToolsDocIsCurrent keeps docs/reference/meta-tools.md, the meta-tools as
// a client lists them on connect, equal to the registered ones.
// TOOLS_DOC_UPDATE=1 rewrites it (make tools-doc).
func TestToolsDocIsCurrent(t *testing.T) {
	meta, _ := aggregator.RegisteredToolsForTest()
	tooldesctest.AssertDoc(t, meta, metaToolsDocHeader, "../../docs/reference/meta-tools.md")
}

const metaToolsDocHeader = "# Meta-tools\n\n" +
	"Generated from the registered meta-tools (`make tools-doc`): every description and input schema exactly as an MCP client lists them on connect. " +
	"Through `muster agent --mcp-server`, `call_tool` also takes `timeout`, which the agent consumes. " +
	"[MCP tools](mcp-tools.md) covers what each one answers."
