package app

import (
	"fmt"
	"log/slog"
	"slices"
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
// is an unreadable configuration: the governed default applies. The floor
// table is the built-in floors with roleFloors added; the removed servers
// shape stops the start.
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
	if cfg.Servers != nil {
		return api.Governance{}, fmt.Errorf("governance.servers is no longer read: name each server in governance.governedServers ({server, role, safe}) and declare roles muster does not build in with governance.roleFloors")
	}
	g.RoleFloors = api.MergeRoleFloors(cfg.RoleFloors)
	for _, s := range cfg.GovernedServers {
		g.Servers = append(g.Servers, api.GovernedServer{Server: s.Server, Role: s.Role, Safe: s.Safe})
	}
	if err := g.Validate(); err != nil {
		return api.Governance{}, err
	}
	for _, s := range g.Servers {
		if s.Role != "" && len(g.RoleFloorsOf(s.Role)) == 0 && !slices.Contains(api.GovernanceRoles(), s.Role) {
			logging.Warn("Governance", "governed server %s has role %s, which governance.roleFloors declares without floors: only the governed default applies to it", s.Server, s.Role)
		}
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
