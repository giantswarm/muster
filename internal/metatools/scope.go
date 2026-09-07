package metatools

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/toolset"
	"github.com/giantswarm/muster/pkg/logging"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// scopedCatalogue is the catalogue a meta-tool reads for one request: the
// session's own tools (server-authentication visibility and workflow
// availability already applied by the aggregator), narrowed by the toolset
// the request declared — the third filter. It is built once per meta-tool
// call and never cached across requests, so two requests on the same session
// with different X-Muster-Toolset headers see different catalogues.
type scopedCatalogue struct {
	// all is the session's catalogue before the toolset filter.
	all []mcp.Tool
	// tools is the catalogue after the toolset filter (equal to all when the
	// request declared no toolset).
	tools []mcp.Tool
	// scoped is true when the request declared a toolset.
	scoped bool
	ts     toolset.Toolset
	res    toolset.Resolution
}

// catalogue lists the session's tools through the handler and applies the
// request's toolset, if any. An invalid toolset was already rejected by
// ExecuteTool; a resolution error here (unknown preset) is returned verbatim.
func (p *Provider) catalogue(ctx context.Context, handler api.MetaToolsHandler) (*scopedCatalogue, *api.CallToolResult) {
	tools, err := handler.ListTools(ctx)
	if err != nil {
		return nil, errorResult(fmt.Sprintf("Failed to list tools: %v", err))
	}
	cat := &scopedCatalogue{all: tools, tools: tools}

	ts, present, err := toolset.FromContext(ctx)
	if err != nil {
		return nil, errorResult(err.Error())
	}
	if !present {
		return cat, nil
	}
	res, err := p.presets.ResolveWith(ts, toolset.EntriesFromTools(tools), p.labelsFor(ctx))
	if err != nil {
		return nil, errorResult(err.Error())
	}
	cat.scoped, cat.ts, cat.res = true, ts, res
	cat.tools = toolset.Filter(tools, res)
	return cat, nil
}

// scope is catalogue for the accessors that do not otherwise list tools
// (call_tool, resources, prompts): it returns nil when the request declared
// no toolset, so an unscoped request does not list the catalogue at all.
// Listing has side effects the unscoped paths never had — ListToolsForContext
// adopts the person's subject-scoped grants, connecting the session to servers
// the caller may be about to sign in to explicitly — and a request without a
// header must stay byte-for-byte today's behaviour.
func (p *Provider) scope(ctx context.Context, handler api.MetaToolsHandler) (*scopedCatalogue, *api.CallToolResult) {
	if _, present, _ := toolset.FromContext(ctx); !present {
		return nil, nil
	}
	return p.catalogue(ctx, handler)
}

// isScoped reports whether the request declared a toolset. Nil-safe, so the
// accessors can hold the nil scope of an unscoped request.
func (c *scopedCatalogue) isScoped() bool {
	return c != nil && c.scoped
}

// outsideError is the refusal every accessor uses for a name that exists in
// the session's catalogue but not in the request's toolset.
func (c *scopedCatalogue) outsideError(kind, name string) *api.CallToolResult {
	return errorResult(fmt.Sprintf("%s %q is outside the toolset %s", kind, name, c.ts))
}

// outside returns the outside-toolset error when the request declared a
// toolset and name is a tool the session could otherwise see; nil when the
// request is unscoped or the tool is unknown altogether (callers then report
// "not found", exactly as without a toolset).
func (c *scopedCatalogue) outside(name string) *api.CallToolResult {
	if !c.isScoped() || c.res.Contains(name) {
		return nil
	}
	for _, t := range c.all {
		if t.Name == name {
			return c.outsideError("tool", name)
		}
	}
	return nil
}

// refuse gates call_tool: with a toolset declared, any name outside it is
// refused — known or not, since either way it is not something the agent was
// composed with — and the refusal is logged with tool, toolset and session.
func (c *scopedCatalogue) refuse(ctx context.Context, name string) *api.CallToolResult {
	if !c.isScoped() || c.res.Contains(name) {
		return nil
	}
	attrs := []slog.Attr{
		slog.String("tool", name),
		slog.String("toolset", c.ts.String()),
	}
	if sessionID := api.GetSessionIDFromContext(ctx); sessionID != "" {
		attrs = append(attrs, slog.String("session", logging.TruncateIdentifier(sessionID)))
	}
	if cs := mcpserver.ClientSessionFromContext(ctx); cs != nil {
		attrs = append(attrs, logging.TransportSessionID(cs.SessionID()))
	}
	logging.InfoWithAttrs("metatools", fmt.Sprintf("call_tool refused: tool %q is outside the toolset %s", name, c.ts), attrs...)
	return c.outsideError("tool", name)
}

// resources narrows the session's resources to the servers inside the
// toolset: a server is inside when at least one of its tools is selected.
func (c *scopedCatalogue) resources(resources []api.ResourceOrigin) []api.ResourceOrigin {
	if !c.isScoped() {
		return resources
	}
	out := make([]api.ResourceOrigin, 0, len(resources))
	for _, r := range resources {
		if c.res.ContainsServer(r.Server) {
			out = append(out, r)
		}
	}
	return out
}

// prompts narrows the session's prompts the same way resources does.
func (c *scopedCatalogue) prompts(prompts []api.PromptOrigin) []api.PromptOrigin {
	if !c.isScoped() {
		return prompts
	}
	out := make([]api.PromptOrigin, 0, len(prompts))
	for _, p := range prompts {
		if c.res.ContainsServer(p.Server) {
			out = append(out, p)
		}
	}
	return out
}

// scopedResources lists the session's resources within the toolset. The
// returned catalogue is nil-safe for unscoped requests (no toolset ⇒ the list
// is untouched) and lets get_resource refuse a resource whose server is
// outside the toolset.
func (p *Provider) scopedResources(ctx context.Context, handler api.MetaToolsHandler) ([]api.ResourceOrigin, *scopedCatalogue, *api.CallToolResult) {
	cat, errResult := p.scope(ctx, handler)
	if errResult != nil {
		return nil, nil, errResult
	}
	resources, err := handler.ListResources(ctx)
	if err != nil {
		return nil, nil, errorResult(fmt.Sprintf("Failed to list resources: %v", err))
	}
	return cat.resources(resources), cat, nil
}

// scopedPrompts mirrors scopedResources for prompts.
func (p *Provider) scopedPrompts(ctx context.Context, handler api.MetaToolsHandler) ([]api.PromptOrigin, *scopedCatalogue, *api.CallToolResult) {
	cat, errResult := p.scope(ctx, handler)
	if errResult != nil {
		return nil, nil, errResult
	}
	prompts, err := handler.ListPrompts(ctx)
	if err != nil {
		return nil, nil, errorResult(fmt.Sprintf("Failed to list prompts: %v", err))
	}
	return cat.prompts(prompts), cat, nil
}
