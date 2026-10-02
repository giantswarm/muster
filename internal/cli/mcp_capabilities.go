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
// itself is taken from the native listing where it carries one; muster's own
// resources (auth://status), which only the native listing has, carry no
// server.
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
// comes from. list_prompts reports no arguments: with withArguments, those of
// a prompt the native listing lacks -- every aggregated one -- are read with
// describe_prompt, one call per prompt.
func (e *ToolExecutor) ListMCPPromptsWithServer(ctx context.Context, withArguments bool) ([]MCPPromptInfo, error) {
	listed, err := metatools.ListAllPrompts(ctx, e.client.CallTool)
	if err != nil {
		return nil, err
	}
	native, err := e.client.ListPromptsFromServer(ctx)
	if err != nil {
		return nil, err
	}
	prompts := attributePrompts(listed, native)
	if !withArguments {
		return prompts, nil
	}
	nativeNames := make(map[string]bool, len(native))
	for _, p := range native {
		nativeNames[p.Name] = true
	}
	for i := range prompts {
		if nativeNames[prompts[i].Name] {
			continue
		}
		args, err := metatools.DescribePromptArguments(ctx, e.client.CallTool, prompts[i].Name)
		if err != nil {
			return nil, err
		}
		prompts[i].Arguments = args
	}
	return prompts, nil
}

// attributeResources joins the list_resources entries with the native
// listing by URI. An entry the native listing lacks is projected from the
// meta-tool's fields; a native resource the meta-tool does not report is
// muster's own and is kept without a server.
func attributeResources(listed []metatools.ResourceInfo, native []MCPResource) []MCPResourceInfo {
	byURI := make(map[string]MCPResource, len(native))
	for _, r := range native {
		byURI[r.URI] = r
	}
	reported := make(map[string]bool, len(listed))
	resources := make([]MCPResourceInfo, 0, len(listed)+len(native))
	for _, l := range listed {
		r, ok := byURI[l.URI]
		if !ok {
			r = MCPResource{URI: l.URI, Name: l.Name, Description: l.Description, MIMEType: l.MIMEType}
		}
		reported[l.URI] = true
		resources = append(resources, MCPResourceInfo{MCPResource: r, Server: l.Server})
	}
	for _, r := range native {
		if !reported[r.URI] {
			resources = append(resources, MCPResourceInfo{MCPResource: r})
		}
	}
	return resources
}

// attributePrompts joins the list_prompts entries with the native listing by
// exposed name. An entry the native listing lacks carries no arguments; a
// native prompt the meta-tool does not report is kept without a server.
func attributePrompts(listed []metatools.PromptInfo, native []MCPPrompt) []MCPPromptInfo {
	byName := make(map[string]MCPPrompt, len(native))
	for _, p := range native {
		byName[p.Name] = p
	}
	reported := make(map[string]bool, len(listed))
	prompts := make([]MCPPromptInfo, 0, len(listed)+len(native))
	for _, l := range listed {
		p, ok := byName[l.Name]
		if !ok {
			p = MCPPrompt{Name: l.Name, Description: l.Description}
		}
		reported[l.Name] = true
		prompts = append(prompts, MCPPromptInfo{MCPPrompt: p, Server: l.Server})
	}
	for _, p := range native {
		if !reported[p.Name] {
			prompts = append(prompts, MCPPromptInfo{MCPPrompt: p})
		}
	}
	return prompts
}

// findResource looks a resource up with describe_resource, which knows every
// aggregated one. muster's own resources (auth://status) are not aggregated:
// only the native listing has them, and they carry no server.
func findResource(ctx context.Context, call metatools.ToolCaller, native func(context.Context) ([]MCPResource, error), uri, server string) (*MCPResourceInfo, error) {
	detail, err := metatools.DescribeResource(ctx, call, uri, server)
	if err != nil {
		return nil, err
	}
	if detail != nil {
		return &MCPResourceInfo{
			MCPResource: MCPResource{URI: detail.URI, Name: detail.Name, Description: detail.Description, MIMEType: detail.MIMEType},
			Server:      detail.Server,
		}, nil
	}
	if server != "" {
		return nil, nil
	}
	resources, err := native(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range resources {
		if r.URI == uri {
			return &MCPResourceInfo{MCPResource: r}, nil
		}
	}
	return nil, nil
}

// findPrompt looks a prompt up with describe_prompt, which knows every
// aggregated one with its arguments; a prompt only the native listing has is
// muster's own and carries no server.
func findPrompt(ctx context.Context, call metatools.ToolCaller, native func(context.Context) ([]MCPPrompt, error), name string) (*MCPPromptInfo, error) {
	detail, err := metatools.DescribePrompt(ctx, call, name)
	if err != nil {
		return nil, err
	}
	if detail != nil {
		return &MCPPromptInfo{
			MCPPrompt: MCPPrompt{Name: detail.Name, Description: detail.Description, Arguments: detail.Arguments},
			Server:    detail.Server,
		}, nil
	}
	prompts, err := native(ctx)
	if err != nil {
		return nil, err
	}
	for _, p := range prompts {
		if p.Name == name {
			return &MCPPromptInfo{MCPPrompt: p}, nil
		}
	}
	return nil, nil
}
