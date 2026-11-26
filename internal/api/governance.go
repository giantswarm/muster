package api

import (
	"fmt"
	"maps"
	"slices"
	"sync"
)

// The governance modes. Trusted applies the floors only; Governed also asks
// agent callers before every tool of a governed server that is not on that
// server's safe list, and before every core tool that is not one of muster's
// own reads.
const (
	GovernanceTrusted  = "trusted"
	GovernanceGoverned = "governed"
)

// The built-in roles a governed server may have. A role names the floors
// that hold on that server in both modes; a governed server without a role
// has no floors, only the governed default. The configuration can declare
// more roles, and add floors to any role, with governance.roleFloors.
const (
	GovernanceRoleAgentManager     = "agentManager"
	GovernanceRoleConnectorManager = "connectorManager"
	GovernanceRoleSessionManager   = "sessionManager"
	GovernanceRoleModelManager     = "modelManager"
	GovernanceRoleClusterManager   = "clusterManager"
	GovernanceRoleVMManager        = "vmManager"
	GovernanceRoleWorkspaceManager = "workspaceManager"
	GovernanceRoleToolchainManager = "toolchainManager"
	GovernanceRoleSkillManager     = "skillManager"
	GovernanceRoleInfrastructure   = "infrastructure"
)

// GovernanceRoles lists the built-in roles.
func GovernanceRoles() []string {
	return []string{GovernanceRoleAgentManager, GovernanceRoleConnectorManager,
		GovernanceRoleSessionManager, GovernanceRoleModelManager, GovernanceRoleClusterManager, GovernanceRoleVMManager,
		GovernanceRoleWorkspaceManager, GovernanceRoleToolchainManager, GovernanceRoleSkillManager, GovernanceRoleInfrastructure}
}

// ApprovalSourceGovernance is a call the governance mode makes ask for agent
// callers.
const ApprovalSourceGovernance = "governance"

// coreFloorTools are muster's own tools every agent caller is asked before,
// in both modes: adding or repointing a connector, changing its approval
// settings, and changing a workflow, whose definition is what its one
// up-front approval trusts.
var coreFloorTools = []string{
	"core_mcpserver_create",
	"core_mcpserver_connect",
	"core_mcpserver_set_approval",
	"core_workflow_create",
	"core_workflow_update",
	"core_workflow_delete",
}

// coreSafeTools are muster's own tools that change nothing (reads, and a
// person's own sign-in): the only core tools an agent calls unasked on
// Governed. Every other core tool -- stopping or starting a service,
// deleting or pausing a connector, a core tool added later -- asks agents
// there, the same default as on a governed server.
var coreSafeTools = []string{
	"core_config_get", "core_config_get_aggregator",
	"core_mcpserver_list", "core_mcpserver_get", "core_mcpserver_validate", "core_mcpserver_detect",
	"core_service_list", "core_service_status",
	"core_workflow_list", "core_workflow_get", "core_workflow_validate", "core_workflow_available",
	"core_workflow_execution_list", "core_workflow_execution_get",
	"core_events", "core_auth_login", "core_auth_logout", "core_auth_status",
}

// builtinRoleFloors are the floors of the built-in roles, in both modes:
// tools that grant an agent an exception, grant a person access, or send
// the person's token, prompts or a credential somewhere new (adding or
// repointing a connector, a model, an egress origin or a credential;
// approving a toolchain source). They are matched by name because they
// apply in both modes; everything else on a governed server is covered by
// the governed default. The configuration can only add to them
// (MergeRoleFloors).
var builtinRoleFloors = map[string][]string{
	GovernanceRoleAgentManager: {"set_agent_approval", "set_organization_connector", "add_organization_member",
		"set_organization_model", "set_organization_egress_policy", "replace_organization_credential"},
	GovernanceRoleConnectorManager: {"mcpserver_create", "mcpserver_connect", "mcpserver_set_approval"},
	GovernanceRoleToolchainManager: {"approve_source"},
	GovernanceRoleModelManager:     {"add_backend"},
}

// MergeRoleFloors returns the effective floor table: the built-in floors
// with the configured ones added, role by role. It is a union: nothing in
// the configuration removes or shrinks a built-in floor. A configured role
// that is not built in becomes a role a governed server may have.
func MergeRoleFloors(configured map[string][]string) map[string][]string {
	merged := make(map[string][]string, len(builtinRoleFloors)+len(configured))
	for role, tools := range builtinRoleFloors {
		merged[role] = slices.Clone(tools)
	}
	for role, tools := range configured {
		list := merged[role]
		for _, tool := range tools {
			if !slices.Contains(list, tool) {
				list = append(list, tool)
			}
		}
		if list == nil {
			list = []string{}
		}
		merged[role] = list
	}
	return merged
}

