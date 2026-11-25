package app

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/giantswarm/muster/v5/internal/aggregator"
	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/config"
	"github.com/giantswarm/muster/v5/pkg/logging"
)

// maxApprovalTTL bounds approval.ttl: an approval is for one call now, not a
// standing permission.
const maxApprovalTTL = 30 * time.Minute

// governanceFromConfig turns the governance configuration into the immutable
// copy muster enforces. A missing section, or a mode muster does not know,
// is an unreadable configuration: the governed default applies. The earlier
// shape (servers per role) is read as governed servers of those roles with
// empty safe lists; it cannot be combined with governedServers.
func governanceFromConfig(cfg *config.GovernanceConfig) (api.Governance, error) {
	if cfg == nil {
		return api.Governance{Mode: api.GovernanceGoverned}, nil
	}
	g := api.Governance{
		Mode:     cfg.Mode,
		Readable: cfg.Mode == api.GovernanceTrusted || cfg.Mode == api.GovernanceGoverned,
	}
	if !g.Readable {
		g.Mode = api.GovernanceGoverned
	}
	legacy := map[string][]string{
		api.GovernanceRoleAgentManager:     cfg.Servers.AgentManager,
		api.GovernanceRolePlatformManager:  cfg.Servers.PlatformManager,
		api.GovernanceRoleConnectorManager: cfg.Servers.ConnectorManager,
	}
	hasLegacy := len(cfg.Servers.AgentManager)+len(cfg.Servers.PlatformManager)+len(cfg.Servers.ConnectorManager) > 0
	if hasLegacy && len(cfg.GovernedServers) > 0 {
		return api.Governance{}, fmt.Errorf("governance: set governedServers or the earlier servers.<role> lists, not both")
	}
	for _, role := range []string{api.GovernanceRoleAgentManager, api.GovernanceRolePlatformManager, api.GovernanceRoleConnectorManager} {
		for _, server := range legacy[role] {
			g.Servers = append(g.Servers, api.GovernedServer{Server: server, Role: role})
		}
	}
	for _, s := range cfg.GovernedServers {
		g.Servers = append(g.Servers, api.GovernedServer{Server: s.Server, Role: s.Role, Safe: s.Safe})
	}
	if err := g.Validate(); err != nil {
		return api.Governance{}, err
	}
	if hasLegacy {
		logging.Warn("Governance", "governance.servers is the earlier shape: its servers are governed with empty safe lists, so on governed every one of their tools asks agents. Use governance.governedServers with safe lists")
	}
	return g, nil
}

// registerGovernance records the governance configuration once, at start,
// and audits the mode muster runs in. A configuration muster cannot enforce
// as written stops the start.
func registerGovernance(cfg *config.GovernanceConfig) error {
	g, err := governanceFromConfig(cfg)
	if err != nil {
		return err
	}
	api.RegisterGovernance(g)
	servers := make([]string, 0, len(g.Servers))
	for _, s := range g.Servers {
		servers = append(servers, fmt.Sprintf("%s(role=%s, safe=%d)", s.Server, s.Role, len(s.Safe)))
	}
	logging.InfoWithAttrs("Governance", "[AUDIT] governance configuration at start",
		slog.String("action", "governance_start"),
		slog.String("mode", g.Mode),
		slog.Bool("readable", g.Readable),
		slog.Any("governedServers", servers))
	if !g.Readable {
		logging.Warn("Governance", "No readable governance configuration (governance.mode trusted or governed): the governed default applies to agent callers -- every core tool but muster's reads asks them -- and the floor tools of a role no governed server holds are refused to them. Set governance in muster's configuration (muster.governance in the muster chart)")
	}
	return nil
}

// approvalTTL parses approval.ttl.
func approvalTTL(cfg config.ApprovalConfig) (time.Duration, error) {
	if cfg.TTL == "" {
		return aggregator.DefaultApprovalTTL, nil
	}
	ttl, err := time.ParseDuration(cfg.TTL)
	if err != nil || ttl <= 0 || ttl > maxApprovalTTL {
		return 0, fmt.Errorf("approval.ttl %q must be a duration above 0 and at most %s", cfg.TTL, maxApprovalTTL)
	}
	return ttl, nil
}
