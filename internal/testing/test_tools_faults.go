package testing

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/clock"
	"github.com/giantswarm/muster/v5/internal/testing/mock"
)

// mockServerRedeployer is the part of a mock MCP HTTP server (plain or
// OAuth-protected) that the redeploy test tool drives.
type mockServerRedeployer interface {
	Redeploy(change mock.ToolSetChange) error
	Port() int
}

// handleRedeployMockServer replaces a running mock MCP server's process
// behind its port: every MCP session it held is forgotten, the port never
// stops accepting, and the tools stay unless the step names what the new
// process offers differently -- a backend pod replaced behind the same
// Service, by the same image or by one with other tools. muster's client
// meets a 404 for the session it holds on its next call. A changed tool set
// is announced to nobody: the new process has never seen muster's session,
// so no notifications/tools/list_changed goes out -- the silent redeployment
// the aggregator's capability poll is for. Distinct from
// test_stop_mock_server/test_start_mock_server, where the endpoint refuses
// connections for a while.
//
// Args:
//   - server: Required. Name of the mock MCP server to redeploy.
//   - add_tools: Optional. Tools the new process offers that the old one did
//     not, each {name, description}; a tool of a name already served is
//     replaced.
//   - remove_tools: Optional. Names of tools the new process no longer serves.
func (h *TestToolsHandler) handleRedeployMockServer(_ context.Context, args map[string]interface{}) (interface{}, error) {
	serverName, ok := args["server"].(string)
	if !ok || serverName == "" {
		return nil, fmt.Errorf("server argument is required")
	}
	change, err := toolSetChangeFromArgs(args)
	if err != nil {
		return nil, err
	}
	srv, err := h.lookupMockServer(serverName)
	if err != nil {
		return nil, err
	}
	redeployer, ok := srv.(mockServerRedeployer)
	if !ok {
		return nil, fmt.Errorf("mock server %s cannot be redeployed", serverName)
	}
	if err := redeployer.Redeploy(change); err != nil {
		return nil, fmt.Errorf("failed to redeploy mock server %s: %w", serverName, err)
	}
	message := fmt.Sprintf("Redeployed mock server '%s' on port %d: sessions forgotten, %s", serverName, redeployer.Port(), change.Summary())
	if h.debug {
		h.logger.Debug("%s\n", message)
	}
	result := map[string]interface{}{
		api.FieldSuccess: true,
		api.FieldMessage: message,
		api.FieldServer:  serverName,
		"port":           redeployer.Port(),
	}
	if len(change.Add) > 0 {
		added := make([]string, 0, len(change.Add))
		for _, tool := range change.Add {
			added = append(added, tool.Name)
		}
		result["tools_added"] = added
	}
	if len(change.Remove) > 0 {
		result["tools_removed"] = change.Remove
	}
	return result, nil
}

// toolSetChangeFromArgs reads the tool set a redeploy step gives the new
// process: add_tools, a list of {name, description}, each answering
// {status: ok, tool: <name>} like a tool test_add_mock_tool adds; and
// remove_tools, a list of names. Both optional.
func toolSetChangeFromArgs(args map[string]interface{}) (mock.ToolSetChange, error) {
	var change mock.ToolSetChange
	if raw, present := args["add_tools"]; present {
		list, ok := raw.([]interface{})
		if !ok {
			return change, fmt.Errorf("add_tools must be a list of {name, description}")
		}
		for _, item := range list {
			spec, ok := item.(map[string]interface{})
			name, _ := spec["name"].(string)
			if !ok || name == "" {
				return change, fmt.Errorf("every add_tools entry needs a name")
			}
			description, _ := spec["description"].(string)
			change.Add = append(change.Add, mock.ToolConfig{
				Name:        name,
				Description: description,
				Responses: []mock.ToolResponse{
					{Response: map[string]interface{}{api.FieldStatus: "ok", "tool": name}},
				},
			})
		}
	}
	if raw, present := args["remove_tools"]; present {
		list, ok := raw.([]interface{})
		if !ok {
			return change, fmt.Errorf("remove_tools must be a list of tool names")
		}
		for _, item := range list {
			name, ok := item.(string)
			if !ok || name == "" {
				return change, fmt.Errorf("every remove_tools entry must be a tool name")
			}
			change.Remove = append(change.Remove, name)
		}
	}
	return change, nil
}

