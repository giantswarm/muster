package aggregator

import (
	"context"
	"errors"
	"fmt"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/mcpserver"
)

// ServiceToolAdapter implements api.ServiceManagerHandler so the legacy
// core_service_{list,start,stop,restart,status} MCP tools keep working
// against MCPServer names. After PR 11 there are no "services" in the
// orchestrator sense anymore — every operation targets an MCPServer's
// upstream-proxy registration in the aggregator. The tool names survive
// because BDD scenarios + operator skills key off them.
type ServiceToolAdapter struct {
	manager *AggregatorManager
}

// NewServiceToolAdapter wraps manager. manager must be non-nil.
func NewServiceToolAdapter(manager *AggregatorManager) *ServiceToolAdapter {
	return &ServiceToolAdapter{manager: manager}
}

// Register publishes the adapter through internal/api so the aggregator's
// CallToolInternal dispatch can route service_* tools to it via
// api.GetServiceManager().
func (a *ServiceToolAdapter) Register() {
	api.RegisterServiceManager(a)
}

// StartService re-registers the named MCPServer through the upstream proxy
// and clears any user-stop record.
func (a *ServiceToolAdapter) StartService(name string) error {
	if a.manager == nil {
		return errMissingAggregatorManager
	}
	a.manager.MarkUserStarted(name)
	return a.manager.RegisterUpstream(context.Background(), name)
}

// StopService deregisters the named MCPServer and remembers the user's
// stop intent so the next reconciler pass does not silently re-register
// it.
func (a *ServiceToolAdapter) StopService(name string) error {
	if a.manager == nil {
		return errMissingAggregatorManager
	}
	a.manager.MarkUserStopped(name)
	return a.manager.DeregisterUpstream(context.Background(), name)
}

// RestartService deregisters then re-registers the named MCPServer,
// clearing any user-stop intent first.
func (a *ServiceToolAdapter) RestartService(name string) error {
	if a.manager == nil {
		return errMissingAggregatorManager
	}
	ctx := context.Background()
	a.manager.MarkUserStarted(name)
	if err := a.manager.DeregisterUpstream(ctx, name); err != nil {
		return err
	}
	return a.manager.RegisterUpstream(ctx, name)
}

// GetServiceStatus reports the named MCPServer's current upstream-proxy
// state. Returns "not found" only when the CRD itself is absent; an
// MCPServer whose dial hasn't landed yet surfaces as Stopped (stdio) or
// Disconnected (remote).
func (a *ServiceToolAdapter) GetServiceStatus(name string) (*api.ServiceStatus, error) {
	if a.manager == nil {
		return nil, errMissingAggregatorManager
	}
	status, ok := mcpServerStatus(a.manager, name)
	if !ok {
		return nil, fmt.Errorf("service %s not found", name)
	}
	return status, nil
}

// GetAllServices returns a synthetic status for every known MCPServer
// CRD. There are no longer any non-MCPServer services in the system, so
// the list is purely MCPServer-derived.
func (a *ServiceToolAdapter) GetAllServices() []api.ServiceStatus {
	if a.manager == nil {
		return nil
	}
	mcpServerMgr := api.GetMCPServerManager()
	if mcpServerMgr == nil {
		return nil
	}
	statuses := make([]api.ServiceStatus, 0)
	for _, info := range mcpServerMgr.ListMCPServers() {
		if status, ok := mcpServerStatus(a.manager, info.Name); ok {
			statuses = append(statuses, *status)
		}
	}
	return statuses
}

// GetTools exposes the legacy core_service_* surface. The names predate
// the muster-in-front pivot; they now operate exclusively on MCPServer
// upstream registrations.
func (a *ServiceToolAdapter) GetTools() []api.ToolMetadata {
	return []api.ToolMetadata{
		{
			Name:        "service_list",
			Description: "List all MCPServers with their current upstream-proxy status",
		},
		{
			Name:        "service_start",
			Description: "Register the named MCPServer through the upstream proxy",
			Args: []api.ArgMetadata{
				{Name: "name", Type: api.ArgTypeString, Required: true, Description: "MCPServer name"},
			},
		},
		{
			Name:        "service_stop",
			Description: "Deregister the named MCPServer and remember the user-stop intent",
			Args: []api.ArgMetadata{
				{Name: "name", Type: api.ArgTypeString, Required: true, Description: "MCPServer name"},
			},
		},
		{
			Name:        "service_restart",
			Description: "Deregister and immediately re-register the named MCPServer",
			Args: []api.ArgMetadata{
				{Name: "name", Type: api.ArgTypeString, Required: true, Description: "MCPServer name"},
			},
		},
		{
			Name:        "service_status",
			Description: "Report the upstream-proxy state of the named MCPServer",
			Args: []api.ArgMetadata{
				{Name: "name", Type: api.ArgTypeString, Required: true, Description: "MCPServer name"},
			},
		},
	}
}

// ExecuteTool routes a service_* tool to the matching handler.
func (a *ServiceToolAdapter) ExecuteTool(_ context.Context, toolName string, args map[string]interface{}) (*api.CallToolResult, error) {
	switch toolName {
	case "service_list":
		return a.handleServiceList(), nil
	case "service_start":
		return a.handleServiceStart(args), nil
	case "service_stop":
		return a.handleServiceStop(args), nil
	case "service_restart":
		return a.handleServiceRestart(args), nil
	case "service_status":
		return a.handleServiceStatus(args), nil
	default:
		return nil, fmt.Errorf("unknown tool: %s", toolName)
	}
}

