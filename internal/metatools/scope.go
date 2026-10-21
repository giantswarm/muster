package metatools

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
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
// availability already applied by the aggregator), narrowed by the toolsets
// that bound the request — the third filter. A bound Session's request is
// bounded by the toolset of its Session binding first and by the toolset its
// header declares within that, so a header narrows the bound toolset and
// never widens it. It is built once per meta-tool call and never cached
// across requests, so two requests on the same session with different
// X-Muster-Toolset headers see different catalogues.
type scopedCatalogue struct {
	// all is the session's catalogue before the toolset filter.
	all []mcp.Tool
	// tools is the catalogue after every toolset filter (equal to all when
	// the request is unscoped).
	tools []mcp.Tool
	// scoped is true when any toolset bounds the request.
	scoped bool
	// layers are the toolsets that bound the request, outermost first: the
	// bound toolset, then the header's. Each resolved within the previous.
	layers []scopeLayer
	// ts and res are the innermost layer's, the toolset the request's tools
	// were last resolved within.
	ts  toolset.Toolset
	res toolset.Resolution
	// pending lists the servers the session must sign in to before their
	// tools appear in all. A toolset that names one of them by server or by
	// tool name still resolves to nothing, so the accessors consult pending
	// to let the aggregator's auth_required answer through instead of
	// refusing the name as outside the toolset. It asks the auth store once
	// per server, so it runs only when a name falls outside the resolution.
	pending func() []api.ServerAuthInfo
}

// scopeLayer is one toolset bounding a request and its resolution.
type scopeLayer struct {
	ts  toolset.Toolset
	res toolset.Resolution
	// bound is true for the toolset of the Session binding.
	bound bool
}

// String names the toolset as refusals and log lines show it.
func (l scopeLayer) String() string {
	if l.bound {
		return l.ts.String() + " bound to the session"
	}
	return l.ts.String()
}

// catalogue lists the session's tools through the handler and applies the
// request's toolsets, if any. An invalid toolset was already rejected by
// ExecuteTool; a resolution error here (unknown preset) is returned verbatim.
func (p *Provider) catalogue(ctx context.Context, handler api.MetaToolsHandler) (*scopedCatalogue, *api.CallToolResult) {
	tools, err := handler.ListTools(ctx)
	if err != nil {
		return nil, errorResult(fmt.Sprintf("Failed to list tools: %v", err))
	}
	cat := &scopedCatalogue{all: tools, tools: tools, pending: sync.OnceValue(func() []api.ServerAuthInfo {
		return handler.ListServersRequiringAuth(ctx)
	})}

	bound, hasBound, err := toolset.BoundFromContext(ctx)
	if err != nil {
		return nil, errorResult(err.Error())
	}
	header, hasHeader, err := toolset.FromContext(ctx)
	if err != nil {
		return nil, errorResult(err.Error())
	}
	var layers []scopeLayer
	if hasBound {
		layers = append(layers, scopeLayer{ts: bound, bound: true})
	}
	if hasHeader {
		layers = append(layers, scopeLayer{ts: header})
	}
	if len(layers) == 0 {
		return cat, nil
	}
	labels := p.labelsFor(ctx)
	for i := range layers {
		res, err := p.presets.ResolveWith(layers[i].ts, toolset.EntriesFromTools(cat.tools), labels)
		if err != nil {
			return nil, errorResult(err.Error())
		}
		layers[i].res = res
		cat.tools = toolset.Filter(cat.tools, res)
	}
	if hasBound && hasHeader && !slices.Equal(bound.Raw, header.Raw) {
		logging.InfoWithAttrs("metatools", "toolset header resolved within the bound toolset",
			append(sessionAttrs(ctx), slog.String("toolset", header.String()), slog.String("bound_toolset", bound.String()))...)
	}
	inner := layers[len(layers)-1]
	cat.scoped, cat.layers, cat.ts, cat.res = true, layers, inner.ts, inner.res
	return cat, nil
}

// toolsets returns the toolsets of the layers, outermost first.
func (c *scopedCatalogue) toolsets() []toolset.Toolset {
	out := make([]toolset.Toolset, 0, len(c.layers))
	for _, l := range c.layers {
		out = append(out, l.ts)
	}
	return out
}

// bound returns the toolset bound to the session, if one bounds the request.
func (c *scopedCatalogue) bound() (toolset.Toolset, bool) {
	if c != nil && len(c.layers) > 0 && c.layers[0].bound {
		return c.layers[0].ts, true
	}
	return toolset.Toolset{}, false
}

// String names every toolset that bounds the request, innermost first.
func (c *scopedCatalogue) String() string {
	names := make([]string, 0, len(c.layers))
	for i := len(c.layers) - 1; i >= 0; i-- {
		names = append(names, c.layers[i].String())
	}
	return strings.Join(names, " within ")
}

// excluding returns the outermost layer that does not contain the tool name.
func (c *scopedCatalogue) excluding(name string) (scopeLayer, bool) {
	for _, l := range c.layers {
		if !l.res.Contains(name) {
			return l, true
		}
	}
	return scopeLayer{}, false
}

