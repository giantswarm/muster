package aggregator

import (
	"context"
	"errors"
	"fmt"

	"github.com/giantswarm/muster/internal/api"
)

// ServiceToolAdapter implements api.ServiceManagerHandler so the
// core_service_{list,status} MCP tools surface live aggregator dial
// state for MCPServer names. Lifecycle operations (pause/resume,
// force-reconnect) live on MCPServer.spec.suspended and
// core_mcpserver_reconnect respectively. The remaining surface is
// slated for removal in Phase 8 when muster's /mcp goes away.
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

// GetAllServices returns a synthetic status for every known MCPServer CRD.
// The list is purely MCPServer-derived; the service surface is a name-shim
// over the MCPServer registry.
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

// GetTools advertises the surviving core_service_* tools.
func (a *ServiceToolAdapter) GetTools() []api.ToolMetadata {
	return []api.ToolMetadata{
		{
			Name:        "service_list",
			Description: "List all MCPServers with their current upstream-proxy status (deprecated; use core_mcpserver_list)",
		},
		{
			Name:        "service_status",
			Description: "Report the upstream-proxy state of the named MCPServer (deprecated; use core_mcpserver_get)",
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

func errResult(msg string) *api.CallToolResult {
	return &api.CallToolResult{Content: []interface{}{msg}, IsError: true}
}

// mcpServerStatus resolves an MCPServer name to a synthetic ServiceStatus
// using the aggregator's upstream-proxy view. Returns ok=false when no
// MCPServer CRD by that name exists; the second return value is the
// status for everything else, including "registered but not yet dialed".
//
// State names diverge between stdio (Running/Stopped) and remote
// (Connected/Disconnected) so BDD scenarios and operator dashboards can
// tell the two transports apart even though every dial goes through
// agentgateway over streamable-http internally.
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
