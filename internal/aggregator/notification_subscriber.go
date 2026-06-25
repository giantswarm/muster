package aggregator

import (
	"context"
	"encoding/json"

	oauthstore "github.com/giantswarm/muster/internal/oauth/store"
	"github.com/giantswarm/muster/pkg/logging"

	"github.com/mark3labs/mcp-go/mcp"
)

// refreshContext returns the aggregator's lifecycle context for notification
// refresh operations. Falls back to context.Background() for test code that
// constructs partial AggregatorServer instances without calling Start().
func (a *AggregatorServer) refreshContext() context.Context {
	if a.ctx != nil {
		return a.ctx
	}
	return context.Background()
}

// isCapabilityNotification returns true if the notification method indicates
// a server-side capability change (tools, resources, or prompts).
func isCapabilityNotification(method string) bool {
	for _, category := range capabilityCategories {
		if category.method == method {
			return true
		}
	}
	return false
}

// capabilityCategories pairs each capability count with its list_changed method
// so isCapabilityNotification and capabilityNotifications share one source.
var capabilityCategories = []struct {
	count  func(*ConnectionResult) int
	method string
}{
	{func(r *ConnectionResult) int { return r.ToolCount }, "notifications/tools/list_changed"},
	{func(r *ConnectionResult) int { return r.ResourceCount }, "notifications/resources/list_changed"},
	{func(r *ConnectionResult) int { return r.PromptCount }, "notifications/prompts/list_changed"},
}

// capabilityNotifications returns the list_changed methods warranted by a
// successful connection result. Empty categories are skipped so clients are not
// woken for capabilities that did not appear.
func capabilityNotifications(result *ConnectionResult) []string {
	if result == nil {
		return nil
	}
	var methods []string
	for _, category := range capabilityCategories {
		if category.count(result) > 0 {
			methods = append(methods, category.method)
		}
	}
	return methods
}

// notifySubjectCapabilitiesChanged pushes list_changed notifications to every
// live transport session for sub. A backend that connects after the client has
// already listed and cached its capabilities would otherwise stay invisible for
// the session's lifetime.
//
// The notification is subject-wide rather than scoped to the one session whose
// capabilityStore entry changed: SendNotificationToSpecificClient addresses a
// transport (Mcp-Session-Id) client, and from a background connect we hold only
// the bearer-derived connection-context session ID, which has no reverse map to
// a transport ID. subjectSessions resolves sub to its transport IDs instead. For
// a subject with multiple concurrent transport sessions this over-broadcasts:
// siblings re-list a server they have no entry for and get nothing new. Harmless,
// and the common agent case is one transport session per subject.
func (a *AggregatorServer) notifySubjectCapabilitiesChanged(sub string, result *ConnectionResult) {
	if a.mcpServer == nil || a.subjectSessions == nil || sub == "" {
		return
	}
	methods := capabilityNotifications(result)
	if len(methods) == 0 {
		return
	}
	for _, sessionID := range a.subjectSessions.GetSessionIDs(sub) {
		for _, method := range methods {
			if err := a.mcpServer.SendNotificationToSpecificClient(sessionID, method, nil); err != nil {
				logging.Debug("Aggregator", "%s to session %s failed: %v",
					method, logging.TruncateIdentifier(sessionID), err)
			}
		}
	}
}

// handleNonOAuthCapabilityChanged handles a capability-change notification
// from a non-OAuth server. Concurrent re-fetches for the same server are
// deduplicated via singleflight.
func (a *AggregatorServer) handleNonOAuthCapabilityChanged(serverName string) {
	sfKey := "notif-caps/" + serverName
	go func() {
		_, _, _ = a.notifRefreshGroup.Do(sfKey, func() (any, error) {
			a.refreshNonOAuthCapabilities(serverName)
			return nil, nil
		})
	}()
}

// refreshNonOAuthCapabilities re-fetches tools, resources, and prompts from a
// non-OAuth server and updates the registry if anything changed.
func (a *AggregatorServer) refreshNonOAuthCapabilities(serverName string) {
	info, exists := a.registry.GetServerInfo(serverName)
	if !exists || info.Client == nil {
		logging.Warn("Aggregator", "Notification refresh: server %s not found or has no client", serverName)
		return
	}

	ctx := a.refreshContext()

	newTools, err := info.Client.ListTools(ctx)
	if err != nil {
		logging.Warn("Aggregator", "Notification refresh: failed to list tools for %s: %v", serverName, err)
		return
	}

	newResources, err := info.Client.ListResources(ctx)
	if err != nil {
		logging.Debug("Aggregator", "Notification refresh: failed to list resources for %s: %v", serverName, err)
		newResources = nil
	}

	newPrompts, err := info.Client.ListPrompts(ctx)
	if err != nil {
		logging.Debug("Aggregator", "Notification refresh: failed to list prompts for %s: %v", serverName, err)
		newPrompts = nil
	}

	info.mu.RLock()
	toolsChanged := !toolListsEqual(info.Tools, newTools)
	resourcesChanged := newResources != nil && !resourceListsEqual(info.Resources, newResources)
	promptsChanged := newPrompts != nil && !promptListsEqual(info.Prompts, newPrompts)
	info.mu.RUnlock()

	if !toolsChanged && !resourcesChanged && !promptsChanged {
		logging.Debug("Aggregator", "Notification refresh: no capability changes for %s", serverName)
		return
	}

	if toolsChanged {
		info.UpdateTools(newTools)
		logging.Info("Aggregator", "Notification refresh: updated %d tools for %s", len(newTools), serverName)
	}
	if resourcesChanged {
		info.UpdateResources(newResources)
		logging.Info("Aggregator", "Notification refresh: updated %d resources for %s", len(newResources), serverName)
	}
	if promptsChanged {
		info.UpdatePrompts(newPrompts)
		logging.Info("Aggregator", "Notification refresh: updated %d prompts for %s", len(newPrompts), serverName)
	}

	a.registry.notifyUpdate()
}

