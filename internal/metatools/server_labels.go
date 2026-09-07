package metatools

import (
	"context"
	"sync"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/toolset"
	"github.com/giantswarm/muster/pkg/logging"
)

// ServerLabelsSource builds, for one request, the lookup that resolves an
// MCPServer name to its resource labels. The lookup is consulted lazily by
// label: preset rules only, so the source must defer any listing until the
// first call.
type ServerLabelsSource func(ctx context.Context) toolset.ServerLabels

// MCPServerLabels is the ServerLabelsSource backed by the MCPServer manager:
// the first label lookup of a request lists the MCPServer resources once
// (live, in both storage modes — a server gaining or losing a label changes
// the next request's resolution without a restart) and every further lookup
// in that request reads the memoised map. A failed listing is logged and
// resolves every server to no labels, so a label rule then selects nothing —
// the closed failure mode, never a wider one.
func MCPServerLabels(ctx context.Context) toolset.ServerLabels {
	var (
		once   sync.Once
		labels map[string]map[string]string
	)
	return func(server string) map[string]string {
		once.Do(func() {
			labels = map[string]map[string]string{}
			manager := api.GetMCPServerManager()
			if manager == nil {
				return
			}
			servers, err := manager.ListMCPServers(ctx)
			if err != nil {
				logging.Warn("metatools", "toolset label rules resolve to nothing: listing MCPServers failed: %v", err)
				return
			}
			for _, s := range servers {
				labels[s.Name] = s.Labels
			}
		})
		return labels[server]
	}
}

// labelsFor returns the request's label lookup, or nil when no source is
// configured (label rules then select nothing).
func (p *Provider) labelsFor(ctx context.Context) toolset.ServerLabels {
	if p.serverLabels == nil {
		return nil
	}
	return p.serverLabels(ctx)
}
