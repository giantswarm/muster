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
// is an unreadable configuration: the governed entries apply.
func governanceFromConfig(cfg *config.GovernanceConfig) api.Governance {
	if cfg == nil {
		return api.Governance{Mode: api.GovernanceGoverned}
	}
	g := api.Governance{
		Mode: cfg.Mode,
		Servers: map[string][]string{
			api.GovernanceRoleAgentManager:     cfg.Servers.AgentManager,
			api.GovernanceRolePlatformManager:  cfg.Servers.PlatformManager,
			api.GovernanceRoleConnectorManager: cfg.Servers.ConnectorManager,
		},
		Readable: cfg.Mode == api.GovernanceTrusted || cfg.Mode == api.GovernanceGoverned,
	}
	if !g.Readable {
		g.Mode = api.GovernanceGoverned
	}
	return g
}

// registerGovernance records the governance configuration once, at start,
// and audits the mode muster runs in.
func registerGovernance(cfg *config.GovernanceConfig) {
	g := governanceFromConfig(cfg)
	api.RegisterGovernance(g)
	logging.InfoWithAttrs("Governance", "[AUDIT] governance configuration at start",
		slog.String("action", "governance_start"),
		slog.String("mode", g.Mode),
		slog.Bool("readable", g.Readable),
		slog.Any("agentManager", g.Servers[api.GovernanceRoleAgentManager]),
		slog.Any("platformManager", g.Servers[api.GovernanceRolePlatformManager]),
		slog.Any("connectorManager", g.Servers[api.GovernanceRoleConnectorManager]))
	if !g.Readable {
		logging.Warn("Governance", "No readable governance configuration (governance.mode trusted or governed): the governed entries apply to agent callers, and floor tools on servers it does not name are refused to them")
	}
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