// floors returns the effective floor table: the one set on g, else the
// built-in floors.
func (g Governance) floors() map[string][]string {
	if len(g.RoleFloors) == 0 {
		return builtinRoleFloors
	}
	return g.RoleFloors
}

// floorRole returns a role the tool name is a floor of, "" for none.
func (g Governance) floorRole(tool string) string {
	floors := g.floors()
	for _, role := range slices.Sorted(maps.Keys(floors)) {
		if slices.Contains(floors[role], tool) {
			return role
		}
	}
	return ""
}

// RoleFloorsOf returns the floor tool names of a role.
func (g Governance) RoleFloorsOf(role string) []string { return slices.Clone(g.floors()[role]) }

// Roles returns the roles a governed server may have: the built-in ones and
// those the floor table declares.
func (g Governance) Roles() []string {
	roles := GovernanceRoles()
	for role := range g.floors() {
		if !slices.Contains(roles, role) {
			roles = append(roles, role)
		}
	}
	slices.Sort(roles)
	return roles
}

// CoreFloorTools returns muster's own floor tools.
func CoreFloorTools() []string { return slices.Clone(coreFloorTools) }

// CoreSafeTools returns muster's own tools agents call unasked on Governed.
func CoreSafeTools() []string { return slices.Clone(coreSafeTools) }

// GovernedServer is a server the governance configuration names: its server
// key, its role (which floors hold on it), and its safe list -- the tool
// names agents call on it unasked on Governed.
type GovernedServer struct {
	Server string
	Role   string
	Safe   []string
}

// Governance is muster's governance configuration: the mode and the
// governed servers. It is read once at start and never changed while muster
// runs.
type Governance struct {
	// Mode is GovernanceTrusted or GovernanceGoverned.
	Mode string
	// Servers are the governed servers.
	Servers []GovernedServer
	// Readable is false when muster started without a readable
	// configuration: the governed default applies then.
	Readable bool
	// RoleFloors is the effective floor table (MergeRoleFloors); nil means
	// the built-in floors.
	RoleFloors map[string][]string
}

// Validate refuses a configuration muster cannot enforce as written: a
// server named twice or without a key, a role neither built in nor declared
// in the floor table, a declared role or floor without a name, or a floor of
// any role on a safe list (a floor asks; listing it as safe would read as if
// it did not).
func (g Governance) Validate() error {
	for role, tools := range g.floors() {
		if role == "" {
			return fmt.Errorf("governance: roleFloors declares a role without a name")
		}
		if slices.Contains(tools, "") {
			return fmt.Errorf("governance: roleFloors.%s names a floor without a tool name", role)
		}
	}
	seen := map[string]bool{}
	for _, s := range g.Servers {
		if s.Server == "" {
			return fmt.Errorf("governance: a governed server has no server key")
		}
		if seen[s.Server] {
			return fmt.Errorf("governance: server %s is named twice", s.Server)
		}
		seen[s.Server] = true
		if s.Role != "" && !slices.Contains(g.Roles(), s.Role) {
			return fmt.Errorf("governance: server %s has unknown role %q (one of %v, or declare it in governance.roleFloors)", s.Server, s.Role, g.Roles())
		}
		for _, tool := range s.Safe {
			if slices.Contains(g.floors()[s.Role], tool) {
				return fmt.Errorf("governance: %s on server %s is a floor of role %s and cannot be on its safe list", tool, s.Server, s.Role)
			}
			if role := g.floorRole(tool); role != "" {
				return fmt.Errorf("governance: %s on server %s is a floor of role %s and cannot be on any safe list: on a governed platform it asks agents on every governed server", tool, s.Server, role)
			}
		}
	}
	return nil
}

// Governed reports whether the governed default applies: the mode says so,
// or the configuration could not be read.
func (g Governance) Governed() bool {
	return !g.Readable || g.Mode != GovernanceTrusted
}

// ModeName is the mode clients are shown: governed whenever the governed
// default applies (also for an unreadable configuration), else trusted.
func (g Governance) ModeName() string {
	if g.Governed() {
		return GovernanceGoverned
	}
	return GovernanceTrusted
}

// governed returns the governed server with the key.
func (g Governance) governed(server string) (GovernedServer, bool) {
	for _, s := range g.Servers {
		if s.Server == server {
			return s, true
		}
	}
	return GovernedServer{}, false
}

// NamesServer reports whether the configuration governs server.
func (g Governance) NamesServer(server string) bool {
	_, ok := g.governed(server)
	return ok
}

// hasRole reports whether a governed server has the role.
func (g Governance) hasRole(role string) bool {
	return slices.ContainsFunc(g.Servers, func(s GovernedServer) bool { return s.Role == role })
}