func (a *ServiceToolAdapter) handleServiceList() *api.CallToolResult {
	svcs := a.GetAllServices()
	return &api.CallToolResult{
		Content: []interface{}{
			map[string]interface{}{
				"services": svcs,
				"total":    len(svcs),
			},
		},
	}
}

func (a *ServiceToolAdapter) handleServiceStart(args map[string]interface{}) *api.CallToolResult {
	name, ok := stringArg(args, "name")
	if !ok {
		return errResult("name is required")
	}
	status, err := a.GetServiceStatus(name)
	if err != nil {
		return errResult(fmt.Sprintf("Failed to start service: %v", err))
	}
	if status.State == api.StateRunning || status.State == api.StateConnected {
		return okResult(fmt.Sprintf("Service '%s' is already running", name))
	}
	if err := a.StartService(name); err != nil {
		if result := formatOAuthAuthError(name, err); result != nil {
			return result
		}
		return errResult(fmt.Sprintf("Failed to start service: %v", err))
	}
	return okResult(fmt.Sprintf("Successfully started service '%s'", name))
}

func (a *ServiceToolAdapter) handleServiceStop(args map[string]interface{}) *api.CallToolResult {
	name, ok := stringArg(args, "name")
	if !ok {
		return errResult("name is required")
	}
	status, err := a.GetServiceStatus(name)
	if err != nil {
		return errResult(fmt.Sprintf("Failed to stop service: %v", err))
	}
	if status.State == api.StateStopped || status.State == api.StateDisconnected {
		return okResult(fmt.Sprintf("Service '%s' is already stopped", name))
	}
	if err := a.StopService(name); err != nil {
		return errResult(fmt.Sprintf("Failed to stop service: %v", err))
	}
	return okResult(fmt.Sprintf("Successfully stopped service '%s'", name))
}

func (a *ServiceToolAdapter) handleServiceRestart(args map[string]interface{}) *api.CallToolResult {
	name, ok := stringArg(args, "name")
	if !ok {
		return errResult("name is required")
	}
	if err := a.RestartService(name); err != nil {
		if result := formatOAuthAuthError(name, err); result != nil {
			return result
		}
		return errResult(fmt.Sprintf("Failed to restart service: %v", err))
	}
	return okResult(fmt.Sprintf("Successfully restarted service '%s'", name))
}

func (a *ServiceToolAdapter) handleServiceStatus(args map[string]interface{}) *api.CallToolResult {
	name, ok := stringArg(args, "name")
	if !ok {
		return errResult("name is required")
	}
	status, err := a.GetServiceStatus(name)
	if err != nil {
		return errResult(fmt.Sprintf("Failed to get service status: %v", err))
	}
	return &api.CallToolResult{Content: []interface{}{status}}
}

func stringArg(args map[string]interface{}, key string) (string, bool) {
	v, ok := args[key].(string)
	return v, ok
}

func okResult(msg string) *api.CallToolResult {
	return &api.CallToolResult{Content: []interface{}{msg}}
}

func errResult(msg string) *api.CallToolResult {
	return &api.CallToolResult{Content: []interface{}{msg}, IsError: true}
}

// formatOAuthAuthError detects AuthRequiredError and converts it into the
// user-facing prompt the muster CLI surfaces. Returns nil for any other
// error so callers fall back to the generic message.
func formatOAuthAuthError(name string, err error) *api.CallToolResult {
	var authErr *mcpserver.AuthRequiredError
	if !errors.As(err, &authErr) {
		return nil
	}
	return &api.CallToolResult{
		Content: []interface{}{fmt.Sprintf(
			"Service '%s' requires OAuth authentication.\n\n"+
				"To connect to this server, use the core_auth_login tool:\n"+
				"  core_auth_login(server=\"%s\")\n\n"+
				"The service start/restart command cannot be used for OAuth-protected servers "+
				"because authentication is session-scoped.",
			name, name,
		)},
		IsError: true,
	}
}

// mcpServerStatus resolves an MCPServer name to a synthetic ServiceStatus
// using the aggregator's upstream-proxy view. Returns ok=false when no
// MCPServer CRD by that name exists; the second return value is the
// status for everything else, including "registered but not yet dialed".
//
// The stdio vs remote state-name divergence (Running/Connected,
// Stopped/Disconnected) is preserved so BDD scenarios and operator
// dashboards keep matching the legacy convention even though every dial
// goes through agentgateway over streamable-http internally.
func mcpServerStatus(manager *AggregatorManager, name string) (*api.ServiceStatus, bool) {
	mcpServerMgr := api.GetMCPServerManager()
	if mcpServerMgr == nil {
		return nil, false
	}
	info, err := mcpServerMgr.GetMCPServer(name)
	if err != nil || info == nil {
		return nil, false
	}
	isRemote := info.Type != "stdio"
	switch manager.UpstreamServerState(name) {
	case api.UpstreamServerConnected:
		state := api.StateRunning
		if isRemote {
			state = api.StateConnected
		}
		return &api.ServiceStatus{
			Name:        name,
			ServiceType: string(api.TypeMCPServer),
			State:       state,
			Health:      api.HealthHealthy,
		}, true
	case api.UpstreamServerAuthRequired:
		return &api.ServiceStatus{
			Name:        name,
			ServiceType: string(api.TypeMCPServer),
			State:       api.StateAuthRequired,
			Health:      api.HealthUnknown,
		}, true
	default:
		state := api.StateStopped
		if isRemote {
			state = api.StateDisconnected
		}
		return &api.ServiceStatus{
			Name:        name,
			ServiceType: string(api.TypeMCPServer),
			State:       state,
			Health:      api.HealthUnknown,
		}, true
	}
}

var errMissingAggregatorManager = errors.New("aggregator manager not available")
