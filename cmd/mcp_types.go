package cmd

import (
	"context"
	"fmt"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/cli"
)

// mcpPrimitiveTypes maps MCP primitive type aliases (singular and plural)
// to their canonical singular form. Used by list and get commands to
// dispatch MCP primitives separately from core resources.
var mcpPrimitiveTypes = map[string]string{
	api.MCPPrimitiveTool:      api.MCPPrimitiveTool,
	api.MCPPrimitiveTools:     api.MCPPrimitiveTool,
	api.MCPPrimitiveResource:  api.MCPPrimitiveResource,
	api.MCPPrimitiveResources: api.MCPPrimitiveResource,
	api.MCPPrimitivePrompt:    api.MCPPrimitivePrompt,
	api.MCPPrimitivePrompts:   api.MCPPrimitivePrompt,
}

// mcpServerSuspended reports whether the named MCPServer currently has
// spec.suspended=true. It calls core_mcpserver_get and parses the
// `suspended` field; absent or false both mean "not suspended".
func mcpServerSuspended(ctx context.Context, executor *cli.ToolExecutor, name string) (bool, error) {
	result, err := executor.ExecuteJSON(ctx, "core_mcpserver_get", map[string]interface{}{
		"name": name,
	})
	if err != nil {
		return false, fmt.Errorf("get mcpserver %q: %w", name, err)
	}
	obj, ok := result.(map[string]interface{})
	if !ok {
		return false, nil
	}
	suspended, _ := obj["suspended"].(bool)
	return suspended, nil
}
