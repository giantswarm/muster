package aggregator

import (
	"fmt"
	"strings"
	"sync"
)

// NameTracker handles prefixing of tool, prompt, and resource names
type NameTracker struct {
	// Map of exposed name -> (server, original name)
	nameMapping map[string]struct {
		serverName   string
		originalName string
		itemType     string // "tool", "prompt", or "resource"
	}
	// Server prefixes configuration
	serverPrefixes map[string]string
	// Global muster prefix
	musterPrefix string
	mu           sync.RWMutex
}

// NewNameTracker creates a new name tracker
func NewNameTracker(musterPrefix string) *NameTracker {
	if musterPrefix == "" {
		musterPrefix = "x"
	}
	return &NameTracker{
		nameMapping: make(map[string]struct {
			serverName   string
			originalName string
			itemType     string
		}),
		serverPrefixes: make(map[string]string),
		musterPrefix:   musterPrefix,
	}
}

// SetServerPrefix sets the prefix for a server
func (nt *NameTracker) SetServerPrefix(serverName, prefix string) {
	nt.mu.Lock()
	defer nt.mu.Unlock()

	// If no prefix is configured, use the server name as prefix
	if prefix == "" {
		prefix = serverName
	}
	nt.serverPrefixes[serverName] = prefix
}

// applyPrefix applies the server prefix to a name if not already present
func (nt *NameTracker) applyPrefix(serverName, name string, isResource bool) string {
	prefix := nt.serverPrefixes[serverName]
	if prefix == "" {
		prefix = serverName
	}

	// For resources (URIs), we handle prefixing differently
	if isResource {
		// Don't prefix URIs that already have a scheme
		if strings.Contains(name, "://") {
			return name
		}
	}

	// Check if the name already starts with the prefix
	expectedPrefix := prefix + "_"
	if strings.HasPrefix(name, expectedPrefix) {
		return name
	}

	// Apply the prefix
	return prefix + "_" + name
}

// GetExposedToolName returns the fully prefixed name for a tool
func (nt *NameTracker) GetExposedToolName(serverName, toolName string) string {
	nt.mu.RLock()
	defer nt.mu.RUnlock()

	// Apply server prefix
	prefixed := nt.applyPrefix(serverName, toolName, false)

	// Apply muster prefix
	exposedName := nt.musterPrefix + "_" + prefixed

	// Store the mapping
	nt.mu.RUnlock()
	nt.mu.Lock()
	nt.nameMapping[exposedName] = struct {
		serverName   string
		originalName string
		itemType     string
	}{
		serverName:   serverName,
		originalName: toolName,
		itemType:     "tool",
	}
	nt.mu.Unlock()
	nt.mu.RLock()

	return exposedName
}

// GetExposedPromptName returns the fully prefixed name for a prompt
func (nt *NameTracker) GetExposedPromptName(serverName, promptName string) string {
	nt.mu.RLock()
	defer nt.mu.RUnlock()

	// Apply server prefix
	prefixed := nt.applyPrefix(serverName, promptName, false)

	// Apply muster prefix
	exposedName := nt.musterPrefix + "_" + prefixed

	// Store the mapping
	nt.mu.RUnlock()
	nt.mu.Lock()
	nt.nameMapping[exposedName] = struct {
		serverName   string
		originalName string
		itemType     string
	}{
		serverName:   serverName,
		originalName: promptName,
		itemType:     "prompt",
	}
	nt.mu.Unlock()
	nt.mu.RLock()

	return exposedName
}

// GetExposedResourceURI returns the fully prefixed URI for a resource
func (nt *NameTracker) GetExposedResourceURI(serverName, resourceURI string) string {
	nt.mu.RLock()
	defer nt.mu.RUnlock()

	// For resources, we might want different handling
	// For now, apply the same prefixing logic
	prefixed := nt.applyPrefix(serverName, resourceURI, true)

	// Resources might not need the muster prefix if they're URIs
	exposedURI := prefixed
	if !strings.Contains(prefixed, "://") {
		exposedURI = nt.musterPrefix + "_" + prefixed
	}

	// Store the mapping
	nt.mu.RUnlock()
	nt.mu.Lock()
	nt.nameMapping[exposedURI] = struct {
		serverName   string
		originalName string
		itemType     string
	}{
		serverName:   serverName,
		originalName: resourceURI,
		itemType:     "resource",
	}
	nt.mu.Unlock()
	nt.mu.RLock()

	return exposedURI
}

// ResolveName resolves an exposed name to server and original name
func (nt *NameTracker) ResolveName(exposedName string) (serverName, originalName string, itemType string, err error) {
	nt.mu.RLock()
	defer nt.mu.RUnlock()

	mapping, exists := nt.nameMapping[exposedName]
	if !exists {
		return "", "", "", fmt.Errorf("unknown name: %s", exposedName)
	}

	return mapping.serverName, mapping.originalName, mapping.itemType, nil
}

// RebuildMappings is no longer needed since we don't track conflicts
func (nt *NameTracker) RebuildMappings(servers map[string]*ServerInfo) {
	// This method is kept for compatibility but does nothing
	// since we no longer need to track conflicts
}
