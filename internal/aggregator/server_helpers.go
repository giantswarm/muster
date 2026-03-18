package aggregator

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/giantswarm/muster/pkg/logging"

	"github.com/mark3labs/mcp-go/mcp"
)

// activeItemManager tracks which meta-tools are currently registered on the MCP server.
// Thread-safe for concurrent access from updateCapabilities and sessionToolFilter.
type activeItemManager struct {
	mu    sync.RWMutex
	items map[string]bool
}

// newActiveItemManager creates a new active item manager.
func newActiveItemManager() *activeItemManager {
	return &activeItemManager{
		items: make(map[string]bool),
	}
}

// isActive checks if an item is currently tracked.
func (m *activeItemManager) isActive(name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.items[name]
}

// track marks an item as active.
func (m *activeItemManager) track(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.items[name] = true
}

// getInactiveItems returns tracked items that are absent from newItems.
func (m *activeItemManager) getInactiveItems(newItems map[string]struct{}) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var inactive []string
	for name := range m.items {
		if _, exists := newItems[name]; !exists {
			inactive = append(inactive, name)
		}
	}
	return inactive
}

// removeItems removes the specified items from the tracked set.
func (m *activeItemManager) removeItems(items []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, item := range items {
		delete(m.items, item)
	}
}

// enrichServerList adds per-session sessionStatus and toolsCount to each
// server in an mcpserver_list response. sessionStatus is determined via
// determineSessionAuthStatus so that auth_required, sso_pending, etc. are
// visible in the listing -- not just "connected" for servers with cached caps.
func (a *AggregatorServer) enrichServerList(ctx context.Context, result *mcp.CallToolResult) *mcp.CallToolResult {
	sessionID := getSessionIDFromContext(ctx)
	if sessionID == "" || result == nil || len(result.Content) == 0 {
		return result
	}

	sub := getUserSubjectFromContext(ctx)

	for i, content := range result.Content {
		textContent, ok := content.(mcp.TextContent)
		if !ok {
			continue
		}

		var responseMap map[string]interface{}
		if err := json.Unmarshal([]byte(textContent.Text), &responseMap); err != nil {
			logging.Debug("ServerHelpers", "Failed to parse mcpserver_list response: %v", err)
			continue
		}

		servers, ok := responseMap["mcpServers"].([]interface{})
		if !ok {
			logging.Debug("ServerHelpers", "mcpserver_list response missing 'mcpServers' array, skipping enrichment")
			continue
		}

		for j, srv := range servers {
			serverMap, ok := srv.(map[string]interface{})
			if !ok {
				continue
			}
			servers[j] = a.enrichServerEntry(sub, sessionID, serverMap)
		}
		responseMap["mcpServers"] = servers

		enrichedJSON, err := json.Marshal(responseMap)
		if err != nil {
			logging.Debug("ServerHelpers", "Failed to serialize enriched response: %v", err)
			continue
		}

		result.Content[i] = mcp.TextContent{
			Type: "text",
			Text: string(enrichedJSON),
		}
	}

	return result
}

// enrichServerEntry adds sessionStatus and toolsCount to a single server map.
func (a *AggregatorServer) enrichServerEntry(sub, sessionID string, entry map[string]interface{}) map[string]interface{} {
	serverName, ok := entry["name"].(string)
	if !ok || serverName == "" {
		return entry
	}

	if info, found := a.registry.GetServerInfo(serverName); found {
		status := a.determineSessionAuthStatus(sub, sessionID, serverName, info)
		entry["sessionStatus"] = string(status)
	}

	if a.capabilityStore != nil {
		caps, err := a.capabilityStore.Get(context.Background(), sessionID, serverName)
		if err == nil && caps != nil && len(caps.Tools) > 0 {
			entry["toolsCount"] = len(caps.Tools)
		}
	}

	return entry
}
