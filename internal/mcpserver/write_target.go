package mcpserver

import (
	"context"
	"fmt"

	"github.com/giantswarm/muster/v5/internal/api"
)

func (a *Adapter) validateWriteTarget(ctx context.Context, key string) *api.CallToolResult {
	if a.client == nil || !a.client.IsKubernetesMode() {
		if err := api.ValidateResourceName(key); err != nil {
			result, _ := simpleError(err.Error())
			return result
		}
		return nil
	}
	namespace, name := api.SplitMCPServerKey(key)
	if !api.IsTenantMCPServerKey(key) {
		namespace = a.namespace
	}
	for _, value := range []string{namespace, name} {
		if err := api.ValidateResourceName(value); err != nil {
			result, _ := simpleError(err.Error())
			return result
		}
	}
	_, _, served, err := a.object(ctx, key)
	if err != nil {
		result, _ := simpleError(fmt.Sprintf("Cannot check MCP server namespace: %v", err))
		return result
	}
	if !served {
		result, _ := simpleError(fmt.Sprintf("MCP server namespace %q is not served by muster", namespace))
		return result
	}
	return nil
}
