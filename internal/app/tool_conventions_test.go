package app

import (
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/client"
	"github.com/giantswarm/muster/v5/internal/config"
	"github.com/giantswarm/muster/v5/internal/events"
	"github.com/giantswarm/muster/v5/internal/mcpserver"
	"github.com/giantswarm/muster/v5/internal/metatools"
	"github.com/giantswarm/muster/v5/internal/orchestrator"
	"github.com/giantswarm/muster/v5/internal/workflow"
)

// listToolsWithoutCursor are list tools that predate the cursor rule. They
// keep their arguments for their callers; remove an entry when its tool moves
// to cursor paging, and never add one.
var listToolsWithoutCursor = []string{
	"list_core_tools",
	"list_prompts",
	"list_resources",
	"list_tools",
	"mcpserver_list",
	"service_list",
	"workflow_execution_list",
	"workflow_list",
}

// TestConventionListToolsPageWithCursor checks every list tool muster serves: it takes
// an optional "cursor" argument and returns the next cursor, as MCP's own list
// methods do, and takes no offset, page number or completeness flag.
func TestConventionListToolsPageWithCursor(t *testing.T) {
	musterClient, err := client.NewMusterClientWithConfig(&client.MusterClientConfig{
		ForceFilesystemMode: true,
		FilesystemPath:      t.TempDir(),
	})
	require.NoError(t, err)

	workflowAdapter := workflow.NewAdapterWithClient(musterClient, "default", nil, nil, t.TempDir())
	t.Cleanup(workflowAdapter.Stop)

	providers := []api.ToolProvider{
		NewConfigAdapter(&config.MusterConfig{}, t.TempDir()),
		events.NewAdapter(musterClient, "default"),
		mcpserver.NewAdapterWithClient(musterClient, "default"),
		metatools.NewAdapter(),
		orchestrator.NewAPIAdapter(nil),
		workflowAdapter,
	}

	forbidden := []string{"offset", "page", "truncated", "has_more", "hasMore"}
	for _, provider := range providers {
		for _, tool := range provider.GetTools() {
			if !strings.HasSuffix(tool.Name, "_list") && !strings.HasPrefix(tool.Name, "list_") {
				continue
			}
			if slices.Contains(listToolsWithoutCursor, tool.Name) {
				continue
			}
			t.Run(tool.Name, func(t *testing.T) {
				argNames := make([]string, 0, len(tool.Args))
				for _, arg := range tool.Args {
					argNames = append(argNames, arg.Name)
				}
				require.Contains(t, argNames, "cursor",
					"list tool %q must take an optional string argument \"cursor\" and return the next cursor; an absent next cursor means the end", tool.Name)
				for _, name := range forbidden {
					require.NotContains(t, argNames, name,
						"list tool %q must page with \"cursor\", not %q", tool.Name, name)
				}
			})
		}
	}
}
