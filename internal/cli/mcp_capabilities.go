package cli

import (
	"context"

	"github.com/giantswarm/muster/v5/internal/metatools"
)

// MCPResourceInfo is a resource as list_resources reports it: the MCP
// resource plus the server it comes from. A resource is exposed under its
// URI, which carries no server prefix, so the attribution is the only way to
// tell where it came from.
type MCPResourceInfo struct {
	MCPResource
	// Server is the MCPServer the resource comes from.
	Server string
}

// MCPPromptInfo is a prompt as list_prompts reports it: the MCP prompt plus
// the server it comes from. The exposed name carries the server's configured
// toolPrefix, not its name, so the attribution is carried alongside.
type MCPPromptInfo struct {
	MCPPrompt
	// Server is the MCPServer the prompt comes from.
	Server string
}

// ListMCPResourcesWithServer returns the caller's resources with the server
// each comes from, one entry per server that exposes a URI. The resource
// itself is taken from the native listing where it carries one.
func (e *ToolExecutor) ListMCPResourcesWithServer(ctx context.Context) ([]MCPResourceInfo, error) {
	listed, err := metatools.ListAllResources(ctx, e.client.CallTool)
	if err != nil {
		return nil, err
	}
	native, err := e.client.ListResourcesFromServer(ctx)
	if err != nil {
		return nil, err
	}
	return attributeResources(listed, native), nil
}

// ListMCPPromptsWithServer returns the caller's prompts with the server each
// comes from. The arguments, which list_prompts does not report, are taken
// from the native listing.
func (e *ToolExecutor) ListMCPPromptsWithServer(ctx context.Context) ([]MCPPromptInfo, error) {
	listed, err := metatools.ListAllPrompts(ctx, e.client.CallTool)
	if err != nil {
		return nil, err
	}
	native, err := e.client.ListPromptsFromServer(ctx)
	if err != nil {
		return nil, err
	}
	return attributePrompts(listed, native), nil
}

// attributeResources joins the list_resources entries with the native
// listing by URI. An entry the native listing lacks is projected from the
// meta-tool's fields.
func attributeResources(listed []metatools.ResourceInfo, native []MCPResource) []MCPResourceInfo {
	byURI := make(map[string]MCPResource, len(native))
	for _, r := range native {
		byURI[r.URI] = r
	}
	resources := make([]MCPResourceInfo, len(listed))
	for i, l := range listed {
		r, ok := byURI[l.URI]
		if !ok {
			r = MCPResource{URI: l.URI, Name: l.Name, Description: l.Description, MIMEType: l.MIMEType}
		}
		resources[i] = MCPResourceInfo{MCPResource: r, Server: l.Server}
	}
	return resources
}

// attributePrompts joins the list_prompts entries with the native listing by
// exposed name. An entry the native listing lacks carries no arguments.
func attributePrompts(listed []metatools.PromptInfo, native []MCPPrompt) []MCPPromptInfo {
	byName := make(map[string]MCPPrompt, len(native))
	for _, p := range native {
		byName[p.Name] = p
	}
	prompts := make([]MCPPromptInfo, len(listed))
	for i, l := range listed {
		p, ok := byName[l.Name]
		if !ok {
			p = MCPPrompt{Name: l.Name, Description: l.Description}
		}
		prompts[i] = MCPPromptInfo{MCPPrompt: p, Server: l.Server}
	}
	return prompts
}
