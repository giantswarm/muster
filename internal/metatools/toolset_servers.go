package metatools

import (
	"context"
	"fmt"
	"sync"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/toolset"
)

// ToolsetServers implements api.ToolsetServerScope with the same toolsets
// list_tools applies: the toolset bound to the session, then the
// X-Muster-Toolset header within it. A server is admitted when every toolset
// bounding the request admits it: its resolution selects one of the server's
// tools, or a server:<name> selector names it. A server awaiting sign-in has
// no tools listed yet, so it is admitted when the toolsets could select one
// of its tools once listed, as servers_requiring_auth reports it.
func (a *Adapter) ToolsetServers(ctx context.Context) (func(string) bool, bool, error) {
	return a.provider.toolsetServers(ctx, a)
}

func (p *Provider) toolsetServers(ctx context.Context, handler api.MetaToolsHandler) (func(string) bool, bool, error) {
	if !toolset.Scoped(ctx) {
		return nil, false, nil
	}
	cat, errResult := p.catalogue(ctx, handler)
	if errResult != nil {
		return nil, true, fmt.Errorf("resolve the toolset: %s", fmt.Sprint(errResult.Content...))
	}
	if !cat.isScoped() {
		return nil, false, nil
	}
	layers := cat.layers
	awaitingSignIn := sync.OnceValue(func() map[string]bool {
		admitted := map[string]bool{}
		for _, s := range p.requiringAuth(ctx, cat.pending(), cat.all, cat.toolsets()...) {
			admitted[s.Name] = true
		}
		return admitted
	})
	return func(server string) bool {
		for _, l := range layers {
			if !l.res.ContainsServer(server) && !namesServer(l.ts, server) {
				return awaitingSignIn()[server]
			}
		}
		return true
	}, true, nil
}

// ToolsetCoreTools implements api.ToolsetServerScope: whether the tools left
// after every toolset bounding the request include a core tool.
func (a *Adapter) ToolsetCoreTools(ctx context.Context) (bool, bool, error) {
	return a.provider.toolsetCoreTools(ctx, a)
}

func (p *Provider) toolsetCoreTools(ctx context.Context, handler api.MetaToolsHandler) (bool, bool, error) {
	if !toolset.Scoped(ctx) {
		return false, false, nil
	}
	cat, errResult := p.catalogue(ctx, handler)
	if errResult != nil {
		return false, true, fmt.Errorf("resolve the toolset: %s", fmt.Sprint(errResult.Content...))
	}
	if !cat.isScoped() {
		return false, false, nil
	}
	for _, t := range cat.tools {
		if toolset.KindOf(t) == toolset.KindEntryCore {
			return true, true, nil
		}
	}
	return false, true, nil
}

// namesServer reports whether a toolset names the server with a server:
// selector.
func namesServer(ts toolset.Toolset, server string) bool {
	for _, s := range ts.Selectors {
		if s.Kind == toolset.KindServer && s.Name == server {
			return true
		}
	}
	return false
}

var _ api.ToolsetServerScope = (*Adapter)(nil)