// ServerEntry returns the approval governance gives an agent caller's call
// of the server's own tool name:
//   - on a governed server, a floor of its role asks in both modes; on
//     Governed every other tool asks too unless it is on the server's safe
//     list, so a tool the server adds or renames later asks until someone
//     lists it (default-deny);
//   - on any other server nothing applies, except for a floor's name: while
//     no governed server holds the floor's role it is refused (refused
//     true), since muster cannot tell the role's real server from a
//     look-alike; once one does, it asks on Governed (an unnamed second
//     instance fails closed) and follows the server's own settings on
//     Trusted. Generic names that are no floor are never touched.
func (g Governance) ServerEntry(server, tool string) (approval ToolApproval, applies, refused bool) {
	if s, ok := g.governed(server); ok {
		if slices.Contains(g.floors()[s.Role], tool) {
			return ToolApproval{Setting: ApprovalSettingAsk, Source: ApprovalSourceBuiltin}, true, false
		}
		if g.Governed() && g.floorRole(tool) != "" {
			// A floor's name on a governed server of another role (a role
			// override, a mis-set role) still asks on Governed: no role
			// configuration removes a floor, built in or configured.
			return ToolApproval{Setting: ApprovalSettingAsk, Source: ApprovalSourceBuiltin}, true, false
		}
		if g.Governed() && !slices.Contains(s.Safe, tool) {
			return ToolApproval{Setting: ApprovalSettingAsk, Source: ApprovalSourceGovernance}, true, false
		}
		return ToolApproval{}, false, false
	}
	// A floor's name on a server the configuration does not name. The
	// verdict looks at every role the name is a floor of, so a name that
	// is a floor of two roles gets one answer, the strictest: on Governed
	// it asks once any of its roles is held (another instance of a
	// governed role under an unnamed key: asked, not allowed) and is
	// refused while none is; on Trusted it is refused while any of its
	// roles is unheld, since muster cannot tell that role's server from a
	// look-alike.
	var held, unheld bool
	for _, role := range slices.Sorted(maps.Keys(g.floors())) {
		if !slices.Contains(g.floors()[role], tool) {
			continue
		}
		if g.hasRole(role) {
			held = true
		} else {
			unheld = true
		}
	}
	switch {
	case !held && !unheld:
		return ToolApproval{}, false, false
	case g.Governed() && held:
		return ToolApproval{Setting: ApprovalSettingAsk, Source: ApprovalSourceGovernance}, true, false
	case unheld:
		return ToolApproval{}, false, true
	}
	return ToolApproval{}, false, false
}

// CoreEntry returns the approval governance gives an agent caller's call of
// one of muster's core tools: a floor asks in both modes; on Governed every
// core tool that is not one of muster's reads asks too.
func (g Governance) CoreEntry(tool string) (ToolApproval, bool) {
	if slices.Contains(coreFloorTools, tool) {
		return ToolApproval{Setting: ApprovalSettingAsk, Source: ApprovalSourceBuiltin}, true
	}
	if g.Governed() && !slices.Contains(coreSafeTools, tool) {
		if _, builtin := BuiltinApproval(tool); !builtin {
			return ToolApproval{Setting: ApprovalSettingAsk, Source: ApprovalSourceGovernance}, true
		}
	}
	return ToolApproval{}, false
}

// UnmatchedSafe returns the safe-list entries of a governed server that
// name none of the tools it lists: a typo or a renamed tool, which then asks
// (fail closed) but should be fixed.
func (g Governance) UnmatchedSafe(server string, tools []string) []string {
	s, ok := g.governed(server)
	if !ok {
		return nil
	}
	var unmatched []string
	for _, safe := range s.Safe {
		if !slices.Contains(tools, safe) {
			unmatched = append(unmatched, safe)
		}
	}
	return unmatched
}

var (
	governance      = Governance{}
	governanceMutex sync.RWMutex
)

func cloneGovernance(g Governance) Governance {
	servers := make([]GovernedServer, len(g.Servers))
	for i, s := range g.Servers {
		servers[i] = GovernedServer{Server: s.Server, Role: s.Role, Safe: slices.Clone(s.Safe)}
	}
	g.Servers = servers
	if g.RoleFloors != nil {
		floors := make(map[string][]string, len(g.RoleFloors))
		for role, tools := range g.RoleFloors {
			floors[role] = slices.Clone(tools)
		}
		g.RoleFloors = floors
	}
	return g
}

// RegisterGovernance records the governance configuration muster started
// with. It is called once at start.
func RegisterGovernance(g Governance) {
	governanceMutex.Lock()
	defer governanceMutex.Unlock()
	governance = cloneGovernance(g)
}

// GetGovernance returns a copy of the governance configuration. Before
// RegisterGovernance it is the unreadable configuration, so the governed
// default applies.
func GetGovernance() Governance {
	governanceMutex.RLock()
	defer governanceMutex.RUnlock()
	return cloneGovernance(governance)
}