// handleSessionCapabilityChanged handles a capability-change notification
// from an authenticated server for a specific session. Concurrent re-fetches
// for the same (sessionID, serverName) pair are deduplicated via singleflight.
func (a *AggregatorServer) handleSessionCapabilityChanged(serverName, sessionID string, client MCPClient) {
	sfKey := sessionID + "/" + serverName
	go func() {
		_, _, _ = a.notifRefreshGroup.Do(sfKey, func() (any, error) {
			ctx := a.refreshContext()
			a.refreshSessionCapabilities(ctx, serverName, sessionID, client)
			return nil, nil
		})
	}()
}

// refreshSessionCapabilities re-fetches capabilities for a single session
// using that session's own client, and updates the CapabilityStore if anything
// changed.
func (a *AggregatorServer) refreshSessionCapabilities(ctx context.Context, serverName, sessionID string, client MCPClient) {
	newTools, err := client.ListTools(ctx)
	if err != nil {
		logging.Warn("Aggregator", "Session notification refresh: failed to list tools for %s (session %s): %v",
			serverName, logging.TruncateIdentifier(sessionID), err)
		return
	}

	newResources, err := client.ListResources(ctx)
	if err != nil {
		logging.Debug("Aggregator", "Session notification refresh: failed to list resources for %s (session %s): %v",
			serverName, logging.TruncateIdentifier(sessionID), err)
		newResources = nil
	}

	newPrompts, err := client.ListPrompts(ctx)
	if err != nil {
		logging.Debug("Aggregator", "Session notification refresh: failed to list prompts for %s (session %s): %v",
			serverName, logging.TruncateIdentifier(sessionID), err)
		newPrompts = nil
	}

	cached, _ := a.capabilityStore.Get(ctx, sessionID, serverName)
	if cached != nil {
		toolsChanged := !toolListsEqual(cached.Tools, newTools)
		resourcesChanged := newResources != nil && !resourceListsEqual(cached.Resources, newResources)
		promptsChanged := newPrompts != nil && !promptListsEqual(cached.Prompts, newPrompts)
		if !toolsChanged && !resourcesChanged && !promptsChanged {
			return
		}
	}

	caps := &oauthstore.Capabilities{
		Tools:     newTools,
		Resources: newResources,
		Prompts:   newPrompts,
	}

	if err := a.capabilityStore.Set(ctx, sessionID, serverName, caps); err != nil {
		logging.Warn("Aggregator", "Session notification refresh: failed to update store for %s (session %s): %v",
			serverName, logging.TruncateIdentifier(sessionID), err)
		return
	}

	logging.Info("Aggregator", "Session notification refresh: updated capabilities for %s (session %s: %d tools, %d resources, %d prompts)",
		serverName, logging.TruncateIdentifier(sessionID), len(newTools), len(newResources), len(newPrompts))
}

// toolListsEqual compares two tool lists by name, description, and
// JSON-marshaled InputSchema.
func toolListsEqual(a, b []mcp.Tool) bool {
	if len(a) != len(b) {
		return false
	}

	byName := make(map[string]mcp.Tool, len(a))
	for _, t := range a {
		byName[t.Name] = t
	}

	for _, t := range b {
		prev, ok := byName[t.Name]
		if !ok {
			return false
		}
		if t.Description != prev.Description {
			return false
		}
		newSchema, _ := json.Marshal(t.InputSchema)
		oldSchema, _ := json.Marshal(prev.InputSchema)
		if string(newSchema) != string(oldSchema) {
			return false
		}
	}
	return true
}

// resourceListsEqual compares two resource lists by URI, name, and description.
// Fields like MimeType are intentionally excluded because they don't affect
// the tool/resource contract exposed to clients.
func resourceListsEqual(a, b []mcp.Resource) bool {
	if len(a) != len(b) {
		return false
	}

	byURI := make(map[string]mcp.Resource, len(a))
	for _, r := range a {
		byURI[r.URI] = r
	}

	for _, r := range b {
		prev, ok := byURI[r.URI]
		if !ok {
			return false
		}
		if r.Name != prev.Name || r.Description != prev.Description {
			return false
		}
	}
	return true
}

// promptListsEqual compares two prompt lists by name and description.
// The Arguments field is excluded because argument metadata changes don't
// alter which prompts are available or their identity.
func promptListsEqual(a, b []mcp.Prompt) bool {
	if len(a) != len(b) {
		return false
	}

	byName := make(map[string]mcp.Prompt, len(a))
	for _, p := range a {
		byName[p.Name] = p
	}

	for _, p := range b {
		prev, ok := byName[p.Name]
		if !ok {
			return false
		}
		if p.Description != prev.Description {
			return false
		}
	}
	return true
}
