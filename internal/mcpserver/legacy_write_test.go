package mcpserver

import (
	"context"
	"maps"
	"slices"

	"github.com/giantswarm/muster/v5/internal/api"
)

// legacyExecute runs a write the way the tests written before the connector
// write tools were split call it: a create without approval settings gets
// the allow-all preset (as a connector applied without settings is Allow),
// and an update that changes where the server is or how muster reaches it
// goes to core_mcpserver_connect (one that also updates other fields keeps
// those alone). Tests of the split itself call ExecuteTool
// directly.
func legacyExecute(adapter *Adapter, ctx context.Context, tool string, args map[string]interface{}) (*api.CallToolResult, error) {
	tool, args = legacyWrite(tool, args)
	return adapter.ExecuteTool(ctx, tool, args)
}

func legacyWrite(tool string, args map[string]interface{}) (string, map[string]interface{}) {
	switch tool {
	case "mcpserver_create":
		if _, ok := args["preset"]; !ok {
			if _, ok := args["approval"]; !ok {
				args = maps.Clone(args)
				args["preset"] = api.ApprovalPresetAllowAll
			}
		}
	case "mcpserver_update":
		var connect, update bool
		for key := range args {
			connect = connect || slices.Contains(connectFields, key)
			update = update || slices.Contains(updateFields, key)
		}
		switch {
		case connect && update:
			// Such a test re-sends the server's type and URL beside the
			// field it updates: the update alone.
			args = maps.Clone(args)
			maps.DeleteFunc(args, func(key string, _ interface{}) bool { return slices.Contains(connectFields, key) })
		case connect:
			return "mcpserver_connect", args
		}
	}
	return tool, args
}