// sessionAttrs names the request's muster and transport sessions for a log
// line.
func sessionAttrs(ctx context.Context) []slog.Attr {
	var attrs []slog.Attr
	if sessionID := api.GetSessionIDFromContext(ctx); sessionID != "" {
		attrs = append(attrs, slog.String("session", logging.TruncateIdentifier(sessionID)))
	}
	if cs := mcpserver.ClientSessionFromContext(ctx); cs != nil {
		attrs = append(attrs, logging.TransportSessionID(cs.SessionID()))
	}
	return attrs
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

// unlisted describes the tool name of a server awaiting sign-in for
// toolset.MaySelect.
func unlisted(owner api.ServerAuthInfo, name string) toolset.Unlisted {
	return toolset.Unlisted{Server: owner.Name, Family: owner.Family, Prefix: owner.ToolPrefix, Name: name}
}

// maySelectSignIn reports whether the request's toolset could select the
// tool of a server awaiting sign-in once the session signs in and lists it.
func (p *Provider) maySelectSignIn(ctx context.Context, c *scopedCatalogue, owner api.ServerAuthInfo, name string) bool {
	if !c.isScoped() {
		return false
	}
	for _, ts := range c.toolsets() {
		if !p.presets.MaySelect(ts, unlisted(owner, name), p.labelsFor(ctx)) {
			return false
		}
	}
	return true
}

// requiringAuth returns the servers awaiting sign-in some of whose tools the
// toolsets could select once the session signs in: the part of the toolset
// a sign-in would unlock, which the resolution cannot report since it only
// sees listed tools. Every toolset given must allow it (an argument resolves
// within the request's toolset). A tool name carries the longest pending
// prefix it starts with, unless the catalogue already lists it. The result
// is sorted by server name.
func (p *Provider) requiringAuth(ctx context.Context, pending []api.ServerAuthInfo, all []mcp.Tool, toolsets ...toolset.Toolset) []api.ServerAuthInfo {
	labels := p.labelsFor(ctx)
	var named []api.ServerAuthInfo
	for _, server := range pending {
		u := unlisted(server, "")
		u.Owns = func(name string) bool {
			if server.ToolPrefix == "" || !strings.HasPrefix(name, server.ToolPrefix) || listed(all, name) {
				return false
			}
			for _, other := range pending {
				if len(other.ToolPrefix) > len(server.ToolPrefix) && strings.HasPrefix(name, other.ToolPrefix) {
					return false
				}
			}
			return true
		}
		selected := true
		for _, ts := range toolsets {
			selected = selected && p.presets.MaySelect(ts, u, labels)
		}
		if selected {
			named = append(named, server)
		}
	}
	slices.SortFunc(named, func(a, b api.ServerAuthInfo) int { return strings.Compare(a.Name, b.Name) })
	return named
}

// scope is catalogue for the accessors that do not otherwise list tools
// (call_tool, resources, prompts): it returns nil when no toolset bounds the
// request, so an unscoped request does not list the catalogue at all.
// Listing has side effects the unscoped paths never had — ListToolsForContext
// adopts the person's subject-scoped grants, connecting the session to servers
// the caller may be about to sign in to explicitly — and a request without a
// header must stay byte-for-byte today's behaviour.
func (p *Provider) scope(ctx context.Context, handler api.MetaToolsHandler) (*scopedCatalogue, *api.CallToolResult) {
	if !toolset.Scoped(ctx) {
		return nil, nil
	}
	return p.catalogue(ctx, handler)
}

// isScoped reports whether a toolset bounds the request. Nil-safe, so the
// accessors can hold the nil scope of an unscoped request.
func (c *scopedCatalogue) isScoped() bool {
	return c != nil && c.scoped
}

// outsideError is the refusal every accessor uses for a name that exists in
// the session's catalogue but not in the request's toolsets.
func (c *scopedCatalogue) outsideError(kind, name string) *api.CallToolResult {
	return errorResult(fmt.Sprintf("%s %q is outside the toolset %s", kind, name, c))
}

// outside returns the outside-toolset error when the request declared a
// toolset and name is a tool the session could otherwise see; nil when the
// request is unscoped or the tool is unknown altogether (callers then report
// "not found", exactly as without a toolset).
func (c *scopedCatalogue) outside(name string) *api.CallToolResult {
	if !c.isScoped() || c.res.Contains(name) || !listed(c.all, name) {
		return nil
	}
	l, _ := c.excluding(name)
	return errorResult(fmt.Sprintf("tool %q is outside the toolset %s", name, l))
}

// refuse gates call_tool: with a toolset declared, any name outside it is
// refused — known or not, since either way it is not something the agent was
// composed with — and so is a call to a family tool on a member the toolset
// does not select it on, selected by the call's instance argument. The
// refusal is logged with tool, member, toolset and session.
func (c *scopedCatalogue) refuse(ctx context.Context, name string, args map[string]any) *api.CallToolResult {
	if !c.isScoped() {
		return nil
	}
	// Every layer must hold the call: a family member a header selects is
	// still outside a bound toolset that does not.
	var member string
	var outside *scopeLayer
	for i := range c.layers {
		m, inside := c.layers[i].res.CallInside(name, args)
		if m != "" {
			member = m
		}
		if !inside {
			outside = &c.layers[i]
			break
		}
	}
	if outside == nil {
		return nil
	}
	attrs := append([]slog.Attr{
		slog.String("tool", name),
		slog.String("toolset", outside.ts.String()),
	}, sessionAttrs(ctx)...)
	if bound, ok := c.bound(); ok {
		attrs = append(attrs, slog.String("bound_toolset", bound.String()))
	}
	if member != "" {
		attrs = append(attrs, slog.String("server", member))
		logging.InfoWithAttrs("metatools", fmt.Sprintf("call_tool refused: tool %q on server %q is outside the toolset %s", name, member, outside), attrs...)
		return errorResult(fmt.Sprintf("tool %q on server %q is outside the toolset %s", name, member, outside))
	}
	logging.InfoWithAttrs("metatools", fmt.Sprintf("call_tool refused: tool %q is outside the toolset %s", name, outside), attrs...)
	return errorResult(fmt.Sprintf("tool %q is outside the toolset %s", name, outside))
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
