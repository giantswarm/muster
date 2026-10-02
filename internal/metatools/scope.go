package metatools

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/toolset"
	"github.com/giantswarm/muster/v5/pkg/logging"

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
	// pending lists the servers the session must sign in to before their
	// tools appear in all. A toolset that names one of them by server or by
	// tool name still resolves to nothing, so the accessors consult pending
	// to let the aggregator's auth_required answer through instead of
	// refusing the name as outside the toolset. It asks the auth store once
	// per server, so it runs only when a name falls outside the resolution.
	pending func() []api.ServerAuthInfo
}

// catalogue lists the session's tools through the handler and applies the
// request's toolset, if any. An invalid toolset was already rejected by
// ExecuteTool; a resolution error here (unknown preset) is returned verbatim.
func (p *Provider) catalogue(ctx context.Context, handler api.MetaToolsHandler) (*scopedCatalogue, *api.CallToolResult) {
	tools, err := handler.ListTools(ctx)
	if err != nil {
		return nil, errorResult(fmt.Sprintf("Failed to list tools: %v", err))
	}
	cat := &scopedCatalogue{all: tools, tools: tools, pending: sync.OnceValue(func() []api.ServerAuthInfo {
		return handler.ListServersRequiringAuth(ctx)
	})}

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

// pendingOwnerOf returns the server awaiting sign-in whose tool prefix is
// the longest one the exposed name carries.
func pendingOwnerOf(pending []api.ServerAuthInfo, name string) (api.ServerAuthInfo, bool) {
	var owner api.ServerAuthInfo
	for _, server := range pending {
		if server.ToolPrefix != "" && strings.HasPrefix(name, server.ToolPrefix) && len(server.ToolPrefix) > len(owner.ToolPrefix) {
			owner = server
		}
	}
	return owner, owner.Name != ""
}

// listed reports whether the session's catalogue, before the toolset filter,
// holds the name.
func listed(tools []mcp.Tool, name string) bool {
	for _, tool := range tools {
		if tool.Name == name {
			return true
		}
	}
	return false
}

// namedForSignIn reports whether a call outside the toolset's resolution is
// one the toolset names on a server the session has not signed in to: an
// inline tool:<name> selector for the tool itself, or server:<name> for its
// server. A preset cannot name it, since a preset's rules match tools the
// catalogue holds. A listed tool is never such a call, whatever its prefix:
// the resolution already left it out. The call goes to AnswerSignIn, which
// answers with the sign-in link and runs nothing else.
func (c *scopedCatalogue) namedForSignIn(name string) bool {
	if !c.isScoped() || c.res.Contains(name) || listed(c.all, name) {
		return false
	}
	server, ok := pendingOwnerOf(c.pending(), name)
	if !ok {
		return false
	}
	for _, selector := range c.ts.Selectors {
		switch selector.Kind {
		case toolset.KindTool:
			if selector.Name == name {
				return true
			}
		case toolset.KindServer:
			if selector.Name == server.Name {
				return true
			}
		}
	}
	return false
}

// requiringAuth returns the servers awaiting sign-in that the toolset names,
// by server or by a tool of theirs: the part of the toolset a sign-in would
// unlock, which the resolution alone reports as unmatched. A tool selector
// naming a listed tool names no pending server.
func requiringAuth(ts toolset.Toolset, pending []api.ServerAuthInfo, all []mcp.Tool) []api.ServerAuthInfo {
	var named []api.ServerAuthInfo
	seen := map[string]bool{}
	for _, selector := range ts.Selectors {
		var server api.ServerAuthInfo
		var ok bool
		switch selector.Kind {
		case toolset.KindServer:
			for _, candidate := range pending {
				if candidate.Name == selector.Name {
					server, ok = candidate, true
					break
				}
			}
		case toolset.KindTool:
			if !listed(all, selector.Name) {
				server, ok = pendingOwnerOf(pending, selector.Name)
			}
		}
		if ok && !seen[server.Name] {
			seen[server.Name] = true
			named = append(named, server)
		}
	}
	return named
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
