package aggregator

import (
	"context"
	"encoding/json"

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

// handleNonOAuthToolListChanged handles a notifications/tools/list_changed
// notification from a non-OAuth server. Concurrent re-fetches for the same
// server are deduplicated via singleflight.
func (a *AggregatorServer) handleNonOAuthToolListChanged(serverName string) {
	sfKey := "notif-tools/" + serverName
	go func() {
		_, _, _ = a.notifRefreshGroup.Do(sfKey, func() (interface{}, error) {
			a.refreshNonOAuthTools(serverName)
			return nil, nil
		})
	}()
}

// refreshNonOAuthTools re-fetches tools, resources, and prompts from a
// non-OAuth server and updates the registry if anything changed.
func (a *AggregatorServer) refreshNonOAuthTools(serverName string) {
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

// handleSSOToolListChanged handles a notifications/tools/list_changed
// notification from an SSO (OAuth) server. Concurrent re-fetches for the
// same server are deduplicated via singleflight.
func (a *AggregatorServer) handleSSOToolListChanged(serverName string) {
	sfKey := "notif-sso/" + serverName
	go func() {
		_, _, _ = a.notifRefreshGroup.Do(sfKey, func() (interface{}, error) {
			a.refreshSSOCapabilities(serverName)
			return nil, nil
		})
	}()
}

// refreshSSOCapabilities re-fetches tools, resources, and prompts from
// an SSO server using any available pooled client, and updates the
// CapabilityStore for all sessions that have cached data for the server.
func (a *AggregatorServer) refreshSSOCapabilities(serverName string) {
	if a.connPool == nil || a.capabilityStore == nil {
		logging.Warn("Aggregator", "SSO notification refresh: pool or capability store not initialized for %s", serverName)
		return
	}

	client := a.connPool.GetAnyForServer(serverName)
	if client == nil {
		logging.Warn("Aggregator", "SSO notification refresh: no pooled client for %s", serverName)
		return
	}

	ctx := a.refreshContext()

	newTools, err := client.ListTools(ctx)
	if err != nil {
		logging.Warn("Aggregator", "SSO notification refresh: failed to list tools for %s: %v", serverName, err)
		return
	}

	newResources, err := client.ListResources(ctx)
	if err != nil {
		logging.Debug("Aggregator", "SSO notification refresh: failed to list resources for %s: %v", serverName, err)
		newResources = nil
	}

	newPrompts, err := client.ListPrompts(ctx)
	if err != nil {
		logging.Debug("Aggregator", "SSO notification refresh: failed to list prompts for %s: %v", serverName, err)
		newPrompts = nil
	}

	if !a.ssoCapabilitiesChanged(ctx, serverName, newTools, newResources, newPrompts) {
		logging.Debug("Aggregator", "SSO notification refresh: no capability changes for %s", serverName)
		return
	}

	caps := &Capabilities{
		Tools:     newTools,
		Resources: newResources,
		Prompts:   newPrompts,
	}

	if err := a.capabilityStore.UpdateServer(ctx, serverName, caps); err != nil {
		logging.Warn("Aggregator", "SSO notification refresh: failed to update capability store for %s: %v", serverName, err)
		return
	}

	logging.Info("Aggregator", "SSO notification refresh: updated capabilities for %s (%d tools, %d resources, %d prompts)",
		serverName, len(newTools), len(newResources), len(newPrompts))
}

// ssoCapabilitiesChanged checks whether the fetched capabilities differ from
// what is currently cached. It samples any pooled session's cached data for
// comparison since all sessions receive the same server-side capabilities.
func (a *AggregatorServer) ssoCapabilitiesChanged(ctx context.Context, serverName string, newTools []mcp.Tool, newResources []mcp.Resource, newPrompts []mcp.Prompt) bool {
	sessionID, ok := a.connPool.GetAnySessionForServer(serverName)
	if !ok {
		return true
	}

	cached, err := a.capabilityStore.Get(ctx, sessionID, serverName)
	if err != nil || cached == nil {
		return true
	}

	toolsChanged := !toolListsEqual(cached.Tools, newTools)
	resourcesChanged := newResources != nil && !resourceListsEqual(cached.Resources, newResources)
	promptsChanged := newPrompts != nil && !promptListsEqual(cached.Prompts, newPrompts)
	return toolsChanged || resourcesChanged || promptsChanged
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
