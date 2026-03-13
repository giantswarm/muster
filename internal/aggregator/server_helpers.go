package aggregator

import (
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

// isActive checks if an item is active
func (m *activeItemManager) isActive(name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.items[name]
}

// setActive marks an item as active
func (m *activeItemManager) setActive(name string, active bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if active {
		m.items[name] = true
	} else {
		delete(m.items, name)
	}
}

// getInactiveItems returns items that are no longer in the new set
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

// removeItems removes the specified items from the active set
func (m *activeItemManager) removeItems(items []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, item := range items {
		delete(m.items, item)
	}
}

// enrichMCPServerWithSessionData adds session-specific state to an MCPServerInfo.
// This includes the session's connection status and tools count from the CapabilityCache.
func enrichMCPServerWithSessionData(serverInfo map[string]interface{}, cache *CapabilityCache, sessionID string) map[string]interface{} {
	if cache == nil || sessionID == "" {
		return serverInfo
	}

	serverName, ok := serverInfo["name"].(string)
	if !ok || serverName == "" {
		return serverInfo
	}

	entry, exists := cache.Get(sessionID, serverName)
	if !exists {
		return serverInfo
	}

	serverInfo["sessionStatus"] = "connected"

	if len(entry.Tools) > 0 {
		serverInfo["toolsCount"] = len(entry.Tools)
	}

	if !entry.StoredAt.IsZero() {
		serverInfo["connectedAt"] = entry.StoredAt
	}

	return serverInfo
}

// enrichMCPServerListResponse enriches the mcpserver_list response with session-specific data
// from the CapabilityCache. It modifies the response in place to add sessionStatus,
// toolsCount, and connectedAt fields to each server.
func enrichMCPServerListResponse(result *mcp.CallToolResult, cache *CapabilityCache, sessionID string) *mcp.CallToolResult {
	if cache == nil || sessionID == "" || result == nil || len(result.Content) == 0 {
		return result
	}

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

		for j, server := range servers {
			serverMap, ok := server.(map[string]interface{})
			if !ok {
				continue
			}
			servers[j] = enrichMCPServerWithSessionData(serverMap, cache, sessionID)
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
