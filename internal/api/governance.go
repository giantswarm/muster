package api

import (
	"slices"
	"sync"
)

// The governance modes. They change no setting's meaning: Governed only adds
// entries that ask agent callers.
const (
	GovernanceTrusted  = "trusted"
	GovernanceGoverned = "governed"
)

// The roles of the servers muster's governance configuration names. A tool
// name below is matched on the server's own tool name, on a server the
// configuration names for that role.
const (
	GovernanceRoleAgentManager     = "agentManager"
	GovernanceRolePlatformManager  = "platformManager"
	GovernanceRoleConnectorManager = "connectorManager"
)

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

// serverFloorTools are the floors on servers the configuration names, by
// role: granting an agent an exception, adding or repointing an organization
// connector past muster, and approving or merging a Platform Manager action;
// the connector manager's write tools once it takes them over.
var serverFloorTools = map[string][]string{
	GovernanceRoleAgentManager:     {"set_agent_approval", "set_organization_connector"},
	GovernanceRolePlatformManager:  {"approve_action", "merge_action"},
	GovernanceRoleConnectorManager: {"mcpserver_create", "mcpserver_connect", "mcpserver_set_approval"},
}

// serverGovernedTools are the entries Governed adds on servers the
// configuration names: an agent creating an agent, widening an agent's reach
// or starting another agent's Session. Pausing a connector is matched on its
// suspended argument (SuspendsConnector).
var serverGovernedTools = map[string][]string{
	GovernanceRoleAgentManager: {"create_agent", "update_agent_access", "start_session"},
}

// Governance is muster's governance configuration: the mode and the servers
// whose tools the floors and governed entries name. It is read once at start
// and never changed while muster runs.
type Governance struct {
	// Mode is GovernanceTrusted or GovernanceGoverned.
	Mode string
	// Servers lists, per role, the server keys the configuration names.
	Servers map[string][]string
	// Readable is false when muster started without a readable
	// configuration: the governed entries apply then.
	Readable bool
}

// Governed reports whether the governed entries apply: the mode says so, or
// the configuration could not be read.
func (g Governance) Governed() bool {
	return !g.Readable || g.Mode != GovernanceTrusted
}

// ServerRoles returns the roles the configuration names server for.
func (g Governance) ServerRoles(server string) []string {
	var roles []string
	for role, servers := range g.Servers {
		if slices.Contains(servers, server) {
			roles = append(roles, role)
		}
	}
	return roles
}

// NamesServer reports whether the configuration names server for any role.
func (g Governance) NamesServer(server string) bool {
	return len(g.ServerRoles(server)) > 0
}

// CoreFloor returns the approval a floor or governed entry gives a core tool
// for an agent caller, false when none applies. args are the call's
// arguments (core_mcpserver_update asks on Governed only when it pauses).
func (g Governance) CoreFloor(tool string, args map[string]any) (ToolApproval, bool) {
	if slices.Contains(coreFloorTools, tool) {
		return ToolApproval{Setting: ApprovalSettingAsk, Source: ApprovalSourceBuiltin}, true
	}
	if tool == "core_mcpserver_update" && g.Governed() && SuspendsConnector(args) {
		return ToolApproval{Setting: ApprovalSettingAsk, Source: ApprovalSourceGovernance}, true
	}
	return ToolApproval{}, false
}

// ServerFloor returns the approval a floor or governed entry gives the
// server's own tool name for an agent caller. refused is true when the tool
// carries such a name on a server the configuration does not name for it: an
// agent could otherwise reach a floor through a look-alike server.
func (g Governance) ServerFloor(server, tool string, args map[string]any) (approval ToolApproval, applies, refused bool) {
	for role, names := range serverFloorTools {
		if !slices.Contains(names, tool) {
			continue
		}
		if !slices.Contains(g.Servers[role], server) {
			return ToolApproval{}, false, true
		}
		return ToolApproval{Setting: ApprovalSettingAsk, Source: ApprovalSourceBuiltin}, true, false
	}
	for role, names := range serverGovernedTools {
		if !slices.Contains(names, tool) {
			continue
		}
		if !slices.Contains(g.Servers[role], server) {
			return ToolApproval{}, false, true
		}
		if g.Governed() {
			return ToolApproval{Setting: ApprovalSettingAsk, Source: ApprovalSourceGovernance}, true, false
		}
		return ToolApproval{}, false, false
	}
	if tool == "mcpserver_update" && SuspendsConnector(args) {
		if !slices.Contains(g.Servers[GovernanceRoleConnectorManager], server) {
			return ToolApproval{}, false, false
		}
		if g.Governed() {
			return ToolApproval{Setting: ApprovalSettingAsk, Source: ApprovalSourceGovernance}, true, false
		}
	}
	return ToolApproval{}, false, false
}

// SuspendsConnector reports whether a connector update's arguments pause it.
func SuspendsConnector(args map[string]any) bool {
	suspended, _ := args["suspended"].(bool)
	return suspended
}

var (
	governance      = Governance{}
	governanceMutex sync.RWMutex
)

// RegisterGovernance records the governance configuration muster started
// with. It is called once at start.
func RegisterGovernance(g Governance) {
	governanceMutex.Lock()
	defer governanceMutex.Unlock()
	servers := make(map[string][]string, len(g.Servers))
	for role, names := range g.Servers {
		servers[role] = slices.Clone(names)
	}
	g.Servers = servers
	governance = g
}

// GetGovernance returns a copy of the governance configuration. Before
// RegisterGovernance it is the unreadable configuration, so the governed
// entries apply.
func GetGovernance() Governance {
	governanceMutex.RLock()
	defer governanceMutex.RUnlock()
	g := governance
	servers := make(map[string][]string, len(g.Servers))
	for role, names := range g.Servers {
		servers[role] = slices.Clone(names)
	}
	g.Servers = servers
	return g
}
