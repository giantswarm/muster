package aggregator

import (
	"fmt"
	"strings"
	"sync"
)

// NameTracker manages intelligent name prefixing and resolution for MCP capabilities.
//
// The name tracker solves the problem of name collisions when multiple MCP servers
// provide tools, prompts, or resources with the same names. It implements a consistent
// prefixing scheme and maintains bidirectional mappings between exposed (prefixed)
// names and their original server/name combinations.
//
// Naming scheme:
//   {muster_prefix}_{server_prefix}_{original_name}
//
// For example:
//   - Original tool: "list_files" from server "github"
//   - Server prefix: "gh" (configured) or "github" (default)
//   - Exposed name: "x_gh_list_files" (with muster prefix "x")
//
// The tracker is thread-safe and supports concurrent operations.
type NameTracker struct {
	// Bidirectional mapping: exposed name -> (server, original name, item type)
	nameMapping map[string]struct {
		serverName   string // Name of the server providing this capability
		originalName string // Original name before prefixing
		itemType     string // Type: "tool", "prompt", or "resource"
	}
	
	// Server-specific prefix configuration
	serverPrefixes map[string]string // Maps server name to its configured prefix
	
	// Global muster prefix applied to all capabilities
	musterPrefix string
	
	// Thread-safety protection
	mu sync.RWMutex
}

// NewNameTracker creates a new name tracker with the specified global prefix.
//
// The muster prefix is applied to all exposed capabilities to distinguish them
// from other MCP tools in mixed environments. If no prefix is provided, "x" is
// used as the default.
//
// Parameters:
//   - musterPrefix: Global prefix for all aggregated capabilities (defaults to "x")
//
// Returns a new name tracker ready for use.
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

// SetServerPrefix configures the prefix to use for a specific server.
//
// Server prefixes allow customization of how capabilities from different servers
// are distinguished. If no prefix is configured for a server, the server name
// itself is used as the prefix.
//
// This method is thread-safe and can be called while the tracker is in use.
//
// Parameters:
//   - serverName: Unique identifier of the server
//   - prefix: Prefix to use for this server's capabilities (uses server name if empty)
func (nt *NameTracker) SetServerPrefix(serverName, prefix string) {
	nt.mu.Lock()
	defer nt.mu.Unlock()

	// If no prefix is configured, use the server name as prefix
	if prefix == "" {
		prefix = serverName
	}
	nt.serverPrefixes[serverName] = prefix
}

// applyPrefix applies the appropriate server prefix to a capability name.
//
// This method handles the logic for prefixing names while avoiding duplicate
// prefixes if the name already includes the expected prefix. It has special
// handling for resource URIs to preserve URI schemes.
//
// Parameters:
//   - serverName: Name of the server providing the capability
//   - name: Original capability name or URI
//   - isResource: Whether this is a resource URI (affects prefixing logic)
//
// Returns the prefixed name, or the original name if prefixing is not needed.
func (nt *NameTracker) applyPrefix(serverName, name string, isResource bool) string {
	prefix := nt.serverPrefixes[serverName]
	if prefix == "" {
		prefix = serverName
	}

	// For resources (URIs), we handle prefixing differently
	if isResource {
		// Don't prefix URIs that already have a scheme (e.g., http://, file://)
		if strings.Contains(name, "://") {
			return name
		}
	}

	// Check if the name already starts with the expected prefix
	expectedPrefix := prefix + "_"
	if strings.HasPrefix(name, expectedPrefix) {
		return name
	}

	// Apply the server prefix
	return prefix + "_" + name
}

// GetExposedToolName returns the fully prefixed name for a tool.
//
// This method applies both server-specific and muster prefixes to create a
// globally unique tool name. It also maintains the internal mapping for
// later resolution.
//
// The naming pattern is: {muster_prefix}_{server_prefix}_{original_name}
//
// Parameters:
//   - serverName: Name of the server providing the tool
//   - toolName: Original tool name from the server
//
// Returns the exposed tool name that clients will see.
func (nt *NameTracker) GetExposedToolName(serverName, toolName string) string {
	nt.mu.RLock()
	defer nt.mu.RUnlock()

	// Apply server prefix to the tool name
	prefixed := nt.applyPrefix(serverName, toolName, false)

	// Apply muster prefix to create the final exposed name
	exposedName := nt.musterPrefix + "_" + prefixed

	// Store the bidirectional mapping for later resolution
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

// GetExposedPromptName returns the fully prefixed name for a prompt.
//
// This method applies both server-specific and muster prefixes to create a
// globally unique prompt name. It also maintains the internal mapping for
// later resolution.
//
// The naming pattern is: {muster_prefix}_{server_prefix}_{original_name}
//
// Parameters:
//   - serverName: Name of the server providing the prompt
//   - promptName: Original prompt name from the server
//
// Returns the exposed prompt name that clients will see.
func (nt *NameTracker) GetExposedPromptName(serverName, promptName string) string {
	nt.mu.RLock()
	defer nt.mu.RUnlock()

	// Apply server prefix to the prompt name
	prefixed := nt.applyPrefix(serverName, promptName, false)

	// Apply muster prefix to create the final exposed name
	exposedName := nt.musterPrefix + "_" + prefixed

	// Store the bidirectional mapping for later resolution
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

// GetExposedResourceURI returns the fully prefixed URI for a resource.
//
// This method handles resource URI prefixing with special consideration for
// URIs that already contain schemes (like http:// or file://). Such URIs
// are left unchanged to preserve their functionality.
//
// For simple resource identifiers, the standard prefixing pattern is applied.
//
// Parameters:
//   - serverName: Name of the server providing the resource
//   - resourceURI: Original resource URI from the server
//
// Returns the exposed resource URI that clients will see.
func (nt *NameTracker) GetExposedResourceURI(serverName, resourceURI string) string {
	nt.mu.RLock()
	defer nt.mu.RUnlock()

	// Apply server prefix with special handling for URIs
	prefixed := nt.applyPrefix(serverName, resourceURI, true)

	// Resources might not need the muster prefix if they're already valid URIs
	exposedURI := prefixed
	if !strings.Contains(prefixed, "://") {
		exposedURI = nt.musterPrefix + "_" + prefixed
	}

	// Store the bidirectional mapping for later resolution
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

// ResolveName resolves an exposed (prefixed) name back to its server and original name.
//
// This method performs the reverse operation of the Get*Name methods, taking an
// exposed name and determining which server provided it and what the original
// name was before prefixing.
//
// This is essential for routing requests to the correct backend server.
//
// Parameters:
//   - exposedName: The prefixed name as seen by clients
//
// Returns the server name, original name, item type, and nil error if successful.
// Returns empty strings and an error if the name cannot be resolved.
func (nt *NameTracker) ResolveName(exposedName string) (serverName, originalName string, itemType string, err error) {
	nt.mu.RLock()
	defer nt.mu.RUnlock()

	mapping, exists := nt.nameMapping[exposedName]
	if !exists {
		return "", "", "", fmt.Errorf("unknown name: %s", exposedName)
	}

	return mapping.serverName, mapping.originalName, mapping.itemType, nil
}

// RebuildMappings is a legacy method maintained for backward compatibility.
//
// In the current implementation, name mappings are maintained automatically
// as capabilities are registered, so this method is no longer needed.
// It is kept to avoid breaking existing code that might call it.
//
// Parameters:
//   - servers: Map of server information (unused in current implementation)
func (nt *NameTracker) RebuildMappings(servers map[string]*ServerInfo) {
	// This method is kept for compatibility but does nothing
	// since mappings are now maintained automatically during registration
}
