package aggregator

import (
	"context"
	"encoding/json"

	"github.com/giantswarm/muster/pkg/logging"

	"github.com/mark3labs/mcp-go/mcp"
)

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

	ctx := context.Background()

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