// handleSetMockServerAuth flips a running OAuth-capable mock MCP server
// between answering anonymously and requiring a bearer token (401 with the
// RFC 9728 challenge and metadata) -- the rollover of a backend from an
// anonymous pod to one that is an OAuth resource server, or back. The mock
// needs oauth.mock_oauth_server_ref; oauth.required is its initial state.
//
// Args:
//   - server: Required. Name of the mock MCP server.
//   - required: Required. true demands a token, false answers anonymously.
func (h *TestToolsHandler) handleSetMockServerAuth(_ context.Context, args map[string]interface{}) (interface{}, error) {
	serverName, ok := args["server"].(string)
	if !ok || serverName == "" {
		return nil, fmt.Errorf("server argument is required")
	}
	required, ok := args["required"].(bool)
	if !ok {
		return nil, fmt.Errorf("required argument is required and must be a boolean")
	}
	if h.instanceManager == nil || h.currentInstance == nil {
		return nil, fmt.Errorf("instance manager or current instance not available")
	}
	srv := h.instanceManager.GetProtectedMCPServer(h.currentInstance.ID, serverName)
	if srv == nil {
		if h.instanceManager.GetMockHTTPServer(h.currentInstance.ID, serverName) != nil {
			return nil, fmt.Errorf("mock server %s has no authorization server to require tokens from: give it oauth.mock_oauth_server_ref (oauth.required sets its initial state)", serverName)
		}
		return nil, fmt.Errorf("mock server %s not found for instance %s", serverName, h.currentInstance.ID)
	}
	srv.SetAuthRequired(required)
	state := "answers anonymously"
	if required {
		state = "requires a bearer token (401 with resource metadata)"
	}
	if h.debug {
		h.logger.Debug("Mock server '%s' now %s\n", serverName, state)
	}
	return map[string]interface{}{
		api.FieldSuccess: true,
		api.FieldMessage: fmt.Sprintf("Mock server '%s' now %s", serverName, state),
		api.FieldServer:  serverName,
		"required":       required,
	}, nil
}

// handleAdvanceClock moves the instance's clock forward: muster serve's own
// clock (through the control socket MUSTER_TEST_CLOCK selected) and the
// clock of every mock authorization server of the instance, together, so
// that TTLs, backoffs and the catalogue age on both sides agree. Timers the
// clock reaches -- the reconnect backoff, the orchestrator's retry and health
// ticks, the aggregator's capability poll, the core catalogue's age -- fire
// at once when they have become due; nothing waits.
//
// Args:
//   - duration: Required. How far to advance (a Go duration, e.g. "31m").
func (h *TestToolsHandler) handleAdvanceClock(_ context.Context, args map[string]interface{}) (interface{}, error) {
	duration, ok := args["duration"].(string)
	if !ok || duration == "" {
		return nil, fmt.Errorf("duration argument is required (e.g. '31m', '2h')")
	}
	d, err := time.ParseDuration(duration)
	if err != nil {
		return nil, fmt.Errorf("invalid duration: %w", err)
	}
	if d <= 0 {
		return nil, fmt.Errorf("duration must be positive, got %s", d)
	}
	if h.instanceManager == nil || h.currentInstance == nil {
		return nil, fmt.Errorf("instance manager or current instance not available")
	}
	if h.currentInstance.ClockSocketPath == "" {
		return nil, fmt.Errorf("instance %s has no clock control socket", h.currentInstance.ID)
	}
	total, err := clock.RemoteAdvance(h.currentInstance.ClockSocketPath, d)
	if err != nil {
		return nil, fmt.Errorf("muster serve exposes no controllable clock (a release that ignores %s cannot be advanced): %w", clock.EnvControlSocket, err)
	}
	h.currentInstance.ClockOffset = total

	advanced := []string{}
	for name := range h.currentInstance.MockOAuthServers {
		oauthServer := h.instanceManager.GetMockOAuthServer(h.currentInstance.ID, name)
		if oauthServer == nil {
			continue
		}
		if advancer, ok := oauthServer.GetClock().(mock.Advancer); ok {
			advancer.Advance(d)
			advanced = append(advanced, name)
		}
	}
	sort.Strings(advanced)
	if h.debug {
		h.logger.Debug("🕐 Advanced the clock by %s (offset now %s; OAuth servers: %v)\n", d, total, advanced)
	}
	return map[string]interface{}{
		api.FieldSuccess:         true,
		api.FieldMessage:         fmt.Sprintf("Advanced the clock by %s (offset now %s)", d, total),
		"advanced_by":            d.String(),
		"offset":                 total.String(),
		"oauth_servers_advanced": advanced,
	}, nil
}
